package project

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/maintainerd/core/internal/platform/response"
)

// Handler exposes the project HTTP API.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{uuid}", h.get)
	r.Patch("/{uuid}", h.update)
	r.Delete("/{uuid}", h.remove)
	return r
}

type createRequest struct {
	TenantUUID  uuid.UUID      `json:"tenant_uuid"`
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Metadata    map[string]any `json:"metadata"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequestBody(w)
		return
	}
	p, err := h.svc.Create(r.Context(), CreateInput{
		TenantUUID:  req.TenantUUID,
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Metadata:    req.Metadata,
	})
	if err != nil {
		response.HandleServiceError(w, r, "Failed to create project", err)
		return
	}
	response.Created(w, p, "Project created")
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		response.BadRequest(w)
		return
	}
	p, err := h.svc.Get(r.Context(), id)
	if err != nil {
		response.HandleServiceError(w, r, "Failed to fetch project", err)
		return
	}
	response.Success(w, p, "")
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantUUID, err := uuid.Parse(r.URL.Query().Get("tenant"))
	if err != nil {
		response.Error(w, http.StatusBadRequest, "A valid ?tenant={uuid} query parameter is required")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, total, err := h.svc.List(r.Context(), tenantUUID, page, limit)
	if err != nil {
		response.HandleServiceError(w, r, "Failed to list projects", err)
		return
	}
	response.Success(w, map[string]any{"items": items, "total": total}, "")
}

type updateRequest struct {
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Status      string         `json:"status"`
	Metadata    map[string]any `json:"metadata"`
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
	p, err := h.svc.Update(r.Context(), id, UpdateInput{
		DisplayName: req.DisplayName,
		Description: req.Description,
		Status:      req.Status,
		Metadata:    req.Metadata,
	})
	if err != nil {
		response.HandleServiceError(w, r, "Failed to update project", err)
		return
	}
	response.Success(w, p, "Project updated")
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		response.BadRequest(w)
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		response.HandleServiceError(w, r, "Failed to delete project", err)
		return
	}
	response.Success(w, nil, "Project deleted")
}
