package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/maintainerd/core/internal/platform/authz"
	"github.com/maintainerd/core/internal/platform/response"
)

// The capability probe: what a client may assume about THIS instance, answerable
// without a credential.
//
// WHY IT IS UNAUTHENTICATED. It answers the questions a client has to settle
// BEFORE it can hold a token: is this control plane's HTTP guard enforced or
// open, has it been provisioned yet, and — if enforced — which issuer, audience,
// token endpoint and console client id a browser needs to run an authorization-
// code+PKCE flow. A console cannot ask any of that WITH a bearer token, because
// obtaining one is the thing it is trying to work out how to do. The alternative,
// which is what this replaces, is CONFIGURATION BY HAND: the console read
// AUTH_ISSUER / CORE_CONSOLE_TOKEN_URL / CORE_CONSOLE_CLIENT_ID from its own
// deployment and rendered "this console has no identity configuration" when any
// were missing — which is a statement about the SPA's env file, not about the
// server. It is wrong in both directions: an enforced core whose console was
// handed no issuer renders as "no identity configured" and then 401s on every
// call, and an open core whose console WAS given an issuer sends the operator
// into an OAuth flow nobody is listening for. Worse, the console client id is
// something CORE ITSELF created (setup step 6, EnsureConsoleClient), so hand-
// copying it out of a database row and into an SPA's environment is copying a
// value from the process that owns it to the process it was made for.
//
// WHAT MAKES IT SAFE. Every field below is a constant, a single bit an existing
// anonymous surface already discloses, or a value that appears in the clear in
// every token core verifies (or in every authorize URL the console builds). The
// field-by-field argument is on each field. The rule for adding one: an
// anonymous caller must learn nothing they could use, and nothing they could not
// have obtained from the port they are already talking to. In particular there is
// no client secret, no control-plane private key, no JWKS URL (routinely an
// in-cluster address — core's own is http://maintainerd-auth:8081/...), no
// deployment substrate, no tenant, project or permission list, and no guard
// Reason (that string names missing environment variables — operator-facing
// text, not caller-facing).
//
// WHY IT IS NOT /setup/status WITH MORE FIELDS. That surface is the wizard's, and
// its full payload is deliberately privileged: internal/setup.slimStatus names
// the control-plane ids, the client ids, the signing JWKS and the deployment
// substrate as material that "maps the install for an attacker" and gates them
// behind CORE_SETUP_TOKEN or core:admin. Widening it would widen what the
// ANONYMOUS branch of a privileged endpoint discloses. This is a separate,
// permanently-anonymous, deliberately tiny surface — which is also why the
// deployment substrate that secret publishes as run_mode has no counterpart
// here: core has already classified that exact value as privileged.

// capabilitiesCacheTTL bounds how long the setup half of the payload may be
// stale.
//
// THE CACHE IS THE RATE LIMIT. This endpoint is anonymous, so without one an
// attacker could turn it into a database read per request — and unlike the setup
// wizard there is no shared secret to key a per-IP budget against a real
// principal. Caching removes the database from the hot path entirely, which is a
// better answer than a budget: a budget would also refuse the console, which
// polls this on boot and again after it drives setup.
//
// A COMPLETED SETUP IS CACHED FOREVER, not for the TTL. Setup is a durable
// one-shot — control_plane.setup_completed_at is written once and Auth's
// CompleteSetup locks the window against a second run — so "complete" is
// monotonic and a cached true can never become wrong. Only the not-yet-complete
// answer expires.
const capabilitiesCacheTTL = 5 * time.Second

// capabilityServiceName is the constant this endpoint reports.
const capabilityServiceName = "core"

// capabilityGuardTransport names WHICH guard the payload describes.
//
// Core resolves two INDEPENDENT guards from one verifier (cmd/server
// resolveHTTPGuard and resolveGRPCGuard) and they do not share a ladder: the
// HTTP guard's third state is "unavailable" (503 on every guarded route) while
// the AgentGateway's is "health-only" (the gRPC surface does not register at
// all). They can also disagree in effect — the gateway refuses to serve
// unauthenticated outside development where HTTP merely 503s. A single
// guard_mode field with no transport would therefore be a claim about "the
// process" that neither guard makes. This payload describes the HTTP guard, the
// one the caller is talking to, and says so.
const capabilityGuardTransport = "http"

// SetupProbe is the slice of the setup orchestrator the capability probe needs.
//
// It yields the console's OAuth client id as a bare string rather than the
// setup.Result the orchestrator holds: that struct also carries the auth tenant
// uuid, the control-plane client ids and the signing JWKS, and a handler that
// never holds it cannot leak one by a later field addition. *setup.Orchestrator
// satisfies this.
type SetupProbe interface {
	ConsoleIdentity(ctx context.Context) (complete bool, consoleOAuthClientID string)
}

// CapabilityInfo is the static half of the payload — facts the process knows
// about itself at boot, handed to Router by the bootstrap rather than read from
// config here, so that every test constructs the surface the same way the
// process does.
type CapabilityInfo struct {
	// Version is this build's version string (config.AppVersion; the release
	// image stamps its tag in at link time).
	Version string

	// AuthIssuer and AuthAudience are the two string comparisons a token must
	// satisfy. They are published ONLY when the HTTP guard actually resolved to
	// ModeEnforced, so a leftover pair in the environment of a dev-open instance
	// can never be advertised as if it were being verified.
	AuthIssuer   string
	AuthAudience string

	// AuthTokenEndpoint is AUTH_TOKEN_URL — Auth's OAuth2 token endpoint, the
	// same one core exchanges its own client assertion at. It is NOT derived
	// from the issuer: Auth's discovery document builds its endpoints off
	// APP_PUBLIC_HOSTNAME, and while that equals core's AUTH_ISSUER today,
	// publishing the value core actually uses cannot drift from the one it uses.
	// Required outside development (config.validateDependencies), so empty here
	// means development.
	AuthTokenEndpoint string

	// AuthAuthorizeEndpoint is the BROWSER-facing authorization page.
	//
	// It cannot be derived from the issuer, and this is the trap. Auth's
	// discovery document advertises authorization_endpoint as
	// issuer + "/api/v1/oauth/authorize", but that route answers JSON (an
	// OAuth login_required error, or {redirect_uri}) — it never redirects a
	// browser. The page a browser must land on is served by the separate
	// identity APP (APP_FRONTEND_IDENTITY_HOSTNAME), which then calls that API.
	// Publishing the derived URL would send an operator's browser to a JSON
	// error body, so this is sourced from configuration only and OMITTED when
	// core does not know it — the console then falls back to its own configured
	// origin instead of following core to a dead end.
	AuthAuthorizeEndpoint string

	// Setup reports whether provisioning completed and, if so, the console
	// OAuth client id it registered. Nil is tolerated (setup_complete=false).
	Setup SetupProbe
}

// capabilitiesPayload is the wire shape. Field-by-field justification below;
// it is the whole reason this type is written out rather than assembled from a
// map.
type capabilitiesPayload struct {
	// Service is the constant "core". It identifies WHICH maintainerd API
	// answered — something a caller already knows from the address it dialled,
	// and something a client pointed at the wrong port needs in order to SAY so.
	// Safe: a constant.
	Service string `json:"service"`

	// Version is the build. Safe because the release images are public and their
	// tags enumerable from the registry, so an anonymous caller can already map
	// an image to a version — and the alternative is a console that cannot tell
	// an operator what they are running. It is a version, not a commit: no build
	// path, no branch, no CI identifier.
	Version string `json:"version"`

	// GuardTransport is the constant "http" — see capabilityGuardTransport for
	// why one guard_mode with no transport would be a claim core cannot make.
	GuardTransport string `json:"guard_transport"`

	// GuardMode is "enforced", "dev-open" or "unavailable" (authz.Mode.Name).
	//
	// Safe, and honest is safer than coy: a caller can already determine this in
	// one unauthenticated request — call any guarded /api/v1 route with no token
	// and read whether it answers 200, 401 or 503 — so withholding it protects
	// nothing while forcing the console to guess. Saying it plainly is what lets
	// the console render "this control plane is answering anonymous callers"
	// from the SERVER's posture instead of from its own missing settings. It
	// names the mode and never Guard.Reason, which lists missing env vars.
	GuardMode string `json:"guard_mode"`

	// SetupComplete is exactly the single bit the anonymous branch of
	// GET /api/v1/setup/status already returns (internal/setup.slimStatus). It is
	// unavoidable — a client has to know whether to show the first-run wizard —
	// and it is already the documented anonymous disclosure, so this adds none.
	SetupComplete bool `json:"setup_complete"`

	// Auth is present ONLY when GuardMode is "enforced".
	//
	// Necessary: these are what a client needs to obtain a token core will
	// accept, and a console that reads them FROM core cannot be misconfigured
	// into pointing at a different Auth than the one core verifies against —
	// which is the single most common way an install ends up 401ing everything.
	//
	// Safe: every token core verifies carries the issuer and audience in the
	// clear, the issuer is a public OIDC discovery origin by design, the
	// audience is a resource-API identifier, and the token/authorize endpoints
	// are the public front door of the authorization server (they are in Auth's
	// own unauthenticated /.well-known/openid-configuration).
	//
	// Omitted when NOT enforced because there is nothing true to say: a dev-open
	// or unavailable guard verifies no issuer, and publishing a half-configured
	// one would send a client at an authorize endpoint that cannot help it.
	//
	// The JWKS URL is deliberately NOT here. It is the one identity value that
	// is routinely an internal address — core's own default is
	// http://maintainerd-auth:8081/.well-known/jwks.json — and a client never
	// needs it: the server fetches keys, the client does not.
	Auth *capabilityAuth `json:"auth,omitempty"`

	// Console is present ONLY once setup has produced the console's OAuth client
	// id. Absent, never an empty string: the console must be able to tell "not
	// provisioned yet, show the wizard" from "provisioned but this value never
	// landed, tell the operator" — and setup step 6 (EnsureConsoleClient) is
	// best-effort, so setup_complete=true with no console block is a real,
	// reachable state that means exactly the second thing.
	//
	// A public OAuth client id is NOT a secret. This client is registered as a
	// public SPA client (authorization_code + PKCE, no client secret to hold),
	// and its id travels in the query string of every authorize request the
	// browser makes — it is printed in the operator's own address bar. Its
	// secrecy protects nothing; PKCE and the registered redirect-URI allowlist
	// are what make the flow safe, and both live in Auth.
	Console *capabilityConsole `json:"console,omitempty"`
}

type capabilityAuth struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	// Omitted rather than empty for the same reason Console is: a console that
	// receives "" cannot tell "core does not know this" from "core knows it is
	// blank".
	TokenEndpoint     string `json:"token_endpoint,omitempty"`
	AuthorizeEndpoint string `json:"authorize_endpoint,omitempty"`
}

type capabilityConsole struct {
	ClientID string `json:"client_id"`
}

// capabilitiesHandler serves the probe. It holds the static info, the resolved
// HTTP guard and the memoized setup half.
type capabilitiesHandler struct {
	info  CapabilityInfo
	guard authz.Guard

	mu sync.Mutex
	// complete/consoleClientID are the cached setup half; checkedAt is zero
	// until the first successful read. Once complete is true the entry never
	// expires — see capabilitiesCacheTTL.
	complete        bool
	consoleClientID string
	checkedAt       time.Time
}

func newCapabilitiesHandler(info CapabilityInfo, guard authz.Guard) *capabilitiesHandler {
	return &capabilitiesHandler{info: info, guard: guard}
}

// mountCapabilities registers the probe on an /api/v1 router. Router and the
// tests share it so the path and the method set cannot drift apart: GET only, so
// a mutating verb is a 405 from chi and never reaches a handler.
func mountCapabilities(r chi.Router, info CapabilityInfo, guard authz.Guard) {
	r.Get("/capabilities", newCapabilitiesHandler(info, guard).serve)
}

// serve answers the capability probe.
//
// It NEVER fails on a setup-read error. A capability endpoint that 500s because
// the database blipped is a console that cannot render its own sign-in page
// during an incident — so a failed read reports setup_complete=false (the
// fail-closed reading: "we cannot confirm this install is provisioned") and
// everything else, which does not touch the database at all.
func (h *capabilitiesHandler) serve(w http.ResponseWriter, r *http.Request) {
	complete, consoleClientID := h.setupIdentity(r.Context())

	payload := capabilitiesPayload{
		Service:        capabilityServiceName,
		Version:        orUnknown(h.info.Version),
		GuardTransport: capabilityGuardTransport,
		GuardMode:      h.guard.Mode.Name(),
		SetupComplete:  complete,
	}
	if h.guard.Mode == authz.ModeEnforced {
		payload.Auth = &capabilityAuth{
			Issuer:            h.info.AuthIssuer,
			Audience:          h.info.AuthAudience,
			TokenEndpoint:     h.info.AuthTokenEndpoint,
			AuthorizeEndpoint: h.info.AuthAuthorizeEndpoint,
		}
	}
	if consoleClientID != "" {
		payload.Console = &capabilityConsole{ClientID: consoleClientID}
	}

	// no-store rather than a short max-age: guard_mode and setup_complete are
	// exactly the facts a client must not act on a stale copy of, and the cache
	// that matters (the one in front of the database) is this process's own.
	w.Header().Set("Cache-Control", "no-store")
	response.Success(w, payload, "")
}

// setupIdentity reads the cached setup half, refreshing it at most once per TTL.
func (h *capabilitiesHandler) setupIdentity(ctx context.Context) (bool, string) {
	if h.info.Setup == nil {
		return false, ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.complete {
		return true, h.consoleClientID // monotonic: setup is a durable one-shot.
	}
	if !h.checkedAt.IsZero() && time.Since(h.checkedAt) < capabilitiesCacheTTL {
		return false, ""
	}
	complete, clientID := h.info.Setup.ConsoleIdentity(ctx)
	h.checkedAt = time.Now()
	h.complete = complete
	h.consoleClientID = clientID
	return complete, clientID
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}
