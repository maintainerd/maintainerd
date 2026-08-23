package setup

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	secretv1 "github.com/maintainerd/core/gen/maintainerd/secret/v1"
)

// fakeAuthStatus stands in for the auth orchestrator's control_plane read — the
// ordering gate's only input.
type fakeAuthStatus struct {
	completed bool
	result    *Result
}

func (f *fakeAuthStatus) Status(context.Context) (bool, *Result) {
	res := f.result
	if res == nil {
		res = &Result{}
	}
	return f.completed, res
}

// fakeSecretSetup records what core sent to secret's SetupService.
type fakeSecretSetup struct {
	mu sync.Mutex

	// completed is what GetSetupStatus reports.
	completed bool
	// unreachableFor makes the next N GetSetupStatus calls fail, standing in for
	// a secret that has not started listening yet.
	unreachableFor int

	statusCalls   int
	setupReqs     []*secretv1.SetupServiceSetupRequest
	completeReqs  []*secretv1.CompleteSetupRequest
	setupTokens   []string
	closes        int
	setupErr      error
	completeErr   error
	statusHardErr error
}

var errSecretUnreachable = errors.New("connect: connection refused")

func (f *fakeSecretSetup) token(ctx context.Context) string {
	md, _ := metadata.FromOutgoingContext(ctx)
	vals := md.Get(setupTokenKey)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func (f *fakeSecretSetup) GetSetupStatus(ctx context.Context, _ *secretv1.GetSetupStatusRequest, _ ...grpc.CallOption) (*secretv1.GetSetupStatusResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
	f.setupTokens = append(f.setupTokens, f.token(ctx))
	if f.statusHardErr != nil {
		return nil, f.statusHardErr
	}
	if f.unreachableFor > 0 {
		f.unreachableFor--
		return nil, errSecretUnreachable
	}
	return &secretv1.GetSetupStatusResponse{Completed: f.completed}, nil
}

func (f *fakeSecretSetup) Setup(ctx context.Context, req *secretv1.SetupServiceSetupRequest, _ ...grpc.CallOption) (*secretv1.SetupServiceSetupResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setupReqs = append(f.setupReqs, req)
	f.setupTokens = append(f.setupTokens, f.token(ctx))
	if f.setupErr != nil {
		return nil, f.setupErr
	}
	return &secretv1.SetupServiceSetupResponse{
		Tenant:      req.GetTenant(),
		Project:     req.GetProject(),
		Environment: req.GetEnvironment(),
	}, nil
}

func (f *fakeSecretSetup) CompleteSetup(ctx context.Context, req *secretv1.CompleteSetupRequest, _ ...grpc.CallOption) (*secretv1.CompleteSetupResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completeReqs = append(f.completeReqs, req)
	f.setupTokens = append(f.setupTokens, f.token(ctx))
	if f.completeErr != nil {
		return nil, f.completeErr
	}
	// Mirror the real service: the window is now closed, so the next status read
	// reports completed and the loop converges.
	f.completed = true
	return &secretv1.CompleteSetupResponse{Completed: true, Controller: req.GetController()}, nil
}

func (f *fakeSecretSetup) counts() (status, setup, complete int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statusCalls, len(f.setupReqs), len(f.completeReqs)
}

func (f *fakeSecretSetup) tokensSent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.setupTokens...)
}

// newTestSecretOrchestrator wires the orchestrator onto a fake client instead of
// a real gRPC connection.
func newTestSecretOrchestrator(auth authSetupStatus, cfg Config, cli *fakeSecretSetup) *SecretOrchestrator {
	if cfg.SecretSetupAddr == "" {
		cfg.SecretSetupAddr = "secret:9090"
	}
	s := &SecretOrchestrator{
		auth:        auth,
		cfg:         cfg,
		last:        ErrSecretSetupPending,
		baseBackoff: time.Millisecond,
		maxBackoff:  5 * time.Millisecond,
	}
	s.dial = func(context.Context) (secretSetupClient, func(), error) {
		return cli, func() {
			cli.mu.Lock()
			cli.closes++
			cli.mu.Unlock()
		}, nil
	}
	return s
}

func completedAuth() *fakeAuthStatus {
	return &fakeAuthStatus{completed: true, result: &Result{
		AuthTenantID: "6f1f2f2e-0000-4000-8000-000000000001",
		CompletedAt:  time.Now(),
	}}
}

func TestSecretSetupProvisionsAndCompletes(t *testing.T) {
	cli := &fakeSecretSetup{}
	s := newTestSecretOrchestrator(completedAuth(), Config{
		TenantName:        "maintainerd",
		TenantDisplayName: "Maintainerd",
		CoreServiceName:   "maintainerd-core",
		SecretProject:     "default",
		SecretEnvironment: "default",
	}, cli)

	require.NoError(t, s.Run(context.Background()))
	require.NoError(t, s.State(), "a provisioned secret must report ready")

	status, setups, completes := cli.counts()
	assert.Equal(t, 1, status)
	require.Equal(t, 1, setups)
	require.Equal(t, 1, completes)

	// Exactly the fields SetupServiceSetupRequest declares, nothing widened.
	req := cli.setupReqs[0]
	assert.Equal(t, "maintainerd", req.GetTenant())
	assert.Equal(t, "Maintainerd", req.GetTenantDisplayName())
	assert.Equal(t, "6f1f2f2e-0000-4000-8000-000000000001", req.GetAuthTenantUuid())
	assert.Equal(t, "default", req.GetProject())
	assert.Equal(t, "default", req.GetEnvironment())
	assert.Equal(t, "maintainerd-core", req.GetController(),
		"core must record ITSELF as the controller — that is what makes secret refuse a competing REST wizard")
	assert.Equal(t, "maintainerd-core", cli.completeReqs[0].GetController())

	assert.Equal(t, 1, cli.closes, "the connection must be released on every pass")
}

func TestSecretSetupIsIdempotentWhenAlreadySetUp(t *testing.T) {
	// The core of the idempotence rule: secret's own GetSetupStatus is the
	// authority across restarts, and a completed instance must NOT be re-driven.
	cli := &fakeSecretSetup{completed: true}
	s := newTestSecretOrchestrator(completedAuth(), Config{TenantName: "maintainerd"}, cli)

	require.NoError(t, s.Run(context.Background()))
	status, setups, completes := cli.counts()
	assert.Equal(t, 1, status)
	assert.Zero(t, setups, "Setup must not be called against an already-set-up secret")
	assert.Zero(t, completes, "CompleteSetup must not be called against an already-set-up secret")
	assert.NoError(t, s.State())
}

func TestSecretSetupDoesNotRedialOnceFinished(t *testing.T) {
	// Second layer of idempotence: a converged install stops dialing entirely, so
	// a long-lived process does not keep asking.
	cli := &fakeSecretSetup{}
	s := newTestSecretOrchestrator(completedAuth(), Config{TenantName: "maintainerd"}, cli)

	require.NoError(t, s.Run(context.Background()))
	require.NoError(t, s.Run(context.Background()))
	require.NoError(t, s.Run(context.Background()))

	status, setups, completes := cli.counts()
	assert.Equal(t, 1, status)
	assert.Equal(t, 1, setups)
	assert.Equal(t, 1, completes)
}

func TestSecretSetupWaitsForAuthSetup(t *testing.T) {
	cli := &fakeSecretSetup{}
	auth := &fakeAuthStatus{completed: false}
	s := newTestSecretOrchestrator(auth, Config{TenantName: "maintainerd"}, cli)

	err := s.Run(context.Background())
	require.ErrorIs(t, err, ErrAuthSetupIncomplete)

	status, setups, completes := cli.counts()
	assert.Zero(t, status, "secret must not even be dialed before auth setup completes")
	assert.Zero(t, setups)
	assert.Zero(t, completes)
	assert.ErrorIs(t, s.State(), ErrAuthSetupIncomplete, "the ordering gate must degrade readiness, not pass it")

	// Auth completes; the very next pass proceeds. Ordering, not exclusion.
	auth.completed = true
	auth.result = &Result{AuthTenantID: "6f1f2f2e-0000-4000-8000-000000000001"}
	require.NoError(t, s.Run(context.Background()))
	_, setups, completes = cli.counts()
	assert.Equal(t, 1, setups)
	assert.Equal(t, 1, completes)
}

func TestSecretSetupStateIsPendingBeforeFirstPass(t *testing.T) {
	s := newTestSecretOrchestrator(completedAuth(), Config{}, &fakeSecretSetup{})
	assert.ErrorIs(t, s.State(), ErrSecretSetupPending,
		"\"not attempted yet\" must not read as ready")
}

func TestSecretSetupStateIsReadyWhenUnconfigured(t *testing.T) {
	// Development with no vault: nothing to drive, and provisioning that needs
	// secret material still fails closed elsewhere.
	s := NewSecretOrchestrator(nil, Config{})
	assert.False(t, s.Enabled())
	assert.NoError(t, s.State())
	assert.ErrorIs(t, s.Run(context.Background()), ErrSecretSetupNotConfigured)
}

func TestSecretSetupRetriesUntilReachable(t *testing.T) {
	// A secret that is unreachable, then reachable, converges — and core is still
	// running afterwards (RunWithRetry returned instead of exiting).
	cli := &fakeSecretSetup{unreachableFor: 2}
	s := newTestSecretOrchestrator(completedAuth(), Config{
		TenantName:      "maintainerd",
		CoreServiceName: "maintainerd-core",
	}, cli)

	require.ErrorIs(t, s.Run(context.Background()), errSecretUnreachable)
	assert.ErrorIs(t, s.State(), errSecretUnreachable, "an unreachable secret must degrade readiness")
	require.ErrorIs(t, s.Run(context.Background()), errSecretUnreachable)
	require.NoError(t, s.Run(context.Background()), "the pass after secret starts answering must converge")

	_, setups, completes := cli.counts()
	assert.Equal(t, 1, setups)
	assert.Equal(t, 1, completes)
	assert.NoError(t, s.State())
	assert.Equal(t, 3, cli.closes, "every failed pass must release its connection too")
}

func TestSecretSetupRunWithRetryConvergesWithoutExiting(t *testing.T) {
	cli := &fakeSecretSetup{unreachableFor: 2}
	s := newTestSecretOrchestrator(completedAuth(), Config{TenantName: "maintainerd"}, cli)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() { s.RunWithRetry(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("RunWithRetry did not converge")
	}
	require.NoError(t, ctx.Err(), "the loop must converge on its own, not be cut off by the deadline")
	assert.NoError(t, s.State())
	_, setups, _ := cli.counts()
	assert.Equal(t, 1, setups)
}

func TestSecretSetupRunWithRetryStopsOnCancel(t *testing.T) {
	// A permanently broken secret must not keep core from shutting down.
	cli := &fakeSecretSetup{statusHardErr: errors.New("permanently broken")}
	s := newTestSecretOrchestrator(completedAuth(), Config{TenantName: "maintainerd"}, cli)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() { s.RunWithRetry(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunWithRetry did not stop on context cancellation")
	}
}

func TestSecretSetupNeverLeaksTheBootstrapToken(t *testing.T) {
	const token = "s3cr3t-bootstrap-token-do-not-log"

	var logs bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	cfg := Config{
		TenantName:        "maintainerd",
		CoreServiceName:   "maintainerd-core",
		SecretSetupToken:  token,
		SecretProject:     "default",
		SecretEnvironment: "default",
	}

	// Every branch that produces output: waiting on auth, a transport failure, a
	// refused Setup, a refused CompleteSetup, and a success.
	var collected []error

	waiting := newTestSecretOrchestrator(&fakeAuthStatus{}, cfg, &fakeSecretSetup{})
	collected = append(collected, waiting.Run(context.Background()))

	unreachable := newTestSecretOrchestrator(completedAuth(), cfg, &fakeSecretSetup{unreachableFor: 1})
	collected = append(collected, unreachable.Run(context.Background()))

	refusedSetup := newTestSecretOrchestrator(completedAuth(), cfg,
		&fakeSecretSetup{setupErr: errors.New("rpc error: code = PermissionDenied desc = invalid bootstrap token")})
	collected = append(collected, refusedSetup.Run(context.Background()))

	refusedComplete := newTestSecretOrchestrator(completedAuth(), cfg,
		&fakeSecretSetup{completeErr: errors.New("rpc error: code = FailedPrecondition desc = setup is already complete")})
	collected = append(collected, refusedComplete.Run(context.Background()))

	ok := &fakeSecretSetup{}
	okOrch := newTestSecretOrchestrator(completedAuth(), cfg, ok)
	collected = append(collected, okOrch.Run(context.Background()))

	for i, err := range collected {
		if err == nil {
			continue
		}
		assert.NotContains(t, err.Error(), token, "error %d leaked the bootstrap token", i)
	}
	assert.NotContains(t, logs.String(), token, "a log line leaked the bootstrap token")
	assert.NotEmpty(t, logs.String(), "the transitions must actually have been logged, or this proves nothing")

	// It is used, though — proving the assertion above is not passing because the
	// token was silently dropped.
	sent := ok.tokensSent()
	require.NotEmpty(t, sent)
	for _, v := range sent {
		assert.Equal(t, token, v, "the bootstrap token must be presented as x-setup-token")
	}
}

func TestSecretSetupLogsTransitionsOnceNotEveryTick(t *testing.T) {
	var logs bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	auth := &fakeAuthStatus{}
	s := newTestSecretOrchestrator(auth, Config{TenantName: "maintainerd"}, &fakeSecretSetup{})
	for range 5 {
		require.ErrorIs(t, s.Run(context.Background()), ErrAuthSetupIncomplete)
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "waiting for auth setup"),
		"an unchanged state must announce once, not once per tick")
}

func TestSecretSetupControllerFallsBackToCoreServiceName(t *testing.T) {
	s := NewSecretOrchestrator(nil, Config{})
	assert.Equal(t, "maintainerd-core", s.controller(),
		"the controller recorded on secret's lock must never be empty — secret rejects that")
}
