package project

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/maintainerd/core/internal/platform/apperror"
	"github.com/maintainerd/core/internal/platform/jsonutil"
	"github.com/maintainerd/core/internal/storage"
)

// Project groups a tenant's resources (tenant -> project -> resource).
type Project struct {
	UUID        uuid.UUID      `json:"project_uuid"`
	TenantUUID  uuid.UUID      `json:"tenant_uuid"`
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Status      string         `json:"status"`
	Metadata    map[string]any `json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type Service struct{ q Repository }

func NewService(q Repository) *Service { return &Service{q: q} }

type CreateInput struct {
	TenantUUID  uuid.UUID
	Name        string
	DisplayName string
	Description string
	Metadata    map[string]any
}

type UpdateInput struct {
	DisplayName string
	Description string
	Status      string
	Metadata    map[string]any
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*Project, error) {
	if in.Name == "" {
		return nil, apperror.NewValidation("name is required")
	}
	t, err := s.q.GetTenantByUUID(ctx, in.TenantUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("tenant")
	}
	if err != nil {
		return nil, err
	}
	meta, err := marshalMap(in.Metadata)
	if err != nil {
		return nil, apperror.NewValidation("invalid metadata")
	}
	row, err := s.q.CreateProject(ctx, storage.CreateProjectParams{
		TenantID:    t.TenantID,
		Name:        in.Name,
		DisplayName: in.DisplayName,
		Description: in.Description,
		Status:      "active",
		Metadata:    meta,
	})
	if err != nil {
		return nil, err
	}
	p := toProject(row, t.TenantUuid)
	return &p, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Project, error) {
	row, err := s.q.GetProjectByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("project")
	}
	if err != nil {
		return nil, err
	}
	tenantUUID, err := s.resolveTenantUUID(ctx, row.TenantID)
	if err != nil {
		return nil, err
	}
	p := toProject(row, tenantUUID)
	return &p, nil
}

func (s *Service) List(ctx context.Context, tenantUUID uuid.UUID, page, limit int) ([]Project, int64, error) {
	t, err := s.q.GetTenantByUUID(ctx, tenantUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, apperror.NewNotFound("tenant")
	}
	if err != nil {
		return nil, 0, err
	}
	page, limit = normalizePage(page, limit)
	rows, err := s.q.ListProjectsByTenant(ctx, storage.ListProjectsByTenantParams{
		TenantID: t.TenantID,
		Limit:    int32(limit),
		Offset:   int32((page - 1) * limit),
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.q.CountProjectsByTenant(ctx, t.TenantID)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Project, 0, len(rows))
	for _, r := range rows {
		out = append(out, toProject(r, t.TenantUuid))
	}
	return out, total, nil
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*Project, error) {
	current, err := s.q.GetProjectByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("project")
	}
	if err != nil {
		return nil, err
	}
	displayName := current.DisplayName
	if in.DisplayName != "" {
		displayName = in.DisplayName
	}
	description := current.Description
	if in.Description != "" {
		description = in.Description
	}
	status := current.Status
	if in.Status != "" {
		status = in.Status
	}
	meta := current.Metadata
	if in.Metadata != nil {
		if meta, err = marshalMap(in.Metadata); err != nil {
			return nil, apperror.NewValidation("invalid metadata")
		}
	}
	row, err := s.q.UpdateProject(ctx, storage.UpdateProjectParams{
		ProjectUuid: id,
		DisplayName: displayName,
		Description: description,
		Status:      status,
		Metadata:    meta,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("project")
	}
	if err != nil {
		return nil, err
	}
	tenantUUID, err := s.resolveTenantUUID(ctx, row.TenantID)
	if err != nil {
		return nil, err
	}
	p := toProject(row, tenantUUID)
	return &p, nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.q.SoftDeleteProject(ctx, id)
}

func (s *Service) resolveTenantUUID(ctx context.Context, tenantID int64) (uuid.UUID, error) {
	t, err := s.q.GetTenantByID(ctx, tenantID)
	if err != nil {
		return uuid.Nil, err
	}
	return t.TenantUuid, nil
}

func toProject(m storage.Project, tenantUUID uuid.UUID) Project {
	return Project{
		UUID:        m.ProjectUuid,
		TenantUUID:  tenantUUID,
		Name:        m.Name,
		DisplayName: m.DisplayName,
		Description: m.Description,
		Status:      m.Status,
		Metadata:    jsonutil.JSONToMap(m.Metadata),
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
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
