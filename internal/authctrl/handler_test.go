package authctrl

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/steward"
)

func newTestRunner(store ControlPlaneStore) *Runner {
	return NewRunner(
		New(Config{TokenURL: "https://auth.local/token", GRPCAddr: "auth:50051"}, store),
		steward.Catalog{},
		newMemKeyStore(),
		&memRegistry{},
	)
}

func doRequest(t *testing.T, h *Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestApplyReportsAnUnprovisionedInstallAsAConflict(t *testing.T) {
	h := NewHandler(newTestRunner(&fakeControlPlaneStore{err: pgx.ErrNoRows}))

	rec := doRequest(t, h, http.MethodPost, "/apply")
	// Not a 500: core is fine, the install simply has not been set up yet, and
	// the operator's next action is to run setup.
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "run setup")
}

func TestStatusIsEmptyBeforeAnyPass(t *testing.T) {
	h := NewHandler(newTestRunner(&fakeControlPlaneStore{err: pgx.ErrNoRows}))

	rec := doRequest(t, h, http.MethodGet, "/status")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data statusResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Nil(t, body.Data.LastRunAt)
	assert.Nil(t, body.Data.Report)
}

func TestStatusReportsTheLastPass(t *testing.T) {
	runner := newTestRunner(&fakeControlPlaneStore{row: provisionedRow(t)})
	h := NewHandler(runner)

	// An empty catalog converges without touching auth, which is enough to prove
	// the report is recorded and surfaced.
	report, err := runner.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, report)

	rec := doRequest(t, h, http.MethodGet, "/status")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data statusResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Data.Report)
	require.NotNil(t, body.Data.LastRunAt)
	// The transport is part of the answer: an operator has to be able to tell
	// setup-window provisioning from control-client provisioning.
	assert.Equal(t, TransportControlClient, body.Data.Report.Transport)
	assert.Equal(t, 0, body.Data.Report.Failed)
}

func TestRunIsSingleFlight(t *testing.T) {
	runner := newTestRunner(&fakeControlPlaneStore{row: provisionedRow(t)})
	// Claim the slot the way an in-flight pass would.
	require.True(t, runner.tryBegin())
	defer runner.end(nil)

	_, err := runner.Run(t.Context())
	// Two interleaved passes would each see the other's half-written state, so
	// the second is refused rather than raced.
	require.ErrorIs(t, err, ErrApplyRunning)

	rec := doRequest(t, NewHandler(runner), http.MethodPost, "/apply")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "already running")
}

func TestRunSurfacesTheNoIdentitySentinel(t *testing.T) {
	runner := newTestRunner(&fakeControlPlaneStore{err: pgx.ErrNoRows})
	_, err := runner.Run(t.Context())
	// Unwrapped, so the boot loop can log-and-retry instead of treating a fresh
	// install as a broken one.
	require.ErrorIs(t, err, ErrNoControlIdentity)
	assert.Nil(t, runner.Last())
}
