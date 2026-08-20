package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sync/errgroup"

	sdkauth "github.com/maintainerd/sdk/auth"

	"github.com/maintainerd/core/internal/app"
	"github.com/maintainerd/core/internal/grpcserver"
	"github.com/maintainerd/core/internal/platform/config"
	"github.com/maintainerd/core/internal/platform/database"
	"github.com/maintainerd/core/internal/platform/telemetry"
	appserver "github.com/maintainerd/core/internal/server"
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

	application := app.New(pool)

	// When SETUP_ENABLED is set, Core drives Auth's gRPC setup on boot to
	// provision the system tenant/admin and register itself as the control
	// service, then records the credentials. Best-effort + retrying (Auth may
	// still be starting); never blocks serving, and no-ops once complete.
	if application.SetupOrch.Enabled() {
		slog.Info("startup: setup orchestration enabled — provisioning against auth in the background")
		go application.SetupOrch.RunWithRetry(ctx)
	}

	// Build the system-Auth (IAM) gate from the SDK verifier. Additive: nil when
	// AUTH_JWKS_URL is unset, leaving the REST API ungated (the dev default).
	authMW, err := buildAuthGate(ctx)
	if err != nil {
		return err
	}

	// Serve the HTTP REST API and the core.v1 AgentGateway gRPC concurrently.
	// If either fails, the group context cancels and the other drains.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return appserver.StartRESTServer(gctx, appserver.Router(application, authMW)) })
	g.Go(func() error {
		return grpcserver.Serve(gctx, grpcListenAddr(), application.AgentSvc, application.ResourceSvc)
	})
	return g.Wait()
}

// buildAuthGate constructs the system-Auth (IAM) enforcement middleware from the
// SDK's token verifier when AUTH_JWKS_URL is set. The verifier validates incoming
// bearer tokens against Auth's public JWKS (optionally checking issuer/audience).
// When AUTH_JWKS_URL is unset it returns nil and the REST API stays ungated — the
// current dev default — so enabling the gate is a config-only change.
func buildAuthGate(ctx context.Context) (func(http.Handler) http.Handler, error) {
	jwksURL := os.Getenv("AUTH_JWKS_URL")
	if jwksURL == "" {
		slog.Warn("AUTH_JWKS_URL not set — REST API running without a system-Auth gate")
		return nil, nil
	}
	v, err := sdkauth.NewVerifier(ctx, jwksURL, os.Getenv("AUTH_ISSUER"), os.Getenv("AUTH_AUDIENCE"))
	if err != nil {
		return nil, fmt.Errorf("initialize system-Auth verifier: %w", err)
	}
	slog.Info("system-Auth (IAM) gate enabled on the REST API", "jwks_url", jwksURL)
	return v.Middleware, nil
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
