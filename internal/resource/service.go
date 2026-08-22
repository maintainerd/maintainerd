package resource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	kitruntime "github.com/maintainerd/kit/runtime"

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
	MRN                string         `json:"mrn"`
	MRNService         string         `json:"mrn_service"`
	MRNTenant          string         `json:"mrn_tenant"`
	MRNProject         string         `json:"mrn_project"`
	MRNResourceType    string         `json:"mrn_resource_type"`
	MRNResourcePath    string         `json:"mrn_resource_path"`
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
	tenantRow, err := s.q.GetTenantByID(ctx, proj.TenantID)
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
	if err := validateSpec(in.Kind, spec); err != nil {
		return nil, apperror.NewValidation(err.Error())
	}
	meta, err := marshalMap(in.Metadata)
	if err != nil {
		return nil, apperror.NewValidation("invalid metadata")
	}
	mrn, err := buildMRN(tenantRow.Name, proj.Name, in.Kind, in.Name, in.Metadata)
	if err != nil {
		return nil, apperror.NewValidation(err.Error())
	}
	row, err := s.q.CreateResource(ctx, storage.CreateResourceParams{
		TenantID:        proj.TenantID,
		ProjectID:       proj.ProjectID,
		ProviderID:      providerID,
		AgentID:         pgtype.Int8{},
		OwnerResourceID: pgtype.Int8{},
		MrnService:      mrn.Service,
		MrnTenant:       mrn.Tenant,
		MrnProject:      mrn.Project,
		MrnResourceType: mrn.ResourceType,
		MrnResourcePath: mrn.ResourcePath,
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
		if err := validateSpec(current.Kind, spec); err != nil {
			return nil, apperror.NewValidation(err.Error())
		}
	}
	meta := current.Metadata
	metaMap := jsonutil.JSONToMap(current.Metadata)
	if in.Metadata != nil {
		if meta, err = marshalMap(in.Metadata); err != nil {
			return nil, apperror.NewValidation("invalid metadata")
		}
		metaMap = in.Metadata
	}
	projectRow, err := s.q.GetProjectByID(ctx, current.ProjectID)
	if err != nil {
		return nil, err
	}
	tenantRow, err := s.q.GetTenantByID(ctx, current.TenantID)
	if err != nil {
		return nil, err
	}
	mrn, err := buildMRN(tenantRow.Name, projectRow.Name, current.Kind, current.Name, metaMap)
	if err != nil {
		return nil, apperror.NewValidation(err.Error())
	}
	row, err := s.q.UpdateResourceSpec(ctx, storage.UpdateResourceSpecParams{
		ResourceUuid:    id,
		Spec:            spec,
		Metadata:        meta,
		MrnService:      mrn.Service,
		MrnTenant:       mrn.Tenant,
		MrnProject:      mrn.Project,
		MrnResourceType: mrn.ResourceType,
		MrnResourcePath: mrn.ResourcePath,
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

// Delete flips the resource to desired-state 'deleting' WITHOUT stamping
// deleted_at: the row must stay in the work feed so PullWork can ship a
// teardown envelope to the owning agent. Erasing the record first would leave
// the actual workload running on the host with nothing left to reconcile it
// away — the delete only finalizes when the agent reports state "removed".
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.q.MarkResourceDeleting(ctx, id)
}

// WorkItem is a resource that needs reconciling — the minimal shape an executor
// (agent) needs to act on. It skips project resolution so the reconciler feed
// stays cheap. State and Metadata ride along because the gateway derives the
// envelope's teardown/tier fields from them.
type WorkItem struct {
	UUID       uuid.UUID
	Kind       string
	Name       string
	State      string
	Spec       map[string]any
	Metadata   map[string]any
	Generation int64
}

// OutOfSync returns resources whose observed state lags their desired spec — a
// read-only view of the work feed (no lease, no assignment). The AgentGateway
// uses ClaimForAgent instead, which atomically leases what it hands out.
func (s *Service) OutOfSync(ctx context.Context, limit int) ([]WorkItem, error) {
	rows, err := s.q.ListOutOfSyncResources(ctx, int32(normalizeFeedLimit(limit)))
	if err != nil {
		return nil, err
	}
	return toWorkItems(rows), nil
}

// ClaimForAgent atomically claims up to limit feed items for one agent and
// leases them for leaseTTL. Items already assigned to the agent are re-fed
// (sticky assignment); unassigned items are claimed on first pull. Two agents
// can never claim the same item concurrently — the claim query locks candidate
// rows with FOR UPDATE SKIP LOCKED and stamps agent_id + leased_until in a
// single statement.
func (s *Service) ClaimForAgent(ctx context.Context, agentID int64, limit int, leaseTTL time.Duration) ([]WorkItem, error) {
	if leaseTTL <= 0 {
		leaseTTL = time.Minute
	}
	rows, err := s.q.ClaimAgentWork(ctx, storage.ClaimAgentWorkParams{
		AgentID:      pgtype.Int8{Int64: agentID, Valid: true},
		LeaseSeconds: leaseTTL.Seconds(),
		MaxItems:     int32(normalizeFeedLimit(limit)),
	})
	if err != nil {
		return nil, err
	}
	return toWorkItems(rows), nil
}

// Retry backoff bounds for failed convergence attempts. A failed item re-enters
// the feed only after min(retryCap, retryBase * 2^attempts) — fast enough that
// a transient failure (image registry blip, engine restart) recovers in
// seconds, slow enough that a genuinely broken spec cannot make its agent
// hot-loop pull→fail→pull against the same poison pill.
const (
	retryBase = 5 * time.Second
	retryCap  = 5 * time.Minute
)

// nextBackoff computes the exponential backoff delay for the given
// (post-increment) attempt count, capped at retryCap.
func nextBackoff(attempts int32) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := retryBase
	for i := int32(1); i < attempts; i++ {
		d *= 2
		if d >= retryCap {
			return retryCap
		}
	}
	if d > retryCap {
		return retryCap
	}
	return d
}

// AgentReportInput is one status report from an agent, as received by the
// AgentGateway.
type AgentReportInput struct {
	State              string
	Status             map[string]any
	ObservedGeneration int64
}

// ApplyAgentReport records one agent status report against a resource, with
// ownership enforced: the resource's assigned agent must be the caller. This
// is the per-resource authorization the gateway's single `core:agent:gateway`
// permission deliberately does not provide — any agent principal may talk to
// the gateway, but it can only ever write status for resources leased to it.
//
// State mapping (the agent's vocabulary is defined in
// ../maintainerd-agent/internal/worker/worker.go):
//   - "running"  → converged: attempts/backoff reset, observed_generation
//     advances, lease released.
//   - "failed"   → retryable failure: attempts++, exponential backoff
//     (nextBackoff), state 'error' — or terminal 'failed' once attempts
//     reaches budget; observed_generation is deliberately NOT advanced so the
//     item stays out-of-sync and re-enters the feed after the backoff. A
//     failing teardown stays 'deleting' (still under budget) so the teardown
//     intent survives retries.
//   - "removed"  → teardown complete: finalizes the delete (deleted_at
//     stamped) when the resource was 'deleting'; otherwise recorded as a
//     plain observation.
//   - anything else ("exited", "unhealthy", ... — drift observations) →
//     recorded as-is; attempts/backoff untouched.
func (s *Service) ApplyAgentReport(ctx context.Context, agentID int64, resourceUUID uuid.UUID, in AgentReportInput, attemptBudget int) (*Resource, error) {
	current, err := s.q.GetResourceByUUID(ctx, resourceUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NewNotFound("resource")
	}
	if err != nil {
		return nil, err
	}
	// Ownership: only the assigned agent may report. Fail closed on an
	// unassigned row too — an unclaimed resource has no legitimate reporter.
	if !current.AgentID.Valid || current.AgentID.Int64 != agentID {
		return nil, apperror.NewForbidden("resource is not assigned to this agent")
	}

	statusJSON := current.Status
	if in.Status != nil {
		if statusJSON, err = marshalMap(in.Status); err != nil {
			return nil, apperror.NewValidation("invalid status")
		}
	}
	if attemptBudget < 1 {
		attemptBudget = 10
	}

	params := storage.ApplyAgentReportParams{
		ResourceUuid:       resourceUUID,
		Status:             statusJSON,
		ObservedGeneration: in.ObservedGeneration,
		Attempts:           current.Attempts,
		NextAttemptAt:      current.NextAttemptAt,
	}
	switch in.State {
	case "running":
		params.State = "running"
		params.Attempts = 0
		params.NextAttemptAt = pgtype.Timestamptz{}
	case "failed":
		attempts := current.Attempts + 1
		params.Attempts = attempts
		// Never advance observed_generation on failure: the item must remain
		// out-of-sync so the feed re-dispatches it after the backoff.
		params.ObservedGeneration = 0
		if int(attempts) >= attemptBudget {
			// Budget exhausted: park terminally. Only a spec change
			// (UpdateResourceSpec resets attempts + re-arms state) revives it.
			params.State = "failed"
			params.NextAttemptAt = pgtype.Timestamptz{}
		} else {
			params.NextAttemptAt = pgtype.Timestamptz{Time: time.Now().Add(nextBackoff(attempts)), Valid: true}
			if current.State == "deleting" {
				params.State = "deleting" // keep the teardown intent across retries
			} else {
				params.State = "error"
			}
		}
	case "removed":
		if current.State == "deleting" {
			params.State = "removed"
			params.Finalize = true
			params.ObservedGeneration = current.Generation
		} else {
			// A "removed" observation outside a teardown (should not happen —
			// the agent only reports it after tearing down) is recorded but
			// must not silently finalize a delete nobody requested.
			params.State = in.State
		}
	default:
		if in.State == "" {
			params.State = current.State
		} else {
			params.State = in.State
		}
	}

	row, err := s.q.ApplyAgentReport(ctx, params)
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

func toWorkItems(rows []storage.Resource) []WorkItem {
	items := make([]WorkItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, WorkItem{
			UUID:       r.ResourceUuid,
			Kind:       r.Kind,
			Name:       r.Name,
			State:      r.State,
			Spec:       jsonutil.JSONToMap(r.Spec),
			Metadata:   jsonutil.JSONToMap(r.Metadata),
			Generation: r.Generation,
		})
	}
	return items
}

func normalizeFeedLimit(limit int) int {
	if limit < 1 || limit > 100 {
		return 20
	}
	return limit
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
		MRN:                renderMRN(m.MrnService, m.MrnTenant, m.MrnProject, m.MrnResourceType, m.MrnResourcePath),
		MRNService:         m.MrnService,
		MRNTenant:          m.MrnTenant,
		MRNProject:         m.MrnProject,
		MRNResourceType:    m.MrnResourceType,
		MRNResourcePath:    m.MrnResourcePath,
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

type mrnParts struct {
	Service      string
	Tenant       string
	Project      string
	ResourceType string
	ResourcePath string
}

func buildMRN(tenantName, projectName, kind, name string, metadata map[string]any) (mrnParts, error) {
	parts := mrnParts{
		Service:      firstMetaString(metadata, "mrn_service", "service"),
		Tenant:       tenantName,
		Project:      projectName,
		ResourceType: firstMetaString(metadata, "mrn_resource_type", "resource_type"),
		ResourcePath: firstMetaString(metadata, "mrn_resource_path", "resource_path"),
	}
	if parts.Service == "" {
		parts.Service = "core"
	}
	if parts.ResourceType == "" {
		parts.ResourceType = strings.ToLower(strings.TrimSpace(kind))
	}
	if parts.ResourcePath == "" {
		parts.ResourcePath = strings.TrimSpace(name)
	}
	if err := validateMRNSegment("service", parts.Service); err != nil {
		return mrnParts{}, err
	}
	if err := validateMRNSegment("tenant", parts.Tenant); err != nil {
		return mrnParts{}, err
	}
	if err := validateMRNSegment("project", parts.Project); err != nil {
		return mrnParts{}, err
	}
	if err := validateMRNSegment("resource_type", parts.ResourceType); err != nil {
		return mrnParts{}, err
	}
	if err := validateMRNPath(parts.ResourcePath); err != nil {
		return mrnParts{}, err
	}
	return parts, nil
}

func firstMetaString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := metadata[key].(string); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func validateMRNSegment(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("mrn_%s is required", name)
	}
	if strings.ContainsAny(value, ":/*") || strings.Contains(value, "..") {
		return fmt.Errorf("mrn_%s contains invalid characters", name)
	}
	return nil
}

func validateMRNPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("mrn_resource_path is required")
	}
	if strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") || strings.Contains(path, ":") ||
		strings.Contains(path, "*") || strings.Contains(path, "..") {
		return fmt.Errorf("mrn_resource_path contains invalid characters")
	}
	return nil
}

func renderMRN(service, tenant, project, resourceType, resourcePath string) string {
	if service == "" && tenant == "" && project == "" && resourceType == "" && resourcePath == "" {
		return ""
	}
	return fmt.Sprintf("mrn:%s:%s:%s:%s/%s", service, tenant, project, resourceType, resourcePath)
}

func validateSpec(kind string, specJSON []byte) error {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "container", "workload":
		var envelope struct {
			Workload json.RawMessage `json:"workload"`
			Teardown bool            `json:"teardown"`
		}
		if err := json.Unmarshal(specJSON, &envelope); err != nil {
			return fmt.Errorf("spec is not valid JSON: %w", err)
		}
		if envelope.Teardown {
			return fmt.Errorf("teardown is a system-generated state and cannot be set in resource spec")
		}
		raw := json.RawMessage(specJSON)
		if len(envelope.Workload) > 0 && string(envelope.Workload) != "null" {
			raw = envelope.Workload
		}
		var workload kitruntime.WorkloadSpec
		if err := json.Unmarshal(raw, &workload); err != nil {
			return fmt.Errorf("workload does not parse as a WorkloadSpec: %w", err)
		}
		if err := workload.Validate(); err != nil {
			return err
		}
	}
	return nil
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
