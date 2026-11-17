package reconcile

import (
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/model"
)

func TestExistingOwnerWinsAndNeverFlaps(t *testing.T) {
	h := newHarness(t)
	incumbent := ctr("zeta", dnsOnly(dnsSpec("web", appHost)))
	h.src.set(incumbent)
	h.cycle()
	recID := h.row(model.KindDNS, appKey).CFID

	newcomer := ctr("alpha", dnsOnly(dnsSpec("web", appHost))) // sorts first, but arrives later
	h.src.set(incumbent, newcomer)
	for i := range 5 {
		st := h.cycle()
		if st.State != SyncError || !strings.Contains(st.Error, "already claimed by local container zeta") {
			t.Fatalf("cycle %d: %+v", i, st)
		}
		if row := h.row(model.KindDNS, appKey); row.Owner.ContainerName != "zeta" || row.Status != model.StatusActive {
			t.Fatalf("cycle %d: owner flapped: %+v", i, row)
		}
		rej := h.st.list(true)
		if len(rej) != 1 || rej[0].Owner.ContainerName != "alpha" {
			t.Fatalf("rejected %+v", rej)
		}
	}
	h.assertIdempotent()

	h.src.set(newcomer) // incumbent leaves: newcomer takes over the same record
	h.cycle()
	row := h.row(model.KindDNS, appKey)
	if row.Owner.ContainerName != "alpha" || row.CFID != recID || row.Status != model.StatusActive {
		t.Fatalf("takeover %+v", row)
	}
	if len(h.st.list(true)) != 0 {
		t.Fatal("rejection not cleared")
	}
}

func TestFirstClaimOrderIsDeterministic(t *testing.T) {
	for range 10 {
		h := newHarness(t)
		h.src.set(ctr("b", dnsOnly(dnsSpec("web", appHost))), ctr("a", dnsOnly(dnsSpec("web", appHost))))
		h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("a", dnsOnly(dnsSpec("web", appHost)))))
		h.cycle()
		if row := h.row(model.KindDNS, appKey); row.Owner != (model.Owner{ContainerName: "a", ContainerID: "id-a", Service: "web"}) {
			t.Fatalf("winner %+v", row.Owner)
		}
		if n := len(h.st.list(true)); n != 2 {
			t.Fatalf("rejected %d", n)
		}
	}
}

func TestAgentIncumbentBeatsLocalNewcomer(t *testing.T) {
	h := newHarness(t)
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", dnsOnly(dnsSpec("web", appHost)))))
	h.cycle()
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	for range 3 {
		h.cycle()
		if row := h.row(model.KindDNS, appKey); row.Owner.AgentID != "edge" {
			t.Fatalf("local newcomer won: %+v", row)
		}
	}
	rej := h.st.list(true)
	if len(rej) != 1 || rej[0].Owner.AgentID != "" {
		t.Fatalf("rejected %+v", rej)
	}
}

func TestHostnameClassConflicts(t *testing.T) {
	cname := model.DNSSpec{Service: "c", Hostname: appHost, Type: model.DNSTypeCNAME, Target: "x.example.net", Proxied: true, Cleanup: true}
	other := tunSpec("t2", appHost, "http://b")
	other.Tunnel = "other"
	tests := []struct {
		name   string
		first  model.ParsedLabels
		second model.ParsedLabels
		class  string
	}{
		{"dns vs tunnel", dnsOnly(dnsSpec("web", appHost)), tunOnly(tunSpec("t", appHost, "http://a")), classRecords},
		{"tunnel vs dns", tunOnly(tunSpec("t", appHost, "http://a")), dnsOnly(dnsSpec("web", appHost)), classTunnelPrefix + tunnelA},
		{"cname vs A", dnsOnly(cname), dnsOnly(dnsSpec("web", appHost)), classCNAME},
		{"two tunnels", tunOnly(tunSpec("t", appHost, "http://a")), tunOnly(other), classTunnelPrefix + tunnelA},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.src.set(ctr("zz", tc.first))
			h.cycle()
			h.src.set(ctr("zz", tc.first), ctr("aa", tc.second))
			h.cf.ResetCalls()
			st := h.cycle()
			if st.State != SyncError {
				t.Fatalf("status %+v", st)
			}
			rej := h.st.list(true)
			if len(rej) != 1 || rej[0].Owner.ContainerName != "aa" || !strings.Contains(rej[0].LastError, "as "+tc.class) {
				t.Fatalf("rejected %+v", rej)
			}
			if h.mutations() != 0 {
				t.Fatalf("conflict mutated: %+v", h.cf.Calls())
			}
		})
	}
}

func TestSameServiceDNSAndTunnelHostnameIsRejected(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", model.ParsedLabels{DNS: []model.DNSSpec{dnsSpec("web", appHost)}, Tunnels: []model.TunnelSpec{tunSpec("web", appHost, "http://web")}}))
	h.cycle()
	rej := h.st.list(true)
	if len(rej) != 1 || rej[0].Kind != model.KindTunnel {
		t.Fatalf("rejected %+v", rej)
	}
	wantStatus(t, h.row(model.KindDNS, appKey), model.StatusActive)
}

func TestStoreErrorsAbortCycle(t *testing.T) {
	for _, op := range []string{"AgentSnapshots", "AllResources", "ReplaceRejected", "UpsertResource", "UpdateResource"} {
		t.Run(op, func(t *testing.T) {
			h := newHarness(t)
			h.src.set(ctr("a", dnsOnly(dnsSpec("web", appHost))), ctr("b", dnsOnly(dnsSpec("web", appHost))))
			h.st.failNext(op, errBoom)
			st := h.cycle()
			if st.State != SyncError || !strings.Contains(st.Error, "boom") {
				t.Fatalf("status %+v", st)
			}
			if op != "UpdateResource" && h.mutations() != 0 {
				t.Fatalf("mutated after store failure: %+v", h.cf.Calls())
			}
			if st := h.cycle(); !strings.Contains(st.Error, "already claimed") || strings.Contains(st.Error, "boom") {
				t.Fatalf("recovery %+v", st)
			}
			wantStatus(t, h.row(model.KindDNS, appKey), model.StatusActive)
		})
	}
}
