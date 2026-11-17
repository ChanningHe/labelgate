package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

func dnsResource(host string, owner model.Owner) model.Resource {
	return model.Resource{
		Kind: model.KindDNS, Key: model.DNSKey(host, model.DNSTypeA, ""), Hostname: host,
		Owner: owner, RecordType: model.DNSTypeA, Content: "1.2.3.4", Proxied: true, TTL: 1,
		Status: model.StatusActive, Cleanup: true,
	}
}

func tunnelResource(tunnelID, host, path string) model.Resource {
	return model.Resource{
		Kind: model.KindTunnel, Key: model.TunnelKey(tunnelID, host, path), Hostname: host,
		Owner:    model.Owner{ContainerName: "web", Service: "web"},
		TunnelID: tunnelID, Path: path, Service: "http://web:80",
		Status: model.StatusPending, Cleanup: true,
	}
}

// fullResource sets every persisted field to a distinct non-zero value.
func fullResource(now time.Time) model.Resource {
	return model.Resource{
		Kind: model.KindDNS, Key: "full.example.com|SRV|10 5 8080 t.example.com", Hostname: "full.example.com",
		Owner:      model.Owner{AgentID: "edge1", ContainerName: "cn", ContainerID: "cid", Service: "svc"},
		Credential: "cred", AccountID: "acct", ZoneID: "zone", CFID: "cf",
		RecordType: model.DNSTypeSRV, Content: "t.example.com", Proxied: true, TTL: 300,
		Priority: 10, Weight: 5, Port: 8080, Flags: 128, Tag: "issue", Comment: "user comment",
		TunnelID: "tid", Path: "/p", Service: "http://svc:1",
		Origin: model.OriginRequest{
			ConnectTimeout: 30 * time.Second, TLSTimeout: 10 * time.Second, TCPKeepAlive: 15 * time.Second,
			KeepAliveTimeout: 90 * time.Second, KeepAliveConnections: 100, NoTLSVerify: true,
			OriginServerName: "o.example.com", CAPool: "/ca.pem", HTTPHostHeader: "h.example.com",
			NoHappyEyeballs: true, DisableChunkedEncoding: true, ProxyType: "socks",
		},
		AppName: "app", PolicyName: "pol", Decision: "allow", PolicyIDs: []string{"p2", "p1"},
		Status: model.StatusError, Cleanup: true, LastError: "oops", Legacy: true,
		OrphanedAt: now.Add(-time.Hour), SyncedAt: now.Add(-time.Minute),
	}
}

func TestUpsertResource(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	s := openTestStore(t, clock)
	created := clock.Now()

	first, err := s.UpsertResource(ctx, dnsResource("a.example.com", model.Owner{}))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if first.ID == 0 || !first.CreatedAt.Equal(created) || !first.UpdatedAt.Equal(created) {
		t.Fatalf("insert result = %+v", first)
	}

	clock.Advance(time.Hour)
	changed := dnsResource("a.example.com", model.Owner{ContainerName: "other"})
	changed.ID = 9999 // caller-supplied IDs are ignored
	changed.Content = "9.9.9.9"
	changed.CreatedAt = clock.Now()
	second, err := s.UpsertResource(ctx, changed)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("ID = %d, want persisted id %d", second.ID, first.ID)
	}
	if !second.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want preserved %v", second.CreatedAt, created)
	}
	if !second.UpdatedAt.Equal(clock.Now()) || second.Content != "9.9.9.9" || second.Owner.ContainerName != "other" {
		t.Errorf("update result = %+v", second)
	}
	all, err := s.AllResources(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("AllResources = %d rows (%v), want 1", len(all), err)
	}
	if !reflect.DeepEqual(all[0], second) {
		t.Errorf("returned row differs from stored row:\n got %+v\nwant %+v", second, all[0])
	}
}

func TestUpsertDistinguishesIdentityFields(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, newTestClock())
	rows := []model.Resource{
		tunnelResource("tid-1", "api.example.com", "/v1"),
		tunnelResource("tid-1", "api.example.com", "/v2"), // same host, other path
		tunnelResource("tid-2", "api.example.com", "/v1"), // same host+path, other tunnel
		dnsResource("api.example.com", model.Owner{}),
	}

	ids := map[int64]bool{}
	for _, r := range rows {
		got, err := s.UpsertResource(ctx, r)
		if err != nil {
			t.Fatalf("upsert %s: %v", r.Key, err)
		}
		ids[got.ID] = true
	}

	if len(ids) != len(rows) {
		t.Errorf("distinct ids = %d, want %d (identity must include tunnel_id and path)", len(ids), len(rows))
	}
}

func TestUpsertRoundTripsEveryField(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	s := openTestStore(t, clock)
	in := fullResource(clock.Now())

	got, err := s.UpsertResource(ctx, in)
	if err != nil {
		t.Fatal(err)
	}

	want := in
	want.ID, want.CreatedAt, want.UpdatedAt = got.ID, clock.Now(), clock.Now()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	all, err := s.AllResources(ctx)
	if err != nil || len(all) != 1 || !reflect.DeepEqual(all[0], got) {
		t.Errorf("AllResources differs from Upsert result: %+v (%v)", all, err)
	}
}

func TestResourceValidation(t *testing.T) {
	base := dnsResource("a.example.com", model.Owner{})
	tests := []struct {
		name   string
		mutate func(r *model.Resource)
		// replaceIgnores: ReplaceRejected forces Status=error, so a bad status is not an input error.
		replaceIgnores bool
	}{
		{"unknown kind", func(r *model.Resource) { r.Kind = "mx" }, false},
		{"empty kind", func(r *model.Resource) { r.Kind = "" }, false},
		{"empty key", func(r *model.Resource) { r.Key = "" }, false},
		{"empty hostname", func(r *model.Resource) { r.Hostname = "" }, false},
		{"empty status", func(r *model.Resource) { r.Status = "" }, true},
		{"unknown status", func(r *model.Resource) { r.Status = "deleted" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTestStore(t, newTestClock())
			r := base
			tt.mutate(&r)

			_, errUp := s.UpsertResource(context.Background(), r)
			errUpd := s.UpdateResource(context.Background(), r)
			errRej := s.ReplaceRejected(context.Background(), []model.Resource{r})

			checks := map[string]error{"upsert": errUp, "update": errUpd}
			if !tt.replaceIgnores {
				checks["replace"] = errRej
			}
			for name, err := range checks {
				if !errors.Is(err, model.ErrInvalid) {
					t.Errorf("%s error = %v, want ErrInvalid", name, err)
				}
			}
		})
	}
}

func TestUpdateResource(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	s := openTestStore(t, clock)
	created := clock.Now()
	row, err := s.UpsertResource(ctx, dnsResource("a.example.com", model.Owner{}))
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.UpsertResource(ctx, dnsResource("b.example.com", model.Owner{}))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("overwrites all but CreatedAt and stamps UpdatedAt", func(t *testing.T) {
		clock.Advance(time.Minute)
		edit := fullResource(clock.Now())
		edit.ID, edit.Kind, edit.Key, edit.Hostname = row.ID, row.Kind, row.Key, row.Hostname
		edit.CreatedAt = clock.Now().Add(100 * time.Hour) // must be ignored

		if err := s.UpdateResource(ctx, edit); err != nil {
			t.Fatalf("UpdateResource: %v", err)
		}

		all, _ := s.AllResources(ctx)
		got := all[0]
		edit.CreatedAt, edit.UpdatedAt = created, clock.Now()
		if !reflect.DeepEqual(got, edit) {
			t.Errorf("stored row:\n got %+v\nwant %+v", got, edit)
		}
		if all[1].ID != other.ID || all[1].Content != "1.2.3.4" {
			t.Errorf("update touched another row: %+v", all[1])
		}
	})
	t.Run("missing id is ErrNotFound", func(t *testing.T) {
		r := dnsResource("a.example.com", model.Owner{})
		r.ID = 424242

		err := s.UpdateResource(ctx, r)

		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("error = %v, want ErrNotFound", err)
		}
	})
	t.Run("identity collision is ErrConflict", func(t *testing.T) {
		clash := dnsResource("b.example.com", model.Owner{})
		clash.ID = row.ID

		err := s.UpdateResource(ctx, clash)

		if !errors.Is(err, model.ErrConflict) {
			t.Errorf("error = %v, want ErrConflict", err)
		}
	})
}

func TestDeleteResource(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, newTestClock())
	row, err := s.UpsertResource(ctx, dnsResource("a.example.com", model.Owner{}))
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteResource(ctx, row.ID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	err = s.DeleteResource(ctx, row.ID)

	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("second delete error = %v, want ErrNotFound", err)
	}
	if all, _ := s.AllResources(ctx); len(all) != 0 {
		t.Errorf("rows left: %d", len(all))
	}
}

func rejected(host, container, svc string) model.Resource {
	r := dnsResource(host, model.Owner{ContainerName: container, Service: svc})
	r.Status = model.StatusPending // forced to error by the store
	r.LastError = "conflict: hostname claimed by someone else"
	return r
}

func TestReplaceRejected(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	s := openTestStore(t, clock)
	owner, err := s.UpsertResource(ctx, dnsResource("a.example.com", model.Owner{ContainerName: "first", Service: "s"}))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("rejected rows coexist with the owner row of the same key", func(t *testing.T) {
		err := s.ReplaceRejected(ctx, []model.Resource{
			rejected("a.example.com", "second", "s"),
			rejected("a.example.com", "third", "s"),
		})
		if err != nil {
			t.Fatalf("ReplaceRejected: %v", err)
		}

		all, _ := s.AllResources(ctx)
		if len(all) != 3 {
			t.Fatalf("rows = %d, want 3", len(all))
		}
		for _, r := range all[1:] {
			if !r.Rejected || r.Status != model.StatusError || r.LastError == "" {
				t.Errorf("rejected row = %+v", r)
			}
		}
		if all[0].ID != owner.ID || all[0].Rejected {
			t.Errorf("owner row disturbed: %+v", all[0])
		}
		byAgent, _ := s.CountResourcesByAgent(ctx)
		if byAgent[""] != 1 {
			t.Errorf("CountResourcesByAgent counts rejected rows: %v", byAgent)
		}
	})
	t.Run("replaces the whole previous set", func(t *testing.T) {
		if err := s.ReplaceRejected(ctx, []model.Resource{rejected("z.example.com", "x", "s")}); err != nil {
			t.Fatal(err)
		}

		all, _ := s.AllResources(ctx)
		if len(all) != 2 || all[1].Hostname != "z.example.com" {
			t.Errorf("rows = %+v", keysOf(all))
		}
	})
	t.Run("empty input clears rejected rows only", func(t *testing.T) {
		if err := s.ReplaceRejected(ctx, nil); err != nil {
			t.Fatal(err)
		}

		all, _ := s.AllResources(ctx)
		if len(all) != 1 || all[0].ID != owner.ID {
			t.Errorf("rows = %+v", keysOf(all))
		}
	})
	t.Run("duplicate claim in input is ErrInvalid and changes nothing", func(t *testing.T) {
		if err := s.ReplaceRejected(ctx, []model.Resource{rejected("keep.example.com", "k", "s")}); err != nil {
			t.Fatal(err)
		}

		err := s.ReplaceRejected(ctx, []model.Resource{
			rejected("dup.example.com", "x", "s"),
			rejected("dup.example.com", "x", "s"),
		})

		if !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("error = %v, want ErrInvalid", err)
		}
		all, _ := s.AllResources(ctx)
		if len(all) != 2 || all[1].Hostname != "keep.example.com" {
			t.Errorf("old set not preserved: %+v", keysOf(all))
		}
	})
	t.Run("same key with different owners is not a duplicate", func(t *testing.T) {
		err := s.ReplaceRejected(ctx, []model.Resource{
			rejected("dup.example.com", "x", "s"),
			rejected("dup.example.com", "y", "s"),
		})
		if err != nil {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("keeps a given CreatedAt, else uses now", func(t *testing.T) {
		kept := rejected("c.example.com", "x", "s")
		kept.CreatedAt = clock.Now().Add(-48 * time.Hour)
		fresh := rejected("d.example.com", "x", "s")

		if err := s.ReplaceRejected(ctx, []model.Resource{kept, fresh}); err != nil {
			t.Fatal(err)
		}

		all, _ := s.AllResources(ctx)
		if !all[1].CreatedAt.Equal(kept.CreatedAt) || !all[2].CreatedAt.Equal(clock.Now()) {
			t.Errorf("created = %v / %v", all[1].CreatedAt, all[2].CreatedAt)
		}
	})
}

func TestReplaceRejectedIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, newTestClock())
	if err := s.ReplaceRejected(ctx, []model.Resource{rejected("old.example.com", "x", "s")}); err != nil {
		t.Fatal(err)
	}
	// Fail the insert of the second new row, after the delete and first insert succeeded.
	mustExec(t, s.db, `CREATE TRIGGER boom BEFORE INSERT ON resources WHEN NEW.hostname = 'boom.example.com'
		BEGIN SELECT RAISE(ABORT, 'injected fault'); END`)

	err := s.ReplaceRejected(ctx, []model.Resource{
		rejected("new.example.com", "x", "s"),
		rejected("boom.example.com", "x", "s"),
	})

	if err == nil {
		t.Fatal("ReplaceRejected succeeded despite injected fault")
	}
	all, _ := s.AllResources(ctx)
	if len(all) != 1 || all[0].Hostname != "old.example.com" {
		t.Errorf("old rejected set not restored: %+v", keysOf(all))
	}
}

func seedList(t *testing.T, s *SQLite) {
	t.Helper()
	ctx := context.Background()
	rows := []model.Resource{
		dnsResource("c.example.com", model.Owner{}),
		dnsResource("a.example.com", model.Owner{AgentID: "edge1", ContainerName: "x", Service: "s"}),
		tunnelResource("tid", "b.example.com", "/x"),
		tunnelResource("tid", "b.example.com", "/y"),
		dnsResource("d.example.com", model.Owner{AgentID: "edge2", ContainerName: "x", Service: "s"}),
	}
	rows[0].Status = model.StatusError
	rows[4].Status = model.StatusOrphaned
	for _, r := range rows {
		if _, err := s.UpsertResource(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListResources(t *testing.T) {
	s := openTestStore(t, newTestClock())
	seedList(t, s)
	tests := []struct {
		name      string
		q         ResourceQuery
		wantHosts []string
		wantTotal int
	}{
		{"all ordered by hostname then id", ResourceQuery{Limit: 10},
			[]string{"a.example.com", "b.example.com", "b.example.com", "c.example.com", "d.example.com"}, 5},
		{"kind", ResourceQuery{Kind: model.KindTunnel, Limit: 10}, []string{"b.example.com", "b.example.com"}, 2},
		{"status", ResourceQuery{Status: model.StatusError, Limit: 10}, []string{"c.example.com"}, 1},
		{"kind and status", ResourceQuery{Kind: model.KindDNS, Status: model.StatusOrphaned, Limit: 10},
			[]string{"d.example.com"}, 1},
		{"hostname", ResourceQuery{Hostname: "b.example.com", Limit: 10}, []string{"b.example.com", "b.example.com"}, 2},
		{"agent id", ResourceQuery{AgentID: "edge1", HasAgentID: true, Limit: 10}, []string{"a.example.com"}, 1},
		{"local agent via HasAgentID and empty id", ResourceQuery{HasAgentID: true, Limit: 10},
			[]string{"b.example.com", "b.example.com", "c.example.com"}, 3},
		{"AgentID without HasAgentID does not filter", ResourceQuery{AgentID: "edge1", Limit: 10},
			[]string{"a.example.com", "b.example.com", "b.example.com", "c.example.com", "d.example.com"}, 5},
		{"no match", ResourceQuery{Hostname: "nope.example.com", Limit: 10}, []string{}, 0},
		{"page 1 keeps the true total", ResourceQuery{Limit: 2}, []string{"a.example.com", "b.example.com"}, 5},
		{"page 2", ResourceQuery{Limit: 2, Offset: 2}, []string{"b.example.com", "c.example.com"}, 5},
		{"last partial page", ResourceQuery{Limit: 2, Offset: 4}, []string{"d.example.com"}, 5},
		{"offset past the end keeps the total", ResourceQuery{Limit: 2, Offset: 50}, []string{}, 5},
		{"filter with pagination counts the filter", ResourceQuery{HasAgentID: true, Limit: 1, Offset: 1},
			[]string{"b.example.com"}, 3},
		{"max limit", ResourceQuery{Limit: MaxLimit}, nil, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := s.ListResources(context.Background(), tt.q)

			if err != nil {
				t.Fatalf("ListResources: %v", err)
			}
			if page.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", page.Total, tt.wantTotal)
			}
			if page.Resources == nil {
				t.Error("Resources is nil, want empty slice")
			}
			if tt.wantHosts == nil {
				return
			}
			got := make([]string, len(page.Resources))
			for i, r := range page.Resources {
				got[i] = r.Hostname
			}
			if !reflect.DeepEqual(got, tt.wantHosts) {
				t.Errorf("hosts = %v, want %v", got, tt.wantHosts)
			}
		})
	}
}

func TestListResourcesRejectsBadPaging(t *testing.T) {
	s := openTestStore(t, newTestClock())
	tests := []struct {
		name string
		q    ResourceQuery
	}{
		{"zero limit", ResourceQuery{}},
		{"negative limit", ResourceQuery{Limit: -1}},
		{"limit above max", ResourceQuery{Limit: MaxLimit + 1}},
		{"negative offset", ResourceQuery{Limit: 5, Offset: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.ListResources(context.Background(), tt.q)

			if !errors.Is(err, model.ErrInvalid) {
				t.Errorf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestListResourcesDoesNotInterpretFilterValues(t *testing.T) {
	s := openTestStore(t, newTestClock())
	seedList(t, s)

	page, err := s.ListResources(context.Background(), ResourceQuery{
		Hostname: "' OR '1'='1", AgentID: "x' --", HasAgentID: true, Limit: 10,
	})

	if err != nil || page.Total != 0 {
		t.Errorf("Total = %d, err = %v; filter values must be bound, not interpreted", page.Total, err)
	}
}

func TestCountResources(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t, newTestClock())
	empty, err := s.CountResources(ctx)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty counts = %v (%v)", empty, err)
	}
	seedList(t, s)
	if err := s.ReplaceRejected(ctx, []model.Resource{rejected("r.example.com", "x", "s")}); err != nil {
		t.Fatal(err)
	}

	got, err := s.CountResources(ctx)
	if err != nil {
		t.Fatal(err)
	}

	want := Counts{
		model.KindDNS: {
			model.StatusActive: 1, model.StatusError: 2, // c.example.com + rejected row
			model.StatusOrphaned: 1,
		},
		model.KindTunnel: {model.StatusPending: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("counts = %v, want %v", got, want)
	}
	byAgent, err := s.CountResourcesByAgent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(byAgent, map[string]int{"": 3, "edge1": 1, "edge2": 1}) {
		t.Errorf("by agent = %v", byAgent)
	}
}
