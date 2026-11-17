package cffake

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

var (
	zone   = cloudflare.Zone{ID: "z1", Name: "example.com", AccountID: "acct1", Credential: "default"}
	acct   = cloudflare.Account{ID: "acct1", Credential: "default"}
	tunnel = cloudflare.Tunnel{Ref: "default", ID: "tun-1", AccountID: "acct1", Credential: "default"}
	bg     = context.Background()
)

func newFake() *Fake {
	f := New()
	f.AddZone(zone)
	f.AddTunnel(tunnel, true)
	return f
}

func TestDNSLifecycleAndNormalization(t *testing.T) {
	// Arrange
	f := newFake()

	// Act
	rec, err := f.CreateDNSRecord(bg, zone, cloudflare.DNSRecord{Name: "MX.Example.com", Type: model.DNSTypeMX, Content: "Mail.Example.com.", Proxied: true, Priority: 10})

	// Assert: normalized like the production client.
	want := cloudflare.DNSRecord{ID: rec.ID, Name: "mx.example.com", Type: model.DNSTypeMX, Content: "mail.example.com", TTL: 1, Priority: 10}
	if err != nil || !reflect.DeepEqual(rec, want) {
		t.Fatalf("create: %+v %v", rec, err)
	}
	if _, err := f.CreateDNSRecord(bg, zone, cloudflare.DNSRecord{Name: "mx.example.com", Type: model.DNSTypeMX, Content: "mail.example.com"}); !errors.Is(err, cloudflare.ErrAlreadyExists) {
		t.Fatalf("identical record: %v", err)
	}
	rec.Comment = model.DNSComment("abcd1234", "hi")
	if _, err := f.UpdateDNSRecord(bg, zone, rec); err != nil {
		t.Fatal(err)
	}
	live, _ := f.ListDNSRecords(bg, zone)
	if len(live) != 1 || live[0].Comment != "labelgate:abcd1234 hi" {
		t.Fatalf("live %+v", live)
	}
	if err := f.DeleteDNSRecord(bg, zone, rec.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteDNSRecord(bg, zone, rec.ID); !errors.Is(err, cloudflare.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	if _, err := f.UpdateDNSRecord(bg, zone, rec); !errors.Is(err, cloudflare.ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	seeded := f.PutRecord("z1", cloudflare.DNSRecord{Name: "A.example.com", Type: model.DNSTypeA, Content: "1.1.1.1", TTL: 0})
	if got := f.Records("z1"); len(got) != 1 || got[0] != seeded || seeded.TTL != 1 {
		t.Fatalf("seed %+v", got)
	}
}

func TestInputValidationMirrorsClient(t *testing.T) {
	f := newFake()
	noCred := cloudflare.Zone{ID: "z1", Credential: "ghost"}
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"list zone without id", second(f.ListDNSRecords(bg, cloudflare.Zone{Credential: "default"})), model.ErrInvalid},
		{"list unknown credential", second(f.ListDNSRecords(bg, noCred)), cloudflare.ErrUnknownCredential},
		{"create unknown credential", second(f.CreateDNSRecord(bg, noCred, cloudflare.DNSRecord{Name: "a.example.com"})), cloudflare.ErrUnknownCredential},
		{"update without id", second(f.UpdateDNSRecord(bg, zone, cloudflare.DNSRecord{})), model.ErrInvalid},
		{"update unknown credential", second(f.UpdateDNSRecord(bg, noCred, cloudflare.DNSRecord{ID: "x"})), cloudflare.ErrUnknownCredential},
		{"delete without id", f.DeleteDNSRecord(bg, zone, ""), model.ErrInvalid},
		{"delete unknown credential", f.DeleteDNSRecord(bg, noCred, "x"), cloudflare.ErrUnknownCredential},
		{"get tunnel without account", second(f.GetTunnelConfig(bg, cloudflare.Tunnel{ID: "tun-1"})), model.ErrInvalid},
		{"put tunnel unknown credential", f.PutTunnelConfig(bg, cloudflare.Tunnel{ID: "tun-1", AccountID: "a", Credential: "ghost"}, cloudflare.TunnelConfig{}), cloudflare.ErrUnknownCredential},
		{"tag without account", f.EnsureAccessTag(bg, cloudflare.Account{Credential: "default"}, "t"), model.ErrInvalid},
		{"apps unknown credential", second(f.ListAccessApps(bg, cloudflare.Account{ID: "a", Credential: "ghost"})), cloudflare.ErrUnknownCredential},
		{"create app unknown credential", second(f.CreateAccessApp(bg, cloudflare.Account{ID: "a", Credential: "ghost"}, cloudflare.AccessApp{})), cloudflare.ErrUnknownCredential},
		{"update app without id", second(f.UpdateAccessApp(bg, acct, cloudflare.AccessApp{})), model.ErrInvalid},
		{"update app unknown credential", second(f.UpdateAccessApp(bg, cloudflare.Account{ID: "a", Credential: "ghost"}, cloudflare.AccessApp{ID: "x"})), cloudflare.ErrUnknownCredential},
		{"delete app without id", f.DeleteAccessApp(bg, acct, ""), model.ErrInvalid},
		{"delete app unknown credential", f.DeleteAccessApp(bg, cloudflare.Account{ID: "a", Credential: "ghost"}, "x"), cloudflare.ErrUnknownCredential},
		{"policies without account", second(f.ListAccessPolicies(bg, cloudflare.Account{Credential: "default"})), model.ErrInvalid},
		{"create policy unknown credential", second(f.CreateAccessPolicy(bg, cloudflare.Account{ID: "a", Credential: "ghost"}, cloudflare.AccessPolicy{})), cloudflare.ErrUnknownCredential},
		{"create policy bad selector", second(f.CreateAccessPolicy(bg, acct, cloudflare.AccessPolicy{Include: []model.AccessRule{{Selector: "bogus"}}})), model.ErrInvalid},
		{"update policy without id", second(f.UpdateAccessPolicy(bg, acct, cloudflare.AccessPolicy{})), model.ErrInvalid},
		{"update policy unknown credential", second(f.UpdateAccessPolicy(bg, cloudflare.Account{ID: "a", Credential: "ghost"}, cloudflare.AccessPolicy{ID: "x"})), cloudflare.ErrUnknownCredential},
		{"update policy bad selector", second(f.UpdateAccessPolicy(bg, acct, cloudflare.AccessPolicy{ID: "x", Require: []model.AccessRule{{Selector: "bogus"}}})), model.ErrInvalid},
		{"delete policy without id", f.DeleteAccessPolicy(bg, acct, ""), model.ErrInvalid},
		{"delete policy unknown credential", f.DeleteAccessPolicy(bg, cloudflare.Account{ID: "a", Credential: "ghost"}, "x"), cloudflare.ErrUnknownCredential},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, tt.err)
			}
		})
	}
}

func second[T any](_ T, err error) error { return err }

func TestTunnelConfigRoundTrip(t *testing.T) {
	f := newFake()
	if cfg, err := f.GetTunnelConfig(bg, tunnel); err != nil || len(cfg.Ingress) != 0 || cfg.Extra != nil {
		t.Fatalf("never configured: %+v %v", cfg, err)
	}
	in := `{"ingress":[{"hostname":"a.example.com","service":"http://a","x":1},{"service":"http_status:404"}],"warp-routing":{"enabled":true}}`
	f.SetTunnelConfig("TUN-1", in)
	cfg, err := f.GetTunnelConfig(bg, tunnel)
	if err != nil || len(cfg.Ingress) != 2 {
		t.Fatalf("get: %+v %v", cfg, err)
	}
	cfg.Ingress[0] = cfg.Ingress[0].WithString(cloudflare.RuleService, "http://b")
	if err := f.PutTunnelConfig(bg, tunnel, cfg); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(f.TunnelConfigJSON("tun-1")), &got); err != nil {
		t.Fatal(err)
	}
	rule := got["ingress"].([]any)[0].(map[string]any)
	if rule["service"] != "http://b" || rule["x"] != 1.0 || got["warp-routing"] == nil {
		t.Fatalf("stored %v", got)
	}
	other := cloudflare.Tunnel{ID: "nope", AccountID: "acct1", Credential: "default"}
	if _, err := f.GetTunnelConfig(bg, other); !errors.Is(err, cloudflare.ErrNotFound) {
		t.Fatalf("unknown tunnel get: %v", err)
	}
	if err := f.PutTunnelConfig(bg, other, cfg); !errors.Is(err, cloudflare.ErrNotFound) {
		t.Fatalf("unknown tunnel put: %v", err)
	}
	f.SetTunnelConfig("tun-1", "{broken")
	if _, err := f.GetTunnelConfig(bg, tunnel); err == nil {
		t.Fatal("corrupt stored config must fail")
	}
}

func TestResolveTunnel(t *testing.T) {
	f := New()
	if _, err := f.ResolveTunnel(""); !errors.Is(err, cloudflare.ErrNoDefaultTunnel) {
		t.Fatalf("no default: %v", err)
	}
	f.AddTunnel(cloudflare.Tunnel{Ref: "edge", ID: "TUN-E", AccountID: "a", Credential: "default"}, false)
	f.AddTunnel(cloudflare.Tunnel{Ref: "default", ID: "tun-r", AccountID: "a", Credential: "default"}, true)
	for ref, want := range map[string]string{"": "tun-r", "edge": "tun-e", "TUN-E": "tun-e", "default": "tun-r"} {
		got, err := f.ResolveTunnel(ref)
		if err != nil || got.ID != want {
			t.Errorf("%q: %+v %v", ref, got, err)
		}
	}
	if _, err := f.ResolveTunnel("ghost"); !errors.Is(err, cloudflare.ErrUnknownTunnel) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestResolveZoneTieBreak(t *testing.T) {
	f := New()
	f.AddCredential("zeta")
	f.AddZone(cloudflare.Zone{ID: "zz", Name: "Shared.net", Credential: "zeta"})
	f.AddZone(cloudflare.Zone{ID: "za", Name: "shared.net", Credential: "alpha"})
	z, err := f.ResolveZone(bg, "x.shared.net", "")
	if err != nil || z.ID != "za" {
		t.Fatalf("smallest credential name wins ties: %+v %v", z, err)
	}
	f.AddZone(cloudflare.Zone{ID: "zr", Name: "shared.net", Credential: "default"})
	if z, _ := f.ResolveZone(bg, "x.shared.net", ""); z.ID != "zr" {
		t.Fatalf("root credential wins ties: %+v", z)
	}
	if z, err := f.ResolveZone(bg, "x.shared.net", "zeta"); err != nil || z.ID != "zz" {
		t.Fatalf("explicit credential: %+v %v", z, err)
	}
	if _, err := f.ResolveZone(bg, "x.other.net", "zeta"); !errors.Is(err, cloudflare.ErrNoZone) {
		t.Fatalf("explicit miss: %v", err)
	}
}

func TestAccessAppsAndPolicies(t *testing.T) {
	// Arrange
	f := newFake()
	if err := f.EnsureAccessTag(bg, acct, "labelgate-abcd1234"); err != nil {
		t.Fatal(err)
	}
	if err := f.EnsureAccessTag(bg, acct, "labelgate-abcd1234"); err != nil {
		t.Fatal(err)
	}
	if got := f.Tags("acct1"); !reflect.DeepEqual(got, []string{"labelgate-abcd1234"}) {
		t.Fatalf("tags %v", got)
	}
	// Act: consecutive same-selector rules are merged like Cloudflare does.
	pol, err := f.CreateAccessPolicy(bg, acct, cloudflare.AccessPolicy{Name: "p", Decision: "allow",
		Include: []model.AccessRule{{Selector: model.SelectorEmails, Values: []string{"a@x.com"}}, {Selector: model.SelectorEmails, Values: []string{"b@x.com"}}},
		Require: []model.AccessRule{}})
	// Assert
	if err != nil || !reflect.DeepEqual(pol.Include, []model.AccessRule{{Selector: model.SelectorEmails, Values: []string{"a@x.com", "b@x.com"}}}) || pol.Require != nil {
		t.Fatalf("policy %+v %v", pol, err)
	}
	if _, err := f.CreateAccessApp(bg, acct, cloudflare.AccessApp{Domain: "a.example.com", PolicyIDs: []string{"ghost"}}); !errors.Is(err, cloudflare.ErrNotFound) {
		t.Fatalf("unknown policy link: %v", err)
	}
	app, err := f.CreateAccessApp(bg, acct, cloudflare.AccessApp{Name: "A", Domain: "A.example.com", Tags: []string{"labelgate-abcd1234"}, PolicyIDs: []string{pol.ID}})
	if err != nil || app.Domain != "a.example.com" {
		t.Fatalf("app %+v %v", app, err)
	}
	if _, err := f.CreateAccessApp(bg, acct, cloudflare.AccessApp{Domain: "a.example.com"}); !errors.Is(err, cloudflare.ErrAlreadyExists) {
		t.Fatalf("duplicate domain: %v", err)
	}
	if err := f.DeleteAccessPolicy(bg, acct, pol.ID); err == nil {
		t.Fatal("policy in use must not be deletable")
	}
	app.PolicyIDs = nil
	if _, err := f.UpdateAccessApp(bg, acct, app); err != nil {
		t.Fatal(err)
	}
	if _, err := f.UpdateAccessApp(bg, acct, cloudflare.AccessApp{ID: "x", PolicyIDs: []string{"ghost"}}); !errors.Is(err, cloudflare.ErrNotFound) {
		t.Fatalf("update missing app: %v", err)
	}
	pol.Include = []model.AccessRule{{Selector: model.SelectorEveryone}}
	if _, err := f.UpdateAccessPolicy(bg, acct, pol); err != nil {
		t.Fatal(err)
	}
	if ps := f.Policies("acct1"); len(ps) != 1 || ps[0].Include[0].Selector != model.SelectorEveryone {
		t.Fatalf("policies %+v", ps)
	}
	if err := f.DeleteAccessPolicy(bg, acct, pol.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.UpdateAccessPolicy(bg, acct, pol); !errors.Is(err, cloudflare.ErrNotFound) {
		t.Fatalf("update missing policy: %v", err)
	}
	if err := f.DeleteAccessPolicy(bg, acct, pol.ID); !errors.Is(err, cloudflare.ErrNotFound) {
		t.Fatalf("delete missing policy: %v", err)
	}
	if err := f.DeleteAccessApp(bg, acct, app.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteAccessApp(bg, acct, app.ID); !errors.Is(err, cloudflare.ErrNotFound) {
		t.Fatalf("delete missing app: %v", err)
	}
}

func TestSeedsAndListsAreCopies(t *testing.T) {
	f := newFake()
	pol := f.PutPolicy("acct1", cloudflare.AccessPolicy{Name: "seed", Include: []model.AccessRule{{Selector: "unsupported:x", Values: []string{`{"x":{}}`}}}})
	bad := f.PutPolicy("acct1", cloudflare.AccessPolicy{Name: "bad", Include: []model.AccessRule{{Selector: "bogus", Values: []string{"v"}}}})
	app := f.PutApp("acct1", cloudflare.AccessApp{Domain: "S.example.com", Tags: []string{"t"}, PolicyIDs: []string{pol.ID}})
	apps, _ := f.ListAccessApps(bg, acct)
	apps[0].Tags[0] = "mutated"
	if f.Apps("acct1")[0].Tags[0] != "t" || app.Domain != "s.example.com" {
		t.Fatal("app state shared with caller")
	}
	pols, _ := f.ListAccessPolicies(bg, acct)
	pols[0].Include[0].Values[0] = "mutated"
	if f.Policies("acct1")[0].Include[0].Values[0] == "mutated" {
		t.Fatal("policy state shared with caller")
	}
	if got := f.Policies("acct1")[1]; got.ID != bad.ID || got.Include[0].Selector != "bogus" {
		t.Fatalf("invalid seed must be stored verbatim: %+v", got)
	}
}

func TestFailureInjectionCallsAndCancellation(t *testing.T) {
	f := newFake()
	boom := errors.New("boom")
	ops := []struct {
		op   Op
		call func() error
	}{
		{OpVerify, func() error { return f.Verify(bg) }},
		{OpResolveZone, func() error { return second(f.ResolveZone(bg, "a.example.com", "")) }},
		{OpCreateDNSRecord, func() error { return second(f.CreateDNSRecord(bg, zone, cloudflare.DNSRecord{Name: "a.example.com"})) }},
		{OpUpdateDNSRecord, func() error { return second(f.UpdateDNSRecord(bg, zone, cloudflare.DNSRecord{ID: "x"})) }},
		{OpDeleteDNSRecord, func() error { return f.DeleteDNSRecord(bg, zone, "x") }},
		{OpGetTunnelConfig, func() error { return second(f.GetTunnelConfig(bg, tunnel)) }},
		{OpPutTunnelConfig, func() error { return f.PutTunnelConfig(bg, tunnel, cloudflare.TunnelConfig{}) }},
		{OpEnsureAccessTag, func() error { return f.EnsureAccessTag(bg, acct, "t") }},
		{OpListAccessApps, func() error { return second(f.ListAccessApps(bg, acct)) }},
		{OpCreateAccessApp, func() error { return second(f.CreateAccessApp(bg, acct, cloudflare.AccessApp{})) }},
		{OpUpdateAccessApp, func() error { return second(f.UpdateAccessApp(bg, acct, cloudflare.AccessApp{ID: "x"})) }},
		{OpDeleteAccessApp, func() error { return f.DeleteAccessApp(bg, acct, "x") }},
		{OpListAccessPolicies, func() error { return second(f.ListAccessPolicies(bg, acct)) }},
		{OpCreateAccessPolicy, func() error { return second(f.CreateAccessPolicy(bg, acct, cloudflare.AccessPolicy{})) }},
		{OpUpdateAccessPolicy, func() error { return second(f.UpdateAccessPolicy(bg, acct, cloudflare.AccessPolicy{ID: "x"})) }},
		{OpDeleteAccessPolicy, func() error { return f.DeleteAccessPolicy(bg, acct, "x") }},
	}
	for _, o := range ops {
		f.FailNext(o.op, boom)
		if err := o.call(); !errors.Is(err, boom) {
			t.Errorf("%s: injected failure not returned: %v", o.op, err)
		}
		if f.CountCalls(o.op) != 1 {
			t.Errorf("%s: calls %d", o.op, f.CountCalls(o.op))
		}
	}
	if len(f.Calls()) != len(ops) {
		t.Fatalf("calls %v", f.Calls())
	}
	f.ResetCalls()
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if err := f.Verify(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ctx: %v", err)
	}
	if len(f.Calls()) != 1 {
		t.Fatalf("cancelled calls are recorded: %v", f.Calls())
	}
	if h := f.Health(); !h.Reachable || h.LastCheck.IsZero() {
		t.Fatalf("health %+v", h)
	}
}
