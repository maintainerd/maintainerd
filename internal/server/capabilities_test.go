package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/platform/authz"
)

// stubSetup is a SetupProbe that counts reads, so the memoization can be
// asserted rather than assumed (the cache IS the rate limit on an anonymous
// endpoint — see capabilitiesCacheTTL).
type stubSetup struct {
	complete bool
	clientID string
	calls    atomic.Int32
}

func (s *stubSetup) ConsoleIdentity(context.Context) (bool, string) {
	s.calls.Add(1)
	return s.complete, s.clientID
}

// capRouter mirrors Router's mount: inside the /api/v1 group, behind the guard
// middleware, registered through the same mountCapabilities the process uses so
// the path and method set cannot drift from production.
func capRouter(info CapabilityInfo, guard authz.Guard) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(guard.Middleware)
		mountCapabilities(r, info, guard)
	})
	return r
}

func probeCapabilities(t *testing.T, info CapabilityInfo, guard authz.Guard) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	// Deliberately NO Authorization header anywhere in this file.
	capRouter(info, guard).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil))

	var envelope struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	assert.True(t, envelope.Success)
	return rec, envelope.Data
}

// enforced is the strictest posture: a verifier that rejects everything, since
// no caller in this file presents a token at all.
func enforced() authz.Guard {
	return authz.Guard{Mode: authz.ModeEnforced, Verify: func(context.Context, string) (*authz.Claims, error) {
		return nil, errRejectEveryToken
	}}
}

var errRejectEveryToken = errors.New("no token is valid in this test")

func fullInfo(setup SetupProbe) CapabilityInfo {
	return CapabilityInfo{
		Version:               "v1.2.3",
		AuthIssuer:            "https://identity-api.auth.example.test",
		AuthAudience:          "https://core.example.test",
		AuthTokenEndpoint:     "https://identity-api.auth.example.test/api/v1/oauth/token",
		AuthAuthorizeEndpoint: "https://identity.auth.example.test/authorize",
		Setup:                 setup,
	}
}

// TestCapabilitiesNeedsNoCredential is the whole point: the probe answers the
// questions a caller must settle BEFORE it can hold a token, in every guard
// posture including the one that 503s every other route.
func TestCapabilitiesNeedsNoCredential(t *testing.T) {
	for _, guard := range []authz.Guard{
		enforced(),
		{Mode: authz.ModeDevOpen, Reason: "missing AUTH_ISSUER"},
		{Mode: authz.ModeUnavailable, Reason: "missing AUTH_JWKS_URL"},
	} {
		rec, data := probeCapabilities(t, fullInfo(&stubSetup{}), guard)
		assert.Equal(t, http.StatusOK, rec.Code, "mode %s", guard.Mode.Name())
		assert.Equal(t, "core", data["service"])
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	}
}

// TestCapabilitiesRejectsMutatingMethods pins that the anonymous surface is
// read-only, in every posture. Only GET is exempt, so a mutating verb is refused
// by whichever layer sees it first — the guard (401 when enforced, because this
// caller holds no token and the exemption no longer applies; 503 when the API is
// disabled) or chi's method router (405 in dev-open, where the guard admits every
// caller). Never 2xx, and never a handler.
func TestCapabilitiesRejectsMutatingMethods(t *testing.T) {
	cases := map[string]struct {
		guard authz.Guard
		want  int
	}{
		"enforced":    {enforced(), http.StatusUnauthorized},
		"dev-open":    {authz.Guard{Mode: authz.ModeDevOpen}, http.StatusMethodNotAllowed},
		"unavailable": {authz.Guard{Mode: authz.ModeUnavailable}, http.StatusServiceUnavailable},
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for name, tc := range cases {
			rec := httptest.NewRecorder()
			capRouter(fullInfo(&stubSetup{}), tc.guard).
				ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/capabilities", nil))
			assert.Equal(t, tc.want, rec.Code, "%s in mode %s", method, name)
		}
	}
}

// TestCapabilitiesOmitsIdentityBeforeSetup: absent, never an empty string. The
// console has to distinguish "not provisioned yet" from "provisioned and
// misconfigured", which an "" would collapse.
func TestCapabilitiesOmitsIdentityBeforeSetup(t *testing.T) {
	_, data := probeCapabilities(t, fullInfo(&stubSetup{complete: false}), enforced())

	assert.Equal(t, false, data["setup_complete"])
	_, present := data["console"]
	assert.False(t, present, "console must be ABSENT before setup, not empty")
}

func TestCapabilitiesIncludesIdentityAfterSetup(t *testing.T) {
	_, data := probeCapabilities(t,
		fullInfo(&stubSetup{complete: true, clientID: "console-oauth-client-id"}), enforced())

	assert.Equal(t, true, data["setup_complete"])
	console, ok := data["console"].(map[string]any)
	require.True(t, ok, "console must be present once setup produced the client id")
	assert.Equal(t, "console-oauth-client-id", console["client_id"])

	auth, ok := data["auth"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://identity-api.auth.example.test", auth["issuer"])
	assert.Equal(t, "https://core.example.test", auth["audience"])
	assert.Equal(t, "https://identity-api.auth.example.test/api/v1/oauth/token", auth["token_endpoint"])
	assert.Equal(t, "https://identity.auth.example.test/authorize", auth["authorize_endpoint"])
}

// A completed setup whose EnsureConsoleClient leg failed (it is best-effort in
// the orchestrator) is a real state: setup_complete is true and console is
// absent, which is exactly the "provisioned but misconfigured" signal.
func TestCapabilitiesReportsSetupCompleteWithNoConsoleClient(t *testing.T) {
	_, data := probeCapabilities(t, fullInfo(&stubSetup{complete: true, clientID: ""}), enforced())

	assert.Equal(t, true, data["setup_complete"])
	_, present := data["console"]
	assert.False(t, present)
}

// TestCapabilitiesPublishesAuthOnlyWhenEnforced: a leftover issuer/audience pair
// in a dev-open instance's environment must never be advertised as if it were
// being verified.
func TestCapabilitiesPublishesAuthOnlyWhenEnforced(t *testing.T) {
	for _, guard := range []authz.Guard{{Mode: authz.ModeDevOpen}, {Mode: authz.ModeUnavailable}} {
		_, data := probeCapabilities(t, fullInfo(&stubSetup{complete: true, clientID: "c"}), guard)
		_, present := data["auth"]
		assert.False(t, present, "mode %s verifies no issuer, so it must publish none", guard.Mode.Name())
	}
}

// TestCapabilitiesReportsTheHTTPGuardSpecifically. Core resolves two independent
// guards from one verifier and their ladders differ in kind — the HTTP guard's
// third state is "unavailable", the AgentGateway's is "health-only" (see
// cmd/server resolveHTTPGuard / resolveGRPCGuard, and TestGuardLaddersDiverge).
// A dev-open HTTP guard must therefore report "dev-open" whatever the gRPC guard
// is doing, and the payload must name the transport rather than imply one value
// covers the process.
func TestCapabilitiesReportsTheHTTPGuardSpecifically(t *testing.T) {
	_, data := probeCapabilities(t, fullInfo(&stubSetup{}), authz.Guard{Mode: authz.ModeDevOpen})

	assert.Equal(t, "http", data["guard_transport"])
	assert.Equal(t, "dev-open", data["guard_mode"])

	_, enforcedData := probeCapabilities(t, fullInfo(&stubSetup{}), enforced())
	assert.Equal(t, "http", enforcedData["guard_transport"])
	assert.Equal(t, "enforced", enforcedData["guard_mode"])
}

// TestCapabilitiesDisclosesNothingElse scans the SERIALIZED body rather than
// trusting the struct: a field added to any nested type, or a value that arrives
// through a future CapabilityInfo, has to trip this.
func TestCapabilitiesDisclosesNothingElse(t *testing.T) {
	rec, data := probeCapabilities(t,
		fullInfo(&stubSetup{complete: true, clientID: "console-oauth-client-id"}), enforced())
	body := strings.ToLower(rec.Body.String())

	// Substrings that must never appear anywhere in the payload, whatever the
	// field is called.
	for _, forbidden := range []string{
		"private key", "private_key", "begin rsa", "begin ec", "begin private",
		"client_secret", "clientsecret", "secret\"", "jwks", "well-known",
		"tenant", "project", "permission", "scope", "core:", "bearer",
		"deployment_mode", "reason", "password", "token\":", "access_token",
	} {
		assert.NotContains(t, body, forbidden, "the anonymous probe must not disclose %q", forbidden)
	}

	// The exact key set, so an ADDITION fails as loudly as a substitution.
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, []string{
		"service", "version", "guard_transport", "guard_mode", "setup_complete", "auth", "console",
	}, keys, "adding a field to the anonymous probe needs the field-by-field argument in capabilities.go")

	auth, ok := data["auth"].(map[string]any)
	require.True(t, ok)
	authKeys := make([]string, 0, len(auth))
	for k := range auth {
		authKeys = append(authKeys, k)
	}
	assert.ElementsMatch(t, []string{"issuer", "audience", "token_endpoint", "authorize_endpoint"}, authKeys)

	console, ok := data["console"].(map[string]any)
	require.True(t, ok)
	consoleKeys := make([]string, 0, len(console))
	for k := range console {
		consoleKeys = append(consoleKeys, k)
	}
	assert.ElementsMatch(t, []string{"client_id"}, consoleKeys)
}

// TestCapabilitiesOmitsUnknownEndpoints: core often does not know the browser
// authorize page (it lives on the identity app, not on the issuer). Omitted, not
// blank — a console that receives "" cannot tell the two apart.
func TestCapabilitiesOmitsUnknownEndpoints(t *testing.T) {
	info := fullInfo(&stubSetup{})
	info.AuthTokenEndpoint = ""
	info.AuthAuthorizeEndpoint = ""

	_, data := probeCapabilities(t, info, enforced())
	auth, ok := data["auth"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"issuer":   "https://identity-api.auth.example.test",
		"audience": "https://core.example.test",
	}, auth)
}

// TestCapabilitiesMemoizesTheSetupRead. The cache is the rate limit: an
// anonymous endpoint must not be a database read per request.
func TestCapabilitiesMemoizesTheSetupRead(t *testing.T) {
	stub := &stubSetup{complete: false}
	info := fullInfo(stub)
	handler := newCapabilitiesHandler(info, enforced())
	for range 5 {
		handler.serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil))
	}
	assert.Equal(t, int32(1), stub.calls.Load(), "the not-yet-complete answer is cached for the TTL")

	// A completed setup is cached FOREVER, not for the TTL: it is a durable
	// one-shot, so a cached true can never become wrong.
	done := &stubSetup{complete: true, clientID: "c"}
	handler = newCapabilitiesHandler(fullInfo(done), enforced())
	for range 5 {
		handler.serve(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil))
	}
	assert.Equal(t, int32(1), done.calls.Load())
}

// A nil probe (no orchestrator wired) reports "not provisioned" rather than
// panicking: the probe must answer during an incident.
func TestCapabilitiesToleratesNoSetupProbe(t *testing.T) {
	info := fullInfo(nil)
	info.Setup = nil
	_, data := probeCapabilities(t, info, enforced())

	assert.Equal(t, false, data["setup_complete"])
	assert.Equal(t, "v1.2.3", data["version"])
}

func TestCapabilitiesVersionFallsBackToUnknown(t *testing.T) {
	info := fullInfo(&stubSetup{})
	info.Version = ""
	_, data := probeCapabilities(t, info, enforced())
	assert.Equal(t, "unknown", data["version"])
}
