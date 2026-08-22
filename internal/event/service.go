// Package event is the platform's durable escalation log.
//
// Supervision cannot be a log line. When Core gives up on bringing a system
// service back, the record has to outlive the process that wrote it: an operator
// looking at a platform that has been restarted twice needs to see that Auth was
// re-dispatched six times and never went healthy, and a console or alerting
// pipeline needs to read that without tailing stdout. slog is per-process and
// ephemeral; platform_events is the platform's memory.
//
// The write side is intentionally tiny and non-fatal by contract: emitting an
// event must never be able to break the loop that noticed the problem.
package event

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/maintainerd/core/internal/platform/apperror"
	"github.com/maintainerd/core/internal/platform/jsonutil"
	"github.com/maintainerd/core/internal/storage"
)

// Event kinds. Stable, machine-readable strings — a console or alert rule
// branches on these, so treat a rename as a breaking change.
const (
	// KindAgentOffline: an agent missed its heartbeat threshold. Emitted once
	// per transition (the sweeper only returns rows it transitioned).
	KindAgentOffline = "agent.offline"
	// KindSystemHostUnreachable: a system-tier workload's host went away. The
	// workload is NOT rescheduled — see resource.Service.FlagHostUnreachable.
	KindSystemHostUnreachable = "system.host_unreachable"
	// KindSystemInstanceMissing: a system service is registered but has no
	// desired-state instance to reconcile. Core does not invent a spec for it.
	KindSystemInstanceMissing = "system.instance_missing"
	// KindSystemRedispatchEscalated: repeated re-dispatch has not produced a
	// healthy system service. This is the "a human is needed" record.
	KindSystemRedispatchEscalated = "system.redispatch_escalated"
	// KindSystemRecovered: an escalated system service came back. Emitted only
	// after an escalation, so the log closes the incident it opened.
	KindSystemRecovered = "system.recovered"
)

// Severities.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Subject types — what SubjectUUID points at.
const (
	SubjectAgent    = "agent"
	SubjectResource = "resource"
	SubjectService  = "service"
)

// Column widths from migrations/00010_create_platform_events.sql. Values are
// truncated rather than rejected: an event is a diagnostic, and losing the
// record of an incident because its kind was two characters too long would be
// the worst possible trade.
const (
	maxKindLen        = 50
	maxSeverityLen    = 20
	maxSubjectTypeLen = 50
)

// Event is the API/domain representation of one platform event.
type Event struct {
	UUID        uuid.UUID      `json:"event_uuid"`
	Kind        string         `json:"kind"`
	Severity    string         `json:"severity"`
	SubjectType string         `json:"subject_type,omitempty"`
	SubjectUUID *uuid.UUID     `json:"subject_uuid,omitempty"`
	Message     string         `json:"message"`
	Details     map[string]any `json:"details"`
	CreatedAt   time.Time      `json:"created_at"`
}

// Service is the platform-event business layer over the sqlc queries.
type Service struct{ q Repository }

func NewService(q Repository) *Service { return &Service{q: q} }

// Input is one event to record.
//
// TenantID is absent by design. System-tier supervision is a PLATFORM concern —
// the tenant that happens to own the row is incidental — so those events are
// platform-scoped (tenant_id NULL) and locate their subject through SubjectUUID.
// The column exists for future tenant-scoped events.
type Input struct {
	Kind        string
	Severity    string
	SubjectType string
	SubjectUUID *uuid.UUID
	Message     string
	Details     map[string]any
}

// Emit records one event. Kind and Message are required — an event nobody can
// identify or read is worse than no event, because it looks like coverage.
func (s *Service) Emit(ctx context.Context, in Input) (*Event, error) {
	kind := strings.TrimSpace(in.Kind)
	message := strings.TrimSpace(in.Message)
	if kind == "" || message == "" {
		return nil, apperror.NewValidation("kind and message are required")
	}
	severity := strings.TrimSpace(in.Severity)
	if severity == "" {
		severity = SeverityWarning
	}
	details, err := marshalDetails(in.Details)
	if err != nil {
		return nil, apperror.NewValidation("invalid details")
	}
	subject := pgtype.UUID{}
	if in.SubjectUUID != nil {
		subject = pgtype.UUID{Bytes: *in.SubjectUUID, Valid: true}
	}
	row, err := s.q.CreatePlatformEvent(ctx, storage.CreatePlatformEventParams{
		TenantID:    pgtype.Int8{},
		Kind:        truncate(kind, maxKindLen),
		Severity:    truncate(severity, maxSeverityLen),
		SubjectType: truncate(strings.TrimSpace(in.SubjectType), maxSubjectTypeLen),
		SubjectUuid: subject,
		Message:     message,
		Details:     details,
	})
	if err != nil {
		return nil, err
	}
	ev := from(row)
	return &ev, nil
}

// List returns events newest-first with the total, for the read-only API.
func (s *Service) List(ctx context.Context, page, limit int) ([]Event, int64, error) {
	page, limit = normalizePage(page, limit)
	rows, err := s.q.ListPlatformEvents(ctx, storage.ListPlatformEventsParams{
		Limit:  int32(limit),
		Offset: int32((page - 1) * limit),
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.q.CountPlatformEvents(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, from(r))
	}
	return out, total, nil
}

// ListBySubject returns the incident timeline for one agent/resource/service.
func (s *Service) ListBySubject(ctx context.Context, subjectType string, subjectUUID uuid.UUID, page, limit int) ([]Event, error) {
	if strings.TrimSpace(subjectType) == "" {
		return nil, apperror.NewValidation("subject_type is required")
	}
	page, limit = normalizePage(page, limit)
	rows, err := s.q.ListPlatformEventsBySubject(ctx, storage.ListPlatformEventsBySubjectParams{
		SubjectType: subjectType,
		SubjectUuid: pgtype.UUID{Bytes: subjectUUID, Valid: true},
		Limit:       int32(limit),
		Offset:      int32((page - 1) * limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, from(r))
	}
	return out, nil
}

func from(row storage.PlatformEvent) Event {
	ev := Event{
		UUID:        row.EventUuid,
		Kind:        row.Kind,
		Severity:    row.Severity,
		SubjectType: row.SubjectType,
		Message:     row.Message,
		Details:     jsonutil.JSONToMap(row.Details),
		CreatedAt:   row.CreatedAt,
	}
	if row.SubjectUuid.Valid {
		id := uuid.UUID(row.SubjectUuid.Bytes)
		ev.SubjectUUID = &id
	}
	return ev
}

func marshalDetails(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
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
