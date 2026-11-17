package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
)

// SchemaVersion is the schema version written by this release (labelgate v0.3).
// Legacy (v0.1/v0.2) databases tracked versions 1..6 in sync_state; v0.3
// continues the sequence with 7 and stores it in meta.schema_version.
const SchemaVersion = 7

// ErrSchemaTooNew is returned when the database was written by a newer
// labelgate release. Downgrades are not supported.
var ErrSchemaTooNew = errors.New("store: database schema is newer than this release supports")

// legacyBackupSuffix is appended to the database path for the pre-migration backup.
const legacyBackupSuffix = ".v0.2.bak"

// Migrate brings db to SchemaVersion. See dev/docs/v0.3-design.md §5.
//
//   - Detection: meta table -> meta.schema_version; else sync_state row
//     schema_version (1..6) -> legacy; else empty database.
//   - A legacy database is first backed up with VACUUM INTO (migration is
//     refused if the backup fails), then brought to legacy version 6 with the
//     frozen v0.2 steps, then converted to schema 7.
//   - Every step runs in its own transaction together with its version bump,
//     so Migrate is idempotent and a failed step leaves the previous version.
//   - Version > SchemaVersion -> ErrSchemaTooNew.
//
// now is the migration time (unix seconds UTC).
func Migrate(ctx context.Context, db *sql.DB, now int64) error {
	return migrate(ctx, db, now, slog.Default())
}

// dbState is the detected schema state of a database.
type dbState struct {
	version int
	legacy  bool // version is tracked in sync_state rather than meta
}

func migrate(ctx context.Context, db *sql.DB, now int64, log *slog.Logger) error {
	st, err := detectState(ctx, db)
	if err != nil {
		return err
	}
	if st.version > SchemaVersion {
		return fmt.Errorf("%w: database is at version %d, this release supports up to %d",
			ErrSchemaTooNew, st.version, SchemaVersion)
	}
	if st.legacy {
		if err := upgradeLegacy(ctx, db, st, now, log); err != nil {
			return err
		}
	}
	return runCurrentSteps(ctx, db, st, now, log)
}

// upgradeLegacy backs the database up and applies the frozen v0.2 steps.
func upgradeLegacy(ctx context.Context, db *sql.DB, st dbState, now int64, log *slog.Logger) error {
	if st.version >= 1 {
		path, err := backupLegacy(ctx, db, now)
		if err != nil {
			return fmt.Errorf("store: refusing to migrate legacy database, backup failed: %w", err)
		}
		if path != "" {
			log.Info("legacy database backed up before migration", "path", path)
		}
	}
	for _, m := range legacyMigrations {
		if m.version <= st.version {
			continue
		}
		err := inTx(ctx, db, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, legacySyncStateDDL); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return err
			}
			return setLegacyVersion(ctx, tx, m.version)
		})
		if err != nil {
			return fmt.Errorf("store: legacy migration %d: %w", m.version, err)
		}
	}
	return nil
}

// currentStep is one schema step at version >= SchemaVersion's sequence (7+).
type currentStep struct {
	version int
	apply   func(ctx context.Context, tx *sql.Tx, env stepEnv) error
}

// stepEnv carries what a step needs besides the transaction.
type stepEnv struct {
	now int64
	log *slog.Logger
}

var currentSteps = []currentStep{
	{version: 7, apply: applyV7},
}

func runCurrentSteps(ctx context.Context, db *sql.DB, st dbState, now int64, log *slog.Logger) error {
	cur := st.version
	if st.legacy {
		cur = legacyFinalVersion
	}
	for _, s := range currentSteps {
		if s.version <= cur {
			continue
		}
		err := inTx(ctx, db, func(tx *sql.Tx) error {
			if err := s.apply(ctx, tx, stepEnv{now: now, log: log}); err != nil {
				return err
			}
			return setMetaVersion(ctx, tx, s.version)
		})
		if err != nil {
			return fmt.Errorf("store: migration to schema %d: %w", s.version, err)
		}
	}
	return nil
}

// detectState reads the schema version. Read errors propagate; they never
// degrade into "version 0".
func detectState(ctx context.Context, db *sql.DB) (dbState, error) {
	hasMeta, err := tableExists(ctx, db, "meta")
	if err != nil {
		return dbState{}, err
	}
	if hasMeta {
		v, err := readVersion(ctx, db, "meta")
		if err != nil {
			return dbState{}, err
		}
		return dbState{version: v}, nil
	}
	hasSync, err := tableExists(ctx, db, "sync_state")
	if err != nil {
		return dbState{}, err
	}
	if hasSync {
		return detectLegacy(ctx, db)
	}
	for _, t := range []string{"managed_resources", "resources", "agents"} {
		ok, err := tableExists(ctx, db, t)
		if err != nil {
			return dbState{}, err
		}
		if ok {
			return dbState{}, fmt.Errorf("%w: table %q exists without a version table", ErrLegacyTooOld, t)
		}
	}
	return dbState{}, nil
}

func detectLegacy(ctx context.Context, db *sql.DB) (dbState, error) {
	v, err := readVersion(ctx, db, "sync_state")
	if errors.Is(err, errNoVersion) {
		return dbState{legacy: true}, nil // sync_state created, never migrated
	}
	if err != nil {
		return dbState{}, err
	}
	if v < 1 || v > legacyFinalVersion {
		return dbState{}, fmt.Errorf("%w: sync_state.schema_version = %d", ErrLegacyTooOld, v)
	}
	return dbState{version: v, legacy: true}, nil
}

var errNoVersion = errors.New("store: no schema_version row")

// queryer is the read subset shared by *sql.DB and *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func readVersion(ctx context.Context, q queryer, table string) (int, error) {
	var raw string
	// table is one of two compile-time constants, never user input.
	err := q.QueryRowContext(ctx, "SELECT value FROM "+table+" WHERE key = 'schema_version'").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errNoVersion
	}
	if err != nil {
		return 0, fmt.Errorf("store: read schema version from %s: %w", table, err)
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("store: invalid schema version %q in %s: %w", raw, table, err)
	}
	return v, nil
}

func tableExists(ctx context.Context, q queryer, name string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: inspect schema: %w", err)
	}
	return n > 0, nil
}

func setLegacyVersion(ctx context.Context, tx *sql.Tx, v int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sync_state (key, value) VALUES ('schema_version', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.Itoa(v))
	return err
}

func setMetaVersion(ctx context.Context, tx *sql.Tx, v int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('schema_version', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.Itoa(v))
	return err
}

// inTx runs fn in a transaction, committing on success and rolling back on error.
func inTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// backupLegacy copies the database with VACUUM INTO. It returns "" when the
// database has no file (in-memory), in which case there is nothing to back up.
func backupLegacy(ctx context.Context, db *sql.DB, now int64) (string, error) {
	file, err := mainDatabaseFile(ctx, db)
	if err != nil || file == "" {
		return "", err
	}
	dst := file + legacyBackupSuffix
	if _, err := os.Stat(dst); err == nil {
		// A previous attempt already left a backup; never overwrite it.
		dst = fmt.Sprintf("%s.%d", dst, now)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return "", err
	}
	return dst, nil
}

func mainDatabaseFile(ctx context.Context, db *sql.DB) (string, error) {
	var file string
	err := db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&file)
	if err != nil {
		return "", fmt.Errorf("store: locate database file: %w", err)
	}
	return file, nil
}

// newInstanceID returns 8 random lower-case hex characters.
func newInstanceID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("store: generate instance id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
