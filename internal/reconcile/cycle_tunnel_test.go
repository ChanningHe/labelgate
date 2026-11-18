package reconcile

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/cloudflare/cffake"
	"github.com/channinghe/labelgate/internal/model"
)

var (
	cnameKey   = model.DNSKey(appHost, model.DNSTypeCNAME, "")
	ruleKey    = model.TunnelKey(tunnelA, appHost, "")
	targetA    = model.TunnelTarget(tunnelA)
	liveConfig = `{"ingress":[` +
		`{"hostname":"manual.example.com","service":"http://manual:80","originRequest":{"access":{"required":true,"teamName":"t"}}},` +
		`{"service":"http_status:503"}],` +
		`"warp-routing":{"enabled":true},"originRequest":{"connectTimeout":10}}`
)

func TestTunnelCreateWithCNAMEAndIdempotent(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80"))))
	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("status %+v", st)
	}
	if got := ruleIDs(h.tunnelConfig(tunnelA)); !reflect.DeepEqual(got, []string{appHost + "=>http://web:80", "=>http_status:404"}) {
		t.Fatalf("ingress %v", got)
	}
	recs := h.recordsNamed(appHost)
	if len(recs) != 1 || recs[0].Type != model.DNSTypeCNAME || recs[0].Content != targetA || !recs[0].Proxied || recs[0].Comment != ownMarker {
		t.Fatalf("records %+v", recs)
	}
	wantStatus(t, h.row(model.KindTunnel, ruleKey), model.StatusActive)
	cname := h.row(model.KindDNS, cnameKey)
	if cname.TunnelID != tunnelA || cname.Status != model.StatusActive {
		t.Fatalf("cname row %+v", cname)
	}
	h.assertIdempotent()
}

func TestTunnelMergePreservesUnmanagedState(t *testing.T) {
	h := newHarness(t)
	h.cf.SetTunnelConfig(tunnelA, liveConfig)
	s := tunSpec("web", appHost, "http://web:80")
	s.Origin = model.OriginRequest{ConnectTimeout: 30 * time.Second, NoTLSVerify: true}
	h.src.set(ctr("web", tunOnly(s)))
	h.cycle()
	cfg := h.tunnelConfig(tunnelA)
	want := []string{appHost + "=>http://web:80", "manual.example.com=>http://manual:80", "=>http_status:503"}
	if got := ruleIDs(cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("ingress %v", got)
	}
	if string(cfg.Extra["warp-routing"]) != `{"enabled":true}` || string(cfg.Extra["originRequest"]) != `{"connectTimeout":10}` {
		t.Fatalf("extra %v", cfg.Extra)
	}
	if !strings.Contains(string(cfg.Ingress[1].Fields["originRequest"]), `"access"`) {
		t.Fatalf("unmanaged rule access lost: %s", cfg.Ingress[1].Fields["originRequest"])
	}
	if got := string(cfg.Ingress[0].Fields["originRequest"]); got != `{"connectTimeout":30,"noTLSVerify":true}` {
		t.Fatalf("origin %s", got)
	}
	h.assertIdempotent()

	// A human adds Access to our rule; a label change must keep it.
	cfg.Ingress[0] = cfg.Ingress[0].With("originRequest", []byte(`{"access":{"required":true},"connectTimeout":30,"noTLSVerify":true}`))
	raw, _ := cfg.MarshalJSON()
	h.cf.SetTunnelConfig(tunnelA, string(raw))
	s.Origin.NoTLSVerify = false
	h.src.set(ctr("web", tunOnly(s)))
	h.cycle()
	if got := string(h.tunnelConfig(tunnelA).Ingress[0].Fields["originRequest"]); got != `{"access":{"required":true},"connectTimeout":30}` {
		t.Fatalf("origin %s", got)
	}

	h.src.set()
	h.cycle()
	h.clk.advance(31 * time.Minute)
	h.cycle()
	want = []string{"manual.example.com=>http://manual:80", "=>http_status:503"}
	if got := ruleIDs(h.tunnelConfig(tunnelA)); !reflect.DeepEqual(got, want) {
		t.Fatalf("after cleanup %v", got)
	}
	if len(h.records()) != 0 || len(h.st.list(false)) != 0 {
		t.Fatalf("leftovers %+v %+v", h.records(), h.st.list(false))
	}
}

func TestTunnelPathRulesOrdering(t *testing.T) {
	h := newHarness(t)
	root := tunSpec("root", appHost, "http://web:80")
	api := tunSpec("api", appHost, "http://api:80")
	api.Path = "/api"
	v2 := tunSpec("v2", appHost, "http://v2:80")
	v2.Path = "/api/v2"
	wild := tunSpec("wild", "*.example.com", "http://w:80")
	h.cf.SetTunnelConfig(tunnelA, liveConfig)
	h.src.set(ctr("b", tunOnly(root, wild)), ctr("a", tunOnly(api, v2)))
	h.cycle()
	want := []string{
		appHost + "/api/v2=>http://v2:80", appHost + "/api=>http://api:80", appHost + "=>http://web:80",
		"manual.example.com=>http://manual:80", "*.example.com=>http://w:80", "=>http_status:503",
	}
	if got := ruleIDs(h.tunnelConfig(tunnelA)); !reflect.DeepEqual(got, want) {
		t.Fatalf("ingress %v", got)
	}
	if recs := h.recordsNamed(appHost); len(recs) != 1 {
		t.Fatalf("one CNAME per hostname, got %+v", recs)
	}
	if row := h.row(model.KindDNS, cnameKey); row.Owner.ContainerName != "a" || row.Owner.Service != "api" {
		t.Fatalf("cname owner %+v", row.Owner)
	}
	h.assertIdempotent()

	// Removing one path removes only that rule.
	h.src.set(ctr("b", tunOnly(root, wild)), ctr("a", tunOnly(v2)))
	h.cycle()
	h.clk.advance(31 * time.Minute)
	h.cycle()
	got := ruleIDs(h.tunnelConfig(tunnelA))
	if len(got) != 5 || strings.Contains(strings.Join(got, " "), "/api=>") {
		t.Fatalf("ingress %v", got)
	}
	if len(h.recordsNamed(appHost)) != 1 {
		t.Fatal("CNAME removed while other paths remain")
	}
}

func TestTunnelUnmanagedSameHostnameIsConflict(t *testing.T) {
	h := newHarness(t)
	h.cf.SetTunnelConfig(tunnelA, `{"ingress":[{"hostname":"app.example.com","path":"/manual","service":"http://m"},{"service":"http_status:404"}]}`)
	h.src.set(ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80"))))
	h.cycle()
	if row := h.row(model.KindTunnel, ruleKey); !strings.HasPrefix(row.LastError, model.ConflictNotOwned) {
		t.Fatalf("row %+v", row)
	}
	if h.cf.CountCalls(cffake.OpPutTunnelConfig) != 0 || len(h.records()) != 0 {
		t.Fatalf("conflict mutated: %+v", h.cf.Calls())
	}
	wantStatus(t, h.row(model.KindDNS, cnameKey), model.StatusError)
}

func TestTunnelPreexistingIdenticalRuleIsNotAdopted(t *testing.T) {
	h := newHarness(t)
	h.cf.SetTunnelConfig(tunnelA, `{"ingress":[{"hostname":"app.example.com","service":"http://human"},{"service":"http_status:404"}]}`)
	before := h.cf.TunnelConfigJSON(tunnelA)
	h.src.set(ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80"))))
	h.cycle()
	if h.cf.TunnelConfigJSON(tunnelA) != before {
		t.Fatal("pre-existing human rule overwritten")
	}
	wantStatus(t, h.row(model.KindTunnel, ruleKey), model.StatusError)
}

func TestTunnelGetFailureNeverPuts(t *testing.T) {
	h := newHarness(t)
	h.cf.SetTunnelConfig(tunnelA, liveConfig)
	h.src.set(ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80"))))
	h.cf.FailNext(cffake.OpGetTunnelConfig, errBoom)
	st := h.cycle()
	if st.State != SyncError || h.cf.CountCalls(cffake.OpPutTunnelConfig) != 0 || len(h.records()) != 0 {
		t.Fatalf("status %+v calls %+v", st, h.cf.Calls())
	}
	if row := h.row(model.KindTunnel, ruleKey); !strings.Contains(row.LastError, "cannot read the configuration of tunnel") {
		t.Fatalf("row %+v", row)
	}
	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("retry %+v", st)
	}

	// GET failure while cleaning up: the rule stays, nothing is PUT, retried later.
	h.src.set()
	h.cycle()
	h.clk.advance(31 * time.Minute)
	h.cf.FailNext(cffake.OpGetTunnelConfig, errBoom)
	h.cf.ResetCalls()
	h.cycle()
	if h.cf.CountCalls(cffake.OpPutTunnelConfig) != 0 {
		t.Fatal("PUT without GET")
	}
	wantStatus(t, h.row(model.KindTunnel, ruleKey), model.StatusError)
	h.cycle()
	h.noRow(model.KindTunnel, ruleKey)
}

func TestTunnelPutFailureRetried(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80"))))
	h.cf.FailNext(cffake.OpPutTunnelConfig, errBoom)
	h.cycle()
	if row := h.row(model.KindTunnel, ruleKey); !strings.Contains(row.LastError, "cannot update the configuration of tunnel") {
		t.Fatalf("row %+v", row)
	}
	if row := h.row(model.KindDNS, cnameKey); !strings.Contains(row.LastError, "waiting for tunnel") || len(h.records()) != 0 {
		t.Fatalf("CNAME applied without tunnel: %+v", row)
	}
	h.cycle()
	wantStatus(t, h.row(model.KindTunnel, ruleKey), model.StatusActive)
	wantStatus(t, h.row(model.KindDNS, cnameKey), model.StatusActive)
}

func TestTunnelCNAMERetriedAfterFailure(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80"))))
	h.cf.FailNext(cffake.OpCreateDNSRecord, errBoom)
	h.cycle()
	if row := h.row(model.KindDNS, cnameKey); row.Status != model.StatusError || row.CFID != "" {
		t.Fatalf("failed CNAME saved active: %+v", row)
	}
	h.cycle()
	wantStatus(t, h.row(model.KindDNS, cnameKey), model.StatusActive)
	if len(h.recordsNamed(appHost)) != 1 {
		t.Fatal("CNAME not created on retry")
	}
}

func TestDNSToTunnelSwitchAndBack(t *testing.T) {
	h := newHarness(t)
	dns := ctr("web", dnsOnly(dnsSpec("web", appHost)))
	tun := ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80")))
	h.src.set(dns)
	h.cycle()

	h.src.set(tun)
	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("switch status %+v", st)
	}
	recs := h.recordsNamed(appHost)
	if len(recs) != 1 || recs[0].Type != model.DNSTypeCNAME {
		t.Fatalf("records %+v", recs)
	}
	h.noRow(model.KindDNS, appKey)
	h.assertIdempotent()

	h.src.set(dns)
	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("switch back status %+v", st)
	}
	recs = h.recordsNamed(appHost)
	if len(recs) != 1 || recs[0].Type != model.DNSTypeA {
		t.Fatalf("records %+v", recs)
	}
	if got := ruleIDs(h.tunnelConfig(tunnelA)); len(got) != 1 {
		t.Fatalf("tunnel rule not removed: %v", got)
	}
	h.noRow(model.KindDNS, cnameKey)
	h.assertIdempotent()
}

// Cloudflare echoes rules back with "originRequest":{}: no PUT every cycle.
func TestEmptyOriginRequestEchoIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80"))))
	h.cycle()
	raw := strings.Replace(h.cf.TunnelConfigJSON(tunnelA), `"service":"http://web:80"`, `"service":"http://web:80","originRequest":{}`, 1)
	h.cf.SetTunnelConfig(tunnelA, raw)
	h.cf.ResetCalls()
	h.cycle()
	if n := h.cf.CountCalls(cffake.OpPutTunnelConfig); n != 0 {
		t.Fatalf("PUT issued %d times for an empty originRequest echo", n)
	}
}

func TestSupersededDeleteFailureWaits(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	h.src.set(ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80"))))
	h.cf.FailNext(cffake.OpDeleteDNSRecord, errBoom)
	h.cycle()
	if row := h.row(model.KindDNS, cnameKey); !strings.Contains(row.LastError, "waiting for the previous record") {
		t.Fatalf("row %+v", row)
	}
	h.cycle()
	if recs := h.recordsNamed(appHost); len(recs) != 1 || recs[0].Type != model.DNSTypeCNAME {
		t.Fatalf("records %+v", recs)
	}
}

func TestTunnelSwitchBetweenTunnels(t *testing.T) {
	h := newHarness(t)
	s := tunSpec("web", appHost, "http://web:80")
	h.src.set(ctr("web", tunOnly(s)))
	h.cycle()
	s.Tunnel = "Other"
	h.src.set(ctr("web", tunOnly(s)))
	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("status %+v", st)
	}
	if got := ruleIDs(h.tunnelConfig(tunnelA)); len(got) != 1 {
		t.Fatalf("old tunnel %v", got)
	}
	if got := ruleIDs(h.tunnelConfig(tunnelB)); len(got) != 2 {
		t.Fatalf("new tunnel %v", got)
	}
	if recs := h.recordsNamed(appHost); len(recs) != 1 || recs[0].Content != model.TunnelTarget(tunnelB) {
		t.Fatalf("records %+v", recs)
	}
	h.assertIdempotent()
}

func TestUnknownTunnelIsRejected(t *testing.T) {
	h := newHarness(t)
	s := tunSpec("web", appHost, "http://web:80")
	s.Tunnel = "missing"
	h.src.set(ctr("web", tunOnly(s)))
	h.cycle()
	if rej := h.st.list(true); len(rej) != 1 || !strings.Contains(rej[0].LastError, "unknown tunnel: use \"default\" (the tunnel in cloudflare.tunnel_id") {
		t.Fatalf("rejected %+v", rej)
	}
}

func TestLegacyTunnelCNAMEAdopted(t *testing.T) {
	h := newHarness(t)
	rec := h.cf.PutRecord(testZone, cloudflare.DNSRecord{Name: appHost, Type: model.DNSTypeCNAME, Content: targetA, Proxied: true, TTL: 1})
	h.cf.SetTunnelConfig(tunnelA, `{"ingress":[{"hostname":"app.example.com","service":"http://web:80"},{"service":"http_status:404"}]}`)
	owner := model.Owner{ContainerName: "web", Service: "web"}
	h.st.seed(model.Resource{Kind: model.KindTunnel, Key: ruleKey, Hostname: appHost, Owner: owner, TunnelID: tunnelA,
		Service: "http://web:80", Status: model.StatusActive, Cleanup: true, AccountID: testAccount, Credential: "default"})
	h.st.seed(model.Resource{Kind: model.KindDNS, Key: cnameKey, Hostname: appHost, Owner: owner, TunnelID: tunnelA,
		RecordType: model.DNSTypeCNAME, Content: targetA, Proxied: true, TTL: 1, Status: model.StatusActive, Cleanup: true, Legacy: true})
	h.src.set(ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80"))))
	h.cycle()
	recs := h.recordsNamed(appHost)
	if len(recs) != 1 || recs[0].ID != rec.ID || recs[0].Comment != ownMarker {
		t.Fatalf("records %+v", recs)
	}
	if row := h.row(model.KindDNS, cnameKey); row.Legacy || row.CFID != rec.ID {
		t.Fatalf("row %+v", row)
	}
	if h.cf.CountCalls(cffake.OpPutTunnelConfig) != 0 {
		t.Fatal("unchanged migrated rule re-PUT")
	}
	h.assertIdempotent()
}
