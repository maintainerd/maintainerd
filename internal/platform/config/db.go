package config

import "fmt"

// GetDBConnectionString builds the libpq keyword/value DSN used by the pgx pool
// (internal/platform/database.NewPool) and by goose migrations.
func GetDBConnectionString() string {
	// The `options` value contains a space, so it must be single-quoted in the
	// keyword/value DSN — otherwise the driver splits it and Postgres receives a
	// bare `-c` (FATAL: invalid command-line argument for server process: -c).
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s options='-c statement_timeout=%d'",
		DBHost, DBPort, DBUser, DBPassword, DBName, DBSSLMode, DBStatementTimeoutMs,
	)
}
