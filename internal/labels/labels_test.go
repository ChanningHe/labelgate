package labels

import (
	"reflect"
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/model"
)

// L is shorthand for a label map.
type L = map[string]string

func errKeys(r model.ParsedLabels) []string {
	keys := make([]string, 0, len(r.Errors))
	for _, e := range r.Errors {
		keys = append(keys, e.Key)
	}
	return keys
}

func hasErr(r model.ParsedLabels, key, contains string) bool {
	for _, e := range r.Errors {
		if e.Key == key && strings.Contains(e.Message, contains) {
			return true
		}
	}
	return false
}

func TestParse_Counts(t *testing.T) {
	tests := []struct {
		name       string
		labels     L
		wantDNS    int
		wantTunnel int
		wantAccess int
		wantErr    bool
	}{
		{"single DNS", L{
			"labelgate.dns.web.hostname": "web.example.com",
			"labelgate.dns.web.type":     "A",
			"labelgate.dns.web.target":   "auto",
		}, 1, 0, 0, false},
		{"multiple DNS", L{
			"labelgate.dns.web.hostname": "web.example.com",
			"labelgate.dns.api.hostname": "api.example.com",
			"labelgate.dns.api.type":     "CNAME",
			"labelgate.dns.api.target":   "web.example.com",
		}, 2, 0, 0, false},
		{"DNS with defaults", L{
			"labelgate.dns.default.proxied":    "false",
			"labelgate.dns.default.credential": "company",
			"labelgate.dns.web.hostname":       "web.example.com",
		}, 1, 0, 0, false},
		{"missing hostname", L{
			"labelgate.dns.web.type":   "A",
			"labelgate.dns.web.target": "auto",
		}, 0, 0, 0, true},
		{"invalid service name", L{
			"labelgate.dns.Web_Service.hostname": "web.example.com",
		}, 0, 0, 0, true},
		{"reserved service name", L{
			"labelgate.dns.hostname.hostname": "web.example.com",
		}, 0, 0, 0, true},
		{"single tunnel", L{
			"labelgate.tunnel.web.hostname": "app.example.com",
			"labelgate.tunnel.web.service":  "http://localhost:8080",
		}, 0, 1, 0, false},
		{"multiple tunnels", L{
			"labelgate.tunnel.web.hostname": "app.example.com",
			"labelgate.tunnel.web.service":  "http://localhost:8080",
			"labelgate.tunnel.api.hostname": "api.example.com",
			"labelgate.tunnel.api.service":  "http://localhost:3000",
		}, 0, 2, 0, false},
		{"tunnel with origin", L{
			"labelgate.tunnel.web.hostname":               "app.example.com",
			"labelgate.tunnel.web.service":                "http://localhost:8080",
			"labelgate.tunnel.web.origin.connect_timeout": "30s",
			"labelgate.tunnel.web.origin.no_tls_verify":   "true",
		}, 0, 1, 0, false},
		{"missing service URL", L{
			"labelgate.tunnel.web.hostname": "app.example.com",
		}, 0, 0, 0, true},
		{"keys outside the prefix are ignored", L{
			"com.docker.compose.project": "x",
			"labelgateway.dns.web":       "x",
			"other.dns.web.hostname":     "web.example.com",
		}, 0, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse("labelgate", tt.labels)
			if (len(got.Errors) > 0) != tt.wantErr {
				t.Errorf("errors = %v, wantErr %v", got.Errors, tt.wantErr)
			}
			if len(got.DNS) != tt.wantDNS || len(got.Tunnels) != tt.wantTunnel || len(got.Access) != tt.wantAccess {
				t.Errorf("counts dns/tunnel/access = %d/%d/%d, want %d/%d/%d",
					len(got.DNS), len(got.Tunnels), len(got.Access), tt.wantDNS, tt.wantTunnel, tt.wantAccess)
			}
		})
	}
}

func TestParse_MalformedKeysAreReported(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"labelgate.dns.hostname", "malformed"},
		{"labelgate.dns.web", "malformed"},
		{"labelgate.dns", "malformed"},
		{"labelgate.enable", "unknown label type"},
		{"labelgate.dsn.web.hostname", "unknown label type"},
		{"labelgate.foo.bar.baz", "unknown label type"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := Parse("labelgate", L{
				tt.key:                      "x",
				"labelgate.dns.ok.hostname": "ok.example.com",
				"labelgate.dns.ok.type":     "A",
				"labelgate.dns.ok.target":   "192.0.2.1",
			})
			if !hasErr(got, tt.key, tt.want) {
				t.Errorf("errors = %v, want key %q containing %q", got.Errors, tt.key, tt.want)
			}
			if len(got.DNS) != 1 || got.DNS[0].Service != "ok" {
				t.Errorf("valid service must survive a malformed sibling: %+v", got.DNS)
			}
		})
	}
}

func TestParse_CustomAndEmptyPrefix(t *testing.T) {
	labels := L{
		"myprefix.dns.web.hostname":  "web.example.com",
		"labelgate.dns.api.hostname": "api.example.com", // other prefix, ignored
	}
	got := Parse("myprefix", labels)
	if len(got.DNS) != 1 || got.DNS[0].Hostname != "web.example.com" {
		t.Fatalf("custom prefix: got %+v", got.DNS)
	}
	got = Parse("", labels)
	if len(got.DNS) != 1 || got.DNS[0].Hostname != "api.example.com" {
		t.Fatalf("empty prefix should default to labelgate: got %+v", got.DNS)
	}
}

func TestParse_Deterministic(t *testing.T) {
	labels := L{
		"labelgate.dns.zeta.hostname":                     "z.example.com",
		"labelgate.dns.alpha.hostname":                    "a.example.com",
		"labelgate.dns.mid.hostname":                      "mid.example.com",
		"labelgate.dns.bad.hostname":                      "x.example.com",
		"labelgate.dns.bad.ttl":                           "abc",
		"labelgate.dns.bad.type":                          "XX",
		"labelgate.dns.alpha.bogus":                       "1",
		"labelgate.tunnel.t2.hostname":                    "t2.example.com",
		"labelgate.tunnel.t2.service":                     "http://t2:80",
		"labelgate.tunnel.t1.hostname":                    "t1.example.com",
		"labelgate.tunnel.t1.service":                     "http://t1:80",
		"labelgate.tunnel.t1.origin.nope":                 "1",
		"labelgate.access.b.policy.include.emails":        "x@y.z",
		"labelgate.access.b.policy.exclude.country":       "US,CN",
		"labelgate.access.b.policy.include.ip_ranges":     "10.0.0.0/8",
		"labelgate.access.a.policy.decision":              "bypass",
		"labelgate.access.a.policy.include.everyone":      "",
		"labelgate.access.a.policy.include.certificate":   "",
		"labelgate.access.c.policy.decision":              "nope",
		"labelgate.access.BAD.policy.decision":            "allow",
		"labelgate.dns.UPPER.hostname":                    "u.example.com",
		"labelgate.tunnel.t3.hostname":                    "t3.example.com",
		"labelgate.tunnel.t3.service":                     "ftp://x",
		"labelgate.dns.default.credential":                "c1",
		"labelgate.tunnel.default.tunnel":                 "main",
		"labelgate.access.default.policy.decision":        "bypass",
		"labelgate.tunnel.default.bogus":                  "x",
		"labelgate.dns.mid.priority":                      "5",
		"labelgate.dns.mid.type":                          "MX",
		"labelgate.dns.mid.target":                        "mail.example.com",
		"labelgate.access.b.policy.include.emails_ending": "x",
	}
	want := Parse("labelgate", labels)
	if len(want.Errors) == 0 {
		t.Fatal("fixture should produce errors")
	}
	for i := 0; i < 100; i++ {
		got := Parse("labelgate", labels)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d differs:\n got %+v\nwant %+v", i, got, want)
		}
	}
	for i := 1; i < len(want.DNS); i++ {
		if want.DNS[i-1].Service >= want.DNS[i].Service {
			t.Errorf("DNS not sorted: %v", want.DNS)
		}
	}
	for i := 1; i < len(want.Tunnels); i++ {
		if want.Tunnels[i-1].Service >= want.Tunnels[i].Service {
			t.Errorf("tunnels not sorted")
		}
	}
	for i := 1; i < len(want.Errors); i++ {
		if want.Errors[i-1].Key > want.Errors[i].Key {
			t.Errorf("errors not sorted by key: %v", errKeys(want))
		}
	}
}

func TestParse_ServiceNameErrorsDeduplicated(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.dns.Bad_Name.hostname": "a.example.com",
		"labelgate.dns.Bad_Name.type":     "A",
		"labelgate.dns.Bad_Name.target":   "auto",
	})
	if len(got.Errors) != 1 || got.Errors[0].Key != "labelgate.dns.Bad_Name" {
		t.Fatalf("want one stem error, got %+v", got.Errors)
	}
}

func TestParse_AccessReservedNameAndDefault(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.access.default.policy.decision":         "bypass",
		"labelgate.access.default.policy.include.everyone": "",
	})
	if len(got.Errors) != 0 || len(got.Access) != 0 {
		t.Fatalf("access default must be skipped silently: %+v", got)
	}
	got = Parse("labelgate", L{"labelgate.access.access.policy.decision": "bypass"})
	if !hasErr(got, "labelgate.access.access", "reserved") {
		t.Fatalf("reserved access name not reported: %+v", got.Errors)
	}
}
