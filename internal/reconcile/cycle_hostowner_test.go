package reconcile

import (
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

const ownedByLocal = "is owned by local"

// wantOnlyRejected asserts every rejected row belongs to agent id and
// carries reason.
func (h *harness) wantOnlyRejected(id, reason string, n int) {
	h.t.Helper()
	rej := h.st.list(true)
	if len(rej) != n {
		h.t.Fatalf("rejected %d rows, want %d: %+v", len(rej), n, rej)
	}
	for _, r := range rej {
		if r.Owner.AgentID != id || !strings.Contains(r.LastError, reason) {
			h.t.Fatalf("rejected row %s %s (%s): %q", r.Kind, r.Key, r.Owner.Key(), r.LastError)
		}
	}
}

// An agent tunnel rule with a catch-all path on a local hostname (same tunnel,
// same class, different key) must not be ordered before the local rule.
func TestAgentCannotHijackLocalTunnelHostname(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80"))))
	h.cycle()
	hijack := tunSpec("evil", appHost, "http://203.0.113.66:80")
	hijack.Path = ".*"
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("x", tunOnly(hijack))))
	h.cf.ResetCalls()
	if st := h.cycle(); st.State != SyncError {
		t.Fatalf("status %+v", st)
	}
	if got := strings.Join(ruleIDs(h.tunnelConfig(tunnelA)), " "); strings.Contains(got, "203.0.113.66") {
		t.Fatalf("agent rule applied: %s", got)
	}
	h.wantOnlyRejected("edge", ownedByLocal, 1)
	if h.mutations() != 0 {
		t.Fatalf("hijack attempt mutated: %+v", h.cf.Calls())
	}
}

// Agent records of other types on a local hostname are rejected.
func TestAgentCannotCoClaimLocalRecords(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	aaaa := model.DNSSpec{Service: "v6", Hostname: appHost, Type: model.DNSTypeAAAA, Target: "2001:db8::66", Cleanup: true}
	mx := model.DNSSpec{Service: "mx", Hostname: appHost, Type: model.DNSTypeMX, Target: "mx.evil.example", Priority: 1, Cleanup: true}
	txt := model.DNSSpec{Service: "txt", Hostname: appHost, Type: model.DNSTypeTXT, Target: "v=spf1 +all", Cleanup: true}
	caa := model.DNSSpec{Service: "caa", Hostname: appHost, Type: model.DNSTypeCAA, Target: "evil-ca.example", Tag: "issue", Cleanup: true}
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("x", dnsOnly(aaaa, mx, txt, caa))))
	h.cf.ResetCalls()
	h.cycle()
	h.wantOnlyRejected("edge", ownedByLocal, 4)
	if recs := h.recordsNamed(appHost); len(recs) != 1 || recs[0].Type != model.DNSTypeA {
		t.Fatalf("records %+v", recs)
	}
	if h.mutations() != 0 {
		t.Fatalf("co-claim mutated: %+v", h.cf.Calls())
	}
}

// Without existing rows, the first source (local before agents, agents by
// ID) takes the hostname; every claim of other sources is rejected.
func TestFreshHostnameGoesToFirstSource(t *testing.T) {
	h := newHarness(t)
	aaaa := model.DNSSpec{Service: "v6", Hostname: appHost, Type: model.DNSTypeAAAA, Target: "2001:db8::66", Cleanup: true}
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.st.setAgents(
		agentSnap("b-edge", h.clk.now(), ctr("x", dnsOnly(aaaa))),
		agentSnap("a-edge", h.clk.now(), ctr("y", dnsOnly(aaaa))),
	)
	h.cycle()
	if rej := h.st.list(true); len(rej) != 2 {
		t.Fatalf("rejected %+v", rej)
	}
	if len(h.st.list(false)) != 1 {
		t.Fatalf("rows %+v", h.st.list(false))
	}

	h2 := newHarness(t)
	h2.st.setAgents(
		agentSnap("b-edge", h2.clk.now(), ctr("x", dnsOnly(aaaa))),
		agentSnap("a-edge", h2.clk.now(), ctr("y", dnsOnly(dnsSpec("web", appHost)))),
	)
	h2.cycle()
	h2.wantOnlyRejected("b-edge", "is owned by agent a-edge", 1)
}

// Containers of one source may share a hostname with different record types
// and paths.
func TestSameSourceSharesHostname(t *testing.T) {
	h := newHarness(t)
	aaaa := model.DNSSpec{Service: "v6", Hostname: appHost, Type: model.DNSTypeAAAA, Target: "2001:db8::5", Cleanup: true}
	h.st.setAgents(agentSnap("edge", h.clk.now(),
		ctr("a", dnsOnly(dnsSpec("web", appHost))), ctr("b", dnsOnly(aaaa))))
	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("status %+v", st)
	}
	if n := len(h.recordsNamed(appHost)); n != 2 {
		t.Fatalf("records %d", n)
	}
}

// A local container absent for one cycle (compose recreate) keeps its
// hostname: a rejected agent claimant on the same key never takes over.
func TestTransientAbsenceKeepsHostname(t *testing.T) {
	h := newHarness(t)
	local := dnsSpec("web", appHost)
	local.Target = "192.0.2.1"
	h.src.set(ctr("web", dnsOnly(local)))
	evil := dnsSpec("evil", appHost)
	evil.Target = "198.51.100.66"
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("x", dnsOnly(evil))))
	h.cycle()
	recID := h.row(model.KindDNS, appKey).CFID

	h.src.set() // container being recreated
	h.cycle()
	h.wantOnlyRejected("edge", ownedByLocal, 1)
	h.src.set(ctr("web", dnsOnly(local)))
	h.cycle()
	row := h.row(model.KindDNS, appKey)
	if row.Owner.AgentID != "" || row.CFID != recID || row.Status != model.StatusActive || !row.OrphanedAt.IsZero() {
		t.Fatalf("row %+v", row)
	}
	if recs := h.recordsNamed(appHost); len(recs) != 1 || recs[0].Content != "192.0.2.1" {
		t.Fatalf("records %+v", recs)
	}
}

// A rejected agent with a bypass-everyone definition never takes over a
// local Access application while its owner is briefly absent.
func TestTransientAbsenceCannotDowngradeAccess(t *testing.T) {
	h := newHarness(t)
	local := accessCtr("web", teamDef("alice@example.com"))
	h.src.set(local)
	h.cycle()
	open := model.AccessPolicyDef{Name: "team", Policies: []model.AccessPolicy{{
		Decision: model.AccessDecisionBypass,
		Include:  []model.AccessRule{{Selector: model.SelectorEveryone, Values: []string{}}},
	}}}
	h.st.setAgents(agentSnap("evil", h.clk.now(), accessCtr("e", open)))
	h.cycle()
	h.src.set()
	h.cycle()
	h.src.set(local)
	h.cycle()
	pols := h.cf.Policies(testAccount)
	if len(pols) != 1 || pols[0].Decision != "allow" || pols[0].Include[0].Values[0] != "alice@example.com" {
		t.Fatalf("policies %+v", pols)
	}
	if row := h.row(model.KindAccess, appHost); row.Owner.AgentID != "" || row.Status != model.StatusActive {
		t.Fatalf("access row %+v", row)
	}
	h.wantOnlyRejected("evil", ownedByLocal, 1)
}

// A class switch within the source supersedes an orphaned cleanup=false
// row at once, but only forgets it: the record stays on Cloudflare and the
// new class reports the clash instead of deleting it.
func TestSupersededCleanupFalseIsForgottenNotDeleted(t *testing.T) {
	h := newHarness(t)
	local := dnsSpec("web", appHost)
	local.Target, local.Cleanup = "192.0.2.1", false
	tun := ctr("zz", tunOnly(tunSpec("t", appHost, "http://zz:80")))
	h.src.set(ctr("web", dnsOnly(local)), tun)
	h.cycle()
	h.src.set(tun)
	for _, step := range []time.Duration{0, 31 * time.Minute, time.Hour} {
		h.clk.advance(step)
		h.cycle()
		recs := h.recordsNamed(appHost)
		if len(recs) != 1 || recs[0].Content != "192.0.2.1" {
			t.Fatalf("after %s: records %+v", step, recs)
		}
		h.noRow(model.KindDNS, appKey)
	}
	if row := h.row(model.KindDNS, cnameKey); row.Status != model.StatusError || !strings.Contains(row.LastError, "cleanup=false") {
		t.Fatalf("cname row %+v", row)
	}
}

// An absent owner's source keeps the hostname until its last row is gone:
// the cycle that deletes the record (cleanup=true at remove_delay) still
// rejects the other source, which only then creates its own record.
func TestHostnameChangesSourceOnlyAfterRowsAreGone(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	recID := h.row(model.KindDNS, appKey).CFID
	h.src.set()
	agent := agentSnap("edge", h.clk.now(), ctr("x", dnsOnly(dnsSpec("web", appHost))))
	h.st.setAgents(agent)
	h.cycle()
	h.wantOnlyRejected("edge", ownedByLocal, 1)
	h.clk.advance(30 * time.Minute)
	h.st.setAgents(agentSnap("edge", h.clk.now(), agent.Containers...))
	h.cycle()
	h.wantOnlyRejected("edge", ownedByLocal, 1)
	h.noRow(model.KindDNS, appKey)
	if len(h.recordsNamed(appHost)) != 0 {
		t.Fatalf("records %+v", h.records())
	}
	h.cycle()
	row := h.row(model.KindDNS, appKey)
	if row.Owner.AgentID != "edge" || row.CFID == recID || row.Status != model.StatusActive {
		t.Fatalf("row %+v", row)
	}
	if len(h.st.list(true)) != 0 {
		t.Fatal("rejection not cleared")
	}
}

// An orphaned cleanup=false row keeps the hostname for its source past
// remove_delay, until orphan_ttl forgets it.
func TestCleanupFalseRowKeepsHostnameUntilForgotten(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.OrphanTTL = 2 * time.Hour })
	local := dnsSpec("web", appHost)
	local.Cleanup = false
	h.src.set(ctr("web", dnsOnly(local)))
	h.cycle()
	h.src.set()
	h.cycle()
	other := dnsSpec("v6", appHost)
	other.Type, other.Target = model.DNSTypeAAAA, "2001:db8::66"
	for _, step := range []time.Duration{31 * time.Minute, time.Hour} {
		h.clk.advance(step)
		h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("x", dnsOnly(other))))
		h.cycle()
		h.wantOnlyRejected("edge", ownedByLocal, 1)
	}
	h.clk.advance(time.Hour) // orphan_ttl: the row is forgotten
	h.cycle()
	h.noRow(model.KindDNS, appKey)
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("x", dnsOnly(other))))
	h.cycle()
	if row := h.row(model.KindDNS, model.DNSKey(appHost, model.DNSTypeAAAA, "")); row.Owner.AgentID != "edge" {
		t.Fatalf("row %+v", row)
	}
}

// A held owned rule whose hostname gains an unmanaged rule stays in the live
// configuration and its row reports the conflict.
func TestHeldTunnelRuleKeptBesideUnmanagedSameHost(t *testing.T) {
	h := newHarness(t)
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", tunOnly(tunSpec("web", "h1.example.com", "http://203.0.113.5:80")))))
	h.cycle()
	held := agentSnap("edge", h.clk.now())
	held.Complete = false
	h.st.setAgents(held)
	raw := strings.Replace(h.cf.TunnelConfigJSON(tunnelA), `{"service":"http_status:404"}`,
		`{"hostname":"h1.example.com","path":"/manual","service":"http://manual:80"},{"service":"http_status:404"}`, 1)
	h.cf.SetTunnelConfig(tunnelA, raw)
	h.src.set(ctr("loc", tunOnly(tunSpec("loc", "h2.example.com", "http://loc:80"))))
	h.cycle()
	cfg := h.cf.TunnelConfigJSON(tunnelA)
	if !strings.Contains(cfg, `"service":"http://203.0.113.5:80"`) || !strings.Contains(cfg, "http://manual:80") {
		t.Fatalf("config %s", cfg)
	}
	row := h.row(model.KindTunnel, model.TunnelKey(tunnelA, "h1.example.com", ""))
	if row.Status != model.StatusError || !strings.Contains(row.LastError, model.ConflictNotOwned) {
		t.Fatalf("held row %+v", row)
	}
}

// With rows of several sources on one hostname (written by older versions),
// the source of the oldest row keeps the hostname.
func TestMixedSourceRowsOldestWins(t *testing.T) {
	h := newHarness(t)
	h.st.seed(model.Resource{Kind: model.KindDNS, Key: model.DNSKey(appHost, model.DNSTypeAAAA, ""), Hostname: appHost,
		Owner: model.Owner{AgentID: "edge", ContainerName: "x", Service: "v6"}, RecordType: model.DNSTypeAAAA,
		Content: "2001:db8::66", TTL: 1, Status: model.StatusActive, Cleanup: true})
	h.clk.advance(time.Minute)
	h.st.seed(model.Resource{Kind: model.KindDNS, Key: appKey, Hostname: appHost,
		Owner: model.Owner{ContainerName: "web", Service: "web"}, RecordType: model.DNSTypeA,
		Content: "203.0.113.10", Proxied: true, TTL: 1, Status: model.StatusActive, Cleanup: true})
	aaaa := model.DNSSpec{Service: "v6", Hostname: appHost, Type: model.DNSTypeAAAA, Target: "2001:db8::66", Cleanup: true}
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("x", dnsOnly(aaaa))))
	h.cycle()
	rej := h.st.list(true)
	if len(rej) != 1 || rej[0].Owner.AgentID != "" || !strings.Contains(rej[0].LastError, "is owned by agent edge") {
		t.Fatalf("rejected %+v", rej)
	}
}
