package cloudflare

import (
	"encoding/json"
	"testing"
)

func TestTunnelConfigRoundTripKeepsUnknownFields(t *testing.T) {
	in := `{"ingress":[{"hostname":"a.example.com","service":"http://a:80","originRequest":{"access":{"required":true}},"x-custom":1},{"service":"http_status:404"}],"originRequest":{"connectTimeout":10},"warp-routing":{"enabled":true}}`
	var cfg TunnelConfig
	if err := json.Unmarshal([]byte(in), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Ingress) != 2 || cfg.Ingress[0].Hostname() != "a.example.com" || !cfg.Ingress[1].IsCatchAll() {
		t.Fatalf("unexpected rules: %+v", cfg.Ingress)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal([]byte(in), &a)
	_ = json.Unmarshal(out, &b)
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("round trip changed config:\n in: %s\nout: %s", ja, jb)
	}
}

func TestIngressRuleImmutable(t *testing.T) {
	r := NewIngressRule("A.example.com", "/api", "http://a")
	r2 := r.WithString(RuleService, "http://b").Without(RulePath)
	if r.Service() != "http://a" || r.Path() != "/api" {
		t.Fatal("original rule mutated")
	}
	if r2.Service() != "http://b" || r2.Path() != "" || r2.Hostname() != "a.example.com" {
		t.Fatalf("unexpected copy: %+v", r2)
	}
	empty, _ := json.Marshal(TunnelConfig{})
	if string(empty) != "{}" {
		t.Fatalf("empty config = %s", empty)
	}
}
