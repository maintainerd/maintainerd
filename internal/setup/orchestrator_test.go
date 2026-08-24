package setup

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/storage"
)

// fakeControlPlaneStore backs the orchestrator's control_plane paths in tests.
type fakeControlPlaneStore struct {
	row     storage.ControlPlane
	has     bool
	upserts []storage.UpsertControlPlaneParams
}

func (f *fakeControlPlaneStore) GetControlPlane(context.Context) (storage.ControlPlane, error) {
	if !f.has {
		return storage.ControlPlane{}, pgx.ErrNoRows
	}
	return f.row, nil
}

func (f *fakeControlPlaneStore) UpsertControlPlane(_ context.Context, arg storage.UpsertControlPlaneParams) (storage.ControlPlane, error) {
	f.upserts = append(f.upserts, arg)

	// MODELS THE REAL QUERY, not a naive assignment. control_plane.sql resolves the
	// key with COALESCE(NULLIF(EXCLUDED…, ''), existing): an empty incoming key
	// means "leave it alone", because blanking it would leave auth verifying
	// private_key_jwt against a public key whose private half no longer exists.
	// A fake that assigned blindly would let a key-blanking regression pass here
	// and only fail in a deployed install, which is the whole class of bug this
	// fake exists to catch.
	key := arg.ControlPrivateKeyPem
	if key == "" {
		key = f.row.ControlPrivateKeyPem
	}

	f.has = true
	f.row = storage.ControlPlane{
		ID:                   1,
		AuthTenantUuid:       arg.AuthTenantUuid,
		Data:                 arg.Data,
		ControlPrivateKeyPem: key,
		DeploymentMode:       arg.DeploymentMode,
		SetupCompletedAt:     arg.SetupCompletedAt,
	}
	return f.row, nil
}

func newTestOrchestrator(cp controlPlaneStore, cfg Config) *Orchestrator {
	return &Orchestrator{cp: cp, cfg: cfg}
}

func TestRunWithSingleFlight(t *testing.T) {
	o := newTestOrchestrator(&fakeControlPlaneStore{}, Config{})

	// Simulate a run in flight (the boot goroutine or the wizard).
	require.True(t, o.tryBegin())
	_, err := o.RunWith(context.Background(), RunInput{})
	assert.ErrorIs(t, err, ErrSetupRunning,
		"a second concurrent run must fail fast instead of racing the first")
	o.end()

	// Once released the guard opens again (the run then fails on missing
	// AUTH_SETUP_ADDR, proving it got PAST the single-flight gate).
	_, err = o.RunWith(context.Background(), RunInput{AdminPassword: "x"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrSetupRunning)
	assert.Contains(t, err.Error(), "AUTH_SETUP_ADDR")
}

func TestControlKeyReusesPersistedKey(t *testing.T) {
	pem, jwks, err := generateControlKey()
	require.NoError(t, err)

	cp := &fakeControlPlaneStore{has: true, row: storage.ControlPlane{ControlPrivateKeyPem: pem}}
	o := newTestOrchestrator(cp, Config{})

	gotPEM, gotJWKS, err := o.controlKey(context.Background())
	require.NoError(t, err)
	assert.Equal(t, pem, gotPEM, "the persisted key must be reused, never regenerated")
	assert.JSONEq(t, jwks, gotJWKS, "the derived JWKS must match the one originally registered in Auth")
}

func TestControlKeyGeneratesOnFirstRun(t *testing.T) {
	o := newTestOrchestrator(&fakeControlPlaneStore{}, Config{})
	pem, jwks, err := o.controlKey(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, pem)
	assert.Contains(t, jwks, `"keys"`)
}

func TestControlKeyRefusesToOverwriteCorruptKey(t *testing.T) {
	cp := &fakeControlPlaneStore{has: true, row: storage.ControlPlane{ControlPrivateKeyPem: "not a pem"}}
	o := newTestOrchestrator(cp, Config{})
	_, _, err := o.controlKey(context.Background())
	require.Error(t, err, "corrupt credential state must surface, not be papered over with a fresh key")
	assert.Contains(t, err.Error(), "refusing to overwrite")
}

func TestPersistStampsDeploymentMode(t *testing.T) {
	cp := &fakeControlPlaneStore{}
	o := newTestOrchestrator(cp, Config{DeploymentMode: "kubernetes"})
	res := &Result{AuthTenantID: "not-a-uuid", CompletedAt: time.Now()}
	require.NoError(t, o.persist(context.Background(), res, "pem"))
	require.Len(t, cp.upserts, 1)
	assert.Equal(t, "kubernetes", cp.upserts[0].DeploymentMode)
}

func TestStatusLoadsDeploymentMode(t *testing.T) {
	data, err := json.Marshal(&Result{ControlClientID: "cc-1"})
	require.NoError(t, err)
	cp := &fakeControlPlaneStore{has: true, row: storage.ControlPlane{
		Data:             data,
		DeploymentMode:   "docker",
		SetupCompletedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}}
	o := newTestOrchestrator(cp, Config{})
	completed, res := o.Status(context.Background())
	assert.True(t, completed)
	assert.Equal(t, "docker", res.DeploymentMode)
	assert.Equal(t, "cc-1", res.ControlClientID)
}

func TestRunWithRetryStopsOnCancel(t *testing.T) {
	// Guard against a regression where ErrSetupRunning (or any failure) makes
	// RunWithRetry spin without honoring ctx.
	o := newTestOrchestrator(&fakeControlPlaneStore{}, Config{})
	require.True(t, o.tryBegin()) // force every attempt into ErrSetupRunning
	defer o.end()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { o.RunWithRetry(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunWithRetry did not stop on context cancellation")
	}
}

func TestErrSetupRunningIsSentinel(t *testing.T) {
	assert.True(t, errors.Is(ErrSetupRunning, ErrSetupRunning))
}

// TestPersistRecordsCompletionWithoutTouchingTheControlKey covers the write that
// unsticks the already-provisioned path, and the hazard it introduced.
//
// THE TRAP IT FIXES. Auth's setup lock is one-shot and durable, so once auth
// reports complete every later boot takes the already-provisioned branch. That
// branch used to return WITHOUT persisting, so an install whose auth database
// survived while core's was reset — they are separate volumes — never stamped
// setup_completed_at of its own. /setup/status answered {"completed": false}
// forever, the console pinned the operator to the wizard with no route to the
// dashboard, and re-running the wizard could not help because it lands on the same
// branch.
//
// THE HAZARD. Recording completion means calling persist with no private key in
// hand: that path deliberately does not regenerate one, because auth holds the
// matching JWKS. So persist must record the timestamp and leave the stored key
// exactly as it was. Blanking it would refuse every control-plane call
// permanently, which is strictly worse than the stuck wizard.
func TestPersistRecordsCompletionWithoutTouchingTheControlKey(t *testing.T) {
	const stored = "-----BEGIN PRIVATE KEY-----stored-----END PRIVATE KEY-----"
	cp := &fakeControlPlaneStore{
		has: true,
		row: storage.ControlPlane{ID: 1, ControlPrivateKeyPem: stored},
	}
	o := newTestOrchestrator(cp, Config{})

	completedAt := time.Now()
	res := &Result{AlreadyComplete: true, CompletedAt: completedAt}

	if err := o.persist(context.Background(), res, ""); err != nil {
		t.Fatalf("persist: %v", err)
	}

	if !cp.row.SetupCompletedAt.Valid {
		t.Fatal("setup_completed_at was not recorded — the wizard stays on screen forever")
	}
	if cp.row.ControlPrivateKeyPem != stored {
		t.Fatalf("the stored control key was overwritten: got %q, want it untouched", cp.row.ControlPrivateKeyPem)
	}
}

// TestPersistStoresAFreshKeyOnFirstRun is the other half: the empty-means-preserve
// rule must not stop a genuine first run from writing its newly minted key.
func TestPersistStoresAFreshKeyOnFirstRun(t *testing.T) {
	const minted = "-----BEGIN PRIVATE KEY-----minted-----END PRIVATE KEY-----"
	cp := &fakeControlPlaneStore{}
	o := newTestOrchestrator(cp, Config{})

	res := &Result{CompletedAt: time.Now()}
	if err := o.persist(context.Background(), res, minted); err != nil {
		t.Fatalf("persist: %v", err)
	}

	if cp.row.ControlPrivateKeyPem != minted {
		t.Fatalf("first run did not store its control key: got %q, want %q", cp.row.ControlPrivateKeyPem, minted)
	}
}
