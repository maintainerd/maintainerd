package authctrl

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/maintainerd/core/internal/platform/response"
)

// Handler exposes the control catalog's reconcile loop to an operator.
//
// Both routes sit under /api/v1 behind the platform authz middleware and
// require core:admin — they read and write the platform's IAM wiring, which is
// the most privileged surface core has.
type Handler struct {
	runner *Runner
}

func NewHandler(runner *Runner) *Handler { return &Handler{runner: runner} }

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/apply", h.apply)
	r.Get("/status", h.status)
	return r
}

type statusResponse struct {
	// LastRunAt is nil until a pass has run in this process. The report is
	// in-memory by design: it describes THIS process's convergence, and a stale
	// report read from a database would claim a state nobody verified.
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	Report    *Report    `json:"report,omitempty"`
}

func (h *Handler) apply(w http.ResponseWriter, r *http.Request) {
	report, err := h.runner.Run(r.Context())
	switch {
	case errors.Is(err, ErrApplyRunning):
		response.Error(w, http.StatusConflict, "a steward apply is already running")
		return
	case errors.Is(err, ErrNoControlIdentity):
		// Not an error in core — the install simply has not been provisioned yet.
		// 409 rather than 500 so an operator reads it as "run setup first".
		response.Error(w, http.StatusConflict,
			"core has no control-plane identity yet; run setup before applying the control catalog")
		return
	case err != nil:
		response.Error(w, http.StatusBadGateway, err.Error())
		return
	}
	// A pass with per-object failures still returns 200 with the report: the
	// caller needs the detail of WHICH objects failed, and some succeeded.
	response.Success(w, report, "")
}

func (h *Handler) status(w http.ResponseWriter, _ *http.Request) {
	report := h.runner.Last()
	out := statusResponse{Report: report}
	if report != nil {
		at := report.StartedAt
		out.LastRunAt = &at
	}
	response.Success(w, out, "")
}
