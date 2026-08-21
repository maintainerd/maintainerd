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
}

func (f *fakeRepo) GetProjectByUUID(context.Context, uuid.UUID) (storage.Project, error) {
	panic("not used")
}
func (f *fakeRepo) GetProjectByID(context.Context, int64) (storage.Project, error) {
	return storage.Project{ProjectID: 1, ProjectUuid: uuid.New()}, nil
}
func (f *fakeRepo) GetProviderByUUID(context.Context, uuid.UUID) (storage.Provider, error) {
	panic("not used")
}
func (f *fakeRepo) CreateResource(context.Context, storage.CreateResourceParams) (storage.Resource, error) {
	panic("not used")
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
	return out, nil
}
func (f *fakeRepo) MarkResourceDeleting(_ context.Context, id uuid.UUID) error {
	f.deleted = append(f.deleted, id)
	return nil
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
