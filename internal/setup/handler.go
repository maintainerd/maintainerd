package setup

import (
	"encoding/json"
	"errors"
	"io"
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
	// Body is optional: the wizard sends the tenant + admin; an empty body falls
	// back to the env-configured defaults.
	var in RunInput
	if body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20)); len(body) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			response.BadRequestBody(w)
			return
		}
	}
	if in.AdminPassword == "" && !h.orch.hasEnvAdminPassword() {
		response.ValidationError(w, errors.New("admin_password is required"))
		return
	}
	res, err := h.orch.RunWith(r.Context(), in)
	if err != nil {
		response.HandleServiceError(w, r, "setup failed", err)
		return
	}
	response.Success(w, res, "setup complete")
}
