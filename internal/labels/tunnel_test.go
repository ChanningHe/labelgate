package labels

import (
	"reflect"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

func TestTunnel_Defaults(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.tunnel.web.hostname": "App.Example.com.",
		"labelgate.tunnel.web.service":  "http://app:80",
	})
	if len(got.Errors) != 0 || len(got.Tunnels) != 1 {
		t.Fatalf("unexpected: %+v", got)
	}
	want := model.TunnelSpec{Service: "web", Hostname: "app.example.com", URL: "http://app:80", Cleanup: true}
	if !reflect.DeepEqual(got.Tunnels[0], want) {
		t.Errorf("got %+v\nwant %+v", got.Tunnels[0], want)
	}
}

func TestTunnel_AllProperties(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.tunnel.web.hostname":   "app.example.com",
		"labelgate.tunnel.web.service":    "https://app:443",
		"labelgate.tunnel.web.tunnel":     " secondary ",
		"labelgate.tunnel.web.path":       `\.(jpg|png)$`,
		"labelgate.tunnel.web.credential": "company",
		"labelgate.tunnel.web.cleanup":    "false",
		"labelgate.tunnel.web.access":     "internal",
	})
	if len(got.Errors) != 0 || len(got.Tunnels) != 1 {
		t.Fatalf("unexpected: %+v", got)
	}
	want := model.TunnelSpec{
		Service: "web", Hostname: "app.example.com", URL: "https://app:443", Tunnel: "secondary",
		Path: `\.(jpg|png)$`, Credential: "company", Access: "internal",
	}
	if !reflect.DeepEqual(got.Tunnels[0], want) {
		t.Errorf("got %+v\nwant %+v", got.Tunnels[0], want)
	}
}

func TestTunnel_DefaultsBlock(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.tunnel.default.tunnel":     "main",
		"labelgate.tunnel.default.credential": "corp",
		"labelgate.tunnel.default.cleanup":    "false",
		"labelgate.tunnel.a.hostname":         "a.example.com",
		"labelgate.tunnel.a.service":          "http://a:80",
		"labelgate.tunnel.b.hostname":         "b.example.com",
		"labelgate.tunnel.b.service":          "http://b:80",
		"labelgate.tunnel.b.tunnel":           "other",
	})
	if len(got.Errors) != 0 || len(got.Tunnels) != 2 {
		t.Fatalf("unexpected: %+v", got)
	}
	a, b := got.Tunnels[0], got.Tunnels[1]
	if a.Tunnel != "main" || a.Credential != "corp" || a.Cleanup {
		t.Errorf("a: %+v", a)
	}
	if b.Tunnel != "other" {
		t.Errorf("b should override default tunnel: %+v", b)
	}

	bad := Parse("labelgate", L{
		"labelgate.tunnel.default.cleanup": "perhaps",
		"labelgate.tunnel.default.service": "http://x:80",
		"labelgate.tunnel.a.hostname":      "a.example.com",
		"labelgate.tunnel.a.service":       "http://a:80",
	})
	if len(bad.Tunnels) != 0 ||
		!hasErr(bad, "labelgate.tunnel.default.cleanup", "invalid boolean") ||
		!hasErr(bad, "labelgate.tunnel.default.service", "unknown property") ||
		!hasErr(bad, "labelgate.tunnel.a", "a labelgate.tunnel.default.* label is invalid") {
		t.Errorf("got %+v", bad)
	}
}

func TestTunnel_ServiceURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"http://localhost:8080", false},
		{"HTTPS://app", false},
		{"tcp://db:5432", false},
		{"ssh://dev:22", false},
		{"rdp://win:3389", false},
		{"udp://dns:53", false},
		{"unix:/var/run/app.sock", false},
		{"unix:///var/run/app.sock", false},
		{"unix+tls:/var/run/app.sock", false},
		{"hello_world", false},
		{"bastion", false},
		{"http_status:404", false},
		{"http_status:99", true},
		{"http_status:600", true},
		{"http_status:abc", true},
		{"ftp://x", true},
		{"http://", true},
		{"http:///path", true},
		{"http://bad host", true},
		{"http://host:notaport", true},
		{"unix:", true},
		{"unix:///", true},
		{"localhost:8080", true},
		{"nonsense", true},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := Parse("labelgate", L{
				"labelgate.tunnel.web.hostname": "app.example.com",
				"labelgate.tunnel.web.service":  tt.url,
			})
			if tt.wantErr {
				if len(got.Tunnels) != 0 || len(got.Errors) == 0 {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if len(got.Errors) != 0 || len(got.Tunnels) != 1 || got.Tunnels[0].URL != tt.url {
				t.Fatalf("unexpected: %+v", got)
			}
		})
	}
}

func TestTunnel_RequiredAndInvalid(t *testing.T) {
	tests := []struct {
		name    string
		labels  L
		wantKey string
		wantMsg string
	}{
		{"missing hostname", L{"service": "http://a:80"}, "labelgate.tunnel.web", "hostname is required: add labelgate.tunnel.web.hostname"},
		{"missing service", L{"hostname": "a.example.com"}, "labelgate.tunnel.web", "service is required: add labelgate.tunnel.web.service"},
		{"bad hostname", L{"hostname": "nodots", "service": "http://a:80"}, "labelgate.tunnel.web.hostname", "invalid hostname"},
		{"bad path regex", L{"hostname": "a.example.com", "service": "http://a:80", "path": "("}, "labelgate.tunnel.web.path", "invalid path regex"},
		{"bad cleanup", L{"hostname": "a.example.com", "service": "http://a:80", "cleanup": "x"}, "labelgate.tunnel.web.cleanup", "invalid boolean"},
		{"bad access ref", L{"hostname": "a.example.com", "service": "http://a:80", "access": "A B"}, "labelgate.tunnel.web.access", "invalid access policy"},
		{"bad scheme", L{"hostname": "a.example.com", "service": "gopher://a"}, "labelgate.tunnel.web.service", "unsupported service scheme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := L{}
			for k, v := range tt.labels {
				labels["labelgate.tunnel.web."+k] = v
			}
			got := Parse("labelgate", labels)
			if len(got.Tunnels) != 0 || !hasErr(got, tt.wantKey, tt.wantMsg) {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestTunnel_UnknownPropertyKeepsService(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.tunnel.web.hostname":     "a.example.com",
		"labelgate.tunnel.web.service":      "http://a:80",
		"labelgate.tunnel.web.colour":       "red",
		"labelgate.tunnel.web.origin.bogus": "1",
	})
	if len(got.Tunnels) != 1 {
		t.Fatalf("service must be kept: %+v", got)
	}
	if !hasErr(got, "labelgate.tunnel.web.colour", "unknown property") ||
		!hasErr(got, "labelgate.tunnel.web.origin.bogus", "unknown property") {
		t.Fatalf("errors: %+v", got.Errors)
	}
}

func TestTunnel_Origin(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.tunnel.web.hostname":                        "a.example.com",
		"labelgate.tunnel.web.service":                         "https://a:443",
		"labelgate.tunnel.web.origin.connect_timeout":          "30s",
		"labelgate.tunnel.web.origin.tls_timeout":              "10",
		"labelgate.tunnel.web.origin.tcp_keepalive":            "2m",
		"labelgate.tunnel.web.origin.keep_alive_timeout":       "90s",
		"labelgate.tunnel.web.origin.keep_alive_connections":   "100",
		"labelgate.tunnel.web.origin.no_tls_verify":            "true",
		"labelgate.tunnel.web.origin.origin_server_name":       " origin.example.com ",
		"labelgate.tunnel.web.origin.ca_pool":                  "/ca.pem",
		"labelgate.tunnel.web.origin.http_host_header":         "host.example.com",
		"labelgate.tunnel.web.origin.no_happy_eyeballs":        "yes",
		"labelgate.tunnel.web.origin.disable_chunked_encoding": "on",
		"labelgate.tunnel.web.origin.proxy_type":               "SOCKS",
	})
	if len(got.Errors) != 0 || len(got.Tunnels) != 1 {
		t.Fatalf("unexpected: %+v", got)
	}
	want := model.OriginRequest{
		ConnectTimeout: 30 * time.Second, TLSTimeout: 10 * time.Second,
		TCPKeepAlive: 2 * time.Minute, KeepAliveTimeout: 90 * time.Second,
		KeepAliveConnections: 100, NoTLSVerify: true,
		OriginServerName: "origin.example.com", CAPool: "/ca.pem", HTTPHostHeader: "host.example.com",
		NoHappyEyeballs: true, DisableChunkedEncoding: true, ProxyType: "socks",
	}
	if got.Tunnels[0].Origin != want {
		t.Errorf("got  %+v\nwant %+v", got.Tunnels[0].Origin, want)
	}
}

func TestTunnel_OriginErrors(t *testing.T) {
	tests := []struct{ key, value, msg string }{
		{"connect_timeout", "soon", "invalid duration"},
		{"connect_timeout", "-5s", "negative"},
		{"connect_timeout", "1500ms", "whole number of seconds"},
		{"keep_alive_connections", "many", "invalid integer"},
		{"keep_alive_connections", "-1", "out of range"},
		{"no_tls_verify", "dunno", "invalid boolean"},
		{"proxy_type", "http", "invalid proxy_type"},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			got := Parse("labelgate", L{
				"labelgate.tunnel.web.hostname":         "a.example.com",
				"labelgate.tunnel.web.service":          "http://a:80",
				"labelgate.tunnel.web.origin." + tt.key: tt.value,
			})
			if len(got.Tunnels) != 0 || !hasErr(got, "labelgate.tunnel.web.origin."+tt.key, tt.msg) {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestTunnel_OriginEmptyProxyType(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.tunnel.web.hostname":          "a.example.com",
		"labelgate.tunnel.web.service":           "http://a:80",
		"labelgate.tunnel.web.origin.proxy_type": "",
	})
	if len(got.Errors) != 0 || !got.Tunnels[0].Origin.IsZero() {
		t.Fatalf("got %+v", got)
	}
}

func TestTunnel_AccessRefAndDNSRef(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.tunnel.web.hostname": "app.example.com",
		"labelgate.tunnel.web.service":  "http://localhost:8080",
		"labelgate.tunnel.web.access":   "internal",
		"labelgate.dns.web.hostname":    "web.example.com",
		"labelgate.dns.web.access":      "public",
	})
	if len(got.Errors) != 0 || got.Tunnels[0].Access != "internal" || got.DNS[0].Access != "public" {
		t.Fatalf("got %+v", got)
	}
}

// Same-container DNS vs tunnel conflicts are the reconciler's concern, not a parse error.
func TestParse_SameHostnameDNSAndTunnelIsNotAParseError(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.dns.web.hostname":    "app.example.com",
		"labelgate.dns.web.type":        "A",
		"labelgate.tunnel.app.hostname": "app.example.com",
		"labelgate.tunnel.app.service":  "http://localhost:8080",
	})
	if len(got.Errors) != 0 || len(got.DNS) != 1 || len(got.Tunnels) != 1 {
		t.Fatalf("got %+v", got)
	}
}
