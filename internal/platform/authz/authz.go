// Package authz guards the core HTTP API the way the platform demands of every
// attached service: sdk-verified bearer tokens (Auth's JWKS + issuer +
// audience) plus a route→permission map that doubles as the surface allowlist.
// Fail-closed by construction — an unmapped route is denied even to a valid
// token, and outside development a missing auth configuration disables the API
// (503) instead of quietly serving it open.
package authz

import (
	"context"
	"net/http"
	"strings"

	sdkauth "github.com/maintainerd/sdk/auth"

	"github.com/maintainerd/core/internal/platform/response"
)

// Claims is the verified identity of a caller: the scopes from the token's
// "scope" claim plus any "permissions" array claim. Both are checked because
// maintainerd-auth can mint either shape.
type Claims struct {
	Subject     string
	Scopes      []string
	Permissions []string
}

// HasPermission checks membership in either claim shape. An absent claim is
// simply "no permissions" — never a bypass.
func (c *Claims) HasPermission(perm string) bool {
	if c == nil {
		return false
	}
	for _, s := range c.Scopes {
		if s == perm {
			return true
		}
	}
	for _, p := range c.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// VerifyFunc validates a bearer token and returns its claims. In production it
// wraps the sdk verifier; tests inject their own.
type VerifyFunc func(ctx context.Context, token string) (*Claims, error)

// SDKVerify adapts the sdk verifier to VerifyFunc, mapping both permission
// claim shapes (space-separated "scope" and the "permissions" array).
func SDKVerify(v *sdkauth.Verifier) VerifyFunc {
	return func(_ context.Context, token string) (*Claims, error) {
		c, err := v.Verify(token)
		if err != nil {
			return nil, err
		}
		out := &Claims{Subject: c.Subject, Scopes: c.Scopes}
		if raw, ok := c.Raw["permissions"].([]any); ok {
			for _, p := range raw {
				if s, ok := p.(string); ok {
					out.Permissions = append(out.Permissions, s)
				}
			}
		}
		return out, nil
	}
}

// Mode is the resolved posture of the HTTP guard.
type Mode int

const (
	// ModeEnforced verifies tokens and permissions on every /api/v1 request —
	// the only mode outside development.
	ModeEnforced Mode = iota
	// ModeDevOpen serves without authentication. Permitted ONLY in
	// development, announced loudly at boot.
	ModeDevOpen
	// ModeUnavailable refuses every guarded route with 503: auth is required
	// (non-development) but not configured. The setup surface stays reachable
	// (it carries its own CORE_SETUP_TOKEN gate) precisely so a fresh install
	// can be provisioned into a state where tokens exist at all.
	ModeUnavailable
)

// Guard is the resolved HTTP-auth posture, decided at startup by the
// bootstrap (see cmd/server).
type Guard struct {
	Mode   Mode
	Verify VerifyFunc // required when Mode == ModeEnforced
	Reason string     // human-readable cause for DevOpen/Unavailable
}

// perms is the read/write permission pair guarding one API segment.
type perms struct {
	Read  string
	Write string
}

// routePermissions maps the first path segment under /api/v1 to its permission
// pair. GET/HEAD require Read; every mutating verb requires Write. The map is
// the allowlist: a segment that is NOT listed here is DENIED even to a valid
// token, so mounting a new router without deciding its permissions fails
// closed instead of shipping an open surface.
//
// "setup" is deliberately absent AND special-cased in Middleware: the setup
// surface must work before Auth exists (it is what provisions Auth), so it is
// self-guarded by CORE_SETUP_TOKEN in the setup handler instead of by tokens
// no one can mint yet.
var routePermissions = map[string]perms{
	"tenants":   {Read: "core:tenant:read", Write: "core:tenant:write"},
	"projects":  {Read: "core:project:read", Write: "core:project:write"},
	"services":  {Read: "core:service:read", Write: "core:service:write"},
	"providers": {Read: "core:provider:read", Write: "core:provider:write"},
	"agents":    {Read: "core:agent:read", Write: "core:agent:write"},
	"resources": {Read: "core:resource:read", Write: "core:resource:write"},
	// The platform escalation log (internal/event). The surface is read-only:
	// events are written by Core's own supervision loop as a record of what it
	// observed, so no mutating route is registered at all — a caller that could
	// POST one could forge evidence, and one that could DELETE one could erase an
	// incident. The write permission is still declared so a future write surface
	// must be granted explicitly instead of inheriting the read grant.
	"events": {Read: "core:event:read", Write: "core:event:write"},
	// The steward surface reads and rewrites the platform's IAM wiring in Auth
	// (services, APIs, permissions, policies, machine clients). There is no
	// meaningful read/write split for it: seeing which principals and grants
	// exist is as sensitive as creating them, so both verbs require the blanket
	// admin permission.
	"steward": {Read: "core:admin", Write: "core:admin"},
}

// setupSegment is the self-guarded first-run surface (see routePermissions).
const setupSegment = "setup"

type ctxKey struct{}

// FromContext returns the Claims placed by Middleware, if any.
func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(*Claims)
	return c, ok
}

// Middleware enforces the guard on every route it wraps. It is mounted on the
// /api/v1 group; /healthz lives outside that group and is therefore exempt by
// construction.
func (g Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		segment := apiSegment(r.URL.Path)
		// The setup surface guards itself (CORE_SETUP_TOKEN) — see the
		// routePermissions doc for why it cannot be token-guarded.
		if segment == setupSegment {
			next.ServeHTTP(w, r)
			return
		}
		switch g.Mode {
		case ModeDevOpen:
			next.ServeHTTP(w, r)
			return
		case ModeUnavailable:
			response.Error(w, http.StatusServiceUnavailable,
				"API authentication is not configured ("+g.Reason+"); the API is disabled outside development")
			return
		}

		token := bearer(r.Header.Get("Authorization"))
		if token == "" {
			response.Error(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		claims, err := g.Verify(r.Context(), token)
		if err != nil {
			// Deliberately generic: which check a forged token failed is
			// oracle material.
			response.Error(w, http.StatusUnauthorized, "invalid token")
			return
		}
		p, known := routePermissions[segment]
		if !known {
			response.Error(w, http.StatusForbidden, "route has no permission mapping")
			return
		}
		required := p.Write
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			required = p.Read
		}
		if !claims.HasPermission(required) {
			response.Error(w, http.StatusForbidden, "requires permission "+required)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, claims)))
	})
}

// apiSegment extracts the first path segment under /api/v1 ("" when the path
// is not under it).
func apiSegment(path string) string {
	const prefix = "/api/v1/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// bearer extracts a "Bearer <token>" Authorization header value.
func bearer(header string) string {
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}
