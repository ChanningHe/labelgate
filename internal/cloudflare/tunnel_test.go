package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/channinghe/labelgate/internal/model"
)

// tunnelFixture is a live configuration with everything labelgate must not lose.
const tunnelFixture = `{
  "ingress": [
    {"hostname": "app.example.com", "path": "^/api", "service": "http://app:8080",
     "originRequest": {"connectTimeout": 10, "access": {"required": true, "teamName": "t", "audTag": ["aud1"]}},
     "x-future-field": {"nested": [1, 2.50, "s"]}},
    {"hostname": "manual.example.com", "service": "https://manual:443", "originRequest": {"noTLSVerify": true}},
    {"service": "http_status:503"}
  ],
  "originRequest": {"connectTimeout": 30, "http2Origin": true},
  "warp-routing": {"enabled": true},
  "x-top-level": "<keep & escape>"
}`

func canonical(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("invalid JSON %s: %v", raw, err)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

var testTunnel = Tunnel{Ref: "default", ID: "tun-root", AccountID: "acct1", Credential: "default"}

func TestTunnelConfigRoundTripPreservesUnknownFields(t *testing.T) {
	// Arrange
	f := newFakeCF(t)
	f.setTunnelConfig("tun-root", tunnelFixture)
	c := f.client(t, rootCfg())
	ctx := context.Background()

	// Act: GET then PUT unchanged.
	cfg, err := c.GetTunnelConfig(ctx, testTunnel)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PutTunnelConfig(ctx, testTunnel, cfg); err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(cfg.Ingress) != 3 || cfg.Ingress[0].Path() != "^/api" || !cfg.Ingress[2].IsCatchAll() {
		t.Fatalf("rules %+v", cfg.Ingress)
	}
	if got, want := canonical(t, f.tunnelConfig("tun-root")), canonical(t, []byte(tunnelFixture)); got != want {
		t.Fatalf("round trip changed config:\n got %s\nwant %s", got, want)
	}
	put := f.reqs()[len(f.reqs())-1]
	if put.Method != "PUT" || put.Path != "/accounts/acct1/cfd_tunnel/tun-root/configurations" {
		t.Fatalf("unexpected request %+v", put)
	}
}

func TestTunnelConfigMergeKeepsUnmodelledRuleFields(t *testing.T) {
	// Emulates the reconciler: modify an owned rule via With/Without and PUT.
	f := newFakeCF(t)
	f.setTunnelConfig("tun-root", tunnelFixture)
	c := f.client(t, rootCfg())
	ctx := context.Background()
	cfg, err := c.GetTunnelConfig(ctx, testTunnel)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Ingress[0] = cfg.Ingress[0].WithString(RuleService, "http://app:9090")
	if err := c.PutTunnelConfig(ctx, testTunnel, cfg); err != nil {
		t.Fatal(err)
	}
	again, err := c.GetTunnelConfig(ctx, testTunnel)
	if err != nil {
		t.Fatal(err)
	}
	r := again.Ingress[0]
	if r.Service() != "http://app:9090" || string(r.Fields["x-future-field"]) == "" || string(r.Fields[RuleOriginRequest]) == "" {
		t.Fatalf("rule lost fields: %v", r.Fields)
	}
	if string(again.Extra["warp-routing"]) != `{"enabled":true}` {
		t.Fatalf("extra lost: %v", again.Extra)
	}
}

func TestGetTunnelConfigNeverConfigured(t *testing.T) {
	tests := []struct{ name, cfg string }{{"null config", ""}, {"empty object", "{}"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCF(t)
			if tt.cfg == "" {
				f.addTunnel("tun-root")
			} else {
				f.setTunnelConfig("tun-root", tt.cfg)
			}
			c := f.client(t, rootCfg())
			cfg, err := c.GetTunnelConfig(context.Background(), testTunnel)
			if err != nil || len(cfg.Ingress) != 0 || len(cfg.Extra) != 0 {
				t.Fatalf("got %+v %v", cfg, err)
			}
		})
	}
}

func TestTunnelConfigErrors(t *testing.T) {
	f := newFakeCF(t)
	c := f.client(t, rootCfg())
	ctx := context.Background()
	if _, err := c.GetTunnelConfig(ctx, testTunnel); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown tunnel: %v", err)
	}
	f.inject("GET", "/accounts/acct1/cfd_tunnel/tun-root/configurations", 1, 200,
		`{"success":true,"errors":[],"result":{"config":{"ingress":"not-a-list"}}}`, nil)
	if _, err := c.GetTunnelConfig(ctx, testTunnel); err == nil {
		t.Fatal("malformed config must fail (caller must not PUT)")
	}
	if err := c.PutTunnelConfig(ctx, Tunnel{Ref: "x"}, TunnelConfig{}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("invalid tunnel: %v", err)
	}
	if _, err := c.GetTunnelConfig(ctx, Tunnel{ID: "t", AccountID: "a", Credential: "ghost"}); !errors.Is(err, ErrUnknownCredential) {
		t.Fatalf("unknown credential: %v", err)
	}
	bad := TunnelConfig{Extra: map[string]json.RawMessage{"x": json.RawMessage("{broken")}}
	if err := c.PutTunnelConfig(ctx, testTunnel, bad); err == nil {
		t.Fatal("unencodable config must fail")
	}
}
