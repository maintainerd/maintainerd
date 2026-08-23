package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func probeReadyz(t *testing.T, checks []ReadinessCheck) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	readyz(checks)(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return rec.Code, body
}

func TestReadyzReportsEveryDependency(t *testing.T) {
	status, body := probeReadyz(t, []ReadinessCheck{
		{Name: "database", Probe: func(context.Context) error { return nil }},
		{Name: "auth", Probe: func(context.Context) error { return nil }},
		{Name: "secret", Probe: func(context.Context) error { return nil }},
	})

	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ready", body["status"])
	assert.Equal(t, map[string]any{"database": "ok", "auth": "ok", "secret": "ok"}, body["checks"])
}

func TestReadyzFailsClosedAndNamesTheDependency(t *testing.T) {
	status, body := probeReadyz(t, []ReadinessCheck{
		{Name: "database", Probe: func(context.Context) error { return nil }},
		{Name: "secret", Probe: func(context.Context) error { return errors.New("unavailable") }},
	})

	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "not_ready", body["status"])
	checks, ok := body["checks"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ok", checks["database"])
	assert.Equal(t, "unhealthy", checks["secret"])
}

// THE DISCLOSURE RULE. /readyz is unauthenticated by construction (it is mounted
// outside the /api/v1 guard), so its body may name a DEPENDENCY and nothing else.
// The cause — which can carry a host, a database name, a driver message or a
// credential — goes to the log only.
func TestReadyzBodyNamesADependencyAndNothingElse(t *testing.T) {
	leaky := errors.New(`dial tcp 10.9.8.7:5432: FATAL: password authentication failed for user "core" (password=corepass)`)

	rec := httptest.NewRecorder()
	readyz([]ReadinessCheck{{Name: "database", Probe: func(context.Context) error { return leaky }}})(
		rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	raw := rec.Body.String()
	assert.Contains(t, raw, "database")
	for _, forbidden := range []string{"10.9.8.7", "5432", "corepass", "password", "FATAL", "core"} {
		assert.NotContains(t, raw, forbidden,
			"the readiness body must not disclose an address, a driver message or a credential")
	}
}

// A probe that ignores its context is reported as failing when the deadline
// passes rather than waited on: a readiness endpoint that hangs is read as
// healthy by some orchestrators and unhealthy by others.
func TestReadyzDoesNotHangOnAnUnboundedProbe(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Millisecond)
	defer cancel()

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		readyz([]ReadinessCheck{{Name: "secret", Probe: func(context.Context) error {
			<-release
			return nil
		}}})(rec, req.WithContext(ctx))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("/readyz hung on a probe that ignored its context")
	}
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// With no checks at all /readyz degenerates to /healthz, which is the honest
// answer for a server with no dependencies. Asserted so it stays a deliberate
// choice rather than an accident of an empty slice.
func TestReadyzWithNoChecksIsReady(t *testing.T) {
	status, body := probeReadyz(t, nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ready", body["status"])
}
