// Package setup makes Core the orchestrator: on a fresh install Core drives
// Auth's gRPC SetupService to create the system tenant + admin and to register
// itself as Auth's control service, then persists everything Auth hands back and
// seeds its own service registry (Auth/Secret/Docker as system services).
package setup

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	authv1 "github.com/maintainerd/core/gen/maintainerd/auth/v1"
	"github.com/maintainerd/core/internal/resource"
	"github.com/maintainerd/core/internal/service"
	"github.com/maintainerd/core/internal/storage"
	"github.com/maintainerd/core/internal/tenant"
)

// setupTokenKey is the gRPC metadata header Auth reads the bootstrap token from.
const setupTokenKey = "x-setup-token"

// ErrSetupRunning is returned when a provisioning run is already in flight —
// the single-flight guard that keeps the boot goroutine (RunWithRetry) and the
// console wizard's POST from provisioning concurrently. Two interleaved runs
// would double-create non-idempotent Auth objects and race each other's
// control-plane persist, each overwriting the other's keypair.
var ErrSetupRunning = errors.New("setup is already running")

// controlPlaneStore is the slice of storage the orchestrator needs for the
// control_plane singleton row. Narrowed to an interface so the security-
// critical paths (keypair reuse, status slimming, single-flight) are testable
// without a database; *storage.Queries satisfies it.
type controlPlaneStore interface {
	GetControlPlane(ctx context.Context) (storage.ControlPlane, error)
	UpsertControlPlane(ctx context.Context, arg storage.UpsertControlPlaneParams) (storage.ControlPlane, error)
}

// Orchestrator runs the one-time provisioning against Auth and records the
// result in the control_plane singleton row.
type Orchestrator struct {
	q   *storage.Queries
	cp  controlPlaneStore
	cfg Config

	// Single-flight guard (see ErrSetupRunning).
	mu      sync.Mutex
	running bool
}

func NewOrchestrator(q *storage.Queries, cfg Config) *Orchestrator {
	return &Orchestrator{q: q, cp: q, cfg: cfg}
}

// tryBegin claims the single-flight slot; callers that get true MUST call end.
func (o *Orchestrator) tryBegin() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.running {
		return false
	}
	o.running = true
	return true
}

func (o *Orchestrator) end() {
	o.mu.Lock()
	o.running = false
	o.mu.Unlock()
}

// Enabled reports whether on-boot orchestration is turned on.
func (o *Orchestrator) Enabled() bool { return o.cfg.Enabled }

// hasEnvAdminPassword reports whether an admin password is configured via env,
// so the wizard may omit it (unattended installs bake it into the environment).
func (o *Orchestrator) hasEnvAdminPassword() bool { return o.cfg.AdminPassword != "" }

// Result is what Core learns from Auth; it is stored as control_plane.data.
type Result struct {
	AuthTenantID          string          `json:"auth_tenant_id"`
	AuthAdminUserID       string          `json:"auth_admin_user_id"`
	AuthDefaultClientID   string          `json:"auth_default_client_id"`
	AuthDefaultProviderID string          `json:"auth_default_provider_id"`
	ControlServiceID      string          `json:"control_service_id"`
	ControlPolicyID       string          `json:"control_policy_id"`
	ControlPolicyName     string          `json:"control_policy_name"`
	ControlClientID       string          `json:"control_client_id"`
	ControlOAuthClientID  string          `json:"control_oauth_client_id"`
	TokenAuthMethod       string          `json:"token_auth_method"`
	ResourceAPIID         string          `json:"resource_api_id"`
	ResourceAPIIdentifier string          `json:"resource_api_identifier"`
	AdminRoleID           string          `json:"admin_role_id"`
	ConsoleClientID       string          `json:"console_client_id"`
	ConsoleOAuthClientID  string          `json:"console_oauth_client_id"`
	JWKS                  json.RawMessage `json:"jwks,omitempty"`
	CompletedAt           time.Time       `json:"completed_at"`
	AlreadyComplete       bool            `json:"already_complete"`

	// DeploymentMode mirrors the control_plane.deployment_mode column (the
	// immutable substrate stamp); populated on load, not stored inside data.
	DeploymentMode string `json:"deployment_mode,omitempty"`
}

func (o *Orchestrator) dial(ctx context.Context) (*grpc.ClientConn, error) {
	// Plaintext only when no TLS material is configured at all.
	if o.cfg.AuthCAFile == "" && o.cfg.AuthClientCertFile == "" {
		return grpc.NewClient(o.cfg.AuthAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	tlsCfg := &tls.Config{ServerName: o.cfg.AuthServerName}
	if o.cfg.AuthCAFile != "" {
		caPEM, err := os.ReadFile(o.cfg.AuthCAFile)
		if err != nil {
			return nil, fmt.Errorf("read auth CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("auth CA file %q holds no valid certificate", o.cfg.AuthCAFile)
		}
		tlsCfg.RootCAs = pool
	}
	// Present a client cert when Auth's gRPC requires mTLS (control-plane mode).
	if o.cfg.AuthClientCertFile != "" && o.cfg.AuthClientKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(o.cfg.AuthClientCertFile, o.cfg.AuthClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load auth client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return grpc.NewClient(o.cfg.AuthAddr, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
}

// authCtx attaches the bootstrap token Auth's setup interceptor expects.
func (o *Orchestrator) authCtx(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, setupTokenKey, o.cfg.AuthToken)
}

// RunInput is the tenant + admin the wizard collects. Empty fields fall back to
// the env-configured defaults (so an unattended on-boot run works with no body).
type RunInput struct {
	TenantName        string `json:"tenant_name"`
	TenantDisplayName string `json:"tenant_display_name"`
	AdminUsername     string `json:"admin_username"`
	AdminFullname     string `json:"admin_fullname"`
	AdminEmail        string `json:"admin_email"`
	AdminPassword     string `json:"admin_password"`
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Run performs the full provisioning using the env-configured defaults (the
// on-boot / unattended path).
func (o *Orchestrator) Run(ctx context.Context) (*Result, error) {
	return o.RunWith(ctx, RunInput{})
}

// RunWith performs the full provisioning using the supplied tenant + admin
// (the console wizard path), falling back to env defaults for any empty field.
// It is safe to re-run once complete (short-circuits when Auth reports done)
// and single-flight: a run started while another is in flight fails fast with
// ErrSetupRunning instead of racing it (see the var doc for why that matters).
func (o *Orchestrator) RunWith(ctx context.Context, in RunInput) (*Result, error) {
	if !o.tryBegin() {
		return nil, ErrSetupRunning
	}
	defer o.end()
	if o.cfg.AuthAddr == "" {
		return nil, fmt.Errorf("AUTH_SETUP_ADDR is not set")
	}
	tenantName := firstNonEmpty(in.TenantName, o.cfg.TenantName)
	tenantDisplay := firstNonEmpty(in.TenantDisplayName, o.cfg.TenantDisplayName, tenantName)
	adminUsername := firstNonEmpty(in.AdminUsername, o.cfg.AdminUsername)
	adminFullname := firstNonEmpty(in.AdminFullname, o.cfg.AdminFullname, adminUsername)
	adminEmail := firstNonEmpty(in.AdminEmail, o.cfg.AdminEmail)
	adminPassword := firstNonEmpty(in.AdminPassword, o.cfg.AdminPassword)
	if adminPassword == "" {
		return nil, fmt.Errorf("admin password is required")
	}

	conn, err := o.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	cli := authv1.NewSetupServiceClient(conn)
	actx := o.authCtx(ctx)

	status, err := cli.GetSetupStatus(actx, &authv1.GetSetupStatusRequest{})
	if err != nil {
		return nil, fmt.Errorf("auth GetSetupStatus: %w", err)
	}
	if status.GetIsSetupComplete() {
		slog.Info("core setup: auth already provisioned — loading persisted control plane")
		res := o.loadPersisted(ctx)
		res.AlreadyComplete = true
		// Ensure the core mirror + registry exist even if a previous run only
		// finished the auth side.
		if err := o.mirror(ctx, res, tenantName, tenantDisplay); err != nil {
			slog.Warn("core setup: mirror after already-complete failed", "err", err)
		}
		return res, nil
	}

	res := &Result{}

	// 1. System tenant. CreateTenant is not idempotent, so skip it if a previous
	// (partial) run already created it — the Ensure* RPCs below resolve the
	// system tenant on their own.
	if !status.GetIsTenantSetup() {
		t, err := cli.CreateTenant(actx, &authv1.CreateTenantRequest{
			Name:        tenantName,
			DisplayName: tenantDisplay,
			Description: "Maintainerd system tenant",
		})
		if err != nil {
			return nil, fmt.Errorf("auth CreateTenant: %w", err)
		}
		res.AuthTenantID = t.GetTenantId()
		res.AuthDefaultClientID = t.GetDefaultClientId()
		res.AuthDefaultProviderID = t.GetDefaultProviderId()
	} else {
		slog.Info("core setup: auth tenant already exists — skipping CreateTenant")
	}

	// The admin is created LAST: Auth's CreateAdmin flips the system tenant to
	// "active", which locks setup (ensureSetupOpen refuses once active). Every
	// control-plane registration must therefore happen while setup is still open,
	// with CreateAdmin as the final gated call.

	// 2. Register Core as the control service (+ its control policy).
	rc, err := cli.RegisterControlService(actx, &authv1.RegisterControlServiceRequest{
		Name:        o.cfg.CoreServiceName,
		DisplayName: "Maintainerd Core",
		Description: "Maintainerd control plane",
		Version:     "v1",
	})
	if err != nil {
		return nil, fmt.Errorf("auth RegisterControlService: %w", err)
	}
	res.ControlServiceID = rc.GetServiceId()
	res.ControlPolicyID = rc.GetPolicyId()
	res.ControlPolicyName = rc.GetPolicyName()

	// 3. Core's M2M control client (private_key_jwt — Core keeps the private key).
	// The keypair is minted ONCE per install: a previously persisted key is
	// reused (its public JWKS re-derived) rather than regenerated. Minting a
	// fresh key on every attempt would overwrite the stored PEM, so any retry
	// after a partial failure would leave Auth holding a JWKS that no longer
	// matches Core's stored private key — permanently breaking private_key_jwt —
	// and would silently rotate a live credential as a side effect of a re-run.
	privPEM, jwks, err := o.controlKey(ctx)
	if err != nil {
		return nil, err
	}
	cc, err := cli.EnsureControlClient(actx, &authv1.EnsureControlClientRequest{
		Name:        o.cfg.CoreServiceName + "-control",
		DisplayName: "Maintainerd Core Control Client",
		ServiceName: o.cfg.CoreServiceName,
		Jwks:        jwks,
		Audience:    o.cfg.CoreAudience,
	})
	if err != nil {
		return nil, fmt.Errorf("auth EnsureControlClient: %w", err)
	}
	res.ControlClientID = cc.GetClientId()
	res.ControlOAuthClientID = cc.GetOauthClientId()
	res.TokenAuthMethod = cc.GetTokenEndpointAuthMethod()
	res.JWKS = json.RawMessage(jwks)

	// Steps 4-6 wire Core's FULL IAM/OAuth integration (resource API, admin role,
	// console OAuth client). They are BEST-EFFORT: the essential control-plane
	// identity is the service + control client above, and the console has no OAuth
	// login yet — so a failure here is logged and setup still completes. These are
	// re-runnable once Auth enforcement is turned on.

	// 4. Core's own resource API (audience) + its permissions.
	if api, err := cli.EnsureResourceAPI(actx, &authv1.EnsureResourceAPIRequest{
		ServiceName:        o.cfg.CoreServiceName,
		ServiceDisplayName: "Maintainerd Core",
		Name:               "Maintainerd Core API",
		DisplayName:        "Maintainerd Core API",
		Identifier:         o.cfg.CoreAudience,
		Permissions: []*authv1.EnsureResourceAPIPermission{
			{Name: "core:admin", Description: "Full administrative access to Maintainerd Core"},
		},
	}); err != nil {
		slog.Warn("core setup: EnsureResourceAPI failed (non-fatal)", "err", err)
	} else {
		res.ResourceAPIID = api.GetApiId()
		res.ResourceAPIIdentifier = api.GetIdentifier()
	}

	// 5. The core-admin role (registered for reuse; the admin gets full access via
	// the built-in super-admin role CreateAdmin grants).
	if role, err := cli.EnsureRole(actx, &authv1.EnsureRoleRequest{
		Name:            "core-admin",
		Description:     "Full access to Maintainerd Core",
		PermissionNames: []string{"core:admin"},
	}); err != nil {
		slog.Warn("core setup: EnsureRole failed (non-fatal)", "err", err)
	} else {
		res.AdminRoleID = role.GetRoleId()
	}

	// 6. The operator console SPA (public, auth_code + PKCE).
	if con, err := cli.EnsureConsoleClient(actx, &authv1.EnsureConsoleClientRequest{
		Name:         "maintainerd-console",
		DisplayName:  "Maintainerd Console",
		Domain:       o.cfg.ConsoleDomain,
		RedirectUris: o.cfg.ConsoleRedirectURIs,
	}); err != nil {
		slog.Warn("core setup: EnsureConsoleClient failed (non-fatal)", "err", err)
	} else {
		res.ConsoleClientID = con.GetClientId()
		res.ConsoleOAuthClientID = con.GetOauthClientId()
	}

	// 7. Reconcile maintainerd's service catalog into Auth while setup is still
	// open. The catalog handles non-core services (secret/runtime/agent) through
	// the same idempotent provisioning RPCs instead of one-off code.
	if err := o.reconcileSteward(actx, cli); err != nil {
		return nil, fmt.Errorf("auth steward reconcile: %w", err)
	}

	// 8. IAM super-admin — the FINAL gated call: it activates the system tenant
	// and locks setup. Not idempotent, so skip if a prior run already created it.
	if !status.GetIsAdminSetup() {
		a, err := cli.CreateAdmin(actx, &authv1.CreateAdminRequest{
			Username: adminUsername,
			Fullname: adminFullname,
			Password: adminPassword,
			Email:    adminEmail,
		})
		if err != nil {
			return nil, fmt.Errorf("auth CreateAdmin: %w", err)
		}
		res.AuthAdminUserID = a.GetUserId()
	} else {
		slog.Info("core setup: auth admin already exists — skipping CreateAdmin")
	}

	// 9. Lock setup (idempotent — CreateAdmin already activated the tenant).
	if _, err := cli.CompleteSetup(actx, &authv1.CompleteSetupRequest{}); err != nil {
		return nil, fmt.Errorf("auth CompleteSetup: %w", err)
	}
	res.CompletedAt = time.Now()

	// Persist what Auth handed back, then mirror the tenant + seed the registry.
	if err := o.persist(ctx, res, privPEM); err != nil {
		return res, fmt.Errorf("persist control plane: %w", err)
	}
	if err := o.mirror(ctx, res, tenantName, tenantDisplay); err != nil {
		slog.Warn("core setup: mirror/registry seeding failed (non-fatal)", "err", err)
	}

	slog.Info("core setup: complete",
		"auth_tenant", res.AuthTenantID,
		"control_client", res.ControlClientID,
		"admin_user", res.AuthAdminUserID)
	return res, nil
}

// controlKey returns Core's private_key_jwt signing key: the persisted one
// when present (public JWKS re-derived from it), a freshly minted one only on
// a truly first run. See the call site in RunWith for why reuse is mandatory.
func (o *Orchestrator) controlKey(ctx context.Context) (privatePEM string, jwksJSON string, err error) {
	if row, gerr := o.cp.GetControlPlane(ctx); gerr == nil && row.ControlPrivateKeyPem != "" {
		jwks, derr := jwksFromPrivatePEM(row.ControlPrivateKeyPem)
		if derr != nil {
			// Fail closed: a stored-but-unparsable key means the install's
			// credential state is corrupt. Generating a replacement here would
			// hide the corruption AND desync Auth; surface it instead.
			return "", "", fmt.Errorf("stored control key is unusable (refusing to overwrite it): %w", derr)
		}
		slog.Info("core setup: reusing persisted control key")
		return row.ControlPrivateKeyPem, jwks, nil
	}
	return generateControlKey()
}

func (o *Orchestrator) persist(ctx context.Context, res *Result, privPEM string) error {
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	var authUUID pgtype.UUID
	if u, perr := uuid.Parse(res.AuthTenantID); perr == nil {
		authUUID = pgtype.UUID{Bytes: u, Valid: true}
	}
	_, err = o.cp.UpsertControlPlane(ctx, storage.UpsertControlPlaneParams{
		AuthTenantUuid:       authUUID,
		Data:                 data,
		ControlPrivateKeyPem: privPEM,
		DeploymentMode:       o.cfg.DeploymentMode,
		SetupCompletedAt:     pgtype.Timestamptz{Time: res.CompletedAt, Valid: !res.CompletedAt.IsZero()},
	})
	return err
}

// mirror ensures Core has its own system tenant (keyed to Auth's) and registers
// the system services (Auth/Secret/Docker) in Core's service registry. Conflicts
// (re-runs) are logged and ignored.
func (o *Orchestrator) mirror(ctx context.Context, res *Result, tenantName, tenantDisplay string) error {
	tsvc := tenant.NewService(o.q)
	ssvc := service.NewService(o.q)

	sys, err := tsvc.GetSystem(ctx)
	var coreTenant uuid.UUID
	if err == nil && sys != nil {
		coreTenant = sys.UUID
	} else {
		var authUUIDPtr *uuid.UUID
		if u, perr := uuid.Parse(res.AuthTenantID); perr == nil {
			authUUIDPtr = &u
		}
		created, cerr := tsvc.Create(ctx, tenant.CreateInput{
			Name:           firstNonEmpty(tenantName, o.cfg.TenantName),
			DisplayName:    firstNonEmpty(tenantDisplay, o.cfg.TenantDisplayName),
			Status:         "active",
			IsSystem:       true,
			AuthTenantUUID: authUUIDPtr,
		})
		if cerr != nil {
			return fmt.Errorf("create core system tenant: %w", cerr)
		}
		coreTenant = created.UUID
	}

	systemServices := []struct{ name, kind, endpoint string }{
		{"auth", "Auth", o.cfg.AuthEndpoint},
		{"secret", "Secret", o.cfg.SecretEndpoint},
		{"docker", "Docker", o.cfg.DockerEndpoint},
	}
	for _, s := range systemServices {
		if _, err := ssvc.Create(ctx, service.CreateInput{
			TenantUUID: coreTenant,
			Name:       s.name,
			Kind:       s.kind,
			IsSystem:   true,
			Endpoint:   s.endpoint,
		}); err != nil {
			slog.Warn("core setup: register system service", "name", s.name, "err", err)
		}
	}
	if err := o.publishSystemResources(ctx, coreTenant); err != nil {
		slog.Warn("core setup: publish system resources failed", "err", err)
	}
	return nil
}

func (o *Orchestrator) publishSystemResources(ctx context.Context, tenantUUID uuid.UUID) error {
	systemImages := []struct {
		name  string
		image string
	}{
		{"auth", o.cfg.SystemAuthImage},
		{"secret", o.cfg.SystemSecretImage},
	}
	hasAny := false
	for _, item := range systemImages {
		if item.image != "" {
			hasAny = true
			break
		}
	}
	if !hasAny {
		return nil
	}
	tenantRow, err := o.q.GetTenantByUUID(ctx, tenantUUID)
	if err != nil {
		return err
	}
	projectRow, err := o.q.GetProjectByTenantAndName(ctx, storage.GetProjectByTenantAndNameParams{
		TenantID: tenantRow.TenantID,
		Name:     "system",
	})
	if errors.Is(err, pgx.ErrNoRows) {
		projectRow, err = o.q.CreateProject(ctx, storage.CreateProjectParams{
			TenantID:    tenantRow.TenantID,
			Name:        "system",
			DisplayName: "System",
			Description: "Maintainerd system-tier workloads",
			Status:      "active",
			Metadata:    []byte(`{"tier":"system"}`),
		})
	}
	if err != nil {
		return err
	}
	rsvc := resource.NewService(o.q)
	for _, item := range systemImages {
		if item.image == "" {
			continue
		}
		if _, err := rsvc.Create(ctx, resource.CreateInput{
			ProjectUUID: projectRow.ProjectUuid,
			Kind:        "Workload",
			Name:        "system-" + item.name,
			Spec: map[string]any{
				"image":       item.image,
				"name":        "maintainerd-" + item.name,
				"pull_policy": "if-not-present",
				"restart_policy": map[string]any{
					"name": "unless-stopped",
				},
			},
			Metadata: map[string]any{
				"tier":              "system",
				"mrn_service":       "core",
				"mrn_resource_type": "service",
				"mrn_resource_path": item.name,
			},
		}); err != nil {
			slog.Warn("core setup: publish system resource", "name", item.name, "err", err)
		}
	}
	return nil
}

// loadPersisted returns the stored control-plane result (empty if none yet).
func (o *Orchestrator) loadPersisted(ctx context.Context) *Result {
	row, err := o.cp.GetControlPlane(ctx)
	if err != nil {
		return &Result{}
	}
	res := &Result{}
	if len(row.Data) > 0 {
		_ = json.Unmarshal(row.Data, res)
	}
	if row.SetupCompletedAt.Valid {
		res.CompletedAt = row.SetupCompletedAt.Time
	}
	res.DeploymentMode = row.DeploymentMode
	return res
}

// Status reports whether Core has recorded a completed setup.
func (o *Orchestrator) Status(ctx context.Context) (completed bool, res *Result) {
	res = o.loadPersisted(ctx)
	return !res.CompletedAt.IsZero(), res
}

// RunWithRetry keeps attempting Run until it succeeds or ctx is cancelled —
// Auth may not be reachable the moment Core boots.
func (o *Orchestrator) RunWithRetry(ctx context.Context) {
	backoff := 3 * time.Second
	const maxBackoff = 30 * time.Second
	for {
		if _, err := o.Run(ctx); err == nil {
			return
		} else if errors.Is(err, ErrSetupRunning) {
			// The wizard beat us to it; poll again later rather than racing it.
			slog.Info("core setup: another run is in flight — will re-check", "retry_in", backoff.String())
		} else {
			slog.Warn("core setup: attempt failed — will retry", "err", err, "retry_in", backoff.String())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
		}
	}
}
