package reconcile

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

func agentSnap(id string, at time.Time, ctrs ...model.Container) model.Snapshot {
	return model.Snapshot{AgentID: id, Complete: true, Containers: ctrs, PublicIP: "198.51.100.7", PublicIPv6: "2001:db8::7", TakenAt: at}
}

func TestAgentGraceKeepsThenOrphans(t *testing.T) {
	h := newHarness(t)
	seen := h.clk.now()
	h.st.setAgents(agentSnap("edge", seen, ctr("web", dnsOnly(dnsSpec("web", appHost)))))
	h.cycle()
	row := h.row(model.KindDNS, appKey)
	if row.Owner.AgentID != "edge" || h.recordsNamed(appHost)[0].Content != "198.51.100.7" {
		t.Fatalf("row %+v records %+v", row, h.records())
	}

	h.clk.advance(23 * time.Hour) // disconnected: last_seen does not move
	h.cf.ResetCalls()
	h.cycle()
	wantStatus(t, h.row(model.KindDNS, appKey), model.StatusActive)
	if h.mutations() != 0 {
		t.Fatalf("within grace mutated: %+v", h.cf.Calls())
	}

	h.clk.advance(time.Hour + time.Minute)
	h.cycle()
	wantStatus(t, h.row(model.KindDNS, appKey), model.StatusPendingCleanup)
	if len(h.records()) != 1 {
		t.Fatal("deleted before remove_delay")
	}
	h.clk.advance(30 * time.Minute)
	h.cycle()
	h.noRow(model.KindDNS, appKey)
	if len(h.records()) != 0 {
		t.Fatal("orphaned agent record not cleaned up")
	}
}

// A connected agent is present even when its last report is older than the
// grace window (e.g. a short agent_grace and a long heartbeat).
func TestConnectedAgentIsPresentBeyondGrace(t *testing.T) {
	connected := true
	h := newHarness(t, func(s *Settings) {
		s.AgentGrace = time.Minute
		s.Connected = func(id string) bool { return connected && id == "edge" }
	})
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", dnsOnly(dnsSpec("web", appHost)))))
	h.cycle()
	h.clk.advance(10 * time.Minute)
	h.cf.ResetCalls()
	h.cycle()
	if row := h.row(model.KindDNS, appKey); row.Status != model.StatusActive || !row.OrphanedAt.IsZero() {
		t.Fatalf("connected agent orphaned: %+v", row)
	}
	if h.mutations() != 0 {
		t.Fatalf("mutations %+v", h.cf.Calls())
	}
	if d := h.r.runCycle(context.Background()); !d.IsZero() && !d.After(h.clk.now()) {
		t.Fatalf("deadline in the past: %s", d)
	}
	connected = false
	h.cycle()
	wantStatus(t, h.row(model.KindDNS, appKey), model.StatusPendingCleanup)
}

func TestAgentNotAllowedOrphans(t *testing.T) {
	allowed := true
	h := newHarness(t, func(s *Settings) {
		s.Agents = func(string) AgentPolicy { return AgentPolicy{Allowed: allowed, Tunnels: []string{""}} }
	})
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", dnsOnly(dnsSpec("web", appHost)))))
	h.cycle()
	allowed = false
	h.cycle()
	wantStatus(t, h.row(model.KindDNS, appKey), model.StatusPendingCleanup)
}

func TestAgentIncompleteSnapshotIsHeld(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.RemoveDelay = 0 })
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", dnsOnly(dnsSpec("web", appHost)))))
	h.cycle()
	before := h.row(model.KindDNS, appKey)
	snap := agentSnap("edge", h.clk.now())
	snap.Complete = false
	h.st.setAgents(snap)
	h.cf.ResetCalls()
	h.clk.advance(time.Hour)
	h.cycle()
	if h.mutations() != 0 || !sameRow(before, h.row(model.KindDNS, appKey)) {
		t.Fatalf("held agent touched: calls %+v", h.cf.Calls())
	}
}

func TestAgentPolicyRejections(t *testing.T) {
	policy := AgentPolicy{Allowed: true, Hostnames: []string{"*.agents.example.com"}, Tunnels: []string{""}}
	tests := []struct {
		name string
		pl   model.ParsedLabels
		want string
	}{
		{"hostname not allowed", dnsOnly(dnsSpec("web", appHost)), "hostnames allow-list"},
		{"tunnel not allowed", func() model.ParsedLabels {
			s := tunSpec("web", "a.agents.example.com", "http://web:80")
			s.Tunnel = "other"
			return tunOnly(s)
		}(), "allowed tunnels"},
		{"unknown tunnel", func() model.ParsedLabels {
			s := tunSpec("web", "a.agents.example.com", "http://web:80")
			s.Tunnel = "nope"
			return tunOnly(s)
		}(), "unknown tunnel"},
	}
	for _, u := range []string{
		"http://127.0.0.1:80", "http://[::1]:80", "http://169.254.169.254/latest", "https://[fe80::1]:443",
		"http://0.0.0.0:80", "http://localhost:8080", "http://a.localhost", "tcp://224.0.0.1:9",
		"unix:/var/run/docker.sock", "unix+tls:/tmp/x.sock", "bastion", "http://",
	} {
		tests = append(tests, struct {
			name string
			pl   model.ParsedLabels
			want string
		}{"ssrf " + u, tunOnly(tunSpec("web", "a.agents.example.com", u)), `agent "edge"`})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(s *Settings) { s.Agents = func(string) AgentPolicy { return policy } })
			h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", tc.pl)))
			st := h.cycle()
			if st.State != SyncError {
				t.Fatalf("status %+v", st)
			}
			rej := h.st.list(true)
			if len(rej) != 1 || rej[0].Owner.AgentID != "edge" || rej[0].Status != model.StatusError {
				t.Fatalf("rejected %+v", rej)
			}
			if tc.want != "" && !strings.Contains(rej[0].LastError, tc.want) && !strings.Contains(rej[0].LastError, "no host") {
				t.Fatalf("reason %q", rej[0].LastError)
			}
			if h.mutations() != 0 || len(h.st.list(false)) != 0 {
				t.Fatalf("rejected claim applied: %+v", h.cf.Calls())
			}
		})
	}
}

func TestAgentAllowedTunnelServices(t *testing.T) {
	h := newHarness(t, func(s *Settings) {
		s.Agents = func(string) AgentPolicy {
			return AgentPolicy{Allowed: true, Tunnels: []string{"other"}, DefaultTunnel: "OTHER", Services: []string{"web"}}
		}
	})
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", tunOnly(tunSpec("web", appHost, "http://web:80")))))
	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("status %+v", st)
	}
	h.row(model.KindTunnel, model.TunnelKey(tunnelB, appHost, ""))
}

func TestAgentAccessDefinitionsAreScopedToSource(t *testing.T) {
	h := newHarness(t)
	local := ctr("defs", model.ParsedLabels{Access: []model.AccessPolicyDef{teamDef("a@example.com")}})
	h.src.set(local)
	svc := dnsSpec("web", appHost)
	svc.Access = "team"
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", dnsOnly(svc))))
	h.cycle()
	rej := h.st.list(true)
	if len(rej) != 1 || !strings.Contains(rej[0].LastError, `access policy "team" is not defined: add labelgate.access.team.* labels`) {
		t.Fatalf("agent bound a local definition: %+v", rej)
	}
	if len(h.cf.Apps(testAccount)) != 0 {
		t.Fatal("access app created from a foreign source definition")
	}
}

func TestAgentRestartKeepsState(t *testing.T) {
	h := newHarness(t)
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", dnsOnly(dnsSpec("web", appHost)))))
	h.cycle()
	// A new process: same store and Cloudflare, agent not connected yet.
	r2, err := New(Deps{Settings: h.r.settings, Cloudflare: h.cf, Store: h.st, PublicIP: h.r.publicIP, Now: h.clk.now})
	if err != nil {
		t.Fatal(err)
	}
	r2.parse = testParse
	h.r = r2
	h.clk.advance(time.Hour)
	h.assertIdempotent()
	wantStatus(t, h.row(model.KindDNS, appKey), model.StatusActive)
}

func TestLabelErrorHoldsResources(t *testing.T) {
	tests := []struct {
		name  string
		agent bool
		key   string
	}{
		{"local invalid service value", false, "labelgate.dns.web.ttl"},
		{"local invalid defaults block", false, "labelgate.dns.default.proxied"},
		{"agent invalid service value", true, "labelgate.dns.web.proxied"},
		{"agent invalid defaults block", true, "labelgate.dns.default.ttl"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			set := func(c model.Container) {
				if tc.agent {
					h.st.setAgents(agentSnap("edge", h.clk.now(), c))
				} else {
					h.src.set(c)
				}
			}
			good := ctr("web", dnsOnly(dnsSpec("web", appHost)))
			set(good)
			h.cycle()
			rec := h.recordsNamed(appHost)[0]

			bad := ctr("web", model.ParsedLabels{Errors: []model.LabelError{{Key: tc.key, Message: "invalid value"}}})
			set(bad)
			h.cf.ResetCalls()
			st := h.cycle()
			if st.State != SyncError || !strings.Contains(st.Error, tc.key) {
				t.Fatalf("status %+v", st)
			}
			row := h.row(model.KindDNS, appKey)
			if row.Status != model.StatusError || !strings.Contains(row.LastError, "label error") || !row.OrphanedAt.IsZero() {
				t.Fatalf("row %+v", row)
			}
			h.clk.advance(2 * time.Hour)
			set(bad)
			h.cycle()
			if h.mutations() != 0 || len(h.recordsNamed(appHost)) != 1 || h.recordsNamed(appHost)[0] != rec {
				t.Fatalf("label error caused mutation: %+v", h.cf.Calls())
			}
			set(good)
			if st := h.cycle(); st.State != SyncSuccess {
				t.Fatalf("fixed labels: %+v", st)
			}
			wantStatus(t, h.row(model.KindDNS, appKey), model.StatusActive)
		})
	}
}

// An unknown property in a defaults block drops nothing: it must not hold
// (or flag) the rows of that type.
func TestUnknownDefaultsPropertyDoesNotHold(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.RemoveDelay = 0 })
	keep, gone := dnsSpec("web", appHost), dnsSpec("api", "api.example.com")
	h.src.set(ctr("web", dnsOnly(keep, gone)))
	h.cycle()
	h.src.set(ctr("web", model.ParsedLabels{
		DNS:    []model.DNSSpec{keep},
		Errors: []model.LabelError{{Key: "labelgate.dns.default.bogus", Message: `unknown property "bogus"`, Unknown: true}},
	}))
	h.cycle()
	wantStatus(t, h.row(model.KindDNS, appKey), model.StatusActive)
	h.noRow(model.KindDNS, model.DNSKey("api.example.com", model.DNSTypeA, "")) // removed service cleaned up
}

func TestLabelErrorScope(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.RemoveDelay = 0 })
	other := dnsSpec("api", "api.example.com")
	h.src.set(ctr("web", model.ParsedLabels{DNS: []model.DNSSpec{dnsSpec("web", appHost), other}, Tunnels: []model.TunnelSpec{tunSpec("t", "t.example.com", "http://web")}}))
	h.cycle()
	// Unknown property on "api" keeps the service; tunnel defaults error holds tunnel rows only;
	// "web" is removed without error and must be cleaned up.
	h.src.set(ctr("web", model.ParsedLabels{
		DNS: []model.DNSSpec{other},
		Errors: []model.LabelError{
			{Key: "labelgate.dns.api.bogus", Message: "unknown property"},
			{Key: "labelgate.tunnel.default.cleanup", Message: "invalid bool"},
		},
	}))
	h.cycle()
	h.noRow(model.KindDNS, appKey)
	wantStatus(t, h.row(model.KindDNS, model.DNSKey("api.example.com", model.DNSTypeA, "")), model.StatusActive)
	wantStatus(t, h.row(model.KindTunnel, model.TunnelKey(tunnelA, "t.example.com", "")), model.StatusError)
	wantStatus(t, h.row(model.KindDNS, model.DNSKey("t.example.com", model.DNSTypeCNAME, "")), model.StatusError)
	if len(h.recordsNamed("t.example.com")) != 1 {
		t.Fatal("held tunnel CNAME deleted")
	}
}
