package agent

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

// fakeRepo implements Repository for the identity-binding and liveness paths;
// the untested paths panic so an accidental call is loud.
type fakeRepo struct {
	row    storage.Agent
	exists bool

	// fleet backs the liveness sweeper (SweepOffline / ListOffline). It mirrors
	// the SQL's semantics rather than returning canned rows, so the test pins
	// the actual rules (transitions only, never-seen rows left alone).
	fleet []storage.Agent
	// sweptSeconds records the threshold the service handed the query.
	sweptSeconds float64
}

func (f *fakeRepo) GetTenantByUUID(context.Context, uuid.UUID) (storage.Tenant, error) {
	panic("not used")
}
func (f *fakeRepo) GetTenantByID(context.Context, int64) (storage.Tenant, error) {
	return storage.Tenant{TenantID: 1, TenantUuid: uuid.New()}, nil
}
func (f *fakeRepo) CreateAgent(context.Context, storage.CreateAgentParams) (storage.Agent, error) {
	panic("not used")
}
func (f *fakeRepo) GetAgentByUUID(_ context.Context, id uuid.UUID) (storage.Agent, error) {
	if !f.exists || id != f.row.AgentUuid {
		return storage.Agent{}, pgx.ErrNoRows
	}
	return f.row, nil
}
func (f *fakeRepo) ListAgentsByTenant(context.Context, storage.ListAgentsByTenantParams) ([]storage.Agent, error) {
	panic("not used")
}
func (f *fakeRepo) CountAgentsByTenant(context.Context, int64) (int64, error) { panic("not used") }
func (f *fakeRepo) UpdateAgentStatus(context.Context, storage.UpdateAgentStatusParams) (storage.Agent, error) {
	panic("not used")
}

// BindAgentSubject mirrors the SQL's conditional-update semantics.
func (f *fakeRepo) BindAgentSubject(_ context.Context, arg storage.BindAgentSubjectParams) (storage.Agent, error) {
	if !f.exists || arg.AgentUuid != f.row.AgentUuid {
		return storage.Agent{}, pgx.ErrNoRows
	}
	if f.row.BoundSubject != "" && f.row.BoundSubject != arg.BoundSubject {
		return storage.Agent{}, pgx.ErrNoRows
	}
	f.row.BoundSubject = arg.BoundSubject
	return f.row, nil
}
func (f *fakeRepo) MarkAgentEnrolled(_ context.Context, arg storage.MarkAgentEnrolledParams) (storage.Agent, error) {
	if !f.exists || arg.AgentUuid != f.row.AgentUuid || f.row.JoinTokenUsedAt.Valid {
		return storage.Agent{}, pgx.ErrNoRows
	}
	f.row.JoinTokenUsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.row.ClientCertPem = arg.ClientCertPem
	return f.row, nil
}

// AgentHeartbeat mirrors the SQL: one statement stamps last_seen_at AND forces
// status back to 'online', which is the recovery path out of 'offline'.
func (f *fakeRepo) AgentHeartbeat(_ context.Context, id uuid.UUID) (storage.Agent, error) {
	for i := range f.fleet {
		if f.fleet[i].AgentUuid != id {
			continue
		}
		f.fleet[i].LastSeenAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		f.fleet[i].Status = "online"
		return f.fleet[i], nil
	}
	if !f.exists || id != f.row.AgentUuid {
		return storage.Agent{}, pgx.ErrNoRows
	}
	f.row.LastSeenAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.row.Status = "online"
	return f.row, nil
}

// MarkStaleAgentsOffline mirrors the SQL's WHERE clause: only rows that are not
// already offline, have been seen at least once, and whose last beat is older
// than the threshold — and it returns ONLY the rows it transitioned.
func (f *fakeRepo) MarkStaleAgentsOffline(_ context.Context, staleSeconds float64) ([]storage.Agent, error) {
	f.sweptSeconds = staleSeconds
	cutoff := time.Now().Add(-time.Duration(staleSeconds * float64(time.Second)))
	out := []storage.Agent{}
	for i := range f.fleet {
		a := &f.fleet[i]
		if a.DeletedAt.Valid || a.Status == "offline" || !a.LastSeenAt.Valid {
			continue
		}
		if !a.LastSeenAt.Time.Before(cutoff) {
			continue
		}
		a.Status = "offline"
		out = append(out, *a)
	}
	return out, nil
}

func (f *fakeRepo) ListOfflineAgents(context.Context) ([]storage.Agent, error) {
	out := []storage.Agent{}
	for _, a := range f.fleet {
		if !a.DeletedAt.Valid && a.Status == "offline" {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *fakeRepo) SoftDeleteAgent(context.Context, uuid.UUID) error { panic("not used") }

func newFake(bound string) *fakeRepo {
	return &fakeRepo{
		exists: true,
		row: storage.Agent{
			AgentID:      7,
			AgentUuid:    uuid.New(),
			TenantID:     1,
			Name:         "host-1",
			BoundSubject: bound,
			Capabilities: []byte("[]"),
			Metadata:     []byte("{}"),
		},
	}
}

func TestBindSubject(t *testing.T) {
	t.Run("empty subject rejected", func(t *testing.T) {
		repo := newFake("")
		_, err := NewService(repo).BindSubject(context.Background(), repo.row.AgentUuid, "")
		var verr *apperror.ValidationError
		assert.ErrorAs(t, err, &verr)
	})

	t.Run("first bind wins and is idempotent", func(t *testing.T) {
		repo := newFake("")
		svc := NewService(repo)
		a, err := svc.BindSubject(context.Background(), repo.row.AgentUuid, "svc-1")
		require.NoError(t, err)
		assert.Equal(t, "svc-1", a.BoundSubject)
		assert.Equal(t, int64(7), a.ID)

		// Same subject again: fine (agent restart).
		_, err = svc.BindSubject(context.Background(), repo.row.AgentUuid, "svc-1")
		require.NoError(t, err)
	})

	t.Run("different subject on a bound agent is forbidden", func(t *testing.T) {
		repo := newFake("svc-1")
		_, err := NewService(repo).BindSubject(context.Background(), repo.row.AgentUuid, "svc-2")
		var ferr *apperror.ForbiddenError
		require.ErrorAs(t, err, &ferr)
		assert.Equal(t, "svc-1", repo.row.BoundSubject, "binding must survive")
	})

	t.Run("unknown agent is not found", func(t *testing.T) {
		repo := newFake("")
		repo.exists = false
		_, err := NewService(repo).BindSubject(context.Background(), repo.row.AgentUuid, "svc-1")
		var nerr *apperror.NotFoundError
		assert.ErrorAs(t, err, &nerr)
	})
}

// fleetAgent builds a row for the liveness tests. lastSeen == 0 means the agent
// has never checked in.
func fleetAgent(id int64, name, status string, lastSeen time.Duration) storage.Agent {
	a := storage.Agent{
		AgentID:      id,
		AgentUuid:    uuid.New(),
		TenantID:     1,
		Name:         name,
		Status:       status,
		Capabilities: []byte("[]"),
		Metadata:     []byte("{}"),
	}
	if lastSeen > 0 {
		a.LastSeenAt = pgtype.Timestamptz{Time: time.Now().Add(-lastSeen), Valid: true}
	}
	return a
}

func TestSweepOffline(t *testing.T) {
	const threshold = 90 * time.Second

	tests := []struct {
		name           string
		fleet          []storage.Agent
		wantOffline    []string // names TRANSITIONED by this sweep
		wantStatusByID map[int64]string
	}{
		{
			name: "stale agents go offline, fresh ones are untouched",
			fleet: []storage.Agent{
				fleetAgent(1, "stale", "online", 5*time.Minute),
				fleetAgent(2, "fresh", "online", 3*time.Second),
			},
			wantOffline:    []string{"stale"},
			wantStatusByID: map[int64]string{1: "offline", 2: "online"},
		},
		{
			name: "an agent just inside the threshold is not flapped offline",
			fleet: []storage.Agent{
				fleetAgent(1, "slow-but-alive", "online", threshold-10*time.Second),
			},
			wantOffline:    []string{},
			wantStatusByID: map[int64]string{1: "online"},
		},
		{
			name: "a never-seen agent keeps its pending status",
			fleet: []storage.Agent{
				fleetAgent(1, "not-yet-enrolled", "pending", 0),
			},
			wantOffline:    []string{},
			wantStatusByID: map[int64]string{1: "pending"},
		},
		{
			name: "an already-offline agent is not reported again",
			fleet: []storage.Agent{
				fleetAgent(1, "long-gone", "offline", time.Hour),
			},
			wantOffline:    []string{},
			wantStatusByID: map[int64]string{1: "offline"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{fleet: tt.fleet}
			got, err := NewService(repo).SweepOffline(context.Background(), threshold)
			require.NoError(t, err)

			names := make([]string, 0, len(got))
			for _, a := range got {
				names = append(names, a.Name)
			}
			assert.ElementsMatch(t, tt.wantOffline, names)
			assert.Equal(t, threshold.Seconds(), repo.sweptSeconds)
			for id, want := range tt.wantStatusByID {
				for _, a := range repo.fleet {
					if a.AgentID == id {
						assert.Equal(t, want, a.Status, "agent %d", id)
					}
				}
			}
		})
	}
}

func TestSweepOfflineRejectsNonPositiveThreshold(t *testing.T) {
	// A zero threshold would mark the entire fleet offline on the first pass.
	repo := &fakeRepo{fleet: []storage.Agent{fleetAgent(1, "a", "online", time.Second)}}
	_, err := NewService(repo).SweepOffline(context.Background(), 0)
	var verr *apperror.ValidationError
	assert.ErrorAs(t, err, &verr)
	assert.Equal(t, "online", repo.fleet[0].Status, "nothing may be written on a rejected sweep")
}

// A heartbeat is the recovery path: an agent the sweeper marked offline must
// return to online on its very next beat, with no extra reconciliation.
func TestHeartbeatRestoresOnlineAfterSweep(t *testing.T) {
	repo := &fakeRepo{fleet: []storage.Agent{fleetAgent(1, "recovering", "online", 5*time.Minute)}}
	svc := NewService(repo)

	swept, err := svc.SweepOffline(context.Background(), 90*time.Second)
	require.NoError(t, err)
	require.Len(t, swept, 1)
	require.Equal(t, "offline", repo.fleet[0].Status)

	back, err := svc.Heartbeat(context.Background(), repo.fleet[0].AgentUuid)
	require.NoError(t, err)
	assert.Equal(t, "online", back.Status)
	require.NotNil(t, back.LastSeenAt)

	// And a second sweep no longer reports it — it is fresh again.
	swept, err = svc.SweepOffline(context.Background(), 90*time.Second)
	require.NoError(t, err)
	assert.Empty(t, swept)

	offline, err := svc.ListOffline(context.Background())
	require.NoError(t, err)
	assert.Empty(t, offline, "a recovered agent must leave the standing offline set")
}

func TestListOfflineReturnsTheStandingSet(t *testing.T) {
	// The standing set, unlike the sweep, keeps reporting hosts that went away
	// several passes ago — that is what makes stranded-workload detection exact.
	repo := &fakeRepo{fleet: []storage.Agent{
		fleetAgent(1, "gone-a", "offline", time.Hour),
		fleetAgent(2, "gone-b", "offline", 2*time.Hour),
		fleetAgent(3, "alive", "online", time.Second),
	}}
	got, err := NewService(repo).ListOffline(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.ElementsMatch(t, []string{"gone-a", "gone-b"}, []string{got[0].Name, got[1].Name})
}

func TestRequireSubject(t *testing.T) {
	tests := []struct {
		name          string
		bound         string
		subject       string
		wantForbidden bool
	}{
		{"match allowed", "svc-1", "svc-1", false},
		{"mismatch forbidden", "svc-1", "svc-2", true},
		{"unbound agent fails closed", "", "svc-1", true},
		{"empty caller subject forbidden", "svc-1", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFake(tt.bound)
			a, err := NewService(repo).RequireSubject(context.Background(), repo.row.AgentUuid, tt.subject)
			if !tt.wantForbidden {
				require.NoError(t, err)
				assert.Equal(t, int64(7), a.ID)
				return
			}
			var ferr *apperror.ForbiddenError
			assert.ErrorAs(t, err, &ferr)
		})
	}
}
