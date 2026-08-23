package authz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func staticVerify(claims *Claims, err error) VerifyFunc {
	return func(context.Context, string) (*Claims, error) { return claims, err }
}

func do(t *testing.T, g Guard, method, path, authHeader string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	var handlerRan bool
	h := g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerRan = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(method, path, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, handlerRan
}

func TestMiddlewareEnforced(t *testing.T) {
	reader := staticVerify(&Claims{Subject: "u", Permissions: []string{"core:tenant:read"}}, nil)
	writer := staticVerify(&Claims{Subject: "u", Scopes: []string{"core:tenant:write"}}, nil)

	tests := []struct {
		name       string
		verify     VerifyFunc
		method     string
		path       string
		auth       string
		wantStatus int
	}{
		{"no token is 401", reader, http.MethodGet, "/api/v1/tenants", "", http.StatusUnauthorized},
		{"invalid token is 401", staticVerify(nil, errors.New("bad")), http.MethodGet, "/api/v1/tenants", "Bearer x", http.StatusUnauthorized},
		{"read permission allows GET", reader, http.MethodGet, "/api/v1/tenants/abc", "Bearer x", http.StatusOK},
		{"read permission does not allow POST", reader, http.MethodPost, "/api/v1/tenants", "Bearer x", http.StatusForbidden},
		{"write permission (scope claim) allows POST", writer, http.MethodPost, "/api/v1/tenants", "Bearer x", http.StatusOK},
		{"write permission on wrong segment denied", writer, http.MethodPost, "/api/v1/resources", "Bearer x", http.StatusForbidden},
		{"unmapped segment denied even with a valid token", writer, http.MethodGet, "/api/v1/internal-debug", "Bearer x", http.StatusForbidden},
		{"DELETE requires write", reader, http.MethodDelete, "/api/v1/tenants/abc", "Bearer x", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, ran := do(t, Guard{Mode: ModeEnforced, Verify: tt.verify}, tt.method, tt.path, tt.auth)
			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, tt.wantStatus == http.StatusOK, ran, "handler must run iff authorized")
		})
	}
}

func TestMiddlewareSetupSurfaceBypasses(t *testing.T) {
	// The setup surface self-guards with CORE_SETUP_TOKEN (it provisions the
	// very Auth that would mint bearer tokens), so the platform guard must let
	// it through in EVERY mode — including Unavailable, or a fresh install
	// with no Auth yet could never be provisioned.
	for _, g := range []Guard{
		{Mode: ModeEnforced, Verify: staticVerify(nil, errors.New("no tokens exist yet"))},
		{Mode: ModeUnavailable, Reason: "missing AUTH_JWKS_URL"},
		{Mode: ModeDevOpen},
	} {
		rec, ran := do(t, g, http.MethodPost, "/api/v1/setup", "")
		assert.Equal(t, http.StatusOK, rec.Code, "mode %v", g.Mode)
		assert.True(t, ran)
		rec, ran = do(t, g, http.MethodGet, "/api/v1/setup/status", "")
		assert.Equal(t, http.StatusOK, rec.Code, "mode %v", g.Mode)
		assert.True(t, ran)
	}
}

// TestUnauthenticatedSurfaceIsExactly is the tripwire on core's unguarded
// surface. It fails on an ADDITION as loudly as on a substitution, because
// adding a segment here is adding a route anyone on the network can call: the
// diff that widens this map is the diff that has to carry the argument for why
// the new surface is UNAUTHENTICATABLE rather than merely inconvenient to
// authenticate.
//
// /healthz and /readyz are not in the map — they are mounted outside the
// /api/v1 group this middleware wraps (internal/server.Router), so they are
// exempt by construction. Their contents are pinned by internal/server tests.
func TestUnauthenticatedSurfaceIsExactly(t *testing.T) {
	assert.Equal(t, map[string][]string{
		"setup":        nil,
		"capabilities": {http.MethodGet, http.MethodHead},
	}, exemptSegments)

	// No exempt segment may also be in the permission map: an entry in both
	// would look guarded while being served open.
	for segment := range exemptSegments {
		_, mapped := routePermissions[segment]
		assert.False(t, mapped, "segment %s cannot be both exempt and permission-mapped", segment)
	}
}

// TestMiddlewareCapabilityProbeBypasses: the probe answers the questions a caller
// must settle BEFORE it can hold a token, so it must be reachable in every mode
// — including Unavailable, where every other route is 503 and the console has
// nothing else to read the posture from.
func TestMiddlewareCapabilityProbeBypasses(t *testing.T) {
	for _, g := range []Guard{
		{Mode: ModeEnforced, Verify: staticVerify(nil, errors.New("no tokens exist yet"))},
		{Mode: ModeUnavailable, Reason: "missing AUTH_JWKS_URL"},
		{Mode: ModeDevOpen},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec, ran := do(t, g, method, "/api/v1/capabilities", "")
			assert.Equal(t, http.StatusOK, rec.Code, "%s in mode %v", method, g.Mode)
			assert.True(t, ran)
		}
	}
}

// The exemption is READ-only: a mutating verb is not exempt, so a write surface
// mounted under this path later fails closed against the allowlist instead of
// inheriting the exemption.
func TestMiddlewareCapabilityProbeExemptsReadsOnly(t *testing.T) {
	g := Guard{Mode: ModeEnforced, Verify: staticVerify(&Claims{Subject: "u", Permissions: []string{"core:admin"}}, nil)}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec, ran := do(t, g, method, "/api/v1/capabilities", "Bearer x")
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s must not inherit the read exemption", method)
		assert.False(t, ran)
	}
}

func TestModeNameIsTheWireContract(t *testing.T) {
	// These strings are published by GET /api/v1/capabilities and parsed by the
	// console; they are deliberately not the Go constant names.
	assert.Equal(t, "enforced", ModeEnforced.Name())
	assert.Equal(t, "dev-open", ModeDevOpen.Name())
	assert.Equal(t, "unavailable", ModeUnavailable.Name())
	assert.Equal(t, "unknown", Mode(99).Name())
}

func TestMiddlewareUnavailableFailsClosed(t *testing.T) {
	g := Guard{Mode: ModeUnavailable, Reason: "missing AUTH_JWKS_URL, AUTH_ISSUER"}
	rec, ran := do(t, g, http.MethodGet, "/api/v1/tenants", "Bearer would-be-valid")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.False(t, ran)
	assert.Contains(t, rec.Body.String(), "AUTH_JWKS_URL")
}

func TestMiddlewareDevOpen(t *testing.T) {
	rec, ran := do(t, Guard{Mode: ModeDevOpen}, http.MethodDelete, "/api/v1/tenants/abc", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, ran)
}

func TestMiddlewarePutsClaimsInContext(t *testing.T) {
	var got *Claims
	g := Guard{Mode: ModeEnforced, Verify: staticVerify(&Claims{Subject: "u1", Permissions: []string{"core:agent:read"}}, nil)}
	h := g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer x")
	h.ServeHTTP(httptest.NewRecorder(), req)
	require.NotNil(t, got)
	assert.Equal(t, "u1", got.Subject)
}

func TestEverySegmentPairIsNamespaced(t *testing.T) {
	// Permissions are namespaced core:<entity>:<read|write> — a stray
	// un-namespaced permission would collide with another service's grants.
	//
	// "core:admin" is the one sanctioned exception: it is the blanket
	// administrative grant Core registers in Auth (see setup.adminPermission),
	// and a surface may require it on BOTH verbs when reading it is as
	// privileged as writing it. It is still namespaced, so the collision the
	// rule guards against cannot happen.
	const blanketAdmin = "core:admin"
	for seg, p := range routePermissions {
		if p.Read != blanketAdmin {
			assert.Regexp(t, `^core:[a-z]+:read$`, p.Read, "segment %s", seg)
		}
		if p.Write != blanketAdmin {
			assert.Regexp(t, `^core:[a-z]+:write$`, p.Write, "segment %s", seg)
		}
		// A blanket grant is all-or-nothing: pairing it with a narrower verb
		// permission would mean one verb silently bypasses the admin gate.
		if p.Read == blanketAdmin || p.Write == blanketAdmin {
			assert.Equal(t, p.Read, p.Write, "segment %s: the blanket admin grant must guard both verbs", seg)
		}
	}
}
