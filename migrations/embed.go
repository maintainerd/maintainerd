// Package migrations embeds the goose SQL migrations so they ship inside the
// binary and can be applied on boot without the migration files on disk. The
// schema here is the single source of truth for the whole control plane.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
