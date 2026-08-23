package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/maintainerd/core/internal/grpcserver"
	"github.com/maintainerd/core/internal/platform/authz"
	"github.com/maintainerd/core/internal/platform/config"
)

// setAuthVars overrides the package-level auth config for the duration of a
// test (the config package documents direct var manipulation as the test
// seam), restoring the previous values afterwards.
func setAuthVars(t *testing.T, jwks, issuer, audience string) {
	t.Helper()
	pj, pi, pa := config.AuthJWKSURL, config.AuthIssuer, config.AuthAudience
	config.AuthJWKSURL, config.AuthIssuer, config.AuthAudience = jwks, issuer, audience
	t.Cleanup(func() { config.AuthJWKSURL, config.AuthIssuer, config.AuthAudience = pj, pi, pa })
}

// TestGuardResolutionFailsClosed pins the security ladder: with no verifier,
// production semantics must NEVER serve an open surface — the HTTP API goes
// 503 and the AgentGateway goes health-only. Development degrades to open,
// which is the documented (and loudly logged) exception.
func TestGuardResolutionFailsClosed(t *testing.T) {
	setAuthVars(t, "", "", "")

	t.Run("production without auth config", func(t *testing.T) {
		httpGuard := resolveHTTPGuard(nil, false)
		assert.Equal(t, authz.ModeUnavailable, httpGuard.Mode)
		assert.Contains(t, httpGuard.Reason, "AUTH_JWKS_URL")

		grpcGuard := resolveGRPCGuard(nil, false)
		assert.Equal(t, grpcserver.GuardHealthOnly, grpcGuard.Mode)
		assert.False(t, grpcGuard.Dev, "reflection must stay off outside development")
	})

	t.Run("development without auth config", func(t *testing.T) {
		httpGuard := resolveHTTPGuard(nil, true)
		assert.Equal(t, authz.ModeDevOpen, httpGuard.Mode)

		grpcGuard := resolveGRPCGuard(nil, true)
		assert.Equal(t, grpcserver.GuardDevOpen, grpcGuard.Mode)
		assert.True(t, grpcGuard.Dev)
	})
}

// TestGuardLaddersDiverge is why GET /api/v1/capabilities names the transport it
// is describing. The two guards are resolved independently from one verifier and
// their third states are not the same state: with no auth config in production
// the HTTP guard is "unavailable" (503 on every guarded route) while the
// AgentGateway is health-only (the service does not register at all). No single
// guard_mode value can honestly describe the process.
func TestGuardLaddersDiverge(t *testing.T) {
	setAuthVars(t, "", "", "")

	assert.Equal(t, "unavailable", resolveHTTPGuard(nil, false).Mode.Name())
	assert.Equal(t, grpcserver.GuardHealthOnly, resolveGRPCGuard(nil, false).Mode,
		"the gRPC ladder has no 'unavailable' — the capability payload must not imply it does")
}

// TestCapabilityInfoSourcesEveryValueFromCore pins that the probe publishes core's
// OWN configuration rather than anything hand-set on the console, and that the
// browser authorize page is omitted when core has no identity-app origin to
// publish (it is NOT derivable from the issuer — see authorizePageURL).
func TestCapabilityInfoSourcesEveryValueFromCore(t *testing.T) {
	setAuthVars(t, "http://auth:8081/.well-known/jwks.json",
		"https://identity-api.auth.example.test", "https://core.example.test")

	prevToken, prevIdentity, prevVersion := config.AuthTokenURL, config.AppFrontendIdentityHostname, config.AppVersion
	t.Cleanup(func() {
		config.AuthTokenURL, config.AppFrontendIdentityHostname, config.AppVersion = prevToken, prevIdentity, prevVersion
	})
	config.AuthTokenURL = "https://identity-api.auth.example.test/api/v1/oauth/token"
	config.AppFrontendIdentityHostname = "https://identity.auth.example.test/"
	config.AppVersion = "v9.9.9"

	info := capabilityInfo(nil)
	assert.Equal(t, "v9.9.9", info.Version)
	assert.Equal(t, "https://identity-api.auth.example.test", info.AuthIssuer)
	assert.Equal(t, "https://core.example.test", info.AuthAudience)
	assert.Equal(t, "https://identity-api.auth.example.test/api/v1/oauth/token", info.AuthTokenEndpoint)
	assert.Equal(t, "https://identity.auth.example.test/authorize", info.AuthAuthorizeEndpoint)

	config.AppFrontendIdentityHostname = ""
	assert.Empty(t, capabilityInfo(nil).AuthAuthorizeEndpoint,
		"with no identity-app origin the field must be omitted, not guessed from the issuer")
}

func TestAuthorizePageURL(t *testing.T) {
	assert.Equal(t, "https://identity.example.test/authorize", authorizePageURL("https://identity.example.test"))
	assert.Equal(t, "https://identity.example.test/authorize", authorizePageURL(" https://identity.example.test// "))
	assert.Empty(t, authorizePageURL(""))
	assert.Empty(t, authorizePageURL("   "))
}

func TestMissingAuthVars(t *testing.T) {
	setAuthVars(t, "", "https://auth", "aud")
	assert.Equal(t, []string{"AUTH_JWKS_URL"}, missingAuthVars())

	setAuthVars(t, "https://auth/jwks", "https://auth", "aud")
	assert.Empty(t, missingAuthVars())
}
