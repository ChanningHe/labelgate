package reconcile

import (
	"strings"
	"testing"

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
