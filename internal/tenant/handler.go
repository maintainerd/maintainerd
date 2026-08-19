package tenant

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/maintainerd/core/internal/platform/response"
)

// Handler exposes the tenant HTTP API.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns the chi router for the tenant resource.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Get("/system", h.getSystem)
	r.Post("/", h.create)
	r.Get("/{uuid}", h.get)
	r.Patch("/{uuid}", h.update)
	r.Delete("/{uuid}", h.remove)
	return r
}

type createRequest struct {
	Name           string         `json:"name"`
	DisplayName    string         `json:"display_name"`
	Status         string         `json:"status"`
	IsSystem       bool           `json:"is_system"`
	AuthTenantUUID *uuid.UUID     `json:"auth_tenant_uuid"`
	Metadata       map[string]any `json:"metadata"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequestBody(w)
		return
	}
	t, err := h.svc.Create(r.Context(), CreateInput{
		Name:           req.Name,
		DisplayName:    req.DisplayName,
		Status:         req.Status,
		IsSystem:       req.IsSystem,
		AuthTenantUUID: req.AuthTenantUUID,
		Metadata:       req.Metadata,
	})
	if err != nil {
		response.HandleServiceError(w, r, "Failed to create tenant", err)
		return
	}
	response.Created(w, t, "Tenant created")
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		response.BadRequest(w)
		return
	}
	t, err := h.svc.Get(r.Context(), id)
	if err != nil {
		response.HandleServiceError(w, r, "Failed to fetch tenant", err)
		return
	}
	response.Success(w, t, "")
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, total, err := h.svc.List(r.Context(), page, limit)
	if err != nil {
		response.HandleServiceError(w, r, "Failed to list tenants", err)
		return
	}
	response.Success(w, map[string]any{"items": items, "total": total}, "")
}

// getSystem returns the platform's root (system) tenant.
func (h *Handler) getSystem(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.GetSystem(r.Context())
	if err != nil {
		response.HandleServiceError(w, r, "Failed to fetch system tenant", err)
		return
	}
	response.Success(w, t, "")
}

type updateRequest struct {
	DisplayName    string         `json:"display_name"`
	Status         string         `json:"status"`
	AuthTenantUUID *uuid.UUID     `json:"auth_tenant_uuid"`
	Metadata       map[string]any `json:"metadata"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		response.BadRequest(w)
		return
	}
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequestBody(w)
		return
	}
	t, err := h.svc.Update(r.Context(), id, UpdateInput{
		DisplayName:    req.DisplayName,
		Status:         req.Status,
		AuthTenantUUID: req.AuthTenantUUID,
		Metadata:       req.Metadata,
	})
	if err != nil {
		response.HandleServiceError(w, r, "Failed to update tenant", err)
		return
	}
	response.Success(w, t, "Tenant updated")
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		response.BadRequest(w)
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		response.HandleServiceError(w, r, "Failed to delete tenant", err)
		return
	}
	response.Success(w, nil, "Tenant deleted")
}
