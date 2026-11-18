package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/reconcile"
	"github.com/channinghe/labelgate/internal/store"
)

const testToken = "s3cret-token"

var (
	testNow     = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	testStarted = testNow.Add(-(3*24*time.Hour + 5*time.Hour + 20*time.Minute))
)

// fakeStore is a Store test double that records calls.
type fakeStore struct {
	mu            sync.Mutex
	page          store.Page
	counts        store.Counts
	byAgent       map[string]int
	agents        []model.AgentRecord
	err           error
	queries       []store.ResourceQuery
	listAgentsN   int
	countByAgentN int
}

func (f *fakeStore) ListResources(_ context.Context, q store.ResourceQuery) (store.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	return f.page, f.err
}

func (f *fakeStore) CountResources(context.Context) (store.Counts, error) { return f.counts, f.err }

func (f *fakeStore) CountResourcesByAgent(context.Context) (map[string]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.countByAgentN++
	return f.byAgent, f.err
}

func (f *fakeStore) ListAgents(context.Context) ([]model.AgentRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listAgentsN++
	return f.agents, f.err
}

type fakeSync struct{ st reconcile.SyncStatus }

func (f fakeSync) Status() reconcile.SyncStatus { return f.st }

type fakePresence map[string]bool

func (p fakePresence) Connected(id string) bool { return p[id] }

type fakeCF struct{ h cloudflare.Health }

func (f fakeCF) Health() cloudflare.Health { return f.h }

type fixture struct {
	store *fakeStore
	deps  Deps
	srv   *Server
}

var testAssets = fstest.MapFS{
	"index.html":                {Data: []byte("<!doctype html><html><head><title>t</title></head><body><div id=root></div></body></html>")},
	"favicon.svg":               {Data: []byte("<svg/>")},
	"assets/index-abc123.js":    {Data: []byte("console.log(1)")},
	"assets/inter-abc123.woff2": {Data: []byte("woff2")},
}

func newFixture(t *testing.T, mutate func(*Deps)) *fixture {
	t.Helper()
	st := &fakeStore{counts: store.Counts{}}
	d := Deps{
		Config:      config.APIConfig{Enabled: true, Address: "127.0.0.1:0", BasePath: "/api", Token: testToken},
		Store:       st,
		Sync:        fakeSync{reconcile.SyncStatus{State: reconcile.SyncSuccess, LastSync: testNow}},
		Agents:      fakePresence{},
		Cloudflare:  fakeCF{cloudflare.Health{Reachable: true, LastCheck: testNow}},
		Version:     "v0.3.0",
		LabelPrefix: "labelgate",
		StartedAt:   testStarted,
		AgentGrace:  24 * time.Hour,
		Now:         func() time.Time { return testNow },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if mutate != nil {
		mutate(&d)
	}
	srv, err := newServer(d, testAssets)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	return &fixture{store: st, deps: d, srv: srv}
}

func (f *fixture) do(t *testing.T, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (f *fixture) get(t *testing.T, target string) *httptest.ResponseRecorder {
	return f.do(t, http.MethodGet, target, map[string]string{"Authorization": "Bearer " + testToken})
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Deps)
		wantErr string
	}{
		{"empty token", func(d *Deps) { d.Config.Token = "" }, ErrNoToken.Error()},
		{"base path without slash", func(d *Deps) { d.Config.BasePath = "api" }, "must start with /"},
		{"base path root", func(d *Deps) { d.Config.BasePath = "/" }, "must start with /"},
		{"base path trailing slash", func(d *Deps) { d.Config.BasePath = "/api/" }, "must start with /"},
		{"base path is dashboard", func(d *Deps) { d.Config.BasePath = "/dashboard" }, "collides"},
		{"base path under dashboard", func(d *Deps) { d.Config.BasePath = "/dashboard/api" }, "collides"},
		{"base path with query", func(d *Deps) { d.Config.BasePath = "/a?b" }, "invalid characters"},
		{"missing store", func(d *Deps) { d.Store = nil }, "required"},
		{"missing sync", func(d *Deps) { d.Sync = nil }, "required"},
		{"missing cloudflare", func(d *Deps) { d.Cloudflare = nil }, "required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newFixture(t, nil).deps
			tt.mutate(&d)
			_, err := newServer(d, testAssets)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestNewDefaultsAndPublicConstructor(t *testing.T) {
	d := newFixture(t, nil).deps
	d.Now, d.Log = nil, nil
	s, err := New(d)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.deps.Now == nil || s.deps.Log == nil {
		t.Fatal("defaults not applied")
	}
}

func TestAuth(t *testing.T) {
	f := newFixture(t, nil)
	tests := []struct {
		name   string
		path   string
		header string
		want   int
	}{
		{"health is open", "/api/health", "", 200},
		{"overview without token", "/api/overview", "", 401},
		{"overview wrong token", "/api/overview", "Bearer nope", 401},
		{"overview prefix of token", "/api/overview", "Bearer " + testToken[:4], 401},
		{"overview token with suffix", "/api/overview", "Bearer " + testToken + "x", 401},
		{"overview basic scheme", "/api/overview", "Basic " + testToken, 401},
		{"overview raw token", "/api/overview", testToken, 401},
		{"overview ok", "/api/overview", "Bearer " + testToken, 200},
		{"scheme is case-insensitive", "/api/overview", "bearer " + testToken, 200},
		{"version without token", "/api/version", "", 401},
		{"agents without token", "/api/agents", "", 401},
		{"dns without token", "/api/resources/dns", "", 401},
		{"tunnels without token", "/api/resources/tunnels", "", 401},
		{"access without token", "/api/resources/access", "", 401},
		{"unknown path needs token too", "/api/nope", "", 401},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr := map[string]string{}
			if tt.header != "" {
				hdr["Authorization"] = tt.header
			}
			rec := f.do(t, http.MethodGet, tt.path, hdr)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.want, rec.Body)
			}
			if tt.want == 401 {
				if rec.Header().Get("WWW-Authenticate") == "" {
					t.Error("missing WWW-Authenticate")
				}
				if got := decode[ErrorDTO](t, rec).Error; got != "unauthorized" {
					t.Errorf("error = %q", got)
				}
			}
		})
	}
}

func TestAPIHeadersAndMethods(t *testing.T) {
	f := newFixture(t, nil)

	rec := f.get(t, "/api/overview")
	for k, want := range map[string]string{
		"Content-Type":           "application/json",
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	if rec := f.do(t, http.MethodGet, "/api/health", nil); rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("health must be no-store")
	}
	if rec := f.do(t, http.MethodHead, "/api/health", nil); rec.Code != 200 {
		t.Errorf("HEAD health = %d", rec.Code)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := f.do(t, m, "/api/health", map[string]string{"Authorization": "Bearer " + testToken})
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") == "" {
			t.Errorf("%s: status=%d allow=%q", m, rec.Code, rec.Header().Get("Allow"))
		}
	}
	if rec := f.get(t, "/api/nope"); rec.Code != 404 {
		t.Errorf("unknown api path = %d", rec.Code)
	}
	if rec := f.get(t, "/api/"); rec.Code != 404 {
		t.Errorf("api root = %d", rec.Code)
	}
}

func TestHealthAndVersion(t *testing.T) {
	f := newFixture(t, nil)
	if got := decode[HealthDTO](t, f.do(t, "GET", "/api/health", nil)); got.Status != "ok" {
		t.Errorf("health = %+v", got)
	}
	v := decode[VersionDTO](t, f.get(t, "/api/version"))
	if v.Version != "v0.3.0" || !strings.HasPrefix(v.GoVersion, "go") {
		t.Errorf("version = %+v", v)
	}
}

func TestOverviewMapping(t *testing.T) {
	f := newFixture(t, func(d *Deps) {
		d.Agents = fakePresence{"a1": true}
	})
	f.store.counts = store.Counts{
		model.KindDNS: {
			model.StatusActive: 5, model.StatusError: 1, model.StatusOrphaned: 2,
			model.StatusPendingCleanup: 3, model.StatusPending: 4,
		},
		model.KindTunnel: {model.StatusActive: 2},
	}
	f.store.agents = []model.AgentRecord{{ID: "a1"}, {ID: "a2"}, {ID: "a3"}}

	o := decode[OverviewDTO](t, f.get(t, "/api/overview"))

	if got, want := o.Resources.DNS, (ResourceCountsDTO{Total: 15, Active: 5, Orphaned: 5, Error: 1}); got != want {
		t.Errorf("dns = %+v, want %+v", got, want)
	}
	if got, want := o.Resources.TunnelIngress, (ResourceCountsDTO{Total: 2, Active: 2}); got != want {
		t.Errorf("tunnel = %+v, want %+v", got, want)
	}
	if o.Resources.AccessApp != (ResourceCountsDTO{}) {
		t.Errorf("access = %+v", o.Resources.AccessApp)
	}
	if o.Agents != (OverviewAgentsDTO{Total: 3, Connected: 1, Disconnected: 2}) {
		t.Errorf("agents = %+v", o.Agents)
	}
	if o.Sync.Status != "success" || o.Sync.Error != "" || !o.Sync.LastSync.Equal(testNow) {
		t.Errorf("sync = %+v", o.Sync)
	}
	if !o.Cloudflare.Reachable || !o.Cloudflare.LastCheck.Equal(testNow) {
		t.Errorf("cloudflare = %+v", o.Cloudflare)
	}
	if o.Version != "v0.3.0" || o.Uptime != "3d 5h 20m" || !o.StartedAt.Equal(testStarted) || o.LabelPrefix != "labelgate" {
		t.Errorf("meta = %+v", o)
	}
}

func TestOverviewSyncStates(t *testing.T) {
	tests := []struct {
		name       string
		st         reconcile.SyncStatus
		wantStatus string
		wantErr    string
	}{
		{"pending", reconcile.SyncStatus{State: reconcile.SyncPending}, "pending", "waiting for first sync"},
		{"success drops stale error text", reconcile.SyncStatus{State: reconcile.SyncSuccess, Error: "old"}, "success", ""},
		{"error keeps message", reconcile.SyncStatus{State: reconcile.SyncError, Error: "2 resources failed"}, "error", "2 resources failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, func(d *Deps) { d.Sync = fakeSync{tt.st} })
			o := decode[OverviewDTO](t, f.get(t, "/api/overview"))
			if o.Sync.Status != tt.wantStatus || o.Sync.Error != tt.wantErr {
				t.Fatalf("sync = %+v", o.Sync)
			}
		})
	}
}

func TestOverviewSyncProblems(t *testing.T) {
	st := reconcile.SyncStatus{State: reconcile.SyncError, Error: "3 problems", ProblemCount: 3, Problems: []reconcile.Problem{
		{Subject: "local container bad label labelgate.dns.web.hostname", Text: "local container bad: label labelgate.dns.web.hostname: is required"},
		{Subject: "local container bad label labelgate.dns.web.type", Text: "local container bad: label labelgate.dns.web.type: unknown type"},
	}}
	f := newFixture(t, func(d *Deps) { d.Sync = fakeSync{st} })
	body := f.get(t, "/api/overview").Body.String()
	for _, want := range []string{
		`"problems":[{"subject":"local container bad label labelgate.dns.web.hostname","text":"local container bad: label labelgate.dns.web.hostname: is required"}`,
		`"problems_total":3`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %s:\n%s", want, body)
		}
	}
}

func TestOverviewSyncProblemsEmptyOnSuccess(t *testing.T) {
	st := reconcile.SyncStatus{State: reconcile.SyncSuccess, ProblemCount: 2, Problems: []reconcile.Problem{{Subject: "s", Text: "stale"}}}
	f := newFixture(t, func(d *Deps) { d.Sync = fakeSync{st} })
	body := f.get(t, "/api/overview").Body.String()
	if !strings.Contains(body, `"problems":[]`) || !strings.Contains(body, `"problems_total":0`) || strings.Contains(body, "stale") {
		t.Fatalf("overview: %s", body)
	}
}

func TestOverviewNilAgentPresence(t *testing.T) {
	f := newFixture(t, func(d *Deps) { d.Agents = nil })
	f.store.agents = []model.AgentRecord{{ID: "a1", LastSeen: testNow}}
	o := decode[OverviewDTO](t, f.get(t, "/api/overview"))
	if o.Agents.Connected != 0 || o.Agents.Total != 1 {
		t.Fatalf("agents = %+v", o.Agents)
	}
}

func TestStorageErrorsAreGeneric500(t *testing.T) {
	for _, path := range []string{
		"/api/overview", "/api/agents",
		"/api/resources/dns", "/api/resources/tunnels", "/api/resources/access",
	} {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t, nil)
			f.store.err = errors.New("sqlite: database is locked at /var/lib/secret.db")
			rec := f.get(t, path)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d", rec.Code)
			}
			body := rec.Body.String()
			if strings.Contains(body, "sqlite") || strings.Contains(body, "secret") {
				t.Fatalf("leaked detail: %s", body)
			}
			if got := decode[ErrorDTO](t, rec).Error; got != "internal error" {
				t.Fatalf("error = %q", got)
			}
		})
	}
}

// failAfterStore fails ListAgents only, to exercise the second call in overview/agents.
type failSecondStore struct {
	*fakeStore
	failAgents, failByAgent bool
}

func (f failSecondStore) ListAgents(ctx context.Context) ([]model.AgentRecord, error) {
	if f.failAgents {
		return nil, errors.New("boom")
	}
	return f.fakeStore.ListAgents(ctx)
}

func (f failSecondStore) CountResourcesByAgent(ctx context.Context) (map[string]int, error) {
	if f.failByAgent {
		return nil, errors.New("boom")
	}
	return f.fakeStore.CountResourcesByAgent(ctx)
}

func TestSecondaryStorageErrors(t *testing.T) {
	tests := []struct {
		name string
		st   failSecondStore
		path string
	}{
		{"overview ListAgents", failSecondStore{failAgents: true}, "/api/overview"},
		{"agents CountResourcesByAgent", failSecondStore{failByAgent: true}, "/api/agents"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.st.fakeStore = &fakeStore{counts: store.Counts{}}
			f := newFixture(t, func(d *Deps) { d.Store = tt.st })
			if rec := f.get(t, tt.path); rec.Code != 500 {
				t.Fatalf("status = %d", rec.Code)
			}
		})
	}
}

func TestResourceListQueryParams(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		want    store.ResourceQuery
		wantErr string
	}{
		{"defaults", "", store.ResourceQuery{Kind: model.KindDNS, Limit: 500}, ""},
		{"limit max", "?limit=1000", store.ResourceQuery{Kind: model.KindDNS, Limit: 1000}, ""},
		{"limit min", "?limit=1", store.ResourceQuery{Kind: model.KindDNS, Limit: 1}, ""},
		{"limit and offset", "?limit=10&offset=20", store.ResourceQuery{Kind: model.KindDNS, Limit: 10, Offset: 20}, ""},
		{"offset without limit uses default limit", "?offset=5", store.ResourceQuery{Kind: model.KindDNS, Limit: 500, Offset: 5}, ""},
		{"status filter", "?status=error", store.ResourceQuery{Kind: model.KindDNS, Limit: 500, Status: model.StatusError}, ""},
		{"pending status", "?status=pending", store.ResourceQuery{Kind: model.KindDNS, Limit: 500, Status: model.StatusPending}, ""},
		{"agent filter", "?agent_id=nas-1", store.ResourceQuery{Kind: model.KindDNS, Limit: 500, AgentID: "nas-1", HasAgentID: true}, ""},
		{"empty agent_id selects local", "?agent_id=", store.ResourceQuery{Kind: model.KindDNS, Limit: 500, HasAgentID: true}, ""},
		{"hostname normalized", "?hostname=%20App.Example.COM.", store.ResourceQuery{Kind: model.KindDNS, Limit: 500, Hostname: "app.example.com"}, ""},
		{"limit zero", "?limit=0", store.ResourceQuery{}, "invalid limit"},
		{"limit negative", "?limit=-1", store.ResourceQuery{}, "invalid limit"},
		{"limit too large", "?limit=1001", store.ResourceQuery{}, "invalid limit"},
		{"limit not a number", "?limit=abc", store.ResourceQuery{}, "invalid limit"},
		{"limit empty", "?limit=", store.ResourceQuery{}, "invalid limit"},
		{"offset negative", "?offset=-1", store.ResourceQuery{}, "invalid offset"},
		{"offset not a number", "?offset=x", store.ResourceQuery{}, "invalid offset"},
		{"unknown status", "?status=deleted", store.ResourceQuery{}, "invalid status"},
		{"bad agent id", "?agent_id=Bad%20ID", store.ResourceQuery{}, "invalid agent_id"},
		{"hostname too long", "?hostname=" + strings.Repeat("a", 254), store.ResourceQuery{}, "invalid hostname"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, nil)
			rec := f.get(t, "/api/resources/dns"+tt.query)
			if tt.wantErr != "" {
				if rec.Code != 400 || decode[ErrorDTO](t, rec).Error != tt.wantErr {
					t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
				}
				if len(f.store.queries) != 0 {
					t.Fatal("store must not be queried on bad input")
				}
				return
			}
			if rec.Code != 200 {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
			if got := f.store.queries[0]; got != tt.want {
				t.Fatalf("query = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResourceKindRouting(t *testing.T) {
	for path, kind := range map[string]model.Kind{
		"/api/resources/dns":     model.KindDNS,
		"/api/resources/tunnels": model.KindTunnel,
		"/api/resources/access":  model.KindAccess,
	} {
		f := newFixture(t, nil)
		f.get(t, path)
		if got := f.store.queries[0].Kind; got != kind {
			t.Errorf("%s -> kind %q, want %q", path, got, kind)
		}
	}
}

func TestResourceListTotalAndNeverNull(t *testing.T) {
	f := newFixture(t, nil)
	f.store.page = store.Page{Total: 1234}
	rec := f.get(t, "/api/resources/dns?limit=1")
	if !strings.Contains(rec.Body.String(), `"resources":[]`) {
		t.Fatalf("resources must be [] not null: %s", rec.Body)
	}
	if got := decode[ResourceListDTO](t, rec); got.Total != 1234 || len(got.Resources) != 0 {
		t.Fatalf("list = %+v", got)
	}
}

func TestResourceMapping(t *testing.T) {
	created := testNow.Add(-time.Hour)
	f := newFixture(t, nil)
	f.store.page = store.Page{Total: 3, Resources: []model.Resource{
		{
			ID: 7, Kind: model.KindDNS, Hostname: "app.example.com", ZoneID: "z1", CFID: "rec1",
			RecordType: model.DNSType("A"), Content: "203.0.113.5", Proxied: true, TTL: 1,
			Owner:      model.Owner{AgentID: "nas", ContainerName: "web", ContainerID: "abc", Service: "web"},
			Credential: "default", AccountID: "acct", Status: model.StatusActive, Cleanup: true,
			CreatedAt: created, UpdatedAt: testNow,
		},
		{
			ID: 8, Kind: model.KindTunnel, Hostname: "t.example.com", TunnelID: "tid", Service: "http://web:80", Path: "/api",
			Content: "ignored", Owner: model.Owner{Service: "web"}, Status: model.StatusError, LastError: "boom",
		},
		{
			ID: 9, Kind: model.KindAccess, Hostname: "a.example.com", CFID: "app1", AppName: "My App",
			PolicyName: "office", Decision: "allow", Owner: model.Owner{Service: "a"}, Status: model.StatusPending,
		},
	}}
	got := decode[ResourceListDTO](t, f.get(t, "/api/resources/dns"))

	dns, tun, acc := got.Resources[0], got.Resources[1], got.Resources[2]
	if dns.ID != "7" || dns.ResourceType != "dns" || dns.CFID != "rec1" || dns.ZoneID != "z1" ||
		dns.RecordType != "A" || dns.Content != "203.0.113.5" || !dns.Proxied || dns.TTL != 1 ||
		dns.ContainerID != "abc" || dns.ContainerName != "web" || dns.ServiceName != "web" || dns.AgentID != "nas" ||
		dns.Credential != "default" || dns.AccountID != "acct" || dns.Status != "active" || !dns.CleanupEnabled ||
		!dns.CreatedAt.Equal(created) || !dns.UpdatedAt.Equal(testNow) || dns.DeletedAt != nil {
		t.Errorf("dns = %+v", dns)
	}
	if tun.ResourceType != "tunnel_ingress" || tun.Service != "http://web:80" || tun.Path != "/api" ||
		tun.TunnelID != "tid" || tun.Content != "" || tun.LastError != "boom" || tun.Status != "error" {
		t.Errorf("tunnel = %+v", tun)
	}
	if acc.ResourceType != "access_app" || acc.AccessAppID != "app1" || acc.CFID != "app1" || acc.AccessAppName != "My App" ||
		acc.AccessPolicyName != "office" || acc.AccessDecision != "allow" || acc.Status != "pending" {
		t.Errorf("access = %+v", acc)
	}
}

func TestAgentsStatusMappingAndSingleQueries(t *testing.T) {
	seen := testNow.Add(-time.Hour)
	f := newFixture(t, func(d *Deps) {
		d.Agents = fakePresence{"live": true}
		d.AgentGrace = 24 * time.Hour
	})
	f.store.agents = []model.AgentRecord{
		{ID: "live", Name: "Live", LastSeen: seen, PublicIP: "203.0.113.1", DefaultTunnel: "t1", CreatedAt: testNow},
		{ID: "held", LastSeen: seen, PublicIPv6: "2001:db8::1"},
		{ID: "stale", LastSeen: testNow.Add(-25 * time.Hour)},
		{ID: "never"},
		{ID: "edge", LastSeen: testNow.Add(-24 * time.Hour)},
	}
	f.store.byAgent = map[string]int{"live": 4, "held": 1}

	got := decode[AgentListDTO](t, f.get(t, "/api/agents"))

	if got.Total != 5 || len(got.Agents) != 5 {
		t.Fatalf("list = %+v", got)
	}
	want := map[string]struct {
		status    string
		connected bool
		count     int
	}{
		"live": {"active", true, 4}, "held": {"disconnected", false, 1}, "stale": {"removed", false, 0},
		"never": {"removed", false, 0}, "edge": {"disconnected", false, 0},
	}
	for _, a := range got.Agents {
		w := want[a.ID]
		if a.Status != w.status || a.Connected != w.connected || a.ResourceCount != w.count {
			t.Errorf("agent %s = %+v, want %+v", a.ID, a, w)
		}
	}
	live, held, never := got.Agents[0], got.Agents[1], got.Agents[3]
	if live.Name != "Live" || live.PublicIP != "203.0.113.1" || live.DefaultTunnel != "t1" || live.LastSeen == nil || !live.LastSeen.Equal(seen) {
		t.Errorf("live = %+v", live)
	}
	if held.PublicIP != "2001:db8::1" {
		t.Errorf("held public ip = %q", held.PublicIP)
	}
	if never.LastSeen != nil {
		t.Errorf("never.LastSeen = %v", never.LastSeen)
	}
	if f.store.listAgentsN != 1 || f.store.countByAgentN != 1 {
		t.Fatalf("queries: ListAgents=%d CountResourcesByAgent=%d, want 1/1 (no N+1)", f.store.listAgentsN, f.store.countByAgentN)
	}
}

func TestAgentsEmptyIsNotNull(t *testing.T) {
	f := newFixture(t, nil)
	rec := f.get(t, "/api/agents")
	if !strings.Contains(rec.Body.String(), `"agents":[]`) {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{-time.Minute, "0m"},
		{30 * time.Second, "0m"},
		{20 * time.Minute, "20m"},
		{5*time.Hour + 20*time.Minute, "5h 20m"},
		{3*24*time.Hour + 5*time.Hour + 20*time.Minute, "3d 5h 20m"},
		{24 * time.Hour, "1d 0h 0m"},
	}
	for _, tt := range tests {
		if got := formatUptime(tt.d); got != tt.want {
			t.Errorf("formatUptime(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestCustomBasePath(t *testing.T) {
	f := newFixture(t, func(d *Deps) { d.Config.BasePath = "/lg/v1" })

	if rec := f.get(t, "/lg/v1/overview"); rec.Code != 200 {
		t.Errorf("custom base overview = %d", rec.Code)
	}
	if rec := f.do(t, "GET", "/lg/v1/health", nil); rec.Code != 200 {
		t.Errorf("custom base health = %d", rec.Code)
	}
	if rec := f.get(t, "/api/overview"); rec.Code != 404 {
		t.Errorf("old base path must 404, got %d", rec.Code)
	}
	if rec := f.get(t, "/lg/v1x/overview"); rec.Code != 404 {
		t.Errorf("sibling prefix must 404, got %d", rec.Code)
	}
	idx := f.do(t, "GET", "/dashboard/", nil).Body.String()
	if !strings.Contains(idx, `<meta name="labelgate-api-base" content="/lg/v1" />`) {
		t.Errorf("index must advertise custom base: %s", idx)
	}
}

func TestRootRedirects(t *testing.T) {
	f := newFixture(t, nil)
	for _, p := range []string{"/", "/dashboard"} {
		rec := f.do(t, "GET", p, nil)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/dashboard/" {
			t.Errorf("%s -> %d %q", p, rec.Code, rec.Header().Get("Location"))
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s redirect lacks CSP", p)
		}
	}
	rec := f.do(t, "GET", "/elsewhere", nil)
	if rec.Code != 404 || decode[ErrorDTO](t, rec).Error != "not found" {
		t.Errorf("unknown path = %d %s", rec.Code, rec.Body)
	}
}

func TestDashboardSecurityHeaders(t *testing.T) {
	f := newFixture(t, nil)
	for _, p := range []string{"/dashboard/", "/dashboard/resources/dns", "/dashboard/assets/index-abc123.js", "/dashboard/missing.js"} {
		rec := f.do(t, "GET", p, nil)
		csp := rec.Header().Get("Content-Security-Policy")
		for _, frag := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'none'", "img-src 'self' data:"} {
			if !strings.Contains(csp, frag) {
				t.Errorf("%s: CSP %q lacks %q", p, csp, frag)
			}
		}
		for k, want := range map[string]string{
			"X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff",
		} {
			if got := rec.Header().Get(k); got != want {
				t.Errorf("%s: %s = %q", p, k, got)
			}
		}
	}
}

func TestDashboardServing(t *testing.T) {
	f := newFixture(t, nil)
	tests := []struct {
		name      string
		method    string
		path      string
		wantCode  int
		wantInfo  string // substring expected in body
		wantCache string
		wantType  string
	}{
		{"index", "GET", "/dashboard/", 200, `name="labelgate-api-base" content="/api"`, "no-cache", "text/html; charset=utf-8"},
		{"explicit index.html", "GET", "/dashboard/index.html", 200, "labelgate-api-base", "no-cache", "text/html; charset=utf-8"},
		{"spa route", "GET", "/dashboard/resources/dns", 200, "labelgate-api-base", "no-cache", "text/html; charset=utf-8"},
		{"spa deep route", "GET", "/dashboard/agents/some-id", 200, "labelgate-api-base", "no-cache", ""},
		{"hashed js", "GET", "/dashboard/assets/index-abc123.js", 200, "console.log(1)", "public, max-age=31536000, immutable", ""},
		{"woff2 type", "GET", "/dashboard/assets/inter-abc123.woff2", 200, "woff2", "public, max-age=31536000, immutable", "font/woff2"},
		{"favicon", "GET", "/dashboard/favicon.svg", 200, "<svg/>", "no-cache", "image/svg+xml"},
		{"missing hashed asset is 404", "GET", "/dashboard/assets/index-old.js", 404, "not found", "", ""},
		{"asset directory itself is a spa route", "GET", "/dashboard/assets/", 200, "labelgate-api-base", "", ""},
		{"missing file with extension is 404", "GET", "/dashboard/robots.txt", 404, "not found", "", ""},
		{"traversal is cleaned into an spa route", "GET", "/dashboard/../../etc/passwd", 200, "labelgate-api-base", "", ""},
		{"head index", "HEAD", "/dashboard/", 200, "", "no-cache", "text/html; charset=utf-8"},
		{"post rejected", "POST", "/dashboard/", 405, "method not allowed", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.do(t, tt.method, tt.path, nil)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.wantCode, rec.Body)
			}
			if tt.wantInfo != "" && !strings.Contains(rec.Body.String(), tt.wantInfo) {
				t.Errorf("body %q lacks %q", rec.Body, tt.wantInfo)
			}
			if tt.wantCache != "" && rec.Header().Get("Cache-Control") != tt.wantCache {
				t.Errorf("Cache-Control = %q, want %q", rec.Header().Get("Cache-Control"), tt.wantCache)
			}
			if tt.wantType != "" && rec.Header().Get("Content-Type") != tt.wantType {
				t.Errorf("Content-Type = %q, want %q", rec.Header().Get("Content-Type"), tt.wantType)
			}
		})
	}
}

func TestDashboardNeedsNoAuthButAPIDoes(t *testing.T) {
	f := newFixture(t, nil)
	if rec := f.do(t, "GET", "/dashboard/", nil); rec.Code != 200 {
		t.Fatalf("dashboard = %d", rec.Code)
	}
	if rec := f.do(t, "GET", "/api/overview", nil); rec.Code != 401 {
		t.Fatalf("api = %d", rec.Code)
	}
}

func TestDashboardWithoutBuild(t *testing.T) {
	var empty fs.FS = fstest.MapFS{}
	f := newFixture(t, nil)
	srv, err := newServer(f.deps, empty)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"GET", "HEAD"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(m, "/dashboard/", nil))
		if rec.Code != 404 {
			t.Errorf("%s unbuilt dashboard = %d", m, rec.Code)
		}
	}
}

func TestInjectAPIBase(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		base string
		want string
	}{
		{"before closing head", "<html><head></head></html>", "/api", `<html><head><meta name="labelgate-api-base" content="/api" />` + "\n" + `</head></html>`},
		{"uppercase head", "<HTML><HEAD></HEAD></HTML>", "/api", `<HTML><HEAD><meta name="labelgate-api-base" content="/api" />` + "\n" + `</HEAD></HTML>`},
		{"no head prepends", "<html></html>", "/api", `<meta name="labelgate-api-base" content="/api" />` + "\n" + `<html></html>`},
		{"escapes attribute", "<head></head>", `/a"b`, `<head><meta name="labelgate-api-base" content="/a&#34;b" />` + "\n" + `</head>`},
	}
	for _, tt := range tests {
		if got := string(injectAPIBase([]byte(tt.doc), tt.base)); got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

func TestServeAndGracefulShutdown(t *testing.T) {
	f := newFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.srv.Run(ctx) }()

	// Run listens on a fixed address; use Serve on our own listener to learn the port.
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	ln := listenLoopback(t)
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- f.srv.Serve(ctx2, ln) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/api/health")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	cancel2()
	if err := <-done2; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestRunReportsListenError(t *testing.T) {
	ln := listenLoopback(t)
	defer func() { _ = ln.Close() }()
	f := newFixture(t, func(d *Deps) { d.Config.Address = ln.Addr().String() })
	err := f.srv.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("err = %v", err)
	}
}

func TestServeReturnsErrorWhenListenerBroken(t *testing.T) {
	f := newFixture(t, nil)
	ln := listenLoopback(t)
	_ = ln.Close()
	if err := f.srv.Serve(context.Background(), ln); err == nil {
		t.Fatal("expected error from closed listener")
	}
}
