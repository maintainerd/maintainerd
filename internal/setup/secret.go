package setup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	secretv1 "github.com/maintainerd/core/gen/maintainerd/secret/v1"
)

// SecretOrchestrator drives maintainerd-secret's gRPC SetupService, the same way
// Orchestrator drives auth's.
//
// WHY IT EXISTS AT ALL. Core registers secret's IAM records in auth from its
// catalog (internal/steward), but that tells AUTH about secret; it does not tell
// SECRET anything. Secret's own first run has two mutually exclusive paths and an
// instance gets exactly one of them: an operator drives its REST wizard
// (standalone), or a controller drives this SetupService (controlled). With
// MAINTAINERD_SECRET_MODE=core the operator has DECLARED the controlled path, so
// the REST wizard is shut from the first boot — and if nothing drives the gRPC
// path the instance has no bootstrap path at all. This is that driver.
//
// LEVEL-TRIGGERED, NOT A ONE-SHOT AT BOOT. auth, core and secret start at the
// same time; core configures auth first and secret afterwards. So reaching this
// step before secret is listening is the NORMAL case, not an error — the same
// argument internal/dependency makes. It converges with backoff and logs each
// state TRANSITION once rather than every tick.
//
// FAILURE IS VISIBLE, NEVER FATAL. A secret that cannot be set up degrades
// readiness (State, wired into /readyz) and keeps being retried. Taking core down
// over it would mean a vault that is slow to start becomes a control-plane
// outage.
type SecretOrchestrator struct {
	// auth is the ORDERING GATE, held as a dependency rather than consulted
	// incidentally: secret cannot be constructed without it. See Run.
	auth authSetupStatus
	cfg  Config

	// dial is swapped in tests. Production opens a real gRPC connection.
	dial secretSetupDialer

	// baseBackoff/maxBackoff are fields rather than constants so a test can drive
	// several passes without waiting out the production cadence.
	baseBackoff time.Duration
	maxBackoff  time.Duration

	mu   sync.Mutex
	done bool
	last error
	// logged is the last state announced to the log — see announce.
	logged string
}

// authSetupStatus is the ordering gate's single question: has core RECORDED
// auth's setup as complete? *Orchestrator answers it from the control_plane row.
type authSetupStatus interface {
	Status(ctx context.Context) (bool, *Result)
}

// secretSetupClient is the slice of secret's SetupService this orchestrator
// drives. secretv1.SetupServiceClient satisfies it; narrowing it is what lets the
// tests exercise idempotence, retry and ordering without a listener.
type secretSetupClient interface {
	GetSetupStatus(context.Context, *secretv1.GetSetupStatusRequest, ...grpc.CallOption) (*secretv1.GetSetupStatusResponse, error)
	Setup(context.Context, *secretv1.SetupServiceSetupRequest, ...grpc.CallOption) (*secretv1.SetupServiceSetupResponse, error)
	CompleteSetup(context.Context, *secretv1.CompleteSetupRequest, ...grpc.CallOption) (*secretv1.CompleteSetupResponse, error)
}

// secretSetupDialer opens a client plus the func that releases it.
type secretSetupDialer func(context.Context) (secretSetupClient, func(), error)

// ErrSecretSetupNotConfigured means SECRET_SETUP_ADDR is unset, so core has no
// secret SetupService to drive. Only reachable in development: an operator
// running the full platform points core at secret's gRPC.
var ErrSecretSetupNotConfigured = errors.New("setup: SECRET_SETUP_ADDR is not configured")

// ErrAuthSetupIncomplete is the ORDERING GATE refusing to run early.
//
// Secret's setup must happen strictly AFTER auth's completes, and the ordering is
// enforced here rather than left to whichever goroutine happens to win. Two
// reasons, both structural: secret's IAM records (its service principal, its
// resource API, its permissions, its OAuth clients) live in auth and are written
// during auth's setup window, and secret verifies the tokens auth issues. A
// secret provisioned before auth exists would be a vault nobody can authenticate
// against, holding a tenant mirror pointing at an auth tenant that does not exist
// yet.
var ErrAuthSetupIncomplete = errors.New("setup: auth setup has not completed yet")

// ErrSecretSetupPending is the state before the first pass lands.
//
// It is an ERROR, not an empty value: "we have not tried yet" and "secret is set
// up" must not read the same, or /readyz would report ready for the window
// between the listener opening and the first pass completing. Same reasoning as
// dependency.ErrNotProbed.
var ErrSecretSetupPending = errors.New("setup: secret setup has not been attempted yet")

// Announced states. They are log-transition keys, not a state machine — the only
// property that matters is that two consecutive identical outcomes announce once.
const (
	secretStateWaitingAuth = "waiting-for-auth"
	secretStateFailed      = "failed"
	secretStateAdopted     = "already-set-up"
	secretStateProvisioned = "provisioned"
)

// Backoff bounds for the convergence loop. It starts fast because a cold stack
// usually settles in seconds, and backs off so a vault that is down does not face
// a tight retry loop from every core replica.
const (
	secretSetupBaseBackoff = 3 * time.Second
	secretSetupMaxBackoff  = 30 * time.Second
)

// NewSecretOrchestrator builds the driver. Passing the auth orchestrator is not
// a convenience — it is how the ordering requirement is made structural.
func NewSecretOrchestrator(auth *Orchestrator, cfg Config) *SecretOrchestrator {
	s := &SecretOrchestrator{
		auth:        auth,
		cfg:         cfg,
		last:        ErrSecretSetupPending,
		baseBackoff: secretSetupBaseBackoff,
		maxBackoff:  secretSetupMaxBackoff,
	}
	s.dial = s.dialSecret
	return s
}

// Enabled reports whether core has a secret SetupService to drive.
func (s *SecretOrchestrator) Enabled() bool { return s.cfg.SecretSetupAddr != "" }

// State is what /readyz reports: nil once secret is set up, the last failure
// otherwise, ErrSecretSetupPending before the first pass.
//
// An UNCONFIGURED instance reports nil. That is development with no vault, the
// same answer the secret reachability check gives for the same reason: the
// instance is deliberately running without one, and any provision that needs
// secret material still fails closed (internal/secretctrl).
func (s *SecretOrchestrator) State() error {
	if !s.Enabled() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil
	}
	return s.last
}

// controller is the identity core records on secret's durable setup lock. It is
// core's registered service name — the same string core registered itself under
// in auth — so the two systems name the same controller.
func (s *SecretOrchestrator) controller() string {
	return firstNonEmpty(s.cfg.CoreServiceName, "maintainerd-core")
}

// setupCtx attaches the bootstrap token secret's setup gate expects. The value is
// never logged and never reaches an error string.
func (s *SecretOrchestrator) setupCtx(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, setupTokenKey, s.cfg.SecretSetupToken)
}

func (s *SecretOrchestrator) dialSecret(context.Context) (secretSetupClient, func(), error) {
	conn, err := dialSetupService(setupDial{
		Service:        "secret",
		Addr:           s.cfg.SecretSetupAddr,
		CAFile:         s.cfg.SecretSetupCAFile,
		ServerName:     s.cfg.SecretSetupServerName,
		ClientCertFile: s.cfg.SecretSetupClientCertFile,
		ClientKeyFile:  s.cfg.SecretSetupClientKeyFile,
	})
	if err != nil {
		return nil, nil, err
	}
	return secretv1.NewSetupServiceClient(conn), func() { _ = conn.Close() }, nil
}

// Run performs one pass.
//
// IDEMPOTENT IN TWO LAYERS. In-process, a completed pass short-circuits so a
// converged install never dials again. Across processes and restarts, the
// authority is secret's own GetSetupStatus: when secret reports itself complete,
// this records that and returns WITHOUT calling Setup. That matters because
// re-driving Setup on a provisioned vault is how a second controller registration
// would be attempted against an instance that already has one — and because
// secret's CompleteSetup closes a one-time window, so a second attempt can only
// be refused.
func (s *SecretOrchestrator) Run(ctx context.Context) error {
	if !s.Enabled() {
		return ErrSecretSetupNotConfigured
	}
	if s.finished() {
		return nil
	}

	// THE ORDERING GATE. Nothing is dialed until core's own record says auth's
	// setup completed — see ErrAuthSetupIncomplete.
	authComplete, authResult := s.auth.Status(ctx)
	if !authComplete {
		s.record(secretStateWaitingAuth, ErrAuthSetupIncomplete)
		return ErrAuthSetupIncomplete
	}

	cli, release, err := s.dial(ctx)
	if err != nil {
		s.record(secretStateFailed, err)
		return err
	}
	defer release()

	sctx := s.setupCtx(ctx)

	// Ask before acting. An unauthenticated caller gets only this one bit, and one
	// bit is all the decision needs.
	status, err := cli.GetSetupStatus(sctx, &secretv1.GetSetupStatusRequest{})
	if err != nil {
		err = fmt.Errorf("secret GetSetupStatus: %w", err)
		s.record(secretStateFailed, err)
		return err
	}
	if status.GetCompleted() {
		s.record(secretStateAdopted, nil)
		return nil
	}

	// Exactly the fields SetupServiceSetupRequest declares, and no more: the
	// tenant core created in auth, the auth tenant the mirror links to, the
	// default vault scope, and core as the controller.
	if _, err := cli.Setup(sctx, &secretv1.SetupServiceSetupRequest{
		Tenant:            s.cfg.TenantName,
		TenantDisplayName: firstNonEmpty(s.cfg.TenantDisplayName, s.cfg.TenantName),
		AuthTenantUuid:    authResult.AuthTenantID,
		Project:           s.cfg.SecretProject,
		Environment:       s.cfg.SecretEnvironment,
		Controller:        s.controller(),
	}); err != nil {
		err = fmt.Errorf("secret Setup: %w", err)
		s.record(secretStateFailed, err)
		return err
	}

	// CompleteSetup is a SEPARATE call and both are required. Setup provisions the
	// scope; Complete writes the durable lock whose controller_kind is what makes
	// secret refuse a competing REST wizard afterwards, and what makes
	// GetSetupStatus answer "completed" on the next pass so this loop converges
	// instead of re-provisioning forever. Provision-then-complete is also the safe
	// order: a failure here leaves the window open and the next pass retries,
	// whereas completing first would lock an instance whose scope does not exist.
	if _, err := cli.CompleteSetup(sctx, &secretv1.CompleteSetupRequest{
		Controller: s.controller(),
	}); err != nil {
		err = fmt.Errorf("secret CompleteSetup: %w", err)
		s.record(secretStateFailed, err)
		return err
	}

	s.record(secretStateProvisioned, nil)
	return nil
}

// RunWithRetry converges until ctx is cancelled or secret is set up. It NEVER
// returns an error and never exits the process — see the type doc.
//
// There is no attempt cap, unlike authctrl's boot loop. That loop has a manual
// fallback (POST /api/v1/steward/apply); this one has none, and an instance whose
// vault was never provisioned stays unusable, so giving up would strand it
// permanently. The backoff cap keeps a permanent failure cheap.
func (s *SecretOrchestrator) RunWithRetry(ctx context.Context) {
	if !s.Enabled() {
		slog.Info("secret setup: SECRET_SETUP_ADDR is not configured — core will not drive secret's setup")
		return
	}
	backoff := s.baseBackoff
	for {
		if err := s.Run(ctx); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < s.maxBackoff {
			backoff *= 2
			if backoff > s.maxBackoff {
				backoff = s.maxBackoff
			}
		}
	}
}

func (s *SecretOrchestrator) finished() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// record stores a pass's outcome and announces only TRANSITIONS.
func (s *SecretOrchestrator) record(state string, err error) {
	s.mu.Lock()
	s.last = err
	if err == nil {
		s.done = true
	}
	changed := s.logged != state
	s.logged = state
	s.mu.Unlock()

	if !changed {
		return
	}
	// One line per transition. The loop ticks every few seconds against a
	// dependency that may take a minute to arrive, so announcing every attempt
	// would bury the line that matters — the moment secret was configured — under
	// a page of identical warnings. Errors here wrap gRPC statuses; none of them
	// carries the bootstrap token.
	switch state {
	case secretStateWaitingAuth:
		slog.Info("secret setup: waiting for auth setup to complete before provisioning secret")
	case secretStateFailed:
		slog.Warn("secret setup: attempt failed — core keeps serving, reports not-ready and will retry", "err", err)
	case secretStateAdopted:
		slog.Info("secret setup: secret reports itself already set up — recording and moving on")
	case secretStateProvisioned:
		slog.Info("secret setup: complete", "controller", s.controller(),
			"project", s.cfg.SecretProject, "environment", s.cfg.SecretEnvironment)
	}
}
