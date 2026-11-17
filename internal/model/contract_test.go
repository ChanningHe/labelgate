package model

import "testing"

func TestIsOwnedComment(t *testing.T) {
	cases := []struct {
		comment string
		want    bool
	}{
		{"labelgate:abcd1234", true},
		{"labelgate:abcd1234 my note", true},
		{"labelgate:abcd12345", false},
		{"labelgate:abcd123", false},
		{"Managed by labelgate", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsOwnedComment("abcd1234", c.comment); got != c.want {
			t.Errorf("IsOwnedComment(%q) = %v, want %v", c.comment, got, c.want)
		}
	}
	if got := DNSComment("abcd1234", "note"); got != "labelgate:abcd1234 note" {
		t.Errorf("DNSComment = %q", got)
	}
}

func TestHostname(t *testing.T) {
	if got := NormalizeHostname("  App.Example.COM. "); got != "app.example.com" {
		t.Errorf("NormalizeHostname = %q", got)
	}
	valid := []string{"app.example.com", "*.example.com", "_xmpp._tcp.example.com", "a-b.example.com"}
	invalid := []string{"", "example", "a..example.com", "-a.example.com", "a.*.example.com", "a b.example.com"}
	for _, h := range valid {
		if !ValidHostname(h) {
			t.Errorf("ValidHostname(%q) = false", h)
		}
	}
	for _, h := range invalid {
		if ValidHostname(h) {
			t.Errorf("ValidHostname(%q) = true", h)
		}
	}
}

func TestIDsAndKeys(t *testing.T) {
	if !ValidAgentID("host-1_a") || ValidAgentID("") || ValidAgentID("!x") || ValidAgentID("Host") {
		t.Error("ValidAgentID mismatch")
	}
	if !ValidInstanceID("abcd1234") || ValidInstanceID("ab") || ValidInstanceID("ABCD1234") {
		t.Error("ValidInstanceID mismatch")
	}
	if DNSKey("a.example.com", DNSTypeA, "1.2.3.4") != "a.example.com|A" {
		t.Error("A key must exclude content")
	}
	if DNSKey("a.example.com", DNSTypeTXT, "v=spf1") != "a.example.com|TXT|v=spf1" {
		t.Error("TXT key must include content")
	}
	local := Owner{Service: "z"}
	agent := Owner{AgentID: "a", Service: "a"}
	if !local.Less(agent) || agent.Less(local) {
		t.Error("local owners must sort first")
	}
}
