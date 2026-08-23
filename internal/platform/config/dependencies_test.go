package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func saveDependencyGlobals(t *testing.T) {
	t.Helper()
	origEnv := AppEnv
	origJWKS := AuthJWKSURL
	origIssuer := AuthIssuer
	origAudience := AuthAudience
	origTokenURL := AuthTokenURL
	origSecret := SecretBaseURL
	t.Cleanup(func() {
		AppEnv = origEnv
		AuthJWKSURL = origJWKS
		AuthIssuer = origIssuer
		AuthAudience = origAudience
		AuthTokenURL = origTokenURL
		SecretBaseURL = origSecret
	})
}

func setDependencyGlobals(t *testing.T) {
	t.Helper()
	saveDependencyGlobals(t)
	AuthJWKSURL = "https://auth.example.com/.well-known/jwks.json"
	AuthIssuer = "https://auth.example.com"
	AuthAudience = "https://core.example.com"
	AuthTokenURL = "https://auth.example.com/oauth/token"
	SecretBaseURL = "https://secret.example.com"
}

func TestValidateDependencies(t *testing.T) {
	t.Run("production accepts a complete configuration", func(t *testing.T) {
		setDependencyGlobals(t)
		AppEnv = "production"
		require.NoError(t, validateDependencies())
	})

	// The gate has to relax in development or a contributor cannot run core
	// without a vault, which is the whole point of the development posture.
	t.Run("development permits everything missing", func(t *testing.T) {
		setDependencyGlobals(t)
		AppEnv = EnvDevelopment
		AuthJWKSURL, AuthIssuer, AuthAudience, AuthTokenURL, SecretBaseURL = "", "", "", "", ""
		require.NoError(t, validateDependencies())
	})

	t.Run("production refuses when auth and secret are unconfigured", func(t *testing.T) {
		setDependencyGlobals(t)
		AppEnv = "production"
		AuthJWKSURL, AuthIssuer, AuthAudience, AuthTokenURL, SecretBaseURL = "", "", "", "", ""

		err := validateDependencies()
		require.Error(t, err)
		// EVERY MISSING VARIABLE IN ONE ERROR. Reporting them one boot at a time
		// turns a five-minute setup into five restarts, so this is asserted
		// name-by-name rather than as "an error occurred".
		for _, name := range []string{
			"AUTH_JWKS_URL", "AUTH_ISSUER", "AUTH_AUDIENCE", "AUTH_TOKEN_URL", "SECRET_BASE_URL",
		} {
			assert.Contains(t, err.Error(), name)
		}
	})

	t.Run("production names only what is actually missing", func(t *testing.T) {
		setDependencyGlobals(t)
		AppEnv = "production"
		SecretBaseURL = ""

		err := validateDependencies()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SECRET_BASE_URL")
		assert.NotContains(t, err.Error(), "AUTH_JWKS_URL")
	})

	// A bearer token that can read every secret core wrote must not ride a
	// plaintext hop.
	t.Run("production refuses a plaintext secret origin", func(t *testing.T) {
		setDependencyGlobals(t)
		AppEnv = "production"
		SecretBaseURL = "http://secret.example.com"

		err := validateDependencies()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "https")
	})

	t.Run("development permits a plaintext secret origin", func(t *testing.T) {
		setDependencyGlobals(t)
		AppEnv = EnvDevelopment
		SecretBaseURL = "http://m9d-secret:8092"
		require.NoError(t, validateDependencies())
	})

	t.Run("production refuses a scheme-less secret origin", func(t *testing.T) {
		setDependencyGlobals(t)
		AppEnv = "production"
		SecretBaseURL = "secret.example.com"

		err := validateDependencies()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SECRET_BASE_URL")
	})
}

func TestInitDependencyGate(t *testing.T) {
	setBaseEnv := func(t *testing.T) {
		t.Helper()
		useEnvSecretManager(t)
		t.Setenv("SECRET_PROVIDER", "env")
		t.Setenv("APP_PUBLIC_HOSTNAME", "https://pub.example.com")
		t.Setenv("APP_PRIVATE_HOSTNAME", "https://priv.example.com")
		t.Setenv("DB_HOST", "localhost")
		t.Setenv("DB_PORT", "5432")
		t.Setenv("DB_USER", "postgres")
		t.Setenv("DB_PASSWORD", "pass")
		t.Setenv("DB_NAME", "coredb")
	}

	t.Run("production Init refuses and names every missing variable", func(t *testing.T) {
		saveDependencyGlobals(t)
		setBaseEnv(t)
		t.Setenv("APP_ENV", "production")

		err := Init()
		require.Error(t, err)
		for _, name := range []string{
			"AUTH_JWKS_URL", "AUTH_ISSUER", "AUTH_AUDIENCE", "AUTH_TOKEN_URL", "SECRET_BASE_URL",
		} {
			assert.Contains(t, err.Error(), name)
		}
	})

	t.Run("development Init permits them missing", func(t *testing.T) {
		saveDependencyGlobals(t)
		setBaseEnv(t)
		t.Setenv("APP_ENV", EnvDevelopment)

		require.NoError(t, Init())
	})

	t.Run("probe timeout is validated, not defaulted on a typo", func(t *testing.T) {
		saveDependencyGlobals(t)
		setBaseEnv(t)
		t.Setenv("APP_ENV", EnvDevelopment)
		t.Setenv("DEPENDENCY_PROBE_TIMEOUT", "0s")

		err := Init()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "DEPENDENCY_PROBE_TIMEOUT")
	})
}
