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

func TestMissingAuthVars(t *testing.T) {
	setAuthVars(t, "", "https://auth", "aud")
	assert.Equal(t, []string{"AUTH_JWKS_URL"}, missingAuthVars())

	setAuthVars(t, "https://auth/jwks", "https://auth", "aud")
	assert.Empty(t, missingAuthVars())
}
