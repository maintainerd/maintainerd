package authctrl

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/steward"
	"github.com/maintainerd/core/internal/storage"
)

type fakeControlPlaneStore struct {
	row storage.ControlPlane
	err error
}

func (f *fakeControlPlaneStore) GetControlPlane(context.Context) (storage.ControlPlane, error) {
	return f.row, f.err
}

func provisionedRow(t *testing.T) storage.ControlPlane {
	t.Helper()
	pem, _, err := steward.GenerateClientKey()
	require.NoError(t, err)
	return storage.ControlPlane{
		ControlPrivateKeyPem: pem,
		Data:                 []byte(`{"control_client_id":"uuid-form","control_oauth_client_id":"oauth-abc","auth_tenant_id":"11111111-1111-1111-1111-111111111111"}`),
	}
}

func TestLoadIdentityReturnsControlCredential(t *testing.T) {
	identity, err := LoadIdentity(context.Background(), &fakeControlPlaneStore{row: provisionedRow(t)})
	require.NoError(t, err)

	// The OAuth client_id, not the client UUID: auth matches the assertion's iss
	// and sub against its identifier column.
	assert.Equal(t, "oauth-abc", identity.ClientID)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", identity.TenantID)
	require.NotNil(t, identity.PrivateKey)
	// The kid must match the JWKS auth already holds, or every assertion signed
	// with this key selects the wrong verification key.
	assert.Equal(t, steward.KeyID(&identity.PrivateKey.PublicKey), identity.KeyID)
}

func TestLoadIdentityFallsBackToTenantColumn(t *testing.T) {
	// A setup run that skipped CreateTenant records no auth_tenant_id in data,
	// but the dedicated column is still populated.
	row := provisionedRow(t)
	row.Data = []byte(`{"control_oauth_client_id":"oauth-abc"}`)
	tenantUUID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	row.AuthTenantUuid = pgtype.UUID{Bytes: tenantUUID, Valid: true}

	identity, err := LoadIdentity(context.Background(), &fakeControlPlaneStore{row: row})
	require.NoError(t, err)
	assert.Equal(t, tenantUUID.String(), identity.TenantID)
}

func TestLoadIdentityReportsNotProvisioned(t *testing.T) {
	provisioned := provisionedRow(t)

	noOAuthID := provisioned
	noOAuthID.Data = []byte(`{"control_client_id":"uuid-form","auth_tenant_id":"11111111-1111-1111-1111-111111111111"}`)

	noTenant := provisioned
	noTenant.Data = []byte(`{"control_oauth_client_id":"oauth-abc"}`)

	noKey := provisioned
	noKey.ControlPrivateKeyPem = "   "

	tests := []struct {
		name  string
		store *fakeControlPlaneStore
	}{
		{"no store at all", nil},
		{"fresh install, no row", &fakeControlPlaneStore{err: pgx.ErrNoRows}},
		{"row with no key", &fakeControlPlaneStore{row: noKey}},
		{"key but no oauth client id", &fakeControlPlaneStore{row: noOAuthID}},
		{"key but no tenant", &fakeControlPlaneStore{row: noTenant}},
		{"empty row", &fakeControlPlaneStore{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var store ControlPlaneStore
			if tt.store != nil {
				store = tt.store
			}
			_, err := LoadIdentity(context.Background(), store)
			// The distinct sentinel is the contract: callers log-and-retry on it
			// instead of treating a not-yet-provisioned install as a crash.
			require.ErrorIs(t, err, ErrNoControlIdentity)
		})
	}
}

func TestLoadIdentitySurfacesRealFailuresSeparately(t *testing.T) {
	t.Run("storage failure is not mistaken for not-provisioned", func(t *testing.T) {
		boom := errors.New("connection refused")
		_, err := LoadIdentity(context.Background(), &fakeControlPlaneStore{err: boom})
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrNoControlIdentity)
		assert.ErrorIs(t, err, boom)
	})

	t.Run("corrupt key is not mistaken for not-provisioned", func(t *testing.T) {
		row := provisionedRow(t)
		row.ControlPrivateKeyPem = "-----BEGIN RSA PRIVATE KEY-----\nZm9v\n-----END RSA PRIVATE KEY-----"
		_, err := LoadIdentity(context.Background(), &fakeControlPlaneStore{row: row})
		require.Error(t, err)
		// Retrying forever against a key that will never parse hides corruption.
		assert.NotErrorIs(t, err, ErrNoControlIdentity)
		assert.ErrorContains(t, err, "unusable")
	})

	t.Run("undecodable data blob", func(t *testing.T) {
		row := provisionedRow(t)
		row.Data = []byte(`{not json`)
		_, err := LoadIdentity(context.Background(), &fakeControlPlaneStore{row: row})
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrNoControlIdentity)
	})
}
