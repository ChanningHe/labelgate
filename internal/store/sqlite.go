package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/channinghe/labelgate/internal/model"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

const (
	memoryPath  = ":memory:"
	dataDirMode = 0o700
	// sqlitePragmas are applied to every new connection through the DSN.
	sqlitePragmas = "_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	metaInstanceID = "instance_id"
)

// SQLite implements Store on modernc.org/sqlite.
//
// Concurrency: the pool is limited to ONE connection. SQLite allows a single
// writer anyway; with one connection database/sql itself serializes every
// statement and transaction, so writers can never collide (no SQLITE_BUSY, no
// lock upgrade deadlocks between a reader transaction and a writer). The cost
// is that readers queue behind writers, which is irrelevant at labelgate's
// volume (hundreds of rows, millisecond statements). A consequence: never run
// a second statement while a *sql.Rows of the same connection is still open;
// every query helper here drains its rows before returning.
type SQLite struct {
	db  *sql.DB
	now func() time.Time
}

var _ Store = (*SQLite)(nil)

// Open opens (creating if needed) and migrates the database at path.
// path ":memory:" is allowed for tests. now nil = time.Now.
func Open(ctx context.Context, path string, now func() time.Time) (*SQLite, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: database path is empty", model.ErrInvalid)
	}
	if now == nil {
		now = time.Now
	}
	if path != memoryPath {
		if err := os.MkdirAll(filepath.Dir(path), dataDirMode); err != nil {
			return nil, fmt.Errorf("store: create data directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: connect %s: %w", path, err)
	}
	if err := Migrate(ctx, db, now().UTC().Unix()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLite{db: db, now: now}, nil
}

// dsn builds a file: URI so paths containing '?', '#' or '%' stay intact.
func dsn(path string) string {
	if path == memoryPath {
		return "file::memory:?" + sqlitePragmas
	}
	escaped := (&url.URL{Path: path}).EscapedPath()
	escaped = strings.NewReplacer("?", "%3F", "#", "%23").Replace(escaped)
	return "file:" + escaped + "?" + sqlitePragmas
}

// nowUnix is the store clock as unix seconds UTC.
func (s *SQLite) nowUnix() int64 { return s.now().UTC().Unix() }

// Close flushes and closes the database.
func (s *SQLite) Close() error { return s.db.Close() }

// InstanceID returns the persisted instance ID, generating one on first use.
func (s *SQLite) InstanceID(ctx context.Context) (string, error) {
	id, err := s.readInstanceID(ctx)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	fresh, err := newInstanceID()
	if err != nil {
		return "", err
	}
	// DO NOTHING keeps the first writer's value if another caller raced us.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`, metaInstanceID, fresh); err != nil {
		return "", fmt.Errorf("store: persist instance id: %w", err)
	}
	return s.readInstanceID(ctx)
}

func (s *SQLite) readInstanceID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, metaInstanceID).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: read instance id: %w", err)
	}
	return id, err
}

// SetInstanceID persists id (config override).
func (s *SQLite) SetInstanceID(ctx context.Context, id string) error {
	if !model.ValidInstanceID(id) {
		return fmt.Errorf("%w: instance id %q", model.ErrInvalid, id)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, metaInstanceID, id)
	if err != nil {
		return fmt.Errorf("store: persist instance id: %w", err)
	}
	return nil
}
