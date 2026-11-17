package reconcile

import (
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/model"
)

// Every known way to reach a local or metadata address without an IP
// literal check: DNS names, inet_aton forms, embedded IPv4 in IPv6.
var ssrfBypasses = []string{
	"http://127.0.0.1.nip.io", "http://metadata.google.internal/computeMetadata/v1/",
	"http://host.docker.internal:8080", "http://web:80", "http://db.internal:5432",
	"http://127.1", "http://2130706433", "http://0x7f000001", "http://0177.0.0.1", "http://0",
	"http://0x7f.0.0.1", "http://127.0.0.1.", "http://localhost..", "http://LOCALHOST",
	"http://[::127.0.0.1]", "http://[::ffff:127.0.0.1]", "http://[::ffff:169.254.169.254]",
	"http://[fec0::1]", "http://[64:ff9b::a9fe:a9fe]", "http://[64:ff9b::7f00:1]",
	"http://[fe80::1%25eth0]", "http://0.1.2.3", "http://[::]", "udp://203.0.113.5:53",
	"http://127.0.0.1:80", "http://[::1]:80", "http://169.254.169.254/latest", "https://[fe80::1]:443",
	"http://0.0.0.0:80", "http://localhost:8080", "http://a.localhost", "tcp://224.0.0.1:9",
	"unix:/var/run/docker.sock", "unix+tls:/tmp/x.sock", "bastion", "http://",
}

func TestAgentServiceRejectsSSRFBypasses(t *testing.T) {
	for _, u := range ssrfBypasses {
		if err := checkAgentService(u, nil); err == nil {
			t.Errorf("%s: accepted", u)
		}
	}
}

func TestAgentServiceDefaultAllows(t *testing.T) {
	for _, u := range []string{
		"https://203.0.113.5", "http://198.51.100.4:8080/x", "http://[2001:db8::1]:8080", "hello_world",
		"http_status:404", "tcp://198.51.100.4:22", "ssh://203.0.113.9:22", "rdp://203.0.113.9:3389",
		"http://10.0.0.5", "http://172.17.0.5:80",
	} {
		if err := checkAgentService(u, nil); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestAgentServiceAllowList(t *testing.T) {
	allow := []string{"web", "*.svc.internal", "10.0.0.0/8", "127.0.0.1", "fd00::/8", "*"}
	for _, u := range []string{
		"http://web:80", "http://a.svc.internal", "http://10.1.2.3", "http://127.0.0.1:8080",
		"http://[fd00::5]:80", "http://metadata.google.internal",
	} {
		if err := checkAgentService(u, allow); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	// inet_aton forms and malformed names are never allow-listed by globs.
	for _, u := range []string{
		"http://127.1", "http://2130706433", "http://0x7f000001", "http://0177.0.0.1", "http://0",
		"http://localhost..", "http://[::ffff:127.0.0.2]", "unix:/x.sock",
	} {
		if err := checkAgentService(u, allow); err == nil {
			t.Errorf("%s: accepted", u)
		}
	}
}

func TestAgentServicesPolicyEndToEnd(t *testing.T) {
	h := newHarness(t, func(s *Settings) {
		s.Agents = func(string) AgentPolicy {
			return AgentPolicy{Allowed: true, Tunnels: []string{""}, Services: []string{"web"}}
		}
	})
	ok := tunSpec("web", appHost, "http://web:80")
	bad := tunSpec("meta", "meta.example.com", "http://metadata.google.internal")
	h.st.setAgents(agentSnap("edge", h.clk.now(), ctr("web", tunOnly(ok, bad))))
	h.cycle()
	wantStatus(t, h.row(model.KindTunnel, ruleKey), model.StatusActive)
	rej := h.st.list(true)
	if len(rej) != 1 || rej[0].Hostname != "meta.example.com" || !strings.Contains(rej[0].LastError, "not allowed") {
		t.Fatalf("rejected %+v", rej)
	}
}
