package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	sdkauth "github.com/maintainerd/sdk/auth"

	"github.com/maintainerd/core/internal/app"
	"github.com/maintainerd/core/internal/grpcserver"
	"github.com/maintainerd/core/internal/platform/authz"
	"github.com/maintainerd/core/internal/platform/config"
	"github.com/maintainerd/core/internal/platform/database"
	"github.com/maintainerd/core/internal/platform/telemetry"
	appserver "github.com/maintainerd/core/internal/server"
	"github.com/maintainerd/core/internal/setup"
	"github.com/maintainerd/core/internal/storage"
)

// run executes the server bootstrap sequence in dependency order. Keep this as
// orchestration only; reusable infrastructure belongs in internal/platform and
// domain wiring belongs in internal/app.
func run(parent context.Context) error {
	// Temporary JSON logger before config is loaded so early failures still log.
	initBootstrapLogger()

	if err := config.Init(); err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	// Bring up the OTel log provider before rebuilding the logger. No-op when disabled.
	logsShutdown, err := telemetry.InitLogs(parent)
	if err != nil {
		return fmt.Errorf("initialize OpenTelemetry logging: %w", err)
	}
	defer shutdownWithTimeout("OpenTelemetry logging", logsShutdown)

	// Rebuild the logger once LOG_LEVEL/PII/OTel are known.
	initConfiguredLogger()

	if config.AppEnv == "production" {
		slog.Info("startup: running in production mode — TLS/SSL enforcement active", "app_env", config.AppEnv)
	} else {
		slog.Warn("startup: running in development mode — security hardening relaxed", "app_env", config.AppEnv)
	}

	telemetryShutdown, err := initTelemetry(parent)
	if err != nil {
		return err
	}
	defer telemetryShutdown()

	// Signal-aware context so SIGINT/SIGTERM triggers graceful shutdown.
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// pgx pool is the process-level database dependency shared by all repositories.
	pool, err := database.NewPool(ctx)
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	defer pool.Close()

	// Migrations (goose) run before wiring so repositories start on a current schema.
	if err := database.RunMigrations(ctx); err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}

	// The stamped deployment mode is immutable: refuse to boot when the env
	// disagrees with the install record (see verifyDeploymentMode).
	if err := verifyDeploymentMode(ctx, pool); err != nil {
		return err
	}

	// Resolve the security posture ONCE, before anything listens: one shared
	// sdk verifier (or a fail-closed/dev-open decision) drives both the HTTP
	// guard and the gRPC AgentGateway guard.
	dev := config.AppEnv == "development"
	verifier := buildVerifier(ctx, dev)
	httpGuard := resolveHTTPGuard(verifier, dev)
	grpcGuard := resolveGRPCGuard(verifier, dev)

	application := app.New(pool, setup.Gate{
		Token:  config.CoreSetupToken,
		Dev:    dev,
		Verify: httpGuard.Verify,
	})

	// When SETUP_ENABLED is set, Core drives Auth's gRPC setup on boot to
	// provision the system tenant/admin and register itself as the control
	// service, then records the credentials. Best-effort + retrying (Auth may
	// still be starting); never blocks serving, and no-ops once complete.
	if application.SetupOrch.Enabled() {
		slog.Info("startup: setup orchestration enabled — provisioning against auth in the background")
		go application.SetupOrch.RunWithRetry(ctx)
	}

	agentCACertPEM, err := readOptionalFile("AGENT_CA_CERT_FILE", config.AgentCACertFile)
	if err != nil {
		return err
	}
	agentCAKeyPEM, err := readOptionalFile("AGENT_CA_KEY_FILE", config.AgentCAKeyFile)
	if err != nil {
		return err
	}

	// Serve the HTTP REST API and the core.v1 AgentGateway gRPC concurrently.
	// If either fails, the group context cancels and the other drains.
	gateway := grpcserver.NewAgentGateway(application.AgentSvc, application.ResourceSvc, grpcserver.Options{
		EnforceBinding: grpcGuard.Mode == grpcserver.GuardEnforced,
		LeaseTTL:       config.GatewayLeaseTTL,
		AttemptBudget:  config.GatewayAttemptBudget,
		AgentCACertPEM: agentCACertPEM,
		AgentCAKeyPEM:  agentCAKeyPEM,
		AgentCertTTL:   config.AgentCertTTL,
	})
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return appserver.StartRESTServer(gctx, appserver.Router(application, httpGuard)) })
	g.Go(func() error {
		return grpcserver.Serve(gctx, grpcListenAddr(), gateway, grpcGuard, grpcserver.TLSOptions{
			CertFile:          config.GRPCTLSCertFile,
			KeyFile:           config.GRPCTLSKeyFile,
			ClientCAFile:      config.GRPCClientCAFile,
			RequireClientCert: config.GRPCClientCAFile != "" && grpcGuard.Mode == grpcserver.GuardEnforced,
		})
	})
	return g.Wait()
}

func readOptionalFile(label, path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s %q: %w", label, path, err)
	}
	return b, nil
}

// verifyDeploymentMode refuses to boot when DEPLOYMENT_MODE disagrees with the
// mode stamped into control_plane at setup. The stamp is immutable by design:
// every reconciled resource was materialized on that substrate (docker
// containers vs kubernetes objects), so booting under a different mode would
// have the agents rebuild the world on a new runtime while the old workloads
// keep running, orphaned and unmanaged. Re-substrating is a migration, not an
// env-var flip. A missing row (fresh install, setup not yet run) passes — the
// stamp is written at setup from this same validated value.
func verifyDeploymentMode(ctx context.Context, pool *pgxpool.Pool) error {
	row, err := storage.New(pool).GetControlPlane(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read control plane install record: %w", err)
	}
	if row.DeploymentMode != "" && row.DeploymentMode != config.DeploymentMode {
		return fmt.Errorf(
			"DEPLOYMENT_MODE=%q conflicts with the immutable install record (stamped %q at setup); "+
				"restore the original value — changing substrates requires a migration, not a config change",
			config.DeploymentMode, row.DeploymentMode)
	}
	return nil
}

// buildVerifier constructs the shared sdk token verifier, or nil when the
// auth config is missing/unusable — the caller decides what nil means per
// surface (fail closed outside development, loud dev-open otherwise).
func buildVerifier(ctx context.Context, dev bool) *sdkauth.Verifier {
	if m := missingAuthVars(); len(m) > 0 {
		slog.Warn("caller authentication not configured", "missing", strings.Join(m, ", "), "development", dev)
		return nil
	}
	v, err := sdkauth.NewVerifier(ctx, config.AuthJWKSURL, config.AuthIssuer, config.AuthAudience)
	if err != nil {
		slog.Error("token verifier init failed", "jwks_url", config.AuthJWKSURL, "err", err)
		return nil
	}
	return v
}

func missingAuthVars() []string {
	var missing []string
	if config.AuthJWKSURL == "" {
		missing = append(missing, "AUTH_JWKS_URL")
	}
	if config.AuthIssuer == "" {
		missing = append(missing, "AUTH_ISSUER")
	}
	if config.AuthAudience == "" {
		missing = append(missing, "AUTH_AUDIENCE")
	}
	return missing
}

// resolveHTTPGuard decides how /api/v1 treats callers. Outside development a
// missing verifier disables the API (503 with a precise reason) instead of
// serving the control plane open; the setup surface stays reachable behind its
// own CORE_SETUP_TOKEN gate. Development degrades to open with a loud warning
// naming the disabled guards.
func resolveHTTPGuard(verifier *sdkauth.Verifier, dev bool) authz.Guard {
	if verifier == nil {
		reason := "missing/unusable " + strings.Join(missingAuthVars(), ", ")
		if len(missingAuthVars()) == 0 {
			reason = "token verifier failed to initialize"
		}
		if dev {
			slog.Warn("SECURITY: HTTP API is UNAUTHENTICATED (development only)",
				"disabled_guards", "bearer-token verification, core:* route permissions",
				"reason", reason)
			return authz.Guard{Mode: authz.ModeDevOpen, Reason: reason}
		}
		return authz.Guard{Mode: authz.ModeUnavailable, Reason: reason}
	}
	return authz.Guard{Mode: authz.ModeEnforced, Verify: authz.SDKVerify(verifier)}
}

// resolveGRPCGuard decides how the AgentGateway listener treats callers.
// Outside development a missing verifier means the gateway does not come up at
// all (grpc-health only): an unauthenticated gateway hands out workload specs
// and accepts forged status reports, so it fails closed. Development degrades
// to open with a loud warning; reflection registers in development only.
func resolveGRPCGuard(verifier *sdkauth.Verifier, dev bool) grpcserver.Guard {
	if verifier == nil {
		reason := "missing/unusable " + strings.Join(missingAuthVars(), ", ")
		if len(missingAuthVars()) == 0 {
			reason = "token verifier failed to initialize"
		}
		if dev {
			return grpcserver.Guard{Mode: grpcserver.GuardDevOpen, Reason: reason, Dev: true}
		}
		return grpcserver.Guard{Mode: grpcserver.GuardHealthOnly, Reason: reason}
	}
	return grpcserver.Guard{Mode: grpcserver.GuardEnforced, Verify: grpcserver.SDKVerify(verifier), Dev: dev}
}

// grpcListenAddr resolves the AgentGateway gRPC listen address (GRPC_PORT, default :8081).
func grpcListenAddr() string {
	p := os.Getenv("GRPC_PORT")
	if p == "" {
		p = "8081"
	}
	if !strings.HasPrefix(p, ":") {
		p = ":" + p
	}
	return p
}
