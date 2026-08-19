package resource

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/maintainerd/core/internal/platform/response"
)

// Handler exposes the resource HTTP API.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{uuid}", h.get)
	r.Patch("/{uuid}", h.updateSpec)
	r.Patch("/{uuid}/status", h.updateStatus)
	r.Delete("/{uuid}", h.remove)
	return r
}

type createRequest struct {
	ProjectUUID  uuid.UUID      `json:"project_uuid"`
	ProviderUUID *uuid.UUID     `json:"provider_uuid"`
	Kind         string         `json:"kind"`
	Name         string         `json:"name"`
	Spec         map[string]any `json:"spec"`
	Metadata     map[string]any `json:"metadata"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequestBody(w)
		return
	}
	res, err := h.svc.Create(r.Context(), CreateInput{
		ProjectUUID:  req.ProjectUUID,
		ProviderUUID: req.ProviderUUID,
		Kind:         req.Kind,
		Name:         req.Name,
		Spec:         req.Spec,
		Metadata:     req.Metadata,
	})
	if err != nil {
		response.HandleServiceError(w, r, "Failed to create resource", err)
		return
	}
	response.Created(w, res, "Resource created")
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		response.BadRequest(w)
		return
	}
	res, err := h.svc.Get(r.Context(), id)
	if err != nil {
		response.HandleServiceError(w, r, "Failed to fetch resource", err)
		return
	}
	response.Success(w, res, "")
}

// list requires a ?project={uuid} query param — resources are always scoped to a project.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	projectUUID, err := uuid.Parse(r.URL.Query().Get("project"))
	if err != nil {
		response.Error(w, http.StatusBadRequest, "A valid ?project={uuid} query parameter is required")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, total, err := h.svc.ListByProject(r.Context(), projectUUID, page, limit)
	if err != nil {
		response.HandleServiceError(w, r, "Failed to list resources", err)
		return
	}
	response.Success(w, map[string]any{"items": items, "total": total}, "")
}

type updateSpecRequest struct {
	Spec     map[string]any `json:"spec"`
	Metadata map[string]any `json:"metadata"`
}

func (h *Handler) updateSpec(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		response.BadRequest(w)
		return
	}
	var req updateSpecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.BadRequestBody(w)
		return
	}
	res, err := h.svc.UpdateSpec(r.Context(), id, UpdateSpecInput{
		Spec:     req.Spec,
		Metadata: req.Metadata,
	})
	if err != nil {
		response.HandleServiceError(w, r, "Failed to update resource", err)
		return
	}
	response.Success(w, res, "Resource updated")
}

type updateStatusRequest struct {
	Status             map[string]any `json:"status"`
	State              string         `json:"state"`
	ObservedGeneration int64          `json:"observed_generation"`
}

// updateStatus is the observed-state callback used by the reconciler/agent.
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
	res, err := h.svc.UpdateStatus(r.Context(), id, UpdateStatusInput{
		Status:             req.Status,
		State:              req.State,
		ObservedGeneration: req.ObservedGeneration,
	})
	if err != nil {
		response.HandleServiceError(w, r, "Failed to update resource status", err)
		return
	}
	response.Success(w, res, "Resource status updated")
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "uuid"))
	if err != nil {
		response.BadRequest(w)
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		response.HandleServiceError(w, r, "Failed to delete resource", err)
		return
	}
	response.Success(w, nil, "Resource deleted")
}
