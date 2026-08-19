package agent

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

// Agent is the on-host executor that pulls work from Core and runs it against
// the local already-installed runtime.
type Agent struct {
	UUID         uuid.UUID      `json:"agent_uuid"`
	TenantUUID   uuid.UUID      `json:"tenant_uuid"`
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	Endpoint     string         `json:"endpoint"`
	Version      string         `json:"version"`
	Capabilities []string       `json:"capabilities"`
	LastSeenAt   *time.Time     `json:"last_seen_at,omitempty"`
	Metadata     map[string]any `json:"metadata"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type Service struct{ q Repository }

func NewService(q Repository) *Service { return &Service{q: q} }

type CreateInput struct {
	TenantUUID   uuid.UUID
	Name         string
	Endpoint     string
	Version      string
	Capabilities []string
	Metadata     map[string]any
}

type UpdateInput struct {
	Status       string
	Endpoint     string
	Version      string
	Capabilities []string
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*Agent, error) {
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
	caps, err := marshalStrings(in.Capabilities)
	if err != nil {
		return nil, apperror.NewValidation("invalid capabilities")
	}
	meta, err := marshalMap(in.Metadata)
	if err != nil {
		return nil, apperror.NewValidation("invalid metadata")
	}
	row, err := s.q.CreateAgent(ctx, storage.CreateAgentParams{
		TenantID:     t.TenantID,
		Name:         in.Name,
		Status:       "pending",
		Endpoint:     in.Endpoint,
		Version:      in.Version,
		Capabilities: caps,
		Metadata:     meta,
	})
	if err != nil {
		return nil, err
	}
	a := toAgent(row, t.TenantUuid)
	return &a, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Agent, error) {
	row, err := s.q.GetAgentByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("agent")
	}
	if err != nil {
		return nil, err
	}
	tenantUUID, err := s.resolveTenantUUID(ctx, row.TenantID)
	if err != nil {
		return nil, err
	}
	a := toAgent(row, tenantUUID)
	return &a, nil
}

func (s *Service) List(ctx context.Context, tenantUUID uuid.UUID, page, limit int) ([]Agent, int64, error) {
	t, err := s.q.GetTenantByUUID(ctx, tenantUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, apperror.NewNotFound("tenant")
	}
	if err != nil {
		return nil, 0, err
	}
	page, limit = normalizePage(page, limit)
	rows, err := s.q.ListAgentsByTenant(ctx, storage.ListAgentsByTenantParams{
		TenantID: t.TenantID,
		Limit:    int32(limit),
		Offset:   int32((page - 1) * limit),
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.q.CountAgentsByTenant(ctx, t.TenantID)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Agent, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAgent(r, t.TenantUuid))
	}
	return out, total, nil
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*Agent, error) {
	current, err := s.q.GetAgentByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("agent")
	}
	if err != nil {
		return nil, err
	}
	status := current.Status
	if in.Status != "" {
		status = in.Status
	}
	endpoint := current.Endpoint
	if in.Endpoint != "" {
		endpoint = in.Endpoint
	}
	version := current.Version
	if in.Version != "" {
		version = in.Version
	}
	caps := current.Capabilities
	if in.Capabilities != nil {
		if caps, err = marshalStrings(in.Capabilities); err != nil {
			return nil, apperror.NewValidation("invalid capabilities")
		}
	}
	row, err := s.q.UpdateAgentStatus(ctx, storage.UpdateAgentStatusParams{
		AgentUuid:    id,
		Status:       status,
		Endpoint:     endpoint,
		Version:      version,
		Capabilities: caps,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("agent")
	}
	if err != nil {
		return nil, err
	}
	tenantUUID, err := s.resolveTenantUUID(ctx, row.TenantID)
	if err != nil {
		return nil, err
	}
	a := toAgent(row, tenantUUID)
	return &a, nil
}

// Heartbeat marks the agent online and stamps last_seen_at. The agent calls this
// on its poll interval so Core can detect offline agents.
func (s *Service) Heartbeat(ctx context.Context, id uuid.UUID) (*Agent, error) {
	row, err := s.q.AgentHeartbeat(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("agent")
	}
	if err != nil {
		return nil, err
	}
	tenantUUID, err := s.resolveTenantUUID(ctx, row.TenantID)
	if err != nil {
		return nil, err
	}
	a := toAgent(row, tenantUUID)
	return &a, nil
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.q.SoftDeleteAgent(ctx, id)
}

func (s *Service) resolveTenantUUID(ctx context.Context, tenantID int64) (uuid.UUID, error) {
	t, err := s.q.GetTenantByID(ctx, tenantID)
	if err != nil {
		return uuid.Nil, err
	}
	return t.TenantUuid, nil
}

func toAgent(m storage.Agent, tenantUUID uuid.UUID) Agent {
	a := Agent{
		UUID:         m.AgentUuid,
		TenantUUID:   tenantUUID,
		Name:         m.Name,
		Status:       m.Status,
		Endpoint:     m.Endpoint,
		Version:      m.Version,
		Capabilities: unmarshalStrings(m.Capabilities),
		Metadata:     jsonutil.JSONToMap(m.Metadata),
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
	if m.LastSeenAt.Valid {
		t := m.LastSeenAt.Time
		a.LastSeenAt = &t
	}
	return a
}

func marshalMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

func marshalStrings(s []string) ([]byte, error) {
	if len(s) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(s)
}

func unmarshalStrings(b []byte) []string {
	out := []string{}
	if len(b) == 0 {
		return out
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return []string{}
	}
	return out
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
