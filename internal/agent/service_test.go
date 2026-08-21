package agent

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/platform/apperror"
	"github.com/maintainerd/core/internal/storage"
)

// fakeRepo implements Repository for the identity-binding paths; the untested
// paths panic so an accidental call is loud.
type fakeRepo struct {
	row    storage.Agent
	exists bool
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
func (f *fakeRepo) AgentHeartbeat(context.Context, uuid.UUID) (storage.Agent, error) {
	panic("not used")
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
