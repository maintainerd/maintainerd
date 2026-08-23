package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/dependency"
	"github.com/maintainerd/core/internal/platform/authz"
	"github.com/maintainerd/core/internal/platform/config"
	"github.com/maintainerd/core/internal/secretctrl"
)

func setProbeTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := config.DependencyProbeTimeout
	config.DependencyProbeTimeout = d
	t.Cleanup(func() { config.DependencyProbeTimeout = prev })
}

func TestProbeAuthReadsTheJWKS(t *testing.T) {
	setProbeTimeout(t, 2*time.Second)

	t.Run("a served key set is reachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"keys":[]}`))
		}))
		defer srv.Close()
		setAuthVars(t, srv.URL, "https://auth", "aud")

		require.NoError(t, probeAuth(context.Background()))
	})

	// An auth that answers /healthz while its JWKS 404s is not usable by core.
	// Probing the key set rather than liveness is what makes that visible.
	t.Run("a missing key set is unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		setAuthVars(t, srv.URL, "https://auth", "aud")

		require.Error(t, probeAuth(context.Background()))
	})

	t.Run("a closed port is unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()
		setAuthVars(t, url, "https://auth", "aud")

		require.Error(t, probeAuth(context.Background()))
	})
}

// The probe set follows CONFIGURATION, not hope: an unconfigured dependency is
// not probed, because a permanently-unhealthy check would make /readyz useless on
// a developer's laptop. Outside development config.Init has already refused the
// unconfigured state.
func TestDependencyProbesFollowConfiguration(t *testing.T) {
	t.Run("both configured", func(t *testing.T) {
		setAuthVars(t, "https://auth/jwks", "https://auth", "aud")
		secret := secretctrl.New(secretctrl.Config{BaseURL: "https://secret.example.com"}, nil)

		names := dependency.New(dependencyProbes(secret), dependency.Options{}).Names()
		assert.Equal(t, []string{"auth", "secret"}, names)
	})

	t.Run("neither configured", func(t *testing.T) {
		setAuthVars(t, "", "", "")
		names := dependency.New(dependencyProbes(secretctrl.New(secretctrl.Config{}, nil)), dependency.Options{}).Names()
		assert.Empty(t, names)
	})
}

func TestReadinessChecksNameDependenciesOnly(t *testing.T) {
	setAuthVars(t, "https://auth/jwks", "https://auth", "aud")
	secret := secretctrl.New(secretctrl.Config{BaseURL: "https://secret.example.com"}, nil)
	rec := dependency.New(dependencyProbes(secret), dependency.Options{})

	// nil pool: the database probe's closure is never invoked here, and passing a
	// real pool would make this a database test instead of a wiring one.
	checks := readinessChecks(nil, rec, authz.Guard{Mode: authz.ModeEnforced}, secret, nil)

	names := make([]string, 0, len(checks))
	for _, c := range checks {
		names = append(names, c.Name)
	}
	// "secret" and "secret-setup" are deliberately separate: the first answers
	// "can core reach the vault", the second "has the vault been provisioned".
	// They fail independently, and a vault that answers every network probe while
	// refusing every real call is exactly the state one check cannot express.
	assert.Equal(t, []string{"database", "auth", "secret", "secret-setup"}, names,
		"the readiness surface names dependencies — never an address, a driver message or a credential")

	// The property the names carry: no check may leak where a dependency lives or
	// what talks to it. Written as a scan rather than trusting the list above, so a
	// new check with an address in its name fails here rather than in production.
	for _, name := range names {
		assert.NotContains(t, name, ".", "readiness check %q looks like a hostname", name)
		assert.NotContains(t, name, ":", "readiness check %q looks like an address", name)
		assert.NotContains(t, name, "/", "readiness check %q looks like a URL", name)
	}
}

func TestReadinessChecksReflectDependencyState(t *testing.T) {
	setAuthVars(t, "https://auth/jwks", "https://auth", "aud")
	secret := secretctrl.New(secretctrl.Config{BaseURL: "https://secret.example.com"}, nil)
	rec := dependency.New(dependencyProbes(secret), dependency.Options{})
	checks := readinessChecks(nil, rec, authz.Guard{Mode: authz.ModeEnforced}, secret, nil)

	// Before the first convergence pass, both dependencies are not-ready.
	require.ErrorIs(t, checks[1].Probe(context.Background()), dependency.ErrNotProbed)
	require.ErrorIs(t, checks[2].Probe(context.Background()), dependency.ErrNotProbed)
}

// A guard that cannot verify a token must not report ready: this replica cannot
// authorize a request, so it must not be receiving them.
func TestReadinessChecksFailClosedOnAnUnavailableGuard(t *testing.T) {
	setAuthVars(t, "", "", "")
	secret := secretctrl.New(secretctrl.Config{}, nil)
	rec := dependency.New(dependencyProbes(secret), dependency.Options{})

	checks := readinessChecks(nil, rec, authz.Guard{Mode: authz.ModeUnavailable, Reason: "missing/unusable AUTH_JWKS_URL"}, secret, nil)
	require.Error(t, checks[1].Probe(context.Background()))
}

// Development with no vault and a deliberately open guard is READY. It has
// already announced both at boot, and any provision needing secret material
// still fails closed.
func TestReadinessChecksAreReadyInDevelopmentWithoutDependencies(t *testing.T) {
	setAuthVars(t, "", "", "")
	secret := secretctrl.New(secretctrl.Config{}, nil)
	rec := dependency.New(dependencyProbes(secret), dependency.Options{})

	checks := readinessChecks(nil, rec, authz.Guard{Mode: authz.ModeDevOpen}, secret, nil)
	assert.NoError(t, checks[1].Probe(context.Background()))
	assert.NoError(t, checks[2].Probe(context.Background()))
}
