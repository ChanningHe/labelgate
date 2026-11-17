package store

import (
	"context"
	"database/sql"
)

// SchemaVersion is the schema version written by this release (labelgate v0.3).
// Legacy (v0.1/v0.2) databases tracked versions 1..6 in sync_state; v0.3
// continues the sequence with 7 and stores it in meta.schema_version.
const SchemaVersion = 7

// Migrate brings db to SchemaVersion. See dev/docs/v0.3-design.md §5.
//
// Required behavior (WP3):
//   - Detect: meta table -> read meta.schema_version; else sync_state with
//     key schema_version (1..6) -> legacy; else empty DB.
//   - Legacy < 6: apply the frozen v0.2 migrations 1..6 first (copied verbatim
//     from `git show main:internal/storage/sqlite.go`).
//   - Every migration step runs in its own transaction together with its
//     version bump; re-running Migrate is a no-op (idempotent).
//   - Version > SchemaVersion -> error (downgrade not supported).
//   - Legacy -> 7 mapping as specified in the design (rows, agents, synthesized
//     tunnel CNAME rows, legacy flags, unix-second times, last_seen = migration time).
func Migrate(ctx context.Context, db *sql.DB, now int64) error {
	return errNotImplemented
}
