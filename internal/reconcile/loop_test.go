package reconcile

import (
	"context"
	"net/netip"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare/cffake"
	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
)

func TestNewValidatesDeps(t *testing.T) {
	ok := Deps{
		Settings:   defaultSettings(),
		Cloudflare: cffake.New(),
		Store:      newMemStore(time.Now),
		PublicIP:   func(context.Context, bool) (netip.Addr, error) { return netip.Addr{}, nil },
	}
	tests := []struct {
		name string
		mut  func(*Deps)
	}{
		{"no cloudflare", func(d *Deps) { d.Cloudflare = nil }},
		{"no store", func(d *Deps) { d.Store = nil }},
		{"no public ip", func(d *Deps) { d.PublicIP = nil }},
		{"no agents", func(d *Deps) { d.Settings.Agents = nil }},
		{"bad instance", func(d *Deps) { d.Settings.InstanceID = "BAD!" }},
		{"no interval", func(d *Deps) { d.Settings.Interval = 0 }},
		{"negative delay", func(d *Deps) { d.Settings.RemoveDelay = -1 }},
	}
	for _, tc := range tests {
		d := ok
		tc.mut(&d)
		if _, err := New(d); err == nil {
			t.Errorf("%s: want error", tc.name)
		}
	}
	d := ok
	d.Settings.Debounce, d.Settings.MaxDebounce, d.Settings.LabelPrefix = 0, 0, ""
	r, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	if r.settings.Debounce != defaultDebounce || r.settings.MaxDebounce != defaultMaxDebounce || r.settings.LabelPrefix != "labelgate" {
		t.Fatalf("defaults %+v", r.settings)
	}
	if r.Status().State != SyncPending {
		t.Fatal("not pending")
	}
}

func TestTriggerNeverBlocks(t *testing.T) {
	h := newHarness(t)
	done := make(chan struct{})
	go func() {
		for range 1000 {
			h.r.Trigger()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Trigger blocked")
	}
}

// countingStore counts cycles via AgentSnapshots (called once per cycle).
type countingStore struct {
	*memStore
	cycles atomic.Int32
}

func (s *countingStore) AgentSnapshots(ctx context.Context) ([]model.Snapshot, error) {
	s.cycles.Add(1)
	return s.memStore.AgentSnapshots(ctx)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRunDebouncesTriggersAndStops(t *testing.T) {
	h := newHarness(t)
	cs := &countingStore{memStore: h.st}
	h.r.store = cs
	h.src.watched = make(chan func(), 1)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- h.r.Run(ctx) }()

	notify := <-h.src.watched
	waitFor(t, func() bool { return cs.cycles.Load() == 1 && h.r.Status().State == SyncSuccess })
	if err := h.r.Run(ctx); err == nil {
		t.Fatal("second Run must fail")
	}
	for range 50 {
		notify() // a burst of Docker events
	}
	waitFor(t, func() bool { return cs.cycles.Load() == 2 })
	time.Sleep(50 * time.Millisecond)
	if n := cs.cycles.Load(); n != 2 {
		t.Fatalf("burst caused %d cycles, want 1 extra", n-1)
	}

	// Continuous triggers are capped by MaxDebounce.
	stop := time.After(80 * time.Millisecond)
loop:
	for {
		select {
		case <-stop:
			break loop
		default:
			h.r.Trigger()
			time.Sleep(time.Millisecond)
		}
	}
	if n := cs.cycles.Load(); n < 4 {
		t.Fatalf("max debounce not honoured: %d cycles", n)
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRunWakesForDeadline(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.Interval = 20 * time.Millisecond })
	cs := &countingStore{memStore: h.st}
	h.r.store = cs
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = h.r.Run(ctx) }()
	waitFor(t, func() bool { return cs.cycles.Load() >= 3 })
}

func TestUntilNext(t *testing.T) {
	h := newHarness(t)
	now := h.clk.now()
	tests := []struct {
		deadline time.Time
		want     time.Duration
	}{
		{time.Time{}, time.Hour},
		{now.Add(10 * time.Minute), 10 * time.Minute},
		{now.Add(-time.Minute), h.r.settings.Debounce},
		{now.Add(2 * time.Hour), time.Hour},
	}
	for _, tc := range tests {
		if got := h.r.untilNext(tc.deadline); got != tc.want {
			t.Errorf("untilNext(%v) = %v, want %v", tc.deadline, got, tc.want)
		}
	}
}

func TestCancelledCycleKeepsStatus(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.r.runCycle(ctx)
	if h.r.Status().State != SyncPending || h.mutations() != 0 {
		t.Fatalf("cancelled cycle: %+v %+v", h.r.Status(), h.cf.Calls())
	}
}

func TestPoliciesFromConfig(t *testing.T) {
	cfg := config.Defaults()
	cfg.Agent.Enabled = true
	cfg.Agent.Agents = map[string]config.AgentEntry{
		"named":   {Token: "t", Hostnames: []string{"*.a.example.com"}, Tunnels: []string{"x"}, DefaultTunnel: "y"},
		"default": {Token: "t"},
	}
	p := PoliciesFromConfig(cfg)
	if got := p("named"); !got.Allowed || !reflect.DeepEqual(got.Tunnels, []string{"x", "y"}) || got.DefaultTunnel != "y" ||
		!reflect.DeepEqual(got.Hostnames, []string{"*.a.example.com"}) {
		t.Fatalf("named %+v", got)
	}
	if got := p("default"); !got.Allowed || !reflect.DeepEqual(got.Tunnels, []string{""}) {
		t.Fatalf("named without tunnels %+v", got)
	}
	if got := p("stranger"); got.Allowed {
		t.Fatalf("stranger allowed without accept_token: %+v", got)
	}
	got := p("named")
	got.Tunnels[0] = "mutated"
	if p("named").Tunnels[0] != "x" {
		t.Fatal("policy shares mutable state")
	}

	cfg.Agent.AcceptToken = "shared"
	cfg.Agent.AcceptHostnames = []string{"*.edge.example.com"}
	p = PoliciesFromConfig(cfg)
	if got := p("stranger"); !got.Allowed || !reflect.DeepEqual(got.Tunnels, []string{""}) || len(got.Hostnames) != 1 {
		t.Fatalf("accept %+v", got)
	}
	cfg.Agent.AcceptTunnels = []string{"edge"}
	if got := PoliciesFromConfig(cfg)("stranger"); !reflect.DeepEqual(got.Tunnels, []string{"edge"}) {
		t.Fatalf("accept tunnels %+v", got)
	}
	cfg.Agent.Enabled = false
	if PoliciesFromConfig(cfg)("named").Allowed {
		t.Fatal("agents disabled but allowed")
	}
}

func TestHostnameAllowed(t *testing.T) {
	tests := []struct {
		globs []string
		host  string
		want  bool
	}{
		{nil, "x.example.com", true},
		{[]string{"*.example.com"}, "a.example.com", true},
		{[]string{"*.Example.com"}, "a.b.example.com", true},
		{[]string{"app.example.com"}, "api.example.com", false},
		{[]string{"[bad"}, "x", false},
	}
	for _, tc := range tests {
		if got := hostnameAllowed(tc.globs, tc.host); got != tc.want {
			t.Errorf("%v %s = %v", tc.globs, tc.host, got)
		}
	}
}
