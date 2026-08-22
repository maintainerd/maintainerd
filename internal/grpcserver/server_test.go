package grpcserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	corev1 "github.com/maintainerd/core/gen/maintainerd/core/v1"
	"github.com/maintainerd/core/internal/agent"
	"github.com/maintainerd/core/internal/resource"
	"github.com/maintainerd/core/internal/storage"
)

// --- fakes -----------------------------------------------------------------

type fakeAgentRepo struct {
	row storage.Agent
}

func (f *fakeAgentRepo) GetTenantByUUID(context.Context, uuid.UUID) (storage.Tenant, error) {
	return storage.Tenant{TenantID: 1, TenantUuid: uuid.New()}, nil
}
func (f *fakeAgentRepo) GetTenantByID(context.Context, int64) (storage.Tenant, error) {
	return storage.Tenant{TenantID: 1, TenantUuid: uuid.New()}, nil
}
func (f *fakeAgentRepo) CreateAgent(context.Context, storage.CreateAgentParams) (storage.Agent, error) {
	panic("not used")
}
func (f *fakeAgentRepo) GetAgentByUUID(_ context.Context, id uuid.UUID) (storage.Agent, error) {
	if id != f.row.AgentUuid {
		return storage.Agent{}, pgx.ErrNoRows
	}
	return f.row, nil
}
func (f *fakeAgentRepo) ListAgentsByTenant(context.Context, storage.ListAgentsByTenantParams) ([]storage.Agent, error) {
	panic("not used")
}
func (f *fakeAgentRepo) CountAgentsByTenant(context.Context, int64) (int64, error) {
	panic("not used")
}
func (f *fakeAgentRepo) UpdateAgentStatus(_ context.Context, arg storage.UpdateAgentStatusParams) (storage.Agent, error) {
	if arg.AgentUuid != f.row.AgentUuid {
		return storage.Agent{}, pgx.ErrNoRows
	}
	f.row.Status = arg.Status
	return f.row, nil
}

// BindAgentSubject mirrors the SQL's conditional-update semantics: only an
// unbound row or the same subject matches.
func (f *fakeAgentRepo) BindAgentSubject(_ context.Context, arg storage.BindAgentSubjectParams) (storage.Agent, error) {
	if arg.AgentUuid != f.row.AgentUuid {
		return storage.Agent{}, pgx.ErrNoRows
	}
	if f.row.BoundSubject != "" && f.row.BoundSubject != arg.BoundSubject {
		return storage.Agent{}, pgx.ErrNoRows
	}
	f.row.BoundSubject = arg.BoundSubject
	return f.row, nil
}
func (f *fakeAgentRepo) MarkAgentEnrolled(_ context.Context, arg storage.MarkAgentEnrolledParams) (storage.Agent, error) {
	if arg.AgentUuid != f.row.AgentUuid || f.row.JoinTokenUsedAt.Valid {
		return storage.Agent{}, pgx.ErrNoRows
	}
	f.row.JoinTokenUsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.row.ClientCertPem = arg.ClientCertPem
	return f.row, nil
}
func (f *fakeAgentRepo) AgentHeartbeat(_ context.Context, id uuid.UUID) (storage.Agent, error) {
	if id != f.row.AgentUuid {
		return storage.Agent{}, pgx.ErrNoRows
	}
	return f.row, nil
}
func (f *fakeAgentRepo) SoftDeleteAgent(context.Context, uuid.UUID) error { panic("not used") }

type fakeResourceRepo struct {
	rows       map[uuid.UUID]*storage.Resource
	claimCalls []storage.ClaimAgentWorkParams
	reports    []storage.ApplyAgentReportParams
}

func (f *fakeResourceRepo) GetProjectByUUID(context.Context, uuid.UUID) (storage.Project, error) {
	panic("not used")
}
func (f *fakeResourceRepo) GetTenantByID(context.Context, int64) (storage.Tenant, error) {
	return storage.Tenant{TenantID: 1, Name: "acme"}, nil
}
func (f *fakeResourceRepo) GetProjectByID(context.Context, int64) (storage.Project, error) {
	return storage.Project{ProjectID: 1, ProjectUuid: uuid.New()}, nil
}
func (f *fakeResourceRepo) GetProviderByUUID(context.Context, uuid.UUID) (storage.Provider, error) {
	panic("not used")
}
func (f *fakeResourceRepo) CreateResource(context.Context, storage.CreateResourceParams) (storage.Resource, error) {
	panic("not used")
}
func (f *fakeResourceRepo) GetResourceByUUID(_ context.Context, id uuid.UUID) (storage.Resource, error) {
	if r, ok := f.rows[id]; ok && !r.DeletedAt.Valid {
		return *r, nil
	}
	return storage.Resource{}, pgx.ErrNoRows
}
func (f *fakeResourceRepo) ListResourcesByProject(context.Context, storage.ListResourcesByProjectParams) ([]storage.Resource, error) {
	panic("not used")
}
func (f *fakeResourceRepo) CountResourcesByProject(context.Context, int64) (int64, error) {
	panic("not used")
}
func (f *fakeResourceRepo) UpdateResourceSpec(context.Context, storage.UpdateResourceSpecParams) (storage.Resource, error) {
	panic("not used")
}
func (f *fakeResourceRepo) UpdateResourceStatus(context.Context, storage.UpdateResourceStatusParams) (storage.Resource, error) {
	panic("not used")
}
func (f *fakeResourceRepo) ListOutOfSyncResources(context.Context, int32) ([]storage.Resource, error) {
	panic("not used")
}

// ClaimAgentWork mirrors the SQL's feed + claim semantics closely enough for
// gateway tests: assigned-to-caller or unassigned, not failed, not deleted.
func (f *fakeResourceRepo) ClaimAgentWork(_ context.Context, arg storage.ClaimAgentWorkParams) ([]storage.Resource, error) {
	f.claimCalls = append(f.claimCalls, arg)
	out := []storage.Resource{}
	for _, r := range f.rows {
		if r.DeletedAt.Valid || r.State == "failed" {
			continue
		}
		if !(r.ObservedGeneration < r.Generation || r.State == "deleting" || r.State == "error") {
			continue
		}
		if r.AgentID.Valid && r.AgentID.Int64 != arg.AgentID.Int64 {
			continue
		}
		r.AgentID = arg.AgentID // sticky claim
		out = append(out, *r)
	}
	return out, nil
}

func (f *fakeResourceRepo) ApplyAgentReport(_ context.Context, arg storage.ApplyAgentReportParams) (storage.Resource, error) {
	f.reports = append(f.reports, arg)
	r, ok := f.rows[arg.ResourceUuid]
	if !ok || r.DeletedAt.Valid {
		return storage.Resource{}, pgx.ErrNoRows
	}
	r.State = arg.State
	if arg.ObservedGeneration > r.ObservedGeneration {
		r.ObservedGeneration = arg.ObservedGeneration
	}
	r.Attempts = arg.Attempts
	r.NextAttemptAt = arg.NextAttemptAt
	if arg.Finalize {
		r.DeletedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	return *r, nil
}
func (f *fakeResourceRepo) MarkResourceDeleting(context.Context, uuid.UUID) error {
	panic("not used")
}

// --- helpers ----------------------------------------------------------------

func ctxWithSubject(subject string) context.Context {
	return context.WithValue(context.Background(), claimsKey{},
		&Claims{Subject: subject, Permissions: []string{gatewayPermission}})
}

func newGatewayFixture(t *testing.T, enforce bool) (*AgentGateway, *fakeAgentRepo, *fakeResourceRepo) {
	t.Helper()
	agentRepo := &fakeAgentRepo{row: storage.Agent{
		AgentID:      7,
		AgentUuid:    uuid.New(),
		TenantID:     1,
		Name:         "host-1",
		Capabilities: []byte("[]"),
		Metadata:     []byte("{}"),
	}}
	resRepo := &fakeResourceRepo{rows: map[uuid.UUID]*storage.Resource{}}
	gw := NewAgentGateway(agent.NewService(agentRepo), resource.NewService(resRepo), Options{
		EnforceBinding: enforce,
		LeaseTTL:       30 * time.Second,
		AttemptBudget:  3,
	})
	return gw, agentRepo, resRepo
}

func addResource(f *fakeResourceRepo, state string, agentID int64, gen, observed int64, meta string) uuid.UUID {
	id := uuid.New()
	r := &storage.Resource{
		ResourceUuid:       id,
		ProjectID:          1,
		Kind:               "Container",
		Name:               "c-" + id.String()[:8],
		State:              state,
		Spec:               []byte(`{"image":"nginx:1"}`),
		Status:             []byte("{}"),
		Generation:         gen,
		ObservedGeneration: observed,
		Metadata:           []byte(meta),
	}
	if agentID != 0 {
		r.AgentID = pgtype.Int8{Int64: agentID, Valid: true}
	}
	f.rows[id] = r
	return id
}

// --- identity binding -------------------------------------------------------

func TestRegisterBindsFirstSubjectAndRejectsTakeover(t *testing.T) {
	gw, agentRepo, _ := newGatewayFixture(t, true)
	agentUUID := agentRepo.row.AgentUuid.String()

	// First authenticated Register binds.
	resp, err := gw.Register(ctxWithSubject("svc-agent-1"), &corev1.RegisterRequest{AgentUuid: agentUUID})
	require.NoError(t, err)
	assert.True(t, resp.GetOk())
	assert.Equal(t, "svc-agent-1", agentRepo.row.BoundSubject)

	// Same subject re-registers freely (agent restart).
	_, err = gw.Register(ctxWithSubject("svc-agent-1"), &corev1.RegisterRequest{AgentUuid: agentUUID})
	require.NoError(t, err)

	// A different principal knowing the UUID must NOT be able to take over.
	_, err = gw.Register(ctxWithSubject("svc-attacker"), &corev1.RegisterRequest{AgentUuid: agentUUID})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
	assert.Equal(t, "svc-agent-1", agentRepo.row.BoundSubject, "binding must survive the takeover attempt")
}

func TestRegisterRequiresSubjectWhenEnforced(t *testing.T) {
	gw, agentRepo, _ := newGatewayFixture(t, true)
	// No claims in context (e.g. mis-wired interceptor) → fail closed.
	_, err := gw.Register(context.Background(), &corev1.RegisterRequest{AgentUuid: agentRepo.row.AgentUuid.String()})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestGatewayCallsEnforceSubjectBinding(t *testing.T) {
	gw, agentRepo, resRepo := newGatewayFixture(t, true)
	agentUUID := agentRepo.row.AgentUuid.String()
	addResource(resRepo, "pending", 7, 1, 0, "{}")

	tests := []struct {
		name string
		call func(ctx context.Context) error
	}{
		{"heartbeat", func(ctx context.Context) error {
			_, err := gw.Heartbeat(ctx, &corev1.HeartbeatRequest{AgentUuid: agentUUID})
			return err
		}},
		{"pullwork", func(ctx context.Context) error {
			_, err := gw.PullWork(ctx, &corev1.PullWorkRequest{AgentUuid: agentUUID})
			return err
		}},
		{"reportstatus", func(ctx context.Context) error {
			_, err := gw.ReportStatus(ctx, &corev1.ReportStatusRequest{AgentUuid: agentUUID})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name+" unbound agent fails closed", func(t *testing.T) {
			agentRepo.row.BoundSubject = ""
			err := tt.call(ctxWithSubject("svc-agent-1"))
			require.Error(t, err)
			assert.Equal(t, codes.PermissionDenied, status.Code(err))
		})
		t.Run(tt.name+" wrong subject denied", func(t *testing.T) {
			agentRepo.row.BoundSubject = "svc-agent-1"
			err := tt.call(ctxWithSubject("svc-attacker"))
			require.Error(t, err)
			assert.Equal(t, codes.PermissionDenied, status.Code(err))
		})
		t.Run(tt.name+" bound subject allowed", func(t *testing.T) {
			agentRepo.row.BoundSubject = "svc-agent-1"
			require.NoError(t, tt.call(ctxWithSubject("svc-agent-1")))
		})
	}
}

func TestDevOpenSkipsBinding(t *testing.T) {
	gw, agentRepo, _ := newGatewayFixture(t, false)
	agentUUID := agentRepo.row.AgentUuid.String()

	// No claims at all (dev-open listener has no interceptor).
	_, err := gw.Register(context.Background(), &corev1.RegisterRequest{AgentUuid: agentUUID})
	require.NoError(t, err)
	assert.Empty(t, agentRepo.row.BoundSubject, "dev-open must not bind an empty subject")

	_, err = gw.Heartbeat(context.Background(), &corev1.HeartbeatRequest{AgentUuid: agentUUID})
	require.NoError(t, err)
}

// --- work dispatch ------------------------------------------------------------

func TestPullWorkScopesToCallerAndLeases(t *testing.T) {
	gw, agentRepo, resRepo := newGatewayFixture(t, true)
	agentRepo.row.BoundSubject = "svc-agent-1"
	agentUUID := agentRepo.row.AgentUuid.String()

	mine := addResource(resRepo, "pending", 7, 1, 0, "{}")
	unassigned := addResource(resRepo, "pending", 0, 1, 0, "{}")
	theirs := addResource(resRepo, "pending", 9, 1, 0, "{}")
	deleting := addResource(resRepo, "deleting", 7, 1, 1, `{"tier":"system"}`)

	resp, err := gw.PullWork(ctxWithSubject("svc-agent-1"), &corev1.PullWorkRequest{AgentUuid: agentUUID, MaxItems: 10})
	require.NoError(t, err)

	got := map[string]*corev1.WorkItem{}
	for _, it := range resp.GetItems() {
		got[it.GetResourceUuid()] = it
	}
	assert.Contains(t, got, mine.String())
	assert.Contains(t, got, unassigned.String())
	assert.Contains(t, got, deleting.String())
	assert.NotContains(t, got, theirs.String(), "another agent's items must never be dispatched")

	// The claim call carried the caller's agent id and the configured lease.
	require.Len(t, resRepo.claimCalls, 1)
	assert.Equal(t, int64(7), resRepo.claimCalls[0].AgentID.Int64)
	assert.Equal(t, float64(30), resRepo.claimCalls[0].LeaseSeconds)

	// Sticky claim: the unassigned row now belongs to the caller.
	assert.Equal(t, int64(7), resRepo.rows[unassigned].AgentID.Int64)

	// Envelope contract (twin: ../maintainerd-agent/internal/worker/envelope.go).
	var plain map[string]any
	require.NoError(t, json.Unmarshal([]byte(got[mine.String()].GetSpecJson()), &plain))
	assert.Equal(t, map[string]any{"image": "nginx:1"}, plain["workload"])
	_, hasTeardown := plain["teardown"]
	assert.False(t, hasTeardown)

	var teardown map[string]any
	require.NoError(t, json.Unmarshal([]byte(got[deleting.String()].GetSpecJson()), &teardown))
	assert.Equal(t, true, teardown["teardown"])
	assert.Equal(t, "system", teardown["tier"])
}

func TestBuildEnvelope(t *testing.T) {
	tests := []struct {
		name string
		item resource.WorkItem
		want map[string]any
	}{
		{
			name: "plain spec becomes the workload",
			item: resource.WorkItem{Spec: map[string]any{"image": "nginx:1", "name": "web"}},
			want: map[string]any{"workload": map[string]any{"image": "nginx:1", "name": "web"}},
		},
		{
			name: "pre-enveloped spec passes its workload through unchanged",
			item: resource.WorkItem{Spec: map[string]any{
				"workload": map[string]any{"image": "redis:7"},
				"tier":     "ignored — derived from metadata, not spec",
			}},
			want: map[string]any{"workload": map[string]any{"image": "redis:7"}},
		},
		{
			name: "system tier from metadata",
			item: resource.WorkItem{
				Spec:     map[string]any{"image": "auth:1"},
				Metadata: map[string]any{"tier": "system"},
			},
			want: map[string]any{"workload": map[string]any{"image": "auth:1"}, "tier": "system"},
		},
		{
			name: "non-system tier omitted",
			item: resource.WorkItem{
				Spec:     map[string]any{"image": "app:1"},
				Metadata: map[string]any{"tier": "user"},
			},
			want: map[string]any{"workload": map[string]any{"image": "app:1"}},
		},
		{
			name: "deleting state emits teardown",
			item: resource.WorkItem{State: "deleting", Spec: map[string]any{"image": "app:1"}},
			want: map[string]any{"workload": map[string]any{"image": "app:1"}, "teardown": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := buildEnvelope(tt.item)
			require.NoError(t, err)
			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &got))
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- status reporting ---------------------------------------------------------

func TestReportStatusEnforcesOwnershipPerReport(t *testing.T) {
	gw, agentRepo, resRepo := newGatewayFixture(t, true)
	agentRepo.row.BoundSubject = "svc-agent-1"
	agentUUID := agentRepo.row.AgentUuid.String()

	mine := addResource(resRepo, "pending", 7, 1, 0, "{}")
	theirs := addResource(resRepo, "pending", 9, 1, 0, "{}")
	unassigned := addResource(resRepo, "pending", 0, 1, 0, "{}")

	resp, err := gw.ReportStatus(ctxWithSubject("svc-agent-1"), &corev1.ReportStatusRequest{
		AgentUuid: agentUUID,
		Reports: []*corev1.StatusReport{
			{ResourceUuid: mine.String(), State: "running", ObservedGeneration: 1},
			{ResourceUuid: theirs.String(), State: "running", ObservedGeneration: 1},
			{ResourceUuid: unassigned.String(), State: "running", ObservedGeneration: 1},
			{ResourceUuid: "not-a-uuid", State: "running"},
		},
	})
	require.NoError(t, err, "a rejected report must not fail the batch")
	assert.Equal(t, int32(1), resp.GetAccepted())

	// Only the owned resource was written.
	require.Len(t, resRepo.reports, 1)
	assert.Equal(t, mine, resRepo.reports[0].ResourceUuid)
	assert.Equal(t, "pending", resRepo.rows[theirs].State, "foreign resource must be untouched")
	assert.Equal(t, "pending", resRepo.rows[unassigned].State, "unassigned resource has no legitimate reporter")
}

func TestReportStatusTeardownFinalizes(t *testing.T) {
	gw, agentRepo, resRepo := newGatewayFixture(t, true)
	agentRepo.row.BoundSubject = "svc-agent-1"
	agentUUID := agentRepo.row.AgentUuid.String()

	deleting := addResource(resRepo, "deleting", 7, 2, 2, "{}")

	resp, err := gw.ReportStatus(ctxWithSubject("svc-agent-1"), &corev1.ReportStatusRequest{
		AgentUuid: agentUUID,
		Reports:   []*corev1.StatusReport{{ResourceUuid: deleting.String(), State: "removed", ObservedGeneration: 2}},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), resp.GetAccepted())
	require.Len(t, resRepo.reports, 1)
	assert.True(t, resRepo.reports[0].Finalize, "removed after deleting must finalize the soft delete")
	assert.True(t, resRepo.rows[deleting].DeletedAt.Valid)
}
