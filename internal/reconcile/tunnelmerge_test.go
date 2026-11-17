package reconcile

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

func parseConfig(t *testing.T, raw string) cloudflare.TunnelConfig {
	t.Helper()
	var cfg cloudflare.TunnelConfig
	if raw == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func keep(host, path, svc string) keepRule {
	return keepRule{Key: rowKey{Kind: model.KindTunnel, Key: host + "|" + path}, Host: host, Path: path, Service: svc}
}

func ids(pairs ...string) map[ingressID]bool {
	out := map[ingressID]bool{}
	for i := 0; i < len(pairs); i += 2 {
		out[ingressID{Host: pairs[i], Path: pairs[i+1]}] = true
	}
	return out
}

func TestMergeIngress(t *testing.T) {
	tests := []struct {
		name      string
		live      string
		owned     map[ingressID]bool
		keep      []keepRule
		want      []string
		changed   bool
		conflicts int
	}{
		{
			name: "empty live, nothing owned: no change",
			want: nil,
		},
		{
			name:    "empty live gets rules and default catch-all",
			keep:    []keepRule{keep("a.example.com", "", "http://a")},
			want:    []string{"a.example.com=>http://a", "=>http_status:404"},
			changed: true,
		},
		{
			name:    "unmanaged order and catch-all kept",
			live:    `{"ingress":[{"hostname":"z.example.com","service":"http://z"},{"hostname":"m.example.com","service":"http://m"},{"service":"http_status:503"}]}`,
			keep:    []keepRule{keep("a.example.com", "", "http://a")},
			want:    []string{"a.example.com=>http://a", "z.example.com=>http://z", "m.example.com=>http://m", "=>http_status:503"},
			changed: true,
		},
		{
			name:    "owned rule removed, unmanaged untouched",
			live:    `{"ingress":[{"hostname":"a.example.com","service":"http://a"},{"hostname":"m.example.com","service":"http://m"},{"service":"http_status:404"}]}`,
			owned:   ids("a.example.com", ""),
			want:    []string{"m.example.com=>http://m", "=>http_status:404"},
			changed: true,
		},
		{
			name:  "identical config: no change despite key order",
			live:  `{"ingress":[{"service":"http://a","hostname":"a.example.com"},{"service":"http_status:404"}]}`,
			owned: ids("a.example.com", ""),
			keep:  []keepRule{keep("a.example.com", "", "http://a")},
			want:  []string{"a.example.com=>http://a", "=>http_status:404"},
		},
		{
			name:    "paths specific first, wildcard after unmanaged",
			live:    `{"ingress":[{"hostname":"m.example.com","service":"http://m"},{"service":"http_status:404"}]}`,
			keep:    []keepRule{keep("*.example.com", "", "http://w"), keep("a.example.com", "", "http://r"), keep("a.example.com", "/b", "http://b"), keep("a.example.com", "/api/v1", "http://v"), keep("a.example.com", "/aa", "http://aa")},
			want:    []string{"a.example.com/api/v1=>http://v", "a.example.com/aa=>http://aa", "a.example.com/b=>http://b", "a.example.com=>http://r", "m.example.com=>http://m", "*.example.com=>http://w", "=>http_status:404"},
			changed: true,
		},
		{
			name:      "unmanaged rule with same hostname is a conflict",
			live:      `{"ingress":[{"hostname":"a.example.com","path":"/x","service":"http://h"},{"service":"http_status:404"}]}`,
			keep:      []keepRule{keep("a.example.com", "", "http://a"), keep("b.example.com", "", "http://b")},
			want:      []string{"b.example.com=>http://b", "a.example.com/x=>http://h", "=>http_status:404"},
			changed:   true,
			conflicts: 1,
		},
		{
			name:      "conflicting owned rule already live is kept verbatim",
			live:      `{"ingress":[{"hostname":"a.example.com","service":"http://old"},{"hostname":"a.example.com","path":"/m","service":"http://h"},{"service":"http_status:404"}]}`,
			owned:     ids("a.example.com", ""),
			keep:      []keepRule{keep("a.example.com", "", "http://new")},
			want:      []string{"a.example.com=>http://old", "a.example.com/m=>http://h", "=>http_status:404"},
			conflicts: 1,
		},
		{
			name:    "duplicate owned live rules collapse",
			live:    `{"ingress":[{"hostname":"a.example.com","service":"http://old"},{"hostname":"a.example.com","service":"http://dup"},{"service":"http_status:404"}]}`,
			owned:   ids("a.example.com", ""),
			keep:    []keepRule{keep("a.example.com", "", "http://a")},
			want:    []string{"a.example.com=>http://a", "=>http_status:404"},
			changed: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			live := parseConfig(t, tc.live)
			res := mergeIngress(live, tc.owned, tc.keep)
			var got []string
			if len(res.Config.Ingress) > 0 {
				got = ruleIDs(res.Config)
			}
			if !reflect.DeepEqual(got, tc.want) || res.Changed != tc.changed || len(res.Conflicts) != tc.conflicts {
				t.Fatalf("got %v changed=%v conflicts=%v", got, res.Changed, res.Conflicts)
			}
		})
	}
}

func TestMergeIngressKeepsExtraAndUnmodelledFields(t *testing.T) {
	live := parseConfig(t, `{"ingress":[{"hostname":"a.example.com","service":"http://a","x-custom":1,
		"originRequest":{"access":{"required":true},"noTLSVerify":true,"httpHostHeader":"old"}},
		{"service":"http_status:404"}],"warp-routing":{"enabled":true},"originRequest":{"proxyType":"socks"}}`)
	k := keep("a.example.com", "", "http://a2")
	k.Origin = model.OriginRequest{
		ConnectTimeout: 1500 * time.Millisecond, TLSTimeout: time.Second, TCPKeepAlive: time.Minute,
		KeepAliveTimeout: time.Minute, KeepAliveConnections: 4, OriginServerName: "o", CAPool: "/ca",
		NoHappyEyeballs: true, DisableChunkedEncoding: true, ProxyType: "socks",
	}
	res := mergeIngress(live, ids("a.example.com", ""), []keepRule{k})
	rule := res.Config.Ingress[0]
	var origin map[string]any
	if err := json.Unmarshal(rule.Fields["originRequest"], &origin); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"access": map[string]any{"required": true}, "connectTimeout": 2.0, "tlsTimeout": 1.0,
		"tcpKeepAlive": 60.0, "keepAliveTimeout": 60.0, "keepAliveConnections": 4.0,
		"originServerName": "o", "caPool": "/ca", "noHappyEyeballs": true,
		"disableChunkedEncoding": true, "proxyType": "socks",
	}
	if !reflect.DeepEqual(origin, want) {
		t.Fatalf("origin %v", origin)
	}
	if string(rule.Fields["x-custom"]) != "1" || rule.Service() != "http://a2" {
		t.Fatalf("rule %v", rule.Fields)
	}
	if string(res.Config.Extra["warp-routing"]) != `{"enabled":true}` || string(res.Config.Extra["originRequest"]) != `{"proxyType":"socks"}` {
		t.Fatalf("extra %v", res.Config.Extra)
	}

	// Clearing every modelled key drops originRequest entirely.
	live2 := parseConfig(t, `{"ingress":[{"hostname":"a.example.com","path":"/p","service":"x","originRequest":{"noTLSVerify":true}},{"service":"http_status:404"}]}`)
	res2 := mergeIngress(live2, ids("a.example.com", "/p"), []keepRule{keep("a.example.com", "/p", "x")})
	if _, ok := res2.Config.Ingress[0].Fields["originRequest"]; ok || !res2.Changed {
		t.Fatalf("empty originRequest kept: %v", res2.Config.Ingress[0].Fields)
	}
}

func TestCanonicalJSONInvalid(t *testing.T) {
	if got := canonicalJSON(func() {}); got == canonicalJSON(1) {
		t.Fatal("invalid input compared equal")
	}
	if canonicalJSON(json.RawMessage(`{"b":1,"a":[1,2]}`)) != canonicalJSON(json.RawMessage(`{"a":[1,2], "b":1}`)) {
		t.Fatal("key order matters")
	}
}
