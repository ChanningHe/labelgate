package reconcile

import (
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

const evilTarget = "198.51.100.66"

// hostileAgent is a second agent with a standing claim on appHost.
func hostileAgent(at time.Time) model.Snapshot {
	s := dnsSpec("evil", appHost)
	s.Target = evilTarget
	return agentSnap("a-evil", at, ctr("x", dnsOnly(s)))
}

func (h *harness) wantNoEvilRecord() {
	h.t.Helper()
	for _, r := range h.records() {
		if r.Content == evilTarget {
			h.t.Fatalf("hostname handed to agent a-evil: %+v", h.records())
		}
	}
}

func victimSpec() model.DNSSpec {
	s := dnsSpec("web", appHost)
	s.Target = "192.0.2.1"
	return s
}

// A source switching its hostname from DNS to tunnel keeps the hostname:
// the superseded record is replaced at once and another source's standing
// claim stays rejected.
func TestClassSwitchNeverHandsHostnameToOtherSource(t *testing.T) {
	h := newHarness(t)
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", dnsOnly(victimSpec()))))
	h.cycle()
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", dnsOnly(victimSpec()))), hostileAgent(h.clk.now()))
	h.cycle()
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", tunOnly(tunSpec("web", appHost, "http://203.0.113.5:80")))),
		hostileAgent(h.clk.now()))
	for range 2 {
		h.cycle()
		h.wantNoEvilRecord()
		h.wantOnlyRejected("a-evil", "is already managed by agent edge", 1)
	}
	if recs := h.recordsNamed(appHost); len(recs) != 1 || recs[0].Type != model.DNSTypeCNAME {
		t.Fatalf("records %+v", recs)
	}
}

// A container rename hands the same record to the new container at once;
// the old container's expiry never frees the hostname for another source.
func TestRenameThenExpiryKeepsHostname(t *testing.T) {
	h := newHarness(t)
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", dnsOnly(victimSpec()))))
	h.cycle()
	recID := h.row(model.KindDNS, appKey).CFID
	set := func(name string) {
		h.st.setAgents(agentSnap("edge", h.clk.now(), ctr(name, dnsOnly(victimSpec()))), hostileAgent(h.clk.now()))
	}
	set("web2")
	h.cf.ResetCalls()
	h.cycle()
	row := h.row(model.KindDNS, appKey)
	if row.Owner.ContainerName != "web2" || row.CFID != recID || row.Status != model.StatusActive {
		t.Fatalf("row %+v", row)
	}
	if h.mutations() != 0 {
		t.Fatalf("rename mutated: %+v", h.cf.Calls())
	}
	h.clk.advance(31 * time.Minute)
	set("web2")
	h.cycle()
	h.wantNoEvilRecord()
	h.wantOnlyRejected("a-evil", "is already managed by agent edge", 1)
}

// A renamed container takes over its tunnel rule without waiting for
// remove_delay.
func TestRenameContainerHandsOverTunnelRule(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80"))))
	h.cycle()
	h.src.set(ctr("web2", tunOnly(tunSpec("web", appHost, "http://web2:80"))))
	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("status %+v", st)
	}
	if got := strings.Join(ruleIDs(h.tunnelConfig(tunnelA)), " "); !strings.Contains(got, "web2") {
		t.Fatalf("rules %s", got)
	}
	h.assertIdempotent()
}

// A renamed service label takes over the record without waiting.
func TestRenameServiceLabelHandsOverRecord(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	recID := h.row(model.KindDNS, appKey).CFID
	h.src.set(ctr("web", dnsOnly(dnsSpec("www", appHost))))
	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("status %+v", st)
	}
	if row := h.row(model.KindDNS, appKey); row.Owner.Service != "www" || row.CFID != recID {
		t.Fatalf("row %+v", row)
	}
}

// Another container of the same source may switch the hostname's class as
// soon as the incumbent stops claiming it.
func TestOtherContainerSwitchesClassAtOnce(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	h.src.set(ctr("proxy", tunOnly(tunSpec("web", appHost, "http://proxy:80"))))
	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("status %+v", st)
	}
	h.noRow(model.KindDNS, appKey)
	if recs := h.recordsNamed(appHost); len(recs) != 1 || recs[0].Type != model.DNSTypeCNAME {
		t.Fatalf("records %+v", recs)
	}
	h.assertIdempotent()
}

// While the incumbent still claims, a same-source claim of another class is
// rejected; same-source competitors for one key keep the row's owner.
func TestIncumbentKeepsClassAndKeyWhileClaiming(t *testing.T) {
	h := newHarness(t)
	web := ctr("web", dnsOnly(dnsSpec("web", appHost)))
	h.src.set(web)
	h.cycle()
	h.src.set(web, ctr("aa", dnsOnly(dnsSpec("web", appHost))), ctr("ab", tunOnly(tunSpec("t", appHost, "http://ab:80"))))
	for range 3 {
		h.cycle()
		if row := h.row(model.KindDNS, appKey); row.Owner.ContainerName != "web" {
			t.Fatalf("row %+v", row)
		}
		if rej := h.st.list(true); len(rej) != 2 {
			t.Fatalf("rejected %+v", rej)
		}
	}
}

// Compose recreate (same name, new ID, absent one cycle) re-attaches with no
// Cloudflare mutation.
func TestComposeRecreateReattachesWithoutMutation(t *testing.T) {
	h := newHarness(t)
	c := ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80")))
	h.src.set(c)
	h.cycle()
	h.src.set()
	h.cf.ResetCalls()
	h.cycle()
	c.ID = "id-web-new"
	h.src.set(c)
	if st := h.cycle(); st.State != SyncSuccess || len(h.st.list(true)) != 0 {
		t.Fatalf("status %+v", st)
	}
	if h.mutations() != 0 {
		t.Fatalf("recreate mutated: %+v", h.cf.Calls())
	}
}

// A row of another source (mixed legacy data) is never taken over by the
// hostname's owning source; it follows its own lifecycle.
func TestMixedSourceRowIsNotTakenOver(t *testing.T) {
	h := newHarness(t)
	h.st.seed(model.Resource{Kind: model.KindDNS, Key: model.DNSKey(appHost, model.DNSTypeAAAA, ""), Hostname: appHost,
		Owner: model.Owner{ContainerName: "web", Service: "v6"}, RecordType: model.DNSTypeAAAA,
		Content: "2001:db8::5", TTL: 1, Status: model.StatusActive, Cleanup: true})
	h.clk.advance(time.Minute)
	h.st.seed(model.Resource{Kind: model.KindDNS, Key: appKey, Hostname: appHost,
		Owner: model.Owner{AgentID: "edge", ContainerName: "x", Service: "web"}, RecordType: model.DNSTypeA,
		Content: "198.51.100.7", Proxied: true, TTL: 1, Status: model.StatusActive, Cleanup: true})
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	if row := h.row(model.KindDNS, appKey); row.Owner.AgentID != "edge" {
		t.Fatalf("row %+v", row)
	}
	rej := h.st.list(true)
	if len(rej) != 1 || rej[0].Owner.AgentID != "" || !strings.Contains(rej[0].LastError, "is already managed by agent edge") {
		t.Fatalf("rejected %+v", rej)
	}
}
