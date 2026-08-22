package resource

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/platform/apperror"
	"github.com/maintainerd/core/internal/storage"
)

// fakeRepo implements Repository for the agent-report/claim/delete paths; the
// untested paths panic so an accidental call is loud.
type fakeRepo struct {
	row        storage.Resource
	lastReport *storage.ApplyAgentReportParams
	lastClaim  *storage.ClaimAgentWorkParams
	deleted    []uuid.UUID

	// supervision paths
	systemTier   []storage.Resource
	redispatched []uuid.UUID
	flagged      []uuid.UUID
}

func (f *fakeRepo) GetProjectByUUID(context.Context, uuid.UUID) (storage.Project, error) {
	return storage.Project{ProjectID: 1, ProjectUuid: uuid.New(), TenantID: 1, Name: "billing-app"}, nil
}
func (f *fakeRepo) GetProjectByID(context.Context, int64) (storage.Project, error) {
	return storage.Project{ProjectID: 1, ProjectUuid: uuid.New(), TenantID: 1, Name: "billing-app"}, nil
}
func (f *fakeRepo) GetTenantByID(context.Context, int64) (storage.Tenant, error) {
	return storage.Tenant{TenantID: 1, Name: "acme"}, nil
}
func (f *fakeRepo) GetProviderByUUID(context.Context, uuid.UUID) (storage.Provider, error) {
	panic("not used")
}
func (f *fakeRepo) CreateResource(context.Context, storage.CreateResourceParams) (storage.Resource, error) {
	return f.row, nil
}
func (f *fakeRepo) GetResourceByUUID(_ context.Context, id uuid.UUID) (storage.Resource, error) {
	if id != f.row.ResourceUuid {
		return storage.Resource{}, pgx.ErrNoRows
	}
	return f.row, nil
}
func (f *fakeRepo) ListResourcesByProject(context.Context, storage.ListResourcesByProjectParams) ([]storage.Resource, error) {
	panic("not used")
}
func (f *fakeRepo) CountResourcesByProject(context.Context, int64) (int64, error) { panic("not used") }
func (f *fakeRepo) UpdateResourceSpec(context.Context, storage.UpdateResourceSpecParams) (storage.Resource, error) {
	panic("not used")
}
func (f *fakeRepo) UpdateResourceStatus(context.Context, storage.UpdateResourceStatusParams) (storage.Resource, error) {
	panic("not used")
}
func (f *fakeRepo) ListOutOfSyncResources(context.Context, int32) ([]storage.Resource, error) {
	panic("not used")
}
func (f *fakeRepo) ClaimAgentWork(_ context.Context, arg storage.ClaimAgentWorkParams) ([]storage.Resource, error) {
	f.lastClaim = &arg
	return []storage.Resource{f.row}, nil
}
func (f *fakeRepo) ApplyAgentReport(_ context.Context, arg storage.ApplyAgentReportParams) (storage.Resource, error) {
	f.lastReport = &arg
	out := f.row
	out.State = arg.State
	out.Health = arg.Health
	return out, nil
}
func (f *fakeRepo) MarkResourceDeleting(_ context.Context, id uuid.UUID) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeRepo) ListSystemTierResources(context.Context) ([]storage.Resource, error) {
	return f.systemTier, nil
}

// RedispatchSystemResource mirrors the SQL: generation bumped, state re-armed,
// lease + retry budget cleared — and the 'deleting' guard.
func (f *fakeRepo) RedispatchSystemResource(_ context.Context, id uuid.UUID) (storage.Resource, error) {
	if id != f.row.ResourceUuid || f.row.State == "deleting" {
		return storage.Resource{}, pgx.ErrNoRows
	}
	f.redispatched = append(f.redispatched, id)
	f.row.Generation++
	f.row.State = "pending"
	f.row.LeasedUntil = pgtype.Timestamptz{}
	f.row.Attempts = 0
	f.row.NextAttemptAt = pgtype.Timestamptz{}
	return f.row, nil
}

// FlagResourceHostUnreachable mirrors the SQL's idempotency guard: a second call
// within the same episode matches no rows.
func (f *fakeRepo) FlagResourceHostUnreachable(_ context.Context, id uuid.UUID) (storage.Resource, error) {
	if id != f.row.ResourceUuid {
		return storage.Resource{}, pgx.ErrNoRows
	}
	for _, seen := range f.flagged {
		if seen == id {
			return storage.Resource{}, pgx.ErrNoRows
		}
	}
	f.flagged = append(f.flagged, id)
	f.row.Health = HealthUnknown
	f.row.Status = []byte(`{"host_unreachable":true}`)
	return f.row, nil
}

func newRow(state string, agentID int64, gen, observed int64, attempts int32) storage.Resource {
	r := storage.Resource{
		ResourceUuid:       uuid.New(),
		ProjectID:          1,
		Kind:               "Container",
		Name:               "c1",
		State:              state,
		Spec:               []byte(`{}`),
		Status:             []byte(`{}`),
		Metadata:           []byte(`{}`),
		Generation:         gen,
		ObservedGeneration: observed,
		Attempts:           attempts,
	}
	if agentID != 0 {
		r.AgentID = pgtype.Int8{Int64: agentID, Valid: true}
	}
	return r
}

func TestNextBackoff(t *testing.T) {
	tests := []struct {
		attempts int32
		want     time.Duration
	}{
		{0, 5 * time.Second}, // defensive floor
		{1, 5 * time.Second},
		{2, 10 * time.Second},
		{3, 20 * time.Second},
		{4, 40 * time.Second},
		{5, 80 * time.Second},
		{6, 160 * time.Second},
		{7, 5 * time.Minute}, // 320s capped
		{50, 5 * time.Minute},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, nextBackoff(tt.attempts), "attempts=%d", tt.attempts)
	}
}

func TestApplyAgentReportOwnership(t *testing.T) {
	tests := []struct {
		name    string
		agentID int64 // on the row; caller is always 7
	}{
		{"assigned to another agent", 9},
		{"unassigned", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{row: newRow("pending", tt.agentID, 1, 0, 0)}
			svc := NewService(repo)
			_, err := svc.ApplyAgentReport(context.Background(), 7, repo.row.ResourceUuid,
				AgentReportInput{State: "running", ObservedGeneration: 1}, 10)
			require.Error(t, err)
			var forbidden *apperror.ForbiddenError
			assert.ErrorAs(t, err, &forbidden)
			assert.Nil(t, repo.lastReport, "an unowned report must never reach storage")
		})
	}
}

func TestApplyAgentReportStateMachine(t *testing.T) {
	const budget = 3
	tests := []struct {
		name        string
		row         storage.Resource
		in          AgentReportInput
		wantState   string
		wantAttempt int32
		wantBackoff bool  // next_attempt_at set
		wantObs     int64 // observed_generation param passed to storage
		wantFinal   bool
	}{
		{
			name:        "running resets the retry budget and advances observation",
			row:         newRow("error", 7, 3, 1, 2),
			in:          AgentReportInput{State: "running", ObservedGeneration: 3},
			wantState:   "running",
			wantAttempt: 0,
			wantObs:     3,
		},
		{
			name:        "failure under budget backs off as error and freezes observation",
			row:         newRow("pending", 7, 2, 1, 0),
			in:          AgentReportInput{State: "failed", ObservedGeneration: 2},
			wantState:   "error",
			wantAttempt: 1,
			wantBackoff: true,
			wantObs:     0, // never advanced on failure — the item must stay out-of-sync
		},
		{
			name:        "failure exhausting the budget parks as failed",
			row:         newRow("error", 7, 2, 1, budget-1),
			in:          AgentReportInput{State: "failed"},
			wantState:   "failed",
			wantAttempt: budget,
			wantObs:     0,
		},
		{
			name:        "failed teardown keeps the deleting intent",
			row:         newRow("deleting", 7, 2, 2, 0),
			in:          AgentReportInput{State: "failed"},
			wantState:   "deleting",
			wantAttempt: 1,
			wantBackoff: true,
			wantObs:     0,
		},
		{
			name:      "removed after deleting finalizes",
			row:       newRow("deleting", 7, 2, 2, 0),
			in:        AgentReportInput{State: "removed", ObservedGeneration: 2},
			wantState: "removed",
			wantObs:   2,
			wantFinal: true,
		},
		{
			name:      "removed outside a teardown is recorded but never finalizes",
			row:       newRow("running", 7, 2, 2, 0),
			in:        AgentReportInput{State: "removed", ObservedGeneration: 2},
			wantState: "removed",
			wantObs:   2,
			wantFinal: false,
		},
		{
			name:      "drift observation recorded as-is",
			row:       newRow("running", 7, 2, 2, 0),
			in:        AgentReportInput{State: "exited", ObservedGeneration: 2},
			wantState: "exited",
			wantObs:   2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{row: tt.row}
			svc := NewService(repo)
			_, err := svc.ApplyAgentReport(context.Background(), 7, tt.row.ResourceUuid, tt.in, budget)
			require.NoError(t, err)
			require.NotNil(t, repo.lastReport)
			assert.Equal(t, tt.wantState, repo.lastReport.State)
			assert.Equal(t, tt.wantAttempt, repo.lastReport.Attempts)
			assert.Equal(t, tt.wantObs, repo.lastReport.ObservedGeneration)
			assert.Equal(t, tt.wantFinal, repo.lastReport.Finalize)
			if tt.wantBackoff {
				require.True(t, repo.lastReport.NextAttemptAt.Valid, "backoff must be scheduled")
				assert.True(t, repo.lastReport.NextAttemptAt.Time.After(time.Now()), "backoff must be in the future")
			} else {
				assert.False(t, repo.lastReport.NextAttemptAt.Valid)
			}
		})
	}
}

func TestClaimForAgentPassesLeaseAndLimit(t *testing.T) {
	repo := &fakeRepo{row: newRow("pending", 7, 1, 0, 0)}
	svc := NewService(repo)
	items, err := svc.ClaimForAgent(context.Background(), 7, 5, 45*time.Second)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotNil(t, repo.lastClaim)
	assert.Equal(t, int64(7), repo.lastClaim.AgentID.Int64)
	assert.True(t, repo.lastClaim.AgentID.Valid)
	assert.Equal(t, float64(45), repo.lastClaim.LeaseSeconds)
	assert.Equal(t, int32(5), repo.lastClaim.MaxItems)
}

func TestDeleteMarksDeletingInsteadOfErasing(t *testing.T) {
	repo := &fakeRepo{row: newRow("running", 7, 1, 1, 0)}
	svc := NewService(repo)
	require.NoError(t, svc.Delete(context.Background(), repo.row.ResourceUuid))
	assert.Equal(t, []uuid.UUID{repo.row.ResourceUuid}, repo.deleted)
}

func TestResolveHealth(t *testing.T) {
	tests := []struct {
		name    string
		current string
		state   string
		status  map[string]any
		want    string
	}{
		{
			name:   "reported health wins",
			state:  "running",
			status: map[string]any{"health": "unhealthy"},
			want:   HealthUnhealthy,
		},
		{
			name:   "reported health is normalized",
			state:  "running",
			status: map[string]any{"health": "  HEALTHY "},
			want:   HealthHealthy,
		},
		{
			name:    "running without a health key keeps the previous value",
			current: HealthHealthy,
			state:   "running",
			status:  map[string]any{"container_id": "abc"},
			want:    HealthHealthy,
		},
		{
			// A stale "healthy" on a dead workload is the most dangerous value
			// in the table: supervision would read a down system service as fine.
			name:    "a failure report clears a stale healthy",
			current: HealthHealthy,
			state:   "failed",
			status:  map[string]any{"error": "image pull failed"},
			want:    HealthUnreported,
		},
		{
			name:    "a teardown report clears health",
			current: HealthHealthy,
			state:   "removed",
			status:  map[string]any{"removed": 1},
			want:    HealthUnreported,
		},
		{
			name:   "an over-long value from a remote agent is truncated, not rejected",
			state:  "running",
			status: map[string]any{"health": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			want:   "aaaaaaaaaaaaaaaaaaaa", // 20 chars = the column width
		},
		{
			name:   "nil status on a drift observation clears health",
			state:  "exited",
			status: nil,
			want:   HealthUnreported,
		},
		{
			// `"health": null` is "nothing to report", not the string "<nil>".
			name:    "a JSON null is treated as absent",
			current: HealthHealthy,
			state:   "running",
			status:  map[string]any{"health": nil},
			want:    HealthHealthy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveHealth(tt.current, tt.state, tt.status))
		})
	}
}

func TestApplyAgentReportPersistsHealth(t *testing.T) {
	repo := &fakeRepo{row: newRow("running", 7, 2, 2, 0)}
	svc := NewService(repo)
	res, err := svc.ApplyAgentReport(context.Background(), 7, repo.row.ResourceUuid, AgentReportInput{
		State:              "running",
		ObservedGeneration: 2,
		Status:             map[string]any{"container_id": "c1", "health": "unhealthy"},
	}, 10)
	require.NoError(t, err)
	require.NotNil(t, repo.lastReport)
	// "running" is not "working": the report says running AND unhealthy, and both
	// must survive into the row for supervision to act on it.
	assert.Equal(t, "running", repo.lastReport.State)
	assert.Equal(t, HealthUnhealthy, repo.lastReport.Health)
	assert.Equal(t, HealthUnhealthy, res.Health, "health is exposed on the API view")
}

func TestRedispatchSystem(t *testing.T) {
	t.Run("bumps generation, clears the lease and resets the retry budget", func(t *testing.T) {
		row := newRow("failed", 7, 3, 1, 9)
		row.LeasedUntil = pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}
		row.NextAttemptAt = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
		repo := &fakeRepo{row: row}

		inst, err := NewService(repo).RedispatchSystem(context.Background(), row.ResourceUuid)
		require.NoError(t, err)
		assert.Equal(t, []uuid.UUID{row.ResourceUuid}, repo.redispatched)
		assert.Equal(t, int64(4), inst.Generation, "generation must advance so the EXISTING feed re-offers it")
		assert.Equal(t, "pending", inst.State, "a system service is never left parked as failed")
		assert.False(t, inst.Leased, "the dead agent's lease must be released")
		assert.Equal(t, int32(0), inst.Attempts, "the retry budget must not park a system service")
		assert.False(t, repo.row.NextAttemptAt.Valid)
	})

	t.Run("refuses to resurrect a requested teardown", func(t *testing.T) {
		repo := &fakeRepo{row: newRow("deleting", 7, 2, 1, 0)}
		_, err := NewService(repo).RedispatchSystem(context.Background(), repo.row.ResourceUuid)
		var ferr *apperror.ForbiddenError
		require.ErrorAs(t, err, &ferr)
		assert.Empty(t, repo.redispatched, "keep-alive must never fight an explicit removal")
	})

	t.Run("unknown resource is not found", func(t *testing.T) {
		repo := &fakeRepo{row: newRow("running", 7, 1, 1, 0)}
		_, err := NewService(repo).RedispatchSystem(context.Background(), uuid.New())
		var nerr *apperror.NotFoundError
		assert.ErrorAs(t, err, &nerr)
	})
}

func TestFlagHostUnreachableIsIdempotentAndNeverReassigns(t *testing.T) {
	row := newRow("running", 7, 2, 2, 0)
	row.Health = HealthHealthy
	repo := &fakeRepo{row: row}
	svc := NewService(repo)

	flagged, err := svc.FlagHostUnreachable(context.Background(), row.ResourceUuid)
	require.NoError(t, err)
	assert.True(t, flagged, "the first call in an episode writes the flag")

	// Second pass in the same episode: no write, no second escalation.
	flagged, err = svc.FlagHostUnreachable(context.Background(), row.ResourceUuid)
	require.NoError(t, err)
	assert.False(t, flagged)
	assert.Len(t, repo.flagged, 1)

	// The agent assignment survives: docker mode has no scheduler and the data
	// may be host-local, so Core must not silently move the workload.
	assert.True(t, repo.row.AgentID.Valid)
	assert.Equal(t, int64(7), repo.row.AgentID.Int64)
	assert.Equal(t, HealthUnknown, repo.row.Health)
}

func TestListSystemTierProjectsSupervisionFields(t *testing.T) {
	row := newRow("error", 9, 4, 2, 3)
	row.Name = "system-auth"
	row.Health = HealthUnhealthy
	row.Metadata = []byte(`{"tier":"system"}`)
	row.LeasedUntil = pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}
	repo := &fakeRepo{systemTier: []storage.Resource{row}}

	got, err := NewService(repo).ListSystemTier(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	inst := got[0]
	assert.Equal(t, "system-auth", inst.Name)
	assert.Equal(t, HealthUnhealthy, inst.Health)
	assert.Equal(t, int64(9), inst.AgentID)
	assert.True(t, inst.Leased)
	assert.Equal(t, int32(3), inst.Attempts)
	assert.False(t, inst.Converged(), "observed 2 < generation 4")
	assert.False(t, inst.Terminating())
	assert.Equal(t, "system", inst.Metadata["tier"])
}

func TestSystemInstanceTerminating(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{"deleting", true},
		// A FINISHED teardown stamps deleted_at and leaves the feed, so a
		// visible 'removed' row means the workload vanished unasked — a
		// keep-alive case, not an exemption.
		{"removed", false},
		{"running", false},
		{"failed", false},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			assert.Equal(t, tt.want, SystemInstance{State: tt.state}.Terminating())
		})
	}
}

func TestBuildMRN(t *testing.T) {
	parts, err := buildMRN("acme", "billing-app", "Container", "web", map[string]any{
		"mrn_service":       "runtime",
		"mrn_resource_type": "instance",
		"mrn_resource_path": "web-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "runtime", parts.Service)
	assert.Equal(t, "acme", parts.Tenant)
	assert.Equal(t, "billing-app", parts.Project)
	assert.Equal(t, "instance", parts.ResourceType)
	assert.Equal(t, "web-1", parts.ResourcePath)
	assert.Equal(t, "mrn:runtime:acme:billing-app:instance/web-1",
		renderMRN(parts.Service, parts.Tenant, parts.Project, parts.ResourceType, parts.ResourcePath))
}

func TestValidateSpec(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		spec    []byte
		wantErr bool
	}{
		{"container legacy valid", "Container", []byte(`{"image":"nginx:1","name":"web"}`), false},
		{"workload envelope valid", "Workload", []byte(`{"workload":{"image":"nginx:1","name":"web"}}`), false},
		{"container missing image", "Container", []byte(`{"name":"web"}`), true},
		{"teardown rejected", "Container", []byte(`{"teardown":true}`), true},
		{"other kinds pass through", "Database", []byte(`{}`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSpec(tt.kind, tt.spec)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
