package event

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/maintainerd/core/internal/platform/response"
)

// Handler exposes the platform-event HTTP API.
//
// READ-ONLY on purpose. Events are emitted by the platform's own loops as a
// record of what it observed; an API caller that could POST one could forge
// evidence, and one that could DELETE one could erase an incident. The route
// table therefore registers no mutating verb at all — the write permission in
// the authz map exists so a future write surface has to be granted explicitly
// instead of inheriting the read grant.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the chi router for platform events.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.list)
	return r
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, total, err := h.svc.List(r.Context(), page, limit)
	if err != nil {
		response.HandleServiceError(w, r, "Failed to list platform events", err)
		return
	}
	response.Success(w, map[string]any{"items": items, "total": total}, "")
}
