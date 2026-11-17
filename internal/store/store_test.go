package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

func containers(ids ...string) []model.Container {
	out := make([]model.Container, len(ids))
	for i, id := range ids {
		ip := "172.17.0." + fmt.Sprint(len(id)+int(id[0]))
		out[i] = model.Container{
			ID: id, Name: "name-" + id, State: "running",
			Labels:   map[string]string{"labelgate.dns.web.hostname": id + ".example.com"},
			Networks: map[string]string{"bridge": ip},
		}
	}
	return out
}

func TestRecordAgentReport(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	s := openTestStore(t, clock)
	seen1 := clock.Now()
	rec := model.AgentRecord{ID: "edge1", Name: "Edge", DefaultTunnel: "tun", PublicIP: "1.1.1.1", PublicIPv6: "::1", LastSeen: seen1}

	t.Run("first report with snapshot changes", func(t *testing.T) {
		changed, err := s.RecordAgentReport(ctx, rec, &model.Snapshot{AgentID: "edge1", Containers: containers("a", "b")})

		if err != nil || !changed {
			t.Fatalf("changed = %v, err = %v; want true, nil", changed, err)
		}
	})
	t.Run("same containers in another order do not change", func(t *testing.T) {
		clock.Advance(time.Minute)
		rec.LastSeen = clock.Now()

		changed, err := s.RecordAgentReport(ctx, rec, &model.Snapshot{Containers: containers("b", "a")})

		if err != nil || changed {
			t.Errorf("changed = %v, err = %v; want false, nil", changed, err)
		}
	})
	t.Run("different containers change", func(t *testing.T) {
		changed, err := s.RecordAgentReport(ctx, rec, &model.Snapshot{Containers: containers("a")})

		if err != nil || !changed {
			t.Errorf("changed = %v, err = %v; want true, nil", changed, err)
		}
	})
	t.Run("public IP change with the same containers changes", func(t *testing.T) {
		rec6 := rec
		rec6.PublicIPv6 = "2001:db8::9"
		changed, err := s.RecordAgentReport(ctx, rec6, &model.Snapshot{Containers: containers("a")})
		if err != nil || !changed {
			t.Fatalf("v6 change: changed = %v, err = %v; want true", changed, err)
		}
		changed, err = s.RecordAgentReport(ctx, rec, &model.Snapshot{Containers: containers("a")})
		if err != nil || !changed {
			t.Fatalf("v6 back: changed = %v, err = %v; want true", changed, err)
		}
	})
	t.Run("nil snapshot keeps the stored one; only an IP change is a change", func(t *testing.T) {
		clock.Advance(time.Minute)
		rec.LastSeen = clock.Now()
		if changed, err := s.RecordAgentReport(ctx, rec, nil); err != nil || changed {
			t.Fatalf("same IP: changed = %v, err = %v; want false, nil", changed, err)
		}
		rec.PublicIP = "2.2.2.2"

		changed, err := s.RecordAgentReport(ctx, rec, nil)

		if err != nil || !changed {
			t.Fatalf("new IP: changed = %v, err = %v; want true, nil", changed, err)
		}
		snaps, _ := s.AgentSnapshots(ctx)
		if len(snaps) != 1 || !snaps[0].Complete || len(snaps[0].Containers) != 1 || snaps[0].Containers[0].ID != "a" {
			t.Errorf("snapshot lost: %+v", snaps)
		}
		if snaps[0].PublicIP != "2.2.2.2" || !snaps[0].TakenAt.Equal(rec.LastSeen) {
			t.Errorf("record fields not updated: %+v", snaps[0])
		}
	})
	t.Run("empty snapshot is a change from a non-empty one, then stable", func(t *testing.T) {
		changed, err := s.RecordAgentReport(ctx, rec, &model.Snapshot{})
		if err != nil || !changed {
			t.Fatalf("first empty: changed = %v, err = %v", changed, err)
		}

		changed, err = s.RecordAgentReport(ctx, rec, &model.Snapshot{Containers: []model.Container{}})

		if err != nil || changed {
			t.Errorf("second empty: changed = %v, err = %v; want false", changed, err)
		}
		snaps, _ := s.AgentSnapshots(ctx)
		if !snaps[0].Complete || len(snaps[0].Containers) != 0 {
			t.Errorf("empty snapshot should be Complete with no containers: %+v", snaps[0])
		}
	})
	t.Run("agent row keeps CreatedAt and mirrors the record", func(t *testing.T) {
		agents, err := s.ListAgents(ctx)
		if err != nil || len(agents) != 1 {
			t.Fatalf("agents = %+v (%v)", agents, err)
		}
		a := agents[0]
		if a.ID != "edge1" || a.Name != "Edge" || a.DefaultTunnel != "tun" || a.PublicIP != "2.2.2.2" || a.PublicIPv6 != "::1" {
			t.Errorf("agent = %+v", a)
		}
		if !a.CreatedAt.Equal(seen1) || !a.UpdatedAt.Equal(clock.Now()) || !a.LastSeen.Equal(rec.LastSeen) {
			t.Errorf("times: created %v updated %v lastSeen %v", a.CreatedAt, a.UpdatedAt, a.LastSeen)
		}
	})
}

func TestRecordAgentReportFirstReportWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, newTestClock())

	changed, err := s.RecordAgentReport(ctx, model.AgentRecord{ID: "edge1"}, nil)

	if err != nil || changed {
		t.Fatalf("changed = %v, err = %v; want false, nil", changed, err)
	}
	snaps, _ := s.AgentSnapshots(ctx)
	if len(snaps) != 1 || snaps[0].Complete || snaps[0].Containers != nil || !snaps[0].TakenAt.IsZero() {
		t.Errorf("snapshot = %+v, want incomplete with zero TakenAt", snaps)
	}
}

func TestRecordAgentReportRejectsInvalidID(t *testing.T) {
	s := openTestStore(t, newTestClock())
	for _, id := range []string{"", "Upper", "-lead", "has space", "a/b"} {
		_, err := s.RecordAgentReport(context.Background(), model.AgentRecord{ID: id}, nil)
		if !errors.Is(err, model.ErrInvalid) {
			t.Errorf("id %q: error = %v, want ErrInvalid", id, err)
		}
	}
}

func TestAgentSnapshotsRoundTripAndOrder(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	s := openTestStore(t, clock)
	cs := containers("c1", "c2")
	cs[0].Networks = nil
	for _, id := range []string{"zeta", "alpha"} {
		rec := model.AgentRecord{ID: id, PublicIP: "9.9.9.9", LastSeen: clock.Now()}
		if _, err := s.RecordAgentReport(ctx, rec, &model.Snapshot{Containers: cs}); err != nil {
			t.Fatal(err)
		}
	}

	snaps, err := s.AgentSnapshots(ctx)

	if err != nil || len(snaps) != 2 || snaps[0].AgentID != "alpha" || snaps[1].AgentID != "zeta" {
		t.Fatalf("snapshots = %+v (%v)", snaps, err)
	}
	if !reflect.DeepEqual(snaps[0].Containers, cs) {
		t.Errorf("containers = %+v, want %+v", snaps[0].Containers, cs)
	}
	if !snaps[0].Complete || snaps[0].PublicIP != "9.9.9.9" || !snaps[0].TakenAt.Equal(clock.Now()) {
		t.Errorf("snapshot meta = %+v", snaps[0])
	}
	agents, _ := s.ListAgents(ctx)
	if len(agents) != 2 || agents[0].ID != "alpha" {
		t.Errorf("ListAgents order = %+v", agents)
	}
}

func TestRecordAgentReportDoesNotMutateInput(t *testing.T) {
	s := openTestStore(t, newTestClock())
	cs := containers("b", "a")
	cs[0].Labels = nil
	snap := &model.Snapshot{Containers: cs}

	if _, err := s.RecordAgentReport(context.Background(), model.AgentRecord{ID: "edge1"}, snap); err != nil {
		t.Fatal(err)
	}

	if cs[0].ID != "b" || cs[0].Labels != nil {
		t.Errorf("input mutated: %+v", cs[0])
	}
}

func TestInstanceID(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "id.db")
	s, err := Open(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}

	first, err := s.InstanceID(ctx)
	if err != nil || !model.ValidInstanceID(first) || len(first) != 8 {
		t.Fatalf("InstanceID = %q, %v", first, err)
	}
	again, _ := s.InstanceID(ctx)
	if again != first {
		t.Errorf("InstanceID not stable: %q vs %q", first, again)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, _ := reopened.InstanceID(ctx)
	if persisted != first {
		t.Errorf("InstanceID after reopen = %q, want %q", persisted, first)
	}

	t.Run("override", func(t *testing.T) {
		if err := reopened.SetInstanceID(ctx, "recovered01"); err != nil {
			t.Fatal(err)
		}
		got, _ := reopened.InstanceID(ctx)
		if got != "recovered01" {
			t.Errorf("InstanceID = %q after SetInstanceID", got)
		}
	})
	t.Run("invalid override", func(t *testing.T) {
		for _, bad := range []string{"", "abc", "UPPER12", "has-dash", "toolong0123456789012345678901234567890"} {
			if err := reopened.SetInstanceID(ctx, bad); !errors.Is(err, model.ErrInvalid) {
				t.Errorf("SetInstanceID(%q) = %v, want ErrInvalid", bad, err)
			}
		}
	})
}

func TestInstanceIDGeneratedWhenMetaRowMissing(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, newTestClock())
	mustExec(t, s.db, `DELETE FROM meta WHERE key = 'instance_id'`)

	id, err := s.InstanceID(ctx)

	if err != nil || !model.ValidInstanceID(id) {
		t.Fatalf("InstanceID = %q, %v", id, err)
	}
	again, _ := s.InstanceID(ctx)
	if again != id {
		t.Errorf("regenerated id not persisted: %q vs %q", id, again)
	}
}

func TestInstanceIDsDifferBetweenDatabases(t *testing.T) {
	ctx := context.Background()
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		s := openTestStore(t, newTestClock())
		id, err := s.InstanceID(ctx)
		if err != nil {
			t.Fatal(err)
		}
		seen[id] = true
	}
	if len(seen) < 5 {
		t.Errorf("instance ids collided: %v", seen)
	}
}

func TestOpen(t *testing.T) {
	ctx := context.Background()

	t.Run("creates the parent directory with 0700", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "nested", "data")
		s, err := Open(ctx, filepath.Join(dir, "x.db"), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		assertDirMode(t, dir, 0o700)
	})
	t.Run("in-memory", func(t *testing.T) {
		s, err := Open(ctx, ":memory:", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := s.UpsertResource(ctx, dnsResource("a.example.com", model.Owner{})); err != nil {
			t.Errorf("upsert on :memory: store: %v", err)
		}
	})
	t.Run("empty path is invalid", func(t *testing.T) {
		if _, err := Open(ctx, "", nil); !errors.Is(err, model.ErrInvalid) {
			t.Errorf("error = %v, want ErrInvalid", err)
		}
	})
	t.Run("parent is a file", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "file")
		mustWriteFile(t, parent)
		if _, err := Open(ctx, filepath.Join(parent, "x.db"), nil); err == nil {
			t.Error("Open succeeded with a file as parent directory")
		}
	})
	t.Run("path with URI metacharacters", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "we?ird#na%me.db")
		s, err := Open(ctx, p, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		assertFileExists(t, p)
	})
	t.Run("applies pragmas", func(t *testing.T) {
		s := openTestStore(t, newTestClock())
		want := map[string]string{"journal_mode": "wal", "foreign_keys": "1", "busy_timeout": "5000", "synchronous": "1"}
		for pragma, exp := range want {
			var got string
			if err := s.db.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil || got != exp {
				t.Errorf("PRAGMA %s = %q (%v), want %q", pragma, got, err, exp)
			}
		}
		if n := s.db.Stats().MaxOpenConnections; n != 1 {
			t.Errorf("MaxOpenConnections = %d, want 1 (single writer)", n)
		}
	})
	t.Run("migration failure closes and reports", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "newer.db")
		raw := openRawDB(t, p)
		mustExec(t, raw, `CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
		mustExec(t, raw, `INSERT INTO meta VALUES ('schema_version', '99')`)
		_ = raw.Close()

		_, err := Open(ctx, p, nil)

		if !errors.Is(err, ErrSchemaTooNew) {
			t.Errorf("error = %v, want ErrSchemaTooNew", err)
		}
	})
	t.Run("reopen keeps data", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "keep.db")
		s, _ := Open(ctx, p, nil)
		row, err := s.UpsertResource(ctx, dnsResource("a.example.com", model.Owner{}))
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()

		s2, err := Open(ctx, p, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer s2.Close()
		all, _ := s2.AllResources(ctx)
		if len(all) != 1 || all[0].ID != row.ID {
			t.Errorf("rows after reopen = %+v", all)
		}
	})
}

// TestTimesAreTimezoneIndependent stores and reads times under different
// process zones and clocks expressed in different zones.
func TestTimesAreTimezoneIndependent(t *testing.T) {
	instant := time.Date(2026, 11, 17, 4, 30, 15, 0, time.UTC)
	zones := []*time.Location{time.UTC, time.FixedZone("CST", 8*3600), time.FixedZone("PST", -8*3600), time.FixedZone("X", 13*3600+45*60)}
	for _, procZone := range zones {
		for _, clockZone := range zones {
			t.Run(procZone.String()+"/"+clockZone.String(), func(t *testing.T) {
				withLocalZone(t, procZone)
				ctx := context.Background()
				s, err := Open(ctx, filepath.Join(t.TempDir(), "tz.db"), func() time.Time { return instant.In(clockZone) })
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				r := dnsResource("tz.example.com", model.Owner{})
				r.OrphanedAt = instant.Add(-time.Hour).In(clockZone)
				r.SyncedAt = instant.Add(-time.Minute).In(procZone)

				got, err := s.UpsertResource(ctx, r)

				if err != nil {
					t.Fatal(err)
				}
				if !got.CreatedAt.Equal(instant) || !got.UpdatedAt.Equal(instant) ||
					!got.OrphanedAt.Equal(instant.Add(-time.Hour)) || !got.SyncedAt.Equal(instant.Add(-time.Minute)) {
					t.Errorf("times = %v %v %v %v", got.CreatedAt, got.UpdatedAt, got.OrphanedAt, got.SyncedAt)
				}
				if got.CreatedAt.Location() != time.UTC {
					t.Errorf("zone = %v, want UTC", got.CreatedAt.Location())
				}
				var raw int64
				if err := s.db.QueryRow(`SELECT created_at FROM resources`).Scan(&raw); err != nil || raw != instant.Unix() {
					t.Errorf("stored created_at = %d (%v), want %d", raw, err, instant.Unix())
				}
			})
		}
	}
}

func TestSubSecondTimesTruncate(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 17, 1, 2, 3, 999_000_000, time.UTC)
	s, err := Open(ctx, ":memory:", func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got, err := s.UpsertResource(ctx, dnsResource("a.example.com", model.Owner{}))

	if err != nil || got.CreatedAt.Nanosecond() != 0 || got.CreatedAt.Unix() != at.Unix() {
		t.Errorf("CreatedAt = %v (%v), want second precision", got.CreatedAt, err)
	}
}

func TestConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, newTestClock())
	const workers, perWorker = 8, 25

	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker*3)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				// Distinct rows per worker plus one hot row everybody upserts.
				if _, err := s.UpsertResource(ctx, dnsResource(fmt.Sprintf("w%d-%d.example.com", w, i), model.Owner{})); err != nil {
					errs <- err
				}
				hot := dnsResource("hot.example.com", model.Owner{ContainerName: fmt.Sprint(w)})
				if _, err := s.UpsertResource(ctx, hot); err != nil {
					errs <- err
				}
				rec := model.AgentRecord{ID: fmt.Sprintf("agent%d", w%3), LastSeen: time.Unix(int64(i), 0)}
				if _, err := s.RecordAgentReport(ctx, rec, &model.Snapshot{Containers: containers(fmt.Sprint(i))}); err != nil {
					errs <- err
				}
				if _, err := s.ListResources(ctx, ResourceQuery{Limit: 10}); err != nil {
					errs <- err
				}
				if _, err := s.CountResources(ctx); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent operation failed: %v", err)
	}
	all, err := s.AllResources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := workers*perWorker + 1; len(all) != want {
		t.Errorf("rows = %d, want %d (hot row must stay a single row)", len(all), want)
	}
	if agents, _ := s.ListAgents(ctx); len(agents) != 3 {
		t.Errorf("agents = %d, want 3", len(agents))
	}
}

func TestConcurrentInstanceIDGeneration(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, newTestClock())
	mustExec(t, s.db, `DELETE FROM meta WHERE key = 'instance_id'`)

	var wg sync.WaitGroup
	ids := make([]string, 16)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := s.InstanceID(ctx)
			if err != nil {
				t.Errorf("InstanceID: %v", err)
			}
			ids[i] = id
		}(i)
	}
	wg.Wait()

	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("callers saw different ids: %v", ids)
		}
	}
}

func TestOperationsFailAfterClose(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, newTestClock())
	row, err := s.UpsertResource(ctx, dnsResource("a.example.com", model.Owner{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	checks := map[string]error{
		"InstanceID":   func() error { _, err := s.InstanceID(ctx); return err }(),
		"SetInstance":  s.SetInstanceID(ctx, "abcd1234"),
		"AllResources": func() error { _, err := s.AllResources(ctx); return err }(),
		"List":         func() error { _, err := s.ListResources(ctx, ResourceQuery{Limit: 1}); return err }(),
		"Count":        func() error { _, err := s.CountResources(ctx); return err }(),
		"CountByAgent": func() error { _, err := s.CountResourcesByAgent(ctx); return err }(),
		"Upsert":       func() error { _, err := s.UpsertResource(ctx, dnsResource("b.example.com", model.Owner{})); return err }(),
		"Update":       s.UpdateResource(ctx, row),
		"Delete":       s.DeleteResource(ctx, row.ID),
		"Replace":      s.ReplaceRejected(ctx, nil),
		"ListAgents":   func() error { _, err := s.ListAgents(ctx); return err }(),
		"Report": func() error {
			_, err := s.RecordAgentReport(ctx, model.AgentRecord{ID: "edge1"}, nil)
			return err
		}(),
		"Snapshots": func() error { _, err := s.AgentSnapshots(ctx); return err }(),
	}
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s succeeded on a closed store", name)
		}
	}
}

func TestCancelledContextFails(t *testing.T) {
	s := openTestStore(t, newTestClock())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.UpsertResource(ctx, dnsResource("a.example.com", model.Owner{})); err == nil {
		t.Error("UpsertResource succeeded with a cancelled context")
	}
	if _, err := s.ListResources(ctx, ResourceQuery{Limit: 1}); err == nil {
		t.Error("ListResources succeeded with a cancelled context")
	}
	if all, _ := s.AllResources(context.Background()); len(all) != 0 {
		t.Errorf("cancelled upsert left %d rows", len(all))
	}
}

func TestCorruptStoredJSONSurfacesAsError(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, newTestClock())
	if _, err := s.UpsertResource(ctx, dnsResource("a.example.com", model.Owner{})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAgentReport(ctx, model.AgentRecord{ID: "edge1"}, &model.Snapshot{Containers: containers("a")}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		sql  string
		read func() error
	}{
		{"origin_json", `UPDATE resources SET origin_json = '{'`, func() error { _, err := s.AllResources(ctx); return err }},
		{"policy_ids", `UPDATE resources SET origin_json = '{}', policy_ids = 'nope'`, func() error { _, err := s.AllResources(ctx); return err }},
		{"snapshot_json", `UPDATE agents SET snapshot_json = '[1'`, func() error { _, err := s.AgentSnapshots(ctx); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mustExec(t, s.db, tt.sql)

			if err := tt.read(); err == nil {
				t.Error("corrupt JSON was silently accepted")
			}
		})
	}
}
