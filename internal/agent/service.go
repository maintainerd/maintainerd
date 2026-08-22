package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
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
//
// ID and BoundSubject are internal control-plane fields (json:"-"): the
// numeric ID keys the resource-assignment queries, and BoundSubject is the
// verified token subject the agent's identity is pinned to — neither belongs
// on the public HTTP payload (the subject would hand an attacker the exact
// principal name to target).
type Agent struct {
	ID           int64          `json:"-"`
	BoundSubject string         `json:"-"`
	UUID         uuid.UUID      `json:"agent_uuid"`
	TenantUUID   uuid.UUID      `json:"tenant_uuid"`
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	Endpoint     string         `json:"endpoint"`
	Version      string         `json:"version"`
	Capabilities []string       `json:"capabilities"`
	LastSeenAt   *time.Time     `json:"last_seen_at,omitempty"`
	Metadata     map[string]any `json:"metadata"`
	JoinToken    string         `json:"join_token,omitempty"`
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
	joinToken, joinHash, err := newJoinToken()
	if err != nil {
		return nil, err
	}
	row, err := s.q.CreateAgent(ctx, storage.CreateAgentParams{
		TenantID:      t.TenantID,
		Name:          in.Name,
		Status:        "pending",
		Endpoint:      in.Endpoint,
		Version:       in.Version,
		Capabilities:  caps,
		Metadata:      meta,
		JoinTokenHash: joinHash,
	})
	if err != nil {
		return nil, err
	}
	a := toAgent(row, t.TenantUuid)
	a.JoinToken = joinToken
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

// BindSubject pins the agent row to the verified token subject that Registers
// it. The bind is first-writer-wins and sticky: re-binding with the SAME
// subject is an idempotent no-op, while a different subject gets Forbidden —
// an agent UUID is discoverable (logs, APIs, config files), so possession of
// the UUID alone must never let a second principal adopt the agent's identity.
func (s *Service) BindSubject(ctx context.Context, id uuid.UUID, subject string) (*Agent, error) {
	if subject == "" {
		return nil, apperror.NewValidation("subject is required")
	}
	row, err := s.q.BindAgentSubject(ctx, storage.BindAgentSubjectParams{
		AgentUuid:    id,
		BoundSubject: subject,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// No row matched: either the agent does not exist, or it is already
		// bound to a different subject. Distinguish so the caller can map to
		// NotFound vs PermissionDenied.
		if _, gerr := s.q.GetAgentByUUID(ctx, id); errors.Is(gerr, pgx.ErrNoRows) {
			return nil, apperror.NewNotFound("agent")
		}
		return nil, apperror.NewForbidden("agent is bound to a different subject")
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

// RequireSubject loads the agent and enforces that the caller's verified token
// subject matches the row's bound subject. An unbound row fails closed too:
// until an authenticated Register has pinned the identity, no other gateway
// call may act as that agent (otherwise the window before first Register would
// be an impersonation window).
func (s *Service) RequireSubject(ctx context.Context, id uuid.UUID, subject string) (*Agent, error) {
	row, err := s.q.GetAgentByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("agent")
	}
	if err != nil {
		return nil, err
	}
	if row.BoundSubject == "" {
		return nil, apperror.NewForbidden("agent is not bound to a subject yet — Register first")
	}
	if subject == "" || row.BoundSubject != subject {
		return nil, apperror.NewForbidden("agent is bound to a different subject")
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

type EnrollInput struct {
	AgentUUID uuid.UUID
	JoinToken string
	CSRPem    []byte
	CACertPEM []byte
	CAKeyPEM  []byte
	CertTTL   time.Duration
}

type Enrollment struct {
	Agent          Agent
	CertificatePEM []byte
	CACertPEM      []byte
	ExpiresAt      time.Time
}

// Enroll consumes a one-time join token and signs the agent's CSR with Core's
// agent-client CA. The token is deliberately verified in Core's storage path
// instead of by the gRPC interceptor: enrollment is the pre-identity exchange
// that creates the mTLS credential later RPCs require.
func (s *Service) Enroll(ctx context.Context, in EnrollInput) (*Enrollment, error) {
	if in.AgentUUID == uuid.Nil {
		return nil, apperror.NewValidation("agent_uuid is required")
	}
	if in.JoinToken == "" {
		return nil, apperror.NewValidation("join_token is required")
	}
	if len(in.CSRPem) == 0 {
		return nil, apperror.NewValidation("csr_pem is required")
	}
	current, err := s.q.GetAgentByUUID(ctx, in.AgentUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("agent")
	}
	if err != nil {
		return nil, err
	}
	if current.JoinTokenHash == "" || current.JoinTokenUsedAt.Valid {
		return nil, apperror.NewForbidden("agent join token has already been used")
	}
	if !joinTokenMatches(current.JoinTokenHash, in.JoinToken) {
		return nil, apperror.NewForbidden("invalid agent join token")
	}
	certPEM, expiresAt, err := SignAgentCSR(SignAgentCSRInput{
		AgentUUID: in.AgentUUID,
		CSRPEM:    in.CSRPem,
		CACertPEM: in.CACertPEM,
		CAKeyPEM:  in.CAKeyPEM,
		TTL:       in.CertTTL,
	})
	if err != nil {
		return nil, apperror.NewValidation(err.Error())
	}
	row, err := s.q.MarkAgentEnrolled(ctx, storage.MarkAgentEnrolledParams{
		AgentUuid:     in.AgentUUID,
		ClientCertPem: string(certPEM),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewForbidden("agent join token has already been used")
	}
	if err != nil {
		return nil, err
	}
	tenantUUID, err := s.resolveTenantUUID(ctx, row.TenantID)
	if err != nil {
		return nil, err
	}
	a := toAgent(row, tenantUUID)
	return &Enrollment{Agent: a, CertificatePEM: certPEM, CACertPEM: in.CACertPEM, ExpiresAt: expiresAt}, nil
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
		ID:           m.AgentID,
		BoundSubject: m.BoundSubject,
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

func newJoinToken() (token string, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, joinTokenHash(token), nil
}

func joinTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func joinTokenMatches(hash, token string) bool {
	want, err := hex.DecodeString(hash)
	if err != nil {
		return false
	}
	got := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}
