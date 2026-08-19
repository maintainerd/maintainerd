package database

import (
	"context"
	"database/sql"
	"fmt"

	// Registers the "pgx" database/sql driver used to run goose migrations.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/maintainerd/core/internal/platform/config"
	"github.com/maintainerd/core/migrations"
)

// RunMigrations applies all pending goose migrations embedded in the binary.
// It opens a short-lived database/sql connection (goose requires one) using the
// pgx stdlib driver, independent of the pgx pool used for queries.
func RunMigrations(ctx context.Context) error {
	sqlDB, err := sql.Open("pgx", config.GetDBConnectionString())
	if err != nil {
		return fmt.Errorf("failed to open migration connection: %w", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("failed to set goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}
	return nil
}
