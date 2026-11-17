package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// testClock is a settable clock safe for concurrent use.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// openTestStore opens a file-backed store in a temp dir.
func openTestStore(t *testing.T, clock *testClock) *SQLite {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "labelgate.db"), clock.Now)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// openRawDB opens path with the driver directly (no migration), one connection.
func openRawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?"+sqlitePragmas)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// buildLegacyDB creates a database exactly as v0.2 would have left it at
// schema version upTo: sync_state first, then every frozen migration <= upTo,
// each followed by its version bump.
func buildLegacyDB(t *testing.T, path string, upTo int) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db := openRawDB(t, path)
	if _, err := db.ExecContext(ctx, legacySyncStateDDL); err != nil {
		t.Fatalf("create sync_state: %v", err)
	}
	for _, m := range legacyMigrations {
		if m.version > upTo {
			break
		}
		if _, err := db.ExecContext(ctx, m.sql); err != nil {
			t.Fatalf("legacy migration %d: %v", m.version, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO sync_state (key, value) VALUES ('schema_version', ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.Itoa(m.version)); err != nil {
			t.Fatalf("legacy version bump %d: %v", m.version, err)
		}
	}
	return db
}

// mustExec executes q and fails the test on error.
func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// withLocalZone swaps time.Local for the duration of the test. Tests using it
// must not run in parallel.
func withLocalZone(t *testing.T, loc *time.Location) {
	t.Helper()
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
}
