package service

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/maintainerd/core/internal/platform/response"
)

// Handler exposes the service-registry HTTP API.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the chi router for the service resource.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Get("/system", h.listSystem)
	r.Post("/", h.create)
	r.Get("/{uuid}", h.get)
	r.Patch("/{uuid}", h.updateStatus)
	r.Delete("/{uuid}", h.remove)
	return r
}

type createRequest struct {
	TenantUUID uuid.UUID      `json:"tenant_uuid"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	IsSystem   bool           `json:"is_system"`
	Endpoint   string         `json:"endpoint"`
	Version    string         `json:"version"`
	Metadata   map[string]any `json:"metadata"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequestBody(w)
		return
	}
	reg, err := h.svc.Create(r.Context(), CreateInput{
		TenantUUID: req.TenantUUID,
		Name:       req.Name,
		Kind:       req.Kind,
		IsSystem:   req.IsSystem,
		Endpoint:   req.Endpoint,
		Version:    req.Version,
		Metadata:   req.Metadata,
	})
	if err != nil {
		response.HandleServiceError(w, r, "Failed to register service", err)
		return
	}
	response.Created(w, reg, "Service registered")
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		response.BadRequest(w)
		return
	}
	reg, err := h.svc.Get(r.Context(), id)
	if err != nil {
		response.HandleServiceError(w, r, "Failed to fetch service", err)
		return
	}
	response.Success(w, reg, "")
}

// list requires a ?tenant={uuid} query param — services are always scoped to a tenant.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantUUID, err := uuid.Parse(r.URL.Query().Get("tenant"))
	if err != nil {
		response.Error(w, http.StatusBadRequest, "A valid ?tenant={uuid} query parameter is required")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, total, err := h.svc.ListByTenant(r.Context(), tenantUUID, page, limit)
	if err != nil {
		response.HandleServiceError(w, r, "Failed to list services", err)
		return
	}
	response.Success(w, map[string]any{"items": items, "total": total}, "")
}

// listSystem returns the platform's system services (undeletable, always-on).
func (h *Handler) listSystem(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListSystem(r.Context())
	if err != nil {
		response.HandleServiceError(w, r, "Failed to list system services", err)
		return
	}
	response.Success(w, map[string]any{"items": items}, "")
}

type updateStatusRequest struct {
	Status   string `json:"status"`
	Endpoint string `json:"endpoint"`
}

func (h *Handler) updateStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		response.BadRequest(w)
		return
	}
	var req updateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequestBody(w)
		return
	}
	reg, err := h.svc.UpdateStatus(r.Context(), id, UpdateStatusInput{
		Status:   req.Status,
		Endpoint: req.Endpoint,
	})
	if err != nil {
		response.HandleServiceError(w, r, "Failed to update service", err)
		return
	}
	response.Success(w, reg, "Service updated")
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		response.BadRequest(w)
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		response.HandleServiceError(w, r, "Failed to delete service", err)
		return
	}
	response.Success(w, nil, "Service deregistered")
}
