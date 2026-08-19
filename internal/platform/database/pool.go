package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/maintainerd/core/internal/platform/config"
	"github.com/maintainerd/core/internal/platform/retry"
)

// NewPool opens a pgx connection pool, waits for the database to be reachable
// (retrying with exponential backoff), enforces SSL in production, and applies
// connection-pool limits. It replaces the previous GORM connection.
func NewPool(ctx context.Context) (*pgxpool.Pool, error) {
	if config.AppEnv == "production" && config.DBSSLMode == "disable" {
		return nil, fmt.Errorf("database SSL is disabled (DB_SSLMODE=disable) — not allowed in production")
	}

	poolCfg, err := pgxpool.ParseConfig(config.GetDBConnectionString())
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}
	poolCfg.MaxConns = int32(config.DBMaxOpenConns)
	poolCfg.MinConns = int32(config.DBMaxIdleConns)
	poolCfg.MaxConnLifetime = time.Duration(config.DBConnMaxLifetimeSec) * time.Second
	poolCfg.MaxConnIdleTime = 90 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create database pool: %w", err)
	}

	if err := retry.WithBackoff(ctx, "postgres", 10, 2*time.Second, func() error {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return pool.Ping(pingCtx)
	}); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	slog.Info("Database connected (pgx pool)",
		"max_conns", config.DBMaxOpenConns,
		"min_conns", config.DBMaxIdleConns,
		"conn_max_lifetime_sec", config.DBConnMaxLifetimeSec,
		"statement_timeout_ms", config.DBStatementTimeoutMs,
	)
	return pool, nil
}
