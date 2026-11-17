package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

const migrationTime = int64(1_800_000_000) // 2027-01-15, after every fixture timestamp

func schemaVersionOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	var v string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v); err != nil {
		t.Fatalf("read meta.schema_version: %v", err)
	}
	return v
}

func tableNames(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[n] = true
	}
	return out
}

func TestMigrateEmptyDatabase(t *testing.T) {
	db := openRawDB(t, filepath.Join(t.TempDir(), "new.db"))
	ctx := context.Background()

	if err := Migrate(ctx, db, migrationTime); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if got := schemaVersionOf(t, db); got != strconv.Itoa(SchemaVersion) {
		t.Errorf("schema_version = %s, want %d", got, SchemaVersion)
	}
	tables := tableNames(t, db)
	for _, want := range []string{"meta", "resources", "agents"} {
		if !tables[want] {
			t.Errorf("table %q missing", want)
		}
	}
	for _, gone := range []string{"managed_resources", "sync_state", "agents_v2"} {
		if tables[gone] {
			t.Errorf("legacy table %q present", gone)
		}
	}
	var id string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'instance_id'`).Scan(&id); err != nil {
		t.Fatalf("instance_id: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(id) || !model.ValidInstanceID(id) {
		t.Errorf("instance_id = %q, want 8 lower-case hex chars", id)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openRawDB(t, filepath.Join(t.TempDir(), "idem.db"))
	ctx := context.Background()
	if err := Migrate(ctx, db, migrationTime); err != nil {
		t.Fatal(err)
	}
	var first string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'instance_id'`).Scan(&first); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := Migrate(ctx, db, migrationTime+int64(i)); err != nil {
			t.Fatalf("re-run %d: %v", i, err)
		}
	}

	var again string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'instance_id'`).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Errorf("instance_id changed across re-runs: %q -> %q", first, again)
	}
	if got := schemaVersionOf(t, db); got != "7" {
		t.Errorf("schema_version = %s", got)
	}
}

func TestMigrateRefusesDowngradeAndUnknownStates(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, db *sql.DB)
		wantErr error
	}{
		{
			name: "newer meta version",
			setup: func(t *testing.T, db *sql.DB) {
				mustExec(t, db, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
				mustExec(t, db, `INSERT INTO meta VALUES ('schema_version', '8')`)
			},
			wantErr: ErrSchemaTooNew,
		},
		{
			name: "legacy version beyond 6",
			setup: func(t *testing.T, db *sql.DB) {
				mustExec(t, db, legacySyncStateDDL)
				mustExec(t, db, `INSERT INTO sync_state (key, value) VALUES ('schema_version', '9')`)
			},
			wantErr: ErrLegacyTooOld,
		},
		{
			name: "legacy tables without version table",
			setup: func(t *testing.T, db *sql.DB) {
				mustExec(t, db, `CREATE TABLE managed_resources (id TEXT PRIMARY KEY)`)
			},
			wantErr: ErrLegacyTooOld,
		},
		{
			name: "meta without schema_version row",
			setup: func(t *testing.T, db *sql.DB) {
				mustExec(t, db, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
			},
			wantErr: errNoVersion,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openRawDB(t, filepath.Join(t.TempDir(), "x.db"))
			tt.setup(t, db)

			err := Migrate(context.Background(), db, migrationTime)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Migrate error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestMigrateVersionReadErrorsPropagate(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, db *sql.DB)
	}{
		{"unparseable meta version", func(t *testing.T, db *sql.DB) {
			mustExec(t, db, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
			mustExec(t, db, `INSERT INTO meta VALUES ('schema_version', 'seven')`)
		}},
		{"unparseable legacy version", func(t *testing.T, db *sql.DB) {
			mustExec(t, db, legacySyncStateDDL)
			mustExec(t, db, `INSERT INTO sync_state (key, value) VALUES ('schema_version', 'x')`)
		}},
		{"sync_state without value column", func(t *testing.T, db *sql.DB) {
			mustExec(t, db, `CREATE TABLE sync_state (key TEXT PRIMARY KEY)`)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openRawDB(t, filepath.Join(t.TempDir(), "x.db"))
			tt.setup(t, db)

			if err := Migrate(context.Background(), db, migrationTime); err == nil {
				t.Fatal("Migrate succeeded; version read error was swallowed")
			}
		})
	}
}

func TestMigrateClosedDatabaseFails(t *testing.T) {
	db := openRawDB(t, filepath.Join(t.TempDir(), "x.db"))
	_ = db.Close()

	if err := Migrate(context.Background(), db, migrationTime); err == nil {
		t.Fatal("Migrate on closed db succeeded")
	}
}

// TestMigrateFromEveryLegacyVersion starts from each v0.2-era schema version
// with one minimal row (columns that exist since version 1) and checks the
// result is a correct schema 7 database.
func TestMigrateFromEveryLegacyVersion(t *testing.T) {
	for v := 1; v <= legacyFinalVersion; v++ {
		t.Run("from_v"+strconv.Itoa(v), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			db := buildLegacyDB(t, path, v)
			mustExec(t, db, `INSERT INTO managed_resources (id, resource_type, hostname, record_type, content, service_name)
				VALUES ('r1', 'dns', 'Old.Example.com', 'A', '1.2.3.4', 'svc')`)
			if v >= 2 {
				mustExec(t, db, `INSERT INTO agents (id, name) VALUES ('a1', 'one')`)
			}

			if err := Migrate(context.Background(), db, migrationTime); err != nil {
				t.Fatalf("Migrate: %v", err)
			}

			if got := schemaVersionOf(t, db); got != "7" {
				t.Errorf("schema_version = %s", got)
			}
			var key, status string
			var legacy int
			err := db.QueryRow(`SELECT key, status, legacy FROM resources`).Scan(&key, &status, &legacy)
			if err != nil {
				t.Fatalf("migrated row: %v", err)
			}
			if key != "old.example.com|A" || status != "active" || legacy != 1 {
				t.Errorf("row = key %q status %q legacy %d", key, status, legacy)
			}
			var agents int
			if err := db.QueryRow(`SELECT COUNT(*) FROM agents`).Scan(&agents); err != nil {
				t.Fatal(err)
			}
			if want := map[bool]int{true: 1, false: 0}[v >= 2]; agents != want {
				t.Errorf("agents = %d, want %d", agents, want)
			}
			if _, err := os.Stat(path + legacyBackupSuffix); err != nil {
				t.Errorf("backup missing: %v", err)
			}
		})
	}
}

func TestMigrateLegacyWithoutVersionRowIsEmpty(t *testing.T) {
	// v0.2's getSchemaVersion created sync_state before running migrations; a
	// crash in between leaves it without a version row and without any data.
	path := filepath.Join(t.TempDir(), "x.db")
	db := openRawDB(t, path)
	mustExec(t, db, legacySyncStateDDL)

	if err := Migrate(context.Background(), db, migrationTime); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if got := schemaVersionOf(t, db); got != "7" {
		t.Errorf("schema_version = %s", got)
	}
	if _, err := os.Stat(path + legacyBackupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backup of an empty database was created (err=%v)", err)
	}
}

func TestMigrateLegacyBackup(t *testing.T) {
	t.Run("backup holds the untouched v0.2 database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy.db")
		db := buildLegacyDB(t, path, 6)
		seedLegacyV6(t, db)

		if err := Migrate(context.Background(), db, migrationTime); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		bak := openRawDB(t, path+legacyBackupSuffix)
		var n int
		if err := bak.QueryRow(`SELECT COUNT(*) FROM managed_resources`).Scan(&n); err != nil {
			t.Fatalf("backup is not a v0.2 database: %v", err)
		}
		if n != 16 {
			t.Errorf("backup rows = %d, want 16", n)
		}
		var v string
		if err := bak.QueryRow(`SELECT value FROM sync_state WHERE key = 'schema_version'`).Scan(&v); err != nil || v != "6" {
			t.Errorf("backup schema_version = %q (%v), want 6", v, err)
		}
	})

	t.Run("existing backup is never overwritten", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy.db")
		db := buildLegacyDB(t, path, 6)
		sentinel := []byte("precious")
		if err := os.WriteFile(path+legacyBackupSuffix, sentinel, 0o600); err != nil {
			t.Fatal(err)
		}

		if err := Migrate(context.Background(), db, migrationTime); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		got, err := os.ReadFile(path + legacyBackupSuffix)
		if err != nil || string(got) != string(sentinel) {
			t.Errorf("existing backup was modified: %q (%v)", got, err)
		}
		if _, err := os.Stat(path + legacyBackupSuffix + "." + strconv.FormatInt(migrationTime, 10)); err != nil {
			t.Errorf("timestamped backup missing: %v", err)
		}
	})

	t.Run("failed backup refuses migration and leaves v0.2 intact", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy.db")
		db := buildLegacyDB(t, path, 6)
		seedLegacyV6(t, db)
		for _, name := range []string{
			path + legacyBackupSuffix,
			path + legacyBackupSuffix + "." + strconv.FormatInt(migrationTime, 10),
		} {
			if err := os.Mkdir(name, 0o700); err != nil { // VACUUM INTO cannot write onto a directory
				t.Fatal(err)
			}
		}

		err := Migrate(context.Background(), db, migrationTime)

		if err == nil {
			t.Fatal("Migrate succeeded without a backup")
		}
		if tableNames(t, db)["meta"] {
			t.Error("schema 7 was created despite the failed backup")
		}
		var v string
		if err := db.QueryRow(`SELECT value FROM sync_state WHERE key = 'schema_version'`).Scan(&v); err != nil || v != "6" {
			t.Errorf("legacy version = %q (%v), want 6", v, err)
		}
	})
}

func TestMigrateFailureRollsBackToLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db := buildLegacyDB(t, path, 6)
	seedLegacyV6(t, db)
	// A NULL agent id is legal in v0.2 (non-INTEGER primary key) but cannot be converted.
	mustExec(t, db, `INSERT INTO agents (id, name) VALUES (NULL, 'ghost')`)

	err := Migrate(context.Background(), db, migrationTime)

	if err == nil {
		t.Fatal("Migrate succeeded on unconvertible agent row")
	}
	tables := tableNames(t, db)
	if tables["meta"] || tables["resources"] || !tables["managed_resources"] || !tables["sync_state"] {
		t.Errorf("failed migration left a partial state: %v", tables)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM managed_resources`).Scan(&n); err != nil || n != 16 {
		t.Errorf("legacy rows = %d (%v), want 16", n, err)
	}
	var v string
	if err := db.QueryRow(`SELECT value FROM sync_state WHERE key = 'schema_version'`).Scan(&v); err != nil || v != "6" {
		t.Errorf("legacy version = %q (%v), want 6", v, err)
	}
}

func TestMigrateLowercasesLegacyAgentIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db := buildLegacyDB(t, path, 6)
	mustExec(t, db, `INSERT INTO managed_resources (id, resource_type, hostname, record_type, content, service_name, agent_id)
		VALUES ('r1', 'dns', 'a.example.com', 'A', '1.2.3.4', 'svc', 'NAS.lan')`)
	mustExec(t, db, `INSERT INTO agents (id, name) VALUES ('NAS.lan', 'nas')`)
	mustExec(t, db, `INSERT INTO agents (id, name) VALUES ('Bad Name', 'bad')`)

	if err := Migrate(context.Background(), db, migrationTime); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var owner string
	if err := db.QueryRow(`SELECT agent_id FROM resources`).Scan(&owner); err != nil || owner != "nas.lan" {
		t.Errorf("resource owner = %q (%v), want nas.lan", owner, err)
	}
	rows, err := db.Query(`SELECT id FROM agents ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 2 || ids[0] != "Bad Name" || ids[1] != "nas.lan" {
		t.Errorf("agent ids = %v, want [Bad Name nas.lan]", ids)
	}
}

func unix(t time.Time) int64 { return t.Unix() }

func findResource(t *testing.T, all []model.Resource, kind model.Kind, key string) model.Resource {
	t.Helper()
	for _, r := range all {
		if r.Kind == kind && r.Key == key && !r.Rejected {
			return r
		}
	}
	t.Fatalf("resource %s %q not found", kind, key)
	return model.Resource{}
}

// TestMigrateFromV02Fixture asserts the §5.2 mapping on a database shaped by
// real v0.2 writes (UTC+8 time.Time.String() timestamps).
func TestMigrateFromV02Fixture(t *testing.T) {
	for _, zone := range []struct {
		name string
		loc  *time.Location
	}{
		{"local_UTC", time.UTC},
		{"local_UTC+8", time.FixedZone("CST", 8*3600)},
		{"local_UTC-8", time.FixedZone("PST", -8*3600)},
	} {
		t.Run(zone.name, func(t *testing.T) {
			withLocalZone(t, zone.loc)
			path := filepath.Join(t.TempDir(), "legacy.db")
			db := buildLegacyDB(t, path, 6)
			seedLegacyV6(t, db)
			_ = db.Close()

			s, err := Open(context.Background(), path, func() time.Time { return time.Unix(migrationTime, 0) })
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer s.Close()
			all, err := s.AllResources(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			assertV02Mapping(t, all, s)
		})
	}
}

func assertV02Mapping(t *testing.T, all []model.Resource, s *SQLite) {
	t.Helper()
	// 16 legacy rows: -1 deleted, -1 duplicate = 14; + CNAMEs for api and same (web already has one) = 16.
	if len(all) != 16 {
		t.Fatalf("migrated %d rows, want 16: %+v", len(all), keysOf(all))
	}

	t.Run("duplicate keeps latest and normalizes hostname", func(t *testing.T) {
		r := findResource(t, all, model.KindDNS, "app.example.com|A")
		if r.CFID != "cf-a2" || r.Content != "2.2.2.2" || r.Hostname != "app.example.com" {
			t.Errorf("got cf=%q content=%q host=%q, want the newer row", r.CFID, r.Content, r.Hostname)
		}
		if !r.Legacy || r.Status != model.StatusActive || !r.Cleanup || r.Owner.AgentID != "" {
			t.Errorf("legacy=%v status=%v cleanup=%v agent=%q", r.Legacy, r.Status, r.Cleanup, r.Owner.AgentID)
		}
		if r.Owner.ContainerName != "web" || r.Owner.ContainerID != "c1" || r.Owner.Service != "web" ||
			r.Credential != "default" || r.ZoneID != "zone1" || !r.Proxied || r.TTL != 1 {
			t.Errorf("field copy wrong: %+v", r)
		}
		if unix(r.CreatedAt) != unix(fxCreated) || unix(r.UpdatedAt) != unix(fxNewer) {
			t.Errorf("times = %v / %v, want %v / %v", r.CreatedAt, r.UpdatedAt, fxCreated.UTC(), fxNewer.UTC())
		}
		if r.CreatedAt.Location() != time.UTC {
			t.Errorf("CreatedAt zone = %v, want UTC", r.CreatedAt.Location())
		}
	})
	t.Run("deleted rows are dropped", func(t *testing.T) {
		for _, r := range all {
			if r.Hostname == "gone.example.com" {
				t.Errorf("deleted row migrated: %+v", r)
			}
		}
	})
	t.Run("orphaned maps by cleanup flag", func(t *testing.T) {
		a := findResource(t, all, model.KindDNS, "txt.example.com|TXT|v=spf1 -all")
		if a.Status != model.StatusPendingCleanup || unix(a.OrphanedAt) != unix(fxNewer) {
			t.Errorf("orphaned+cleanup: status=%v orphanedAt=%v", a.Status, a.OrphanedAt)
		}
		b := findResource(t, all, model.KindDNS, "keep.example.com|A")
		if b.Status != model.StatusOrphaned || b.Cleanup || unix(b.OrphanedAt) != unix(fxOlder) {
			t.Errorf("orphaned+!cleanup: status=%v cleanup=%v orphanedAt=%v", b.Status, b.Cleanup, b.OrphanedAt)
		}
	})
	t.Run("tunnel rows keep path identity and are not legacy", func(t *testing.T) {
		v1 := findResource(t, all, model.KindTunnel, "tid-b|api.example.com|/v1")
		v2 := findResource(t, all, model.KindTunnel, "tid-b|api.example.com|/v2")
		if v1.Legacy || v2.Legacy || v1.Service != "http://a:80" || v2.Service != "http://web:80" {
			t.Errorf("tunnel rows wrong: %+v / %+v", v1, v2)
		}
		if v2.Owner.AgentID != "edge1" || v2.Credential != "main" || v2.Path != "/v2" || v2.TunnelID != "tid-b" {
			t.Errorf("owner/credential not copied: %+v", v2)
		}
		if v1.RecordType != "" || v1.Content != "" {
			t.Errorf("tunnel row carries DNS fields: %q %q", v1.RecordType, v1.Content)
		}
	})
	t.Run("synthesized tunnel CNAME from smallest service", func(t *testing.T) {
		c := findResource(t, all, model.KindDNS, "api.example.com|CNAME")
		if c.Content != model.TunnelTarget("tid-b") || !c.Proxied || c.TTL != 1 || c.RecordType != model.DNSTypeCNAME {
			t.Errorf("CNAME fields: %+v", c)
		}
		if c.TunnelID != "tid-b" || c.CFID != "" || !c.Legacy || c.Status != model.StatusActive || !c.Cleanup {
			t.Errorf("CNAME state: %+v", c)
		}
		if c.Owner.Service != "svca" || c.Owner.ContainerName != "api2" || c.Owner.AgentID != "" {
			t.Errorf("CNAME owner = %+v, want service svca (smallest)", c.Owner)
		}
		if c.ZoneID != "zone1" {
			t.Errorf("CNAME zone = %q", c.ZoneID)
		}
	})
	t.Run("existing CNAME row suppresses synthesis", func(t *testing.T) {
		n := 0
		for _, r := range all {
			if r.Key == "web.example.com|CNAME" {
				n++
				if r.CFID != "cf-c" {
					t.Errorf("existing CNAME replaced: %+v", r)
				}
			}
		}
		if n != 1 {
			t.Errorf("web.example.com CNAME rows = %d, want 1", n)
		}
	})
	t.Run("one CNAME per hostname across tunnels", func(t *testing.T) {
		c := findResource(t, all, model.KindDNS, "same.example.com|CNAME")
		if c.TunnelID != "tid-a" {
			t.Errorf("CNAME tunnel = %q, want lowest tunnel id tid-a", c.TunnelID)
		}
	})
	t.Run("access row uses access_app_id", func(t *testing.T) {
		a := findResource(t, all, model.KindAccess, "secure.example.com")
		if a.CFID != "app-1" || a.AccountID != "acct1" || a.AppName != "Secure" || a.PolicyName != "p" ||
			a.Decision != "allow" || !a.Legacy || len(a.PolicyIDs) != 0 {
			t.Errorf("access row: %+v", a)
		}
	})
	t.Run("error, NULL and odd values", func(t *testing.T) {
		e := findResource(t, all, model.KindDNS, "err.example.com|A")
		if e.Status != model.StatusError || e.LastError != "boom" || !e.Cleanup {
			t.Errorf("error row: status=%v err=%q cleanup=%v", e.Status, e.LastError, e.Cleanup)
		}
		w := findResource(t, all, model.KindDNS, "weird.example.com|A")
		if w.Status != model.StatusPending {
			t.Errorf("unknown legacy status -> %v, want pending", w.Status)
		}
	})
	t.Run("timestamps: unparseable falls back to migration time, other layouts parse", func(t *testing.T) {
		b := findResource(t, all, model.KindDNS, "badtime.example.com|A")
		if b.CreatedAt.Unix() != migrationTime || b.UpdatedAt.Unix() != migrationTime {
			t.Errorf("fallback times = %v / %v, want %d", b.CreatedAt, b.UpdatedAt, migrationTime)
		}
		m := findResource(t, all, model.KindDNS, "mono.example.com|A")
		wantCreated := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC).Unix()
		wantUpdated := time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC).Unix()
		if m.CreatedAt.Unix() != wantCreated || m.UpdatedAt.Unix() != wantUpdated {
			t.Errorf("times = %v / %v, want %d / %d", m.CreatedAt, m.UpdatedAt, wantCreated, wantUpdated)
		}
	})
	t.Run("agents are held until they report", func(t *testing.T) {
		assertMigratedAgents(t, s)
	})
	t.Run("instance id exists and legacy tables are gone", func(t *testing.T) {
		id, err := s.InstanceID(context.Background())
		if err != nil || !model.ValidInstanceID(id) {
			t.Errorf("InstanceID = %q, %v", id, err)
		}
		tables := tableNames(t, s.db)
		if tables["managed_resources"] || tables["sync_state"] || tables["agents_v2"] {
			t.Errorf("legacy tables remain: %v", tables)
		}
	})
}

func assertMigratedAgents(t *testing.T, s *SQLite) {
	t.Helper()
	ctx := context.Background()
	agents, err := s.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 2 || agents[0].ID != "edge1" || agents[1].ID != "edge2" {
		t.Fatalf("agents = %+v", agents)
	}
	a := agents[0]
	if a.Name != "Edge One" || a.PublicIP != "9.9.9.9" || a.DefaultTunnel != "tid-b" || a.PublicIPv6 != "" {
		t.Errorf("agent fields: %+v", a)
	}
	if a.LastSeen.Unix() != migrationTime {
		t.Errorf("LastSeen = %v, want migration time (agent-supplied value must be discarded)", a.LastSeen)
	}
	if a.CreatedAt.Unix() != unix(fxCreated) {
		t.Errorf("CreatedAt = %v, want %v", a.CreatedAt, fxCreated.UTC())
	}
	snaps, err := s.AgentSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sn := range snaps {
		if sn.Complete || len(sn.Containers) != 0 {
			t.Errorf("migrated agent %q has a snapshot: %+v", sn.AgentID, sn)
		}
	}
}

func keysOf(all []model.Resource) []string {
	out := make([]string, len(all))
	for i, r := range all {
		out[i] = string(r.Kind) + ":" + r.Key
	}
	return out
}

func TestParseLegacyTime(t *testing.T) {
	want := time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC).Unix()
	tests := []struct {
		name   string
		in     string
		want   int64
		wantOK bool
	}{
		{"go String() UTC+8", "2026-10-01 10:00:00 +0800 CST", want, true},
		{"go String() with fraction", "2026-10-01 10:00:00.987654321 +0800 CST", want, true},
		{"go String() with monotonic suffix", "2026-10-01 10:00:00.5 +0800 CST m=+3.141592", want, true},
		{"go String() negative monotonic", "2026-10-01 10:00:00 +0800 CST m=-0.5", want, true},
		{"go String() UTC", "2026-10-01 02:00:00 +0000 UTC", want, true},
		{"go String() negative zone", "2026-09-30 18:00:00 -0800 PST", want, true},
		{"go String() zone number as name", "2026-10-01 10:00:00 +0800 +08", want, true},
		{"sqlite CURRENT_TIMESTAMP", "2026-10-01 02:00:00", want, true},
		{"sqlite with fraction", "2026-10-01 02:00:00.250", want, true},
		{"RFC3339", "2026-10-01T10:00:00+08:00", want, true},
		{"RFC3339Nano Z", "2026-10-01T02:00:00.123456789Z", want, true},
		{"padded", "  2026-10-01 02:00:00  ", want, true},
		{"empty", "", 0, false},
		{"garbage", "yesterday", 0, false},
		{"date only", "2026-10-01", 0, false},
	}
	for _, zone := range []*time.Location{time.UTC, time.FixedZone("X", 5*3600), time.FixedZone("Y", -11*3600)} {
		for _, tt := range tests {
			t.Run(tt.name+"/"+zone.String(), func(t *testing.T) {
				withLocalZone(t, zone)

				got, ok := parseLegacyTime(tt.in)

				if ok != tt.wantOK || (ok && got != tt.want) {
					t.Errorf("parseLegacyTime(%q) = %d, %v; want %d, %v", tt.in, got, ok, tt.want, tt.wantOK)
				}
			})
		}
	}
}
