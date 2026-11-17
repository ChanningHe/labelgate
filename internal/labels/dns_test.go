package labels

import (
	"reflect"
	"testing"

	"github.com/channinghe/labelgate/internal/model"
)

func TestDNS_Defaults(t *testing.T) {
	got := Parse("labelgate", L{"labelgate.dns.web.hostname": "web.example.com"})
	if len(got.Errors) != 0 || len(got.DNS) != 1 {
		t.Fatalf("unexpected: %+v", got)
	}
	want := model.DNSSpec{
		Service: "web", Hostname: "web.example.com", Type: model.DNSTypeA,
		Target: "auto", Proxied: true, TTL: 0, Cleanup: true, Credential: "",
	}
	if !reflect.DeepEqual(got.DNS[0], want) {
		t.Errorf("got %+v\nwant %+v", got.DNS[0], want)
	}
}

func TestDNS_DefaultsBlockAndOverride(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.dns.default.proxied":    "false",
		"labelgate.dns.default.credential": "company",
		"labelgate.dns.default.cleanup":    "false",
		"labelgate.dns.default.ttl":        "300",
		"labelgate.dns.default.type":       "aaaa",
		"labelgate.dns.default.target":     "container",
		"labelgate.dns.web.hostname":       "web.example.com",
		"labelgate.dns.api.hostname":       "api.example.com",
		"labelgate.dns.api.proxied":        "true",
	})
	if len(got.Errors) != 0 || len(got.DNS) != 2 {
		t.Fatalf("unexpected: %+v", got)
	}
	api, web := got.DNS[0], got.DNS[1]
	if api.Service != "api" || web.Service != "web" {
		t.Fatalf("order: %s, %s", api.Service, web.Service)
	}
	if web.Proxied || web.Credential != "company" || web.Cleanup || web.TTL != 300 ||
		web.Type != model.DNSTypeAAAA || web.Target != "container" {
		t.Errorf("web should inherit defaults: %+v", web)
	}
	if !api.Proxied || api.TTL != 0 {
		t.Errorf("api overrides proxied (ttl auto when proxied): %+v", api)
	}
}

func TestDNS_DefaultsErrors(t *testing.T) {
	t.Run("invalid default value drops services", func(t *testing.T) {
		got := Parse("labelgate", L{
			"labelgate.dns.default.proxied": "maybe",
			"labelgate.dns.web.hostname":    "web.example.com",
		})
		if len(got.DNS) != 0 {
			t.Fatalf("services must be dropped: %+v", got.DNS)
		}
		if !hasErr(got, "labelgate.dns.default.proxied", "invalid boolean") ||
			!hasErr(got, "labelgate.dns.web", "invalid labelgate.dns.default") {
			t.Fatalf("errors: %+v", got.Errors)
		}
	})
	t.Run("property not allowed in defaults is reported", func(t *testing.T) {
		got := Parse("labelgate", L{
			"labelgate.dns.default.hostname": "x.example.com",
			"labelgate.dns.web.hostname":     "web.example.com",
		})
		if len(got.DNS) != 1 || !hasErr(got, "labelgate.dns.default.hostname", "unknown property") {
			t.Fatalf("got %+v", got)
		}
		if len(got.Errors) != 1 || !got.Errors[0].Unknown {
			t.Fatalf("unknown property not marked: %+v", got.Errors)
		}
	})
}

func TestDNS_HostnameNormalization(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{"Web.Example.COM", "web.example.com", false},
		{"  web.example.com.  ", "web.example.com", false},
		{"*.example.com", "*.example.com", false},
		{"_dmarc.example.com", "_dmarc.example.com", false},
		{"nodots", "", true},
		{"bad host.example.com", "", true},
		{"-bad.example.com", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := Parse("labelgate", L{"labelgate.dns.web.hostname": tt.in})
			if tt.wantErr {
				if len(got.DNS) != 0 || len(got.Errors) == 0 {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if len(got.Errors) != 0 || got.DNS[0].Hostname != tt.want {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestDNS_Types(t *testing.T) {
	tests := []struct {
		name    string
		labels  L
		want    model.DNSSpec
		wantErr string // substring of the first error; "" = none
	}{
		{
			name:   "A literal",
			labels: L{"type": "a", "target": "203.0.113.1"},
			want:   model.DNSSpec{Type: "A", Target: "203.0.113.1", Proxied: true, Cleanup: true},
		},
		{
			name:   "A container target",
			labels: L{"type": "A", "target": "Container"},
			want:   model.DNSSpec{Type: "A", Target: "container", Proxied: true, Cleanup: true},
		},
		{
			name:    "A with IPv6 target",
			labels:  L{"type": "A", "target": "2001:db8::1"},
			wantErr: "not valid for A",
		},
		{
			name:    "AAAA with IPv4 target",
			labels:  L{"type": "AAAA", "target": "1.2.3.4"},
			wantErr: "not valid for AAAA",
		},
		{
			name:   "AAAA literal",
			labels: L{"type": "AAAA", "target": "2001:DB8::1"},
			want:   model.DNSSpec{Type: "AAAA", Target: "2001:db8::1", Proxied: true, Cleanup: true},
		},
		{
			name:    "A garbage target",
			labels:  L{"target": "not-an-ip"},
			wantErr: "invalid A target",
		},
		{
			name:    "A zone target",
			labels:  L{"type": "AAAA", "target": "fe80::1%eth0"},
			wantErr: "invalid AAAA target",
		},
		{
			name:   "CNAME normalized",
			labels: L{"type": "CNAME", "target": "Web.Example.com."},
			want:   model.DNSSpec{Type: "CNAME", Target: "web.example.com", Proxied: true, Cleanup: true},
		},
		{
			name:    "CNAME requires target",
			labels:  L{"type": "CNAME"},
			wantErr: "target is required",
		},
		{
			name:    "CNAME auto target rejected",
			labels:  L{"type": "CNAME", "target": "auto"},
			wantErr: "target is required",
		},
		{
			name:    "CNAME invalid target",
			labels:  L{"type": "CNAME", "target": "bad target"},
			wantErr: "invalid target hostname",
		},
		{
			name:   "TXT raw target, never proxied",
			labels: L{"type": "TXT", "target": "v=spf1 include:_spf.google.com ~all", "proxied": "true", "ttl": "120"},
			want:   model.DNSSpec{Type: "TXT", Target: "v=spf1 include:_spf.google.com ~all", TTL: 120, Cleanup: true},
		},
		{
			name:    "TXT blank target",
			labels:  L{"type": "TXT", "target": "  "},
			wantErr: "target is required",
		},
		{
			name:   "MX default priority 0",
			labels: L{"type": "MX", "target": "mail.provider.com"},
			want:   model.DNSSpec{Type: "MX", Target: "mail.provider.com", Cleanup: true},
		},
		{
			name:   "MX priority",
			labels: L{"type": "MX", "target": "mail.provider.com", "priority": "10", "weight": "3", "flags": "9"},
			want:   model.DNSSpec{Type: "MX", Target: "mail.provider.com", Priority: 10, Cleanup: true},
		},
		{
			name:   "SRV",
			labels: L{"type": "SRV", "target": "xmpp.example.com", "priority": "10", "weight": "5", "port": "5222", "tag": "issue"},
			want:   model.DNSSpec{Type: "SRV", Target: "xmpp.example.com", Priority: 10, Weight: 5, Port: 5222, Cleanup: true},
		},
		{
			name:   "CAA defaults tag issue",
			labels: L{"type": "CAA", "target": "letsencrypt.org"},
			want:   model.DNSSpec{Type: "CAA", Target: "letsencrypt.org", Tag: "issue", Cleanup: true},
		},
		{
			name:   "CAA flags and tag",
			labels: L{"type": "CAA", "target": "mailto:sec@example.com", "flags": "128", "tag": "IODEF", "priority": "4"},
			want:   model.DNSSpec{Type: "CAA", Target: "mailto:sec@example.com", Flags: 128, Tag: "iodef", Cleanup: true},
		},
		{name: "CAA bad tag", labels: L{"type": "CAA", "target": "x", "tag": "bogus"}, wantErr: "invalid CAA tag"},
		{name: "CAA flags too large", labels: L{"type": "CAA", "target": "x", "flags": "256"}, wantErr: "out of range"},
		{name: "CAA flags negative", labels: L{"type": "CAA", "target": "x", "flags": "-1"}, wantErr: "out of range"},
		{name: "SRV port too large", labels: L{"type": "SRV", "target": "a.example.com", "port": "70000"}, wantErr: "out of range"},
		{name: "unknown type", labels: L{"type": "PTR"}, wantErr: "invalid record type"},
		{name: "bad ttl", labels: L{"ttl": "soon"}, wantErr: "invalid integer"},
		{name: "ttl too big", labels: L{"ttl": "86401"}, wantErr: "out of range"},
		{name: "bad bool", labels: L{"proxied": "maybe"}, wantErr: "invalid boolean"},
		{name: "bad cleanup", labels: L{"cleanup": "2"}, wantErr: "invalid boolean"},
		{name: "bad access ref", labels: L{"access": "Bad Name"}, wantErr: "invalid access policy name"},
		{
			name:   "ttl 1 normalized to auto",
			labels: L{"ttl": "1", "proxied": "false"},
			want:   model.DNSSpec{Type: "A", Target: "auto", TTL: 0, Cleanup: true},
		},
		{
			name:   "ttl kept when not proxied",
			labels: L{"ttl": "300", "proxied": "no"},
			want:   model.DNSSpec{Type: "A", Target: "auto", TTL: 300, Cleanup: true},
		},
		{
			name:   "ttl dropped when proxied",
			labels: L{"ttl": "300"},
			want:   model.DNSSpec{Type: "A", Target: "auto", Proxied: true, TTL: 0, Cleanup: true},
		},
		{
			name:   "comment, access and credential",
			labels: L{"comment": " keep  spaces ", "access": "internal", "credential": " corp ", "cleanup": "false"},
			want: model.DNSSpec{Type: "A", Target: "auto", Proxied: true, Comment: " keep  spaces ",
				Access: "internal", Credential: "corp"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := L{"labelgate.dns.svc.hostname": "svc.example.com"}
			for k, v := range tt.labels {
				labels["labelgate.dns.svc."+k] = v
			}
			got := Parse("labelgate", labels)
			if tt.wantErr != "" {
				if len(got.DNS) != 0 || len(got.Errors) == 0 || !hasErr(got, got.Errors[0].Key, tt.wantErr) {
					t.Fatalf("want error %q, got %+v", tt.wantErr, got)
				}
				return
			}
			if len(got.Errors) != 0 || len(got.DNS) != 1 {
				t.Fatalf("unexpected: %+v", got)
			}
			tt.want.Service, tt.want.Hostname = "svc", "svc.example.com"
			if !reflect.DeepEqual(got.DNS[0], tt.want) {
				t.Errorf("got  %+v\nwant %+v", got.DNS[0], tt.want)
			}
		})
	}
}

func TestDNS_UnknownPropertyKeepsService(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.dns.web.hostname": "web.example.com",
		"labelgate.dns.web.colour":   "red",
	})
	if len(got.DNS) != 1 {
		t.Fatalf("service must be kept: %+v", got)
	}
	if !hasErr(got, "labelgate.dns.web.colour", "unknown property") {
		t.Fatalf("unknown property not reported: %+v", got.Errors)
	}
}

func TestDNS_ValueErrorDropsServiceOnly(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.dns.bad.hostname": "bad.example.com",
		"labelgate.dns.bad.proxied":  "perhaps",
		"labelgate.dns.ok.hostname":  "ok.example.com",
	})
	if len(got.DNS) != 1 || got.DNS[0].Service != "ok" {
		t.Fatalf("got %+v", got.DNS)
	}
	if !hasErr(got, "labelgate.dns.bad.proxied", "invalid boolean") {
		t.Fatalf("errors: %+v", got.Errors)
	}
}

func TestBoolValues(t *testing.T) {
	tests := []struct {
		in      string
		want    bool
		wantErr bool
	}{
		{"true", true, false}, {"True", true, false}, {"TRUE", true, false}, {"1", true, false},
		{"yes", true, false}, {"on", true, false}, {" true ", true, false},
		{"false", false, false}, {"False", false, false}, {"0", false, false},
		{"no", false, false}, {"off", false, false},
		{"invalid", false, true}, {"", false, true},
	}
	for _, tt := range tests {
		got, err := parseBool(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseBool(%q) = %v, %v", tt.in, got, err)
		}
	}
}

func TestIntValues(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"123", 123, false}, {"0", 0, false}, {" 100 ", 100, false},
		{"-1", 0, true}, {"invalid", 0, true}, {"", 0, true}, {"1000", 0, true},
	}
	for _, tt := range tests {
		got, err := parseInt(tt.in, 0, 999)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseInt(%q) = %v, %v", tt.in, got, err)
		}
	}
}
