package setup

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/maintainerd/core/internal/platform/response"
)

// Handler exposes the setup status + a manual trigger. Core also runs the
// orchestration on boot when SETUP_ENABLED is set; these endpoints let an
// operator (or the console) inspect and re-drive it.
type Handler struct {
	orch *Orchestrator
}

func NewHandler(orch *Orchestrator) *Handler { return &Handler{orch: orch} }

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/status", h.status)
	r.Post("/", h.trigger)
	return r
}

type statusResponse struct {
	Enabled   bool    `json:"enabled"`
	Completed bool    `json:"completed"`
	Result    *Result `json:"result,omitempty"`
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	completed, res := h.orch.Status(r.Context())
	response.Success(w, statusResponse{Enabled: h.orch.Enabled(), Completed: completed, Result: res}, "")
}

func (h *Handler) trigger(w http.ResponseWriter, r *http.Request) {
	res, err := h.orch.Run(r.Context())
	if err != nil {
		response.HandleServiceError(w, r, "setup failed", err)
		return
	}
	response.Success(w, res, "setup complete")
}
