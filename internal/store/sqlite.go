package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/channinghe/labelgate/internal/model"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

var errNotImplemented = errors.New("store: not implemented")

// SQLite implements Store on modernc.org/sqlite.
//
// Required behavior (WP3):
//   - DSN pragmas: journal_mode(WAL), synchronous(NORMAL), busy_timeout(5000),
//     foreign_keys(ON); db.SetMaxOpenConns(1) (single writer, no SQLITE_BUSY).
//   - Creates the parent directory (0700) if missing.
//   - Runs Migrate before returning; on failure closes the DB and returns the error.
type SQLite struct {
	db  *sql.DB
	now func() time.Time
}

var _ Store = (*SQLite)(nil)

// Open opens (creating if needed) and migrates the database at path.
// path ":memory:" is allowed for tests. now nil = time.Now.
func Open(ctx context.Context, path string, now func() time.Time) (*SQLite, error) {
	return nil, errNotImplemented
}

func (s *SQLite) Close() error                                       { return errNotImplemented }
func (s *SQLite) InstanceID(ctx context.Context) (string, error)     { return "", errNotImplemented }
func (s *SQLite) SetInstanceID(ctx context.Context, id string) error { return errNotImplemented }
func (s *SQLite) AllResources(ctx context.Context) ([]model.Resource, error) {
	return nil, errNotImplemented
}
func (s *SQLite) ListResources(ctx context.Context, q ResourceQuery) (Page, error) {
	return Page{}, errNotImplemented
}
func (s *SQLite) CountResources(ctx context.Context) (Counts, error) { return nil, errNotImplemented }
func (s *SQLite) CountResourcesByAgent(ctx context.Context) (map[string]int, error) {
	return nil, errNotImplemented
}
func (s *SQLite) UpsertResource(ctx context.Context, r model.Resource) (model.Resource, error) {
	return model.Resource{}, errNotImplemented
}
func (s *SQLite) UpdateResource(ctx context.Context, r model.Resource) error {
	return errNotImplemented
}
func (s *SQLite) DeleteResource(ctx context.Context, id int64) error { return errNotImplemented }
func (s *SQLite) ReplaceRejected(ctx context.Context, rows []model.Resource) error {
	return errNotImplemented
}
func (s *SQLite) ListAgents(ctx context.Context) ([]model.AgentRecord, error) {
	return nil, errNotImplemented
}
func (s *SQLite) RecordAgentReport(ctx context.Context, rec model.AgentRecord, snap *model.Snapshot) (bool, error) {
	return false, errNotImplemented
}
func (s *SQLite) AgentSnapshots(ctx context.Context) ([]model.Snapshot, error) {
	return nil, errNotImplemented
}
