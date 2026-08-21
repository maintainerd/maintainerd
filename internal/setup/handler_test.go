package setup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/platform/authz"
	"github.com/maintainerd/core/internal/storage"
)

func completedStore(t *testing.T) *fakeControlPlaneStore {
	t.Helper()
	data, err := json.Marshal(&Result{ControlClientID: "cc-1", ConsoleClientID: "con-1"})
	require.NoError(t, err)
	return &fakeControlPlaneStore{has: true, row: storage.ControlPlane{
		Data:             data,
		DeploymentMode:   "docker",
		SetupCompletedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}}
}

func adminVerify(perm string) authz.VerifyFunc {
	return func(_ context.Context, token string) (*authz.Claims, error) {
		if token != "valid-admin" {
			return nil, errors.New("invalid")
		}
		return &authz.Claims{Subject: "admin", Permissions: []string{perm}}, nil
	}
}

func getStatus(t *testing.T, h *Handler, mutate func(*http.Request)) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Data
}

func TestStatusSlimsForUnauthenticatedCallers(t *testing.T) {
	h := NewHandler(newTestOrchestrator(completedStore(t), Config{}),
		Gate{Token: "sekrit", Verify: adminVerify("core:admin")})

	tests := []struct {
		name     string
		mutate   func(*http.Request)
		wantFull bool
	}{
		{"anonymous gets only completed", nil, false},
		{"wrong setup token gets only completed", func(r *http.Request) {
			r.Header.Set(setupTokenHeader, "guess")
		}, false},
		{"invalid bearer gets only completed", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer forged")
		}, false},
		{"bearer without core:admin gets only completed", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer valid-admin")
		}, false}, // verifier below maps valid-admin → core:other
		{"setup token gets the full result", func(r *http.Request) {
			r.Header.Set(setupTokenHeader, "sekrit")
		}, true},
		{"admin token gets the full result", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer valid-admin")
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := h
			if strings.Contains(tt.name, "without core:admin") {
				handler = NewHandler(newTestOrchestrator(completedStore(t), Config{}),
					Gate{Token: "sekrit", Verify: adminVerify("core:other")})
			}
			data := getStatus(t, handler, tt.mutate)
			assert.Equal(t, true, data["completed"])
			if tt.wantFull {
				require.Contains(t, data, "result", "authorized caller should see the full result")
				assert.Equal(t, "docker", data["deployment_mode"])
			} else {
				// The slim shape must not leak control-plane IDs, the JWKS, or
				// the substrate — they map the install for an attacker.
				assert.NotContains(t, data, "result")
				assert.NotContains(t, data, "deployment_mode")
				assert.NotContains(t, data, "enabled")
			}
		})
	}
}

func postSetup(t *testing.T, h *Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"admin_password":"pw"}`))
	if token != "" {
		req.Header.Set(setupTokenHeader, token)
	}
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)
	return rec
}

func TestTriggerGate(t *testing.T) {
	t.Run("empty token outside development disables setup", func(t *testing.T) {
		h := NewHandler(newTestOrchestrator(&fakeControlPlaneStore{}, Config{}), Gate{Token: "", Dev: false})
		rec := postSetup(t, h, "any")
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Contains(t, rec.Body.String(), "CORE_SETUP_TOKEN")
	})

	t.Run("missing token is unauthorized", func(t *testing.T) {
		h := NewHandler(newTestOrchestrator(&fakeControlPlaneStore{}, Config{}), Gate{Token: "sekrit"})
		assert.Equal(t, http.StatusUnauthorized, postSetup(t, h, "").Code)
	})

	t.Run("wrong token is unauthorized", func(t *testing.T) {
		h := NewHandler(newTestOrchestrator(&fakeControlPlaneStore{}, Config{}), Gate{Token: "sekrit"})
		assert.Equal(t, http.StatusUnauthorized, postSetup(t, h, "guess").Code)
	})

	t.Run("correct token passes the gate", func(t *testing.T) {
		h := NewHandler(newTestOrchestrator(&fakeControlPlaneStore{}, Config{}), Gate{Token: "sekrit"})
		rec := postSetup(t, h, "sekrit")
		// Past the gate the run fails on the unset AUTH_SETUP_ADDR — a 502,
		// which proves authorization succeeded.
		assert.Equal(t, http.StatusBadGateway, rec.Code)
		assert.Contains(t, rec.Body.String(), "AUTH_SETUP_ADDR")
	})

	t.Run("development with empty token stays open", func(t *testing.T) {
		h := NewHandler(newTestOrchestrator(&fakeControlPlaneStore{}, Config{}), Gate{Token: "", Dev: true})
		rec := postSetup(t, h, "")
		assert.Equal(t, http.StatusBadGateway, rec.Code) // reached the orchestrator
	})

	t.Run("concurrent run is a conflict", func(t *testing.T) {
		o := newTestOrchestrator(&fakeControlPlaneStore{}, Config{})
		require.True(t, o.tryBegin())
		defer o.end()
		h := NewHandler(o, Gate{Token: "sekrit"})
		rec := postSetup(t, h, "sekrit")
		assert.Equal(t, http.StatusConflict, rec.Code)
	})
}

func TestGateTokenMatchesIsExactAndNonEmpty(t *testing.T) {
	g := Gate{Token: "sekrit"}
	assert.True(t, g.tokenMatches("sekrit"))
	assert.False(t, g.tokenMatches("sekri"))
	assert.False(t, g.tokenMatches("sekrit "))
	assert.False(t, g.tokenMatches(""))
	// An empty configured token must never match anything — otherwise an
	// unset env var would turn into a match-all credential.
	assert.False(t, Gate{}.tokenMatches(""))
	assert.False(t, Gate{}.tokenMatches("anything"))
}
