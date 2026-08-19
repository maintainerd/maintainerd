package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sync/errgroup"

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

	// Serve the HTTP REST API and the core.v1 AgentGateway gRPC concurrently.
	// If either fails, the group context cancels and the other drains.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return appserver.StartRESTServer(gctx, appserver.Router(application)) })
	g.Go(func() error {
		return grpcserver.Serve(gctx, grpcListenAddr(), application.AgentSvc, application.ResourceSvc)
	})
	return g.Wait()
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
