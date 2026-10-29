// Package migrations embeds the backend's ordered, forward-only SQLite schema
// migrations (AD-18). Each file is NNNN_name.sql; the store applies every one
// above the database's schema_version, in order, and there is no down path.
package migrations

import "embed"

// FS holds every NNNN_name.sql migration.
//
//go:embed *.sql
var FS embed.FS
