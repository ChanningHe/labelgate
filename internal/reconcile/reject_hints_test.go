package reconcile

import (
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
)

const mainConfigHint = "main instance config"

func namedHintPolicy() AgentPolicy {
	return AgentPolicy{Allowed: true, Hostnames: []string{"*.edge.example.com"}, Tunnels: []string{"default"}}
}

func acceptHintPolicy() AgentPolicy {
	p := namedHintPolicy()
	p.viaAccept = true
	return p
}

func hintClaim(id string, p AgentPolicy, res model.Resource) claim {
	return claim{Res: res, Src: &sourceInfo{AgentID: id, Policy: p}, Tunnel: cloudflare.Tunnel{Ref: "other"}}
}

func tunnelRes(host, service string) model.Resource {
	return model.Resource{Kind: model.KindTunnel, Hostname: host, Service: service, TunnelID: "tid"}
}

// wantAll fails unless msg contains every part.
func wantAll(t *testing.T, msg string, parts ...string) {
	t.Helper()
	if msg == "" {
		t.Fatal("not rejected")
	}
	for _, p := range parts {
		if !strings.Contains(msg, p) {
			t.Errorf("message %q lacks %q", msg, p)
		}
	}
	if strings.ContainsAny(msg, "\n\r") {
		t.Errorf("message %q is not one line", msg)
	}
}

func TestPolicyKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.Agent.Enabled = true
	cfg.Agent.AcceptToken = "shared"
	cfg.Agent.Agents = map[string]config.AgentEntry{"edge1": {Token: "t"}}
	p := PoliciesFromConfig(cfg)
	if got := p("edge1").key("edge1", "services"); got != "agent.agents.edge1.services" {
		t.Errorf("named key %q", got)
	}
	if got := p("stranger").key("stranger", "services"); got != "agent.accept_services" {
		t.Errorf("accept key %q", got)
	}
	if got := p("stranger").fixHint("stranger", "tunnels"); !strings.Contains(got, "LABELGATE_AGENT_ACCEPT_TUNNELS") {
		t.Errorf("accept hint %q", got)
	}
}

func TestServiceHostnameRejectNamesFix(t *testing.T) {
	err := checkAgentService("edge1", namedHintPolicy(), "http://traefik:80")
	if err == nil {
		t.Fatal("accepted")
	}
	wantAll(t, err.Error(), `agent "edge1"`, `"traefik"`, `add "traefik" to agent.agents.edge1.services`, mainConfigHint)

	err = checkAgentService("edge9", acceptHintPolicy(), "http://traefik:80")
	if err == nil {
		t.Fatal("accepted")
	}
	wantAll(t, err.Error(), `agent "edge9"`, `add "traefik" to agent.accept_services`, "LABELGATE_AGENT_ACCEPT_SERVICES", mainConfigHint)
	if strings.Contains(err.Error(), "agent.agents.") {
		t.Errorf("accept message names a named-agent key: %q", err)
	}
}

func TestServiceForbiddenAddrRejectNamesCategory(t *testing.T) {
	tests := []struct{ url, category, entry string }{
		{"http://127.0.0.1:80", "loopback address 127.0.0.1", "127.0.0.1"},
		{"http://169.254.169.254/x", "link-local address 169.254.169.254", "169.254.169.254"},
		{"http://100.100.100.200", "carrier-grade NAT address 100.100.100.200", "100.100.100.200"},
		{"http://[::1]:80", "loopback address ::1", "::1"},
		{"tcp://224.0.0.1:9", "multicast address 224.0.0.1", "224.0.0.1"},
	}
	for _, tc := range tests {
		for _, p := range []AgentPolicy{namedHintPolicy(), acceptHintPolicy()} {
			err := checkAgentService("edge1", p, tc.url)
			if err == nil {
				t.Fatalf("%s: accepted", tc.url)
			}
			wantAll(t, err.Error(), `agent "edge1"`, tc.category, `add "`+tc.entry+`"`, p.key("edge1", "services"), mainConfigHint)
		}
	}
}

func TestServiceUnfixableRejectsSayWhatToUse(t *testing.T) {
	for _, u := range []string{"unix:/var/run/docker.sock", "bastion", "ftp://203.0.113.5"} {
		err := checkAgentService("edge1", namedHintPolicy(), u)
		if err == nil {
			t.Fatalf("%s: accepted", u)
		}
		wantAll(t, err.Error(), `agent "edge1"`, u, "http, https, tcp, ssh or rdp", "no allow-list can")
	}
	err := checkAgentService("edge1", namedHintPolicy(), "http://user:pw@203.0.113.5")
	if err == nil {
		t.Fatal("user info accepted")
	}
	wantAll(t, err.Error(), `agent "edge1"`, "user info")
	if strings.Contains(err.Error(), "pw") {
		t.Errorf("password leaked: %q", err)
	}
	err = checkAgentService("edge1", namedHintPolicy(), "http://127.1")
	if err == nil {
		t.Fatal("shorthand accepted")
	}
	wantAll(t, err.Error(), `agent "edge1"`, `"127.1"`, "never")
	if strings.Contains(err.Error(), "add ") {
		t.Errorf("suggests an allow-list entry that cannot work: %q", err)
	}
}

func TestAgentRejectHostnameAndTunnelNameKeys(t *testing.T) {
	allowed := map[string]bool{"tid": false}
	for _, p := range []AgentPolicy{namedHintPolicy(), acceptHintPolicy()} {
		host := tunnelRes("app.example.com", "http://203.0.113.5")
		wantAll(t, agentReject(hintClaim("edge1", p, host), allowed),
			`agent "edge1"`, `"app.example.com"`, `add "app.example.com"`, p.key("edge1", "hostnames"), mainConfigHint)

		wild := tunnelRes("*.example.com", "http://203.0.113.5")
		wantAll(t, agentReject(hintClaim("edge1", p, wild), allowed),
			`agent "edge1"`, "wildcard", `add "*.example.com"`, p.key("edge1", "hostnames"), mainConfigHint)

		tun := tunnelRes("a.edge.example.com", "http://203.0.113.5")
		wantAll(t, agentReject(hintClaim("edge1", p, tun), allowed),
			`agent "edge1"`, `tunnel "other"`, `add "other"`, p.key("edge1", "tunnels"), mainConfigHint)

		svc := tunnelRes("a.edge.example.com", "http://web:80")
		wantAll(t, agentReject(hintClaim("edge1", p, svc), map[string]bool{"tid": true}),
			`agent "edge1"`, `add "web"`, p.key("edge1", "services"), mainConfigHint)
	}
}

func TestAgentRejectTunnelCNAMESuggestsTunnelLabel(t *testing.T) {
	res := model.Resource{Kind: model.KindDNS, Hostname: "a.edge.example.com", RecordType: model.DNSTypeCNAME, Content: "x." + tunnelDomain}
	wantAll(t, agentReject(hintClaim("edge1", namedHintPolicy(), res), nil),
		`agent "edge1"`, tunnelDomain, "tunnel label", "no allow-list")
}
