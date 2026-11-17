package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/cloudflare/cffake"
	"github.com/channinghe/labelgate/internal/model"
)

const (
	testInstance = "abcd1234"
	testZone     = "z1"
	testAccount  = "acc1"
	tunnelA      = "11111111-1111-1111-1111-111111111111"
	tunnelB      = "22222222-2222-2222-2222-222222222222"
	specLabel    = "test.spec"
)

var (
	ownMarker  = model.DNSMarker(testInstance)
	ownTag     = model.AccessTag(testInstance)
	testZoneV  = cloudflare.Zone{ID: testZone, Name: "example.com", AccountID: testAccount, Credential: "default"}
	testAcct   = cloudflare.Account{ID: testAccount, Credential: "default"}
	errBoom    = errors.New("boom")
	mutatingOp = map[cffake.Op]bool{
		cffake.OpCreateDNSRecord: true, cffake.OpUpdateDNSRecord: true, cffake.OpDeleteDNSRecord: true,
		cffake.OpPutTunnelConfig: true, cffake.OpCreateAccessApp: true, cffake.OpUpdateAccessApp: true,
		cffake.OpDeleteAccessApp: true, cffake.OpCreateAccessPolicy: true, cffake.OpUpdateAccessPolicy: true,
		cffake.OpDeleteAccessPolicy: true,
	}
)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeSource is a settable local source.
type fakeSource struct {
	mu      sync.Mutex
	ctrs    []model.Container
	err     error
	watched chan func()
}

func (s *fakeSource) set(ctrs ...model.Container) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctrs, s.err = append([]model.Container(nil), ctrs...), nil
}

func (s *fakeSource) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *fakeSource) Snapshot(ctx context.Context) (model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return model.Snapshot{}, s.err
	}
	return model.Snapshot{Complete: true, Containers: append([]model.Container(nil), s.ctrs...)}, nil
}

func (s *fakeSource) Watch(ctx context.Context, notify func()) error {
	if s.watched != nil {
		s.watched <- notify
	}
	<-ctx.Done()
	return ctx.Err()
}

// testParse decodes the JSON ParsedLabels stored in specLabel.
func testParse(_ string, labels map[string]string) model.ParsedLabels {
	var pl model.ParsedLabels
	if raw, ok := labels[specLabel]; ok {
		if err := json.Unmarshal([]byte(raw), &pl); err != nil {
			panic(err)
		}
	}
	return pl
}

// ctr builds a container whose labels encode pl.
func ctr(name string, pl model.ParsedLabels) model.Container {
	raw, err := json.Marshal(pl)
	if err != nil {
		panic(err)
	}
	return model.Container{
		ID: "id-" + name, Name: name, Labels: map[string]string{specLabel: string(raw)},
		Networks: map[string]string{"bridge": "172.17.0.5", "v6net": "fd00::5"},
	}
}

func dnsSpec(svc, host string) model.DNSSpec {
	return model.DNSSpec{Service: svc, Hostname: host, Type: model.DNSTypeA, Target: model.TargetAuto, Proxied: true, Cleanup: true}
}

func tunSpec(svc, host, url string) model.TunnelSpec {
	return model.TunnelSpec{Service: svc, Hostname: host, URL: url, Cleanup: true}
}

func dnsOnly(specs ...model.DNSSpec) model.ParsedLabels { return model.ParsedLabels{DNS: specs} }

func tunOnly(specs ...model.TunnelSpec) model.ParsedLabels { return model.ParsedLabels{Tunnels: specs} }

type harness struct {
	t   *testing.T
	r   *Reconciler
	cf  *cffake.Fake
	st  *memStore
	src *fakeSource
	clk *clock
	ip  netip.Addr
}

func defaultSettings() Settings {
	return Settings{
		InstanceID: testInstance, LabelPrefix: "labelgate", Interval: time.Hour,
		RemoveDelay: 30 * time.Minute, AgentGrace: 24 * time.Hour,
		Debounce: 5 * time.Millisecond, MaxDebounce: 20 * time.Millisecond,
		Agents: func(string) AgentPolicy { return AgentPolicy{Allowed: true, Tunnels: []string{""}} },
	}
}

func newHarness(t *testing.T, mod ...func(*Settings)) *harness {
	t.Helper()
	h := &harness{t: t, cf: cffake.New(), src: &fakeSource{}, clk: &clock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}}
	h.ip = netip.MustParseAddr("203.0.113.10")
	h.st = newMemStore(h.clk.now)
	h.cf.AddZone(testZoneV)
	h.cf.AddTunnel(cloudflare.Tunnel{Ref: "default", ID: tunnelA, AccountID: testAccount, Credential: "default"}, true)
	h.cf.AddTunnel(cloudflare.Tunnel{Ref: "other", ID: tunnelB, AccountID: testAccount, Credential: "default"}, false)
	s := defaultSettings()
	for _, m := range mod {
		m(&s)
	}
	r, err := New(Deps{
		Settings: s, Cloudflare: h.cf, Store: h.st, Local: h.src, Now: h.clk.now,
		PublicIP: func(_ context.Context, v6 bool) (netip.Addr, error) {
			if v6 {
				return netip.MustParseAddr("2001:db8::10"), nil
			}
			return h.ip, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.parse = testParse
	h.r = r
	return h
}

// cycle runs one reconcile cycle and returns the resulting status.
func (h *harness) cycle() SyncStatus {
	h.t.Helper()
	h.r.runCycle(context.Background())
	return h.r.Status()
}

// mutations counts mutating Cloudflare calls since the last reset.
func (h *harness) mutations() int {
	n := 0
	for _, c := range h.cf.Calls() {
		if mutatingOp[c.Op] {
			n++
		}
	}
	return n
}

// settle runs cycles and asserts the last one issued zero mutations.
func (h *harness) assertIdempotent() {
	h.t.Helper()
	h.cf.ResetCalls()
	writes := h.st.writeCount()
	h.cycle()
	if n := h.mutations(); n != 0 {
		h.t.Fatalf("second cycle issued %d mutating calls: %+v", n, h.cf.Calls())
	}
	if w := h.st.writeCount(); w != writes {
		h.t.Fatalf("second cycle wrote the store %d times", w-writes)
	}
}

func (h *harness) row(kind model.Kind, key string) model.Resource {
	h.t.Helper()
	r, ok := h.st.get(kind, key)
	if !ok {
		h.t.Fatalf("no row %s %s; rows: %+v", kind, key, h.st.list(false))
	}
	return r
}

func (h *harness) noRow(kind model.Kind, key string) {
	h.t.Helper()
	if r, ok := h.st.get(kind, key); ok {
		h.t.Fatalf("unexpected row %+v", r)
	}
}

func (h *harness) records() []cloudflare.DNSRecord { return h.cf.Records(testZone) }

func (h *harness) recordsNamed(name string) []cloudflare.DNSRecord {
	var out []cloudflare.DNSRecord
	for _, r := range h.records() {
		if r.Name == name {
			out = append(out, r)
		}
	}
	return out
}

func (h *harness) tunnelConfig(id string) cloudflare.TunnelConfig {
	h.t.Helper()
	var cfg cloudflare.TunnelConfig
	raw := h.cf.TunnelConfigJSON(id)
	if raw == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		h.t.Fatal(err)
	}
	return cfg
}

func ruleIDs(cfg cloudflare.TunnelConfig) []string {
	out := make([]string, 0, len(cfg.Ingress))
	for _, r := range cfg.Ingress {
		out = append(out, r.Hostname()+r.Path()+"=>"+r.Service())
	}
	return out
}

func wantStatus(t *testing.T, r model.Resource, s model.Status) {
	t.Helper()
	if r.Status != s {
		t.Fatalf("%s %s: status %s (%q), want %s", r.Kind, r.Key, r.Status, r.LastError, s)
	}
}
