package resource

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/maintainerd/core/internal/platform/apperror"
	"github.com/maintainerd/core/internal/platform/jsonutil"
	"github.com/maintainerd/core/internal/storage"
)

// Resource is the declarative unit Core reconciles: a desired `spec` driven
// toward an observed `status`. generation/observed_generation track how far the
// reconciler has caught up with the latest spec.
type Resource struct {
	UUID               uuid.UUID      `json:"resource_uuid"`
	ProjectUUID        uuid.UUID      `json:"project_uuid"`
	Kind               string         `json:"kind"`
	Name               string         `json:"name"`
	State              string         `json:"state"`
	Spec               map[string]any `json:"spec"`
	Status             map[string]any `json:"status"`
	Generation         int64          `json:"generation"`
	ObservedGeneration int64          `json:"observed_generation"`
	Metadata           map[string]any `json:"metadata"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type Service struct{ q Repository }

func NewService(q Repository) *Service { return &Service{q: q} }

type CreateInput struct {
	ProjectUUID  uuid.UUID
	ProviderUUID *uuid.UUID
	Kind         string
	Name         string
	Spec         map[string]any
	Metadata     map[string]any
}

type UpdateSpecInput struct {
	Spec     map[string]any
	Metadata map[string]any
}

// UpdateStatusInput is what the reconciler/agent reports back after acting.
type UpdateStatusInput struct {
	Status             map[string]any
	State              string
	ObservedGeneration int64
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*Resource, error) {
	if in.Kind == "" || in.Name == "" {
		return nil, apperror.NewValidation("kind and name are required")
	}
	proj, err := s.q.GetProjectByUUID(ctx, in.ProjectUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("project")
	}
	if err != nil {
		return nil, err
	}
	providerID := pgtype.Int8{}
	if in.ProviderUUID != nil {
		prov, err := s.q.GetProviderByUUID(ctx, *in.ProviderUUID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.NewNotFound("provider")
		}
		if err != nil {
			return nil, err
		}
		providerID = pgtype.Int8{Int64: prov.ProviderID, Valid: true}
	}
	spec, err := marshalMap(in.Spec)
	if err != nil {
		return nil, apperror.NewValidation("invalid spec")
	}
	meta, err := marshalMap(in.Metadata)
	if err != nil {
		return nil, apperror.NewValidation("invalid metadata")
	}
	row, err := s.q.CreateResource(ctx, storage.CreateResourceParams{
		TenantID:        proj.TenantID,
		ProjectID:       proj.ProjectID,
		ProviderID:      providerID,
		AgentID:         pgtype.Int8{},
		OwnerResourceID: pgtype.Int8{},
		Kind:            in.Kind,
		Name:            in.Name,
		Spec:            spec,
		Metadata:        meta,
	})
	if err != nil {
		return nil, err
	}
	r := toResource(row, proj.ProjectUuid)
	return &r, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Resource, error) {
	row, err := s.q.GetResourceByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("resource")
	}
	if err != nil {
		return nil, err
	}
	projectUUID, err := s.resolveProjectUUID(ctx, row.ProjectID)
	if err != nil {
		return nil, err
	}
	r := toResource(row, projectUUID)
	return &r, nil
}

func (s *Service) ListByProject(ctx context.Context, projectUUID uuid.UUID, page, limit int) ([]Resource, int64, error) {
	proj, err := s.q.GetProjectByUUID(ctx, projectUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, apperror.NewNotFound("project")
	}
	if err != nil {
		return nil, 0, err
	}
	page, limit = normalizePage(page, limit)
	rows, err := s.q.ListResourcesByProject(ctx, storage.ListResourcesByProjectParams{
		ProjectID: proj.ProjectID,
		Limit:     int32(limit),
		Offset:    int32((page - 1) * limit),
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.q.CountResourcesByProject(ctx, proj.ProjectID)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Resource, 0, len(rows))
	for _, r := range rows {
		out = append(out, toResource(r, proj.ProjectUuid))
	}
	return out, total, nil
}

// UpdateSpec changes desired state; the query bumps generation and re-arms the
// reconciler (state -> pending).
func (s *Service) UpdateSpec(ctx context.Context, id uuid.UUID, in UpdateSpecInput) (*Resource, error) {
	current, err := s.q.GetResourceByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("resource")
	}
	if err != nil {
		return nil, err
	}
	spec := current.Spec
	if in.Spec != nil {
		if spec, err = marshalMap(in.Spec); err != nil {
			return nil, apperror.NewValidation("invalid spec")
		}
	}
	meta := current.Metadata
	if in.Metadata != nil {
		if meta, err = marshalMap(in.Metadata); err != nil {
			return nil, apperror.NewValidation("invalid metadata")
		}
	}
	row, err := s.q.UpdateResourceSpec(ctx, storage.UpdateResourceSpecParams{
		ResourceUuid: id,
		Spec:         spec,
		Metadata:     meta,
	})
	if err != nil {
		return nil, err
	}
	projectUUID, err := s.resolveProjectUUID(ctx, row.ProjectID)
	if err != nil {
		return nil, err
	}
	r := toResource(row, projectUUID)
	return &r, nil
}

// UpdateStatus records observed state reported by the reconciler/agent.
func (s *Service) UpdateStatus(ctx context.Context, id uuid.UUID, in UpdateStatusInput) (*Resource, error) {
	current, err := s.q.GetResourceByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("resource")
	}
	if err != nil {
		return nil, err
	}
	status := current.Status
	if in.Status != nil {
		if status, err = marshalMap(in.Status); err != nil {
			return nil, apperror.NewValidation("invalid status")
		}
	}
	state := current.State
	if in.State != "" {
		state = in.State
	}
	observedGen := current.ObservedGeneration
	if in.ObservedGeneration > 0 {
		observedGen = in.ObservedGeneration
	}
	row, err := s.q.UpdateResourceStatus(ctx, storage.UpdateResourceStatusParams{
		ResourceUuid:       id,
		Status:             status,
		State:              state,
		ObservedGeneration: observedGen,
	})
	if err != nil {
		return nil, err
	}
	projectUUID, err := s.resolveProjectUUID(ctx, row.ProjectID)
	if err != nil {
		return nil, err
	}
	r := toResource(row, projectUUID)
	return &r, nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.q.SoftDeleteResource(ctx, id)
}

// WorkItem is a resource that needs reconciling — the minimal shape an executor
// (agent) needs to act on. It skips project resolution so the reconciler feed
// stays cheap.
type WorkItem struct {
	UUID       uuid.UUID
	Kind       string
	Name       string
	Spec       map[string]any
	Generation int64
}

// OutOfSync returns resources whose observed state lags their desired spec — the
// work feed Core hands to agents via the AgentGateway.
func (s *Service) OutOfSync(ctx context.Context, limit int) ([]WorkItem, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.q.ListOutOfSyncResources(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	items := make([]WorkItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, WorkItem{
			UUID:       r.ResourceUuid,
			Kind:       r.Kind,
			Name:       r.Name,
			Spec:       jsonutil.JSONToMap(r.Spec),
			Generation: r.Generation,
		})
	}
	return items, nil
}

func (s *Service) resolveProjectUUID(ctx context.Context, projectID int64) (uuid.UUID, error) {
	p, err := s.q.GetProjectByID(ctx, projectID)
	if err != nil {
		return uuid.Nil, err
	}
	return p.ProjectUuid, nil
}

func toResource(m storage.Resource, projectUUID uuid.UUID) Resource {
	return Resource{
		UUID:               m.ResourceUuid,
		ProjectUUID:        projectUUID,
		Kind:               m.Kind,
		Name:               m.Name,
		State:              m.State,
		Spec:               jsonutil.JSONToMap(m.Spec),
		Status:             jsonutil.JSONToMap(m.Status),
		Generation:         m.Generation,
		ObservedGeneration: m.ObservedGeneration,
		Metadata:           jsonutil.JSONToMap(m.Metadata),
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
	}
}

func marshalMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

func normalizePage(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return page, limit
}
