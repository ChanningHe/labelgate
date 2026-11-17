package labels

import (
	"reflect"
	"testing"

	"github.com/channinghe/labelgate/internal/model"
)

func TestAccess_BasicAllow(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.access.internal.policy.decision":       "allow",
		"labelgate.access.internal.policy.name":           "Allow Internal",
		"labelgate.access.internal.policy.include.emails": "admin@example.com",
	})
	if len(got.Errors) != 0 || len(got.Access) != 1 {
		t.Fatalf("unexpected: %+v", got)
	}
	want := model.AccessPolicyDef{
		Name: "internal", AppName: "labelgate-internal", SessionDuration: "24h",
		Policies: []model.AccessPolicy{{
			Decision: "allow", Name: "Allow Internal",
			Include: []model.AccessRule{{Selector: "emails", Values: []string{"admin@example.com"}}},
		}},
	}
	if !reflect.DeepEqual(got.Access[0], want) {
		t.Errorf("got  %+v\nwant %+v", got.Access[0], want)
	}
}

func TestAccess_Decisions(t *testing.T) {
	tests := []struct {
		name     string
		decision string // "" = label omitted (default allow)
		extra    L
		wantErr  bool
	}{
		{"allow requires include (ok)", "allow", L{"emails": "a@b.com"}, false},
		{"block requires include (ok)", "block", L{"ip_ranges": "10.0.0.0/8"}, false},
		{"bypass with everyone", "bypass", L{"everyone": ""}, false},
		{"bypass without include", "bypass", nil, false},
		{"service_auth with token", "service_auth", L{"service_token": "token-uuid-123"}, false},
		{"decision is case-insensitive", " ALLOW ", L{"emails": "a@b.com"}, false},
		{"default decision allow with include", "", L{"emails": "a@b.com"}, false},
		{"default decision allow without include", "", nil, true},
		{"invalid decision", "invalid_decision", nil, true},
		{"allow without include fails", "allow", nil, true},
		{"block without include fails", "block", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := L{"labelgate.access.test.session_duration": "1h"}
			if tt.decision != "" {
				labels["labelgate.access.test.policy.decision"] = tt.decision
			}
			for k, v := range tt.extra {
				labels["labelgate.access.test.policy.include."+k] = v
			}
			got := Parse("labelgate", labels)
			if tt.wantErr {
				if len(got.Errors) == 0 || len(got.Access) != 0 {
					t.Fatalf("want error and no def, got %+v", got)
				}
				return
			}
			if len(got.Errors) != 0 || len(got.Access) != 1 {
				t.Fatalf("unexpected: %+v", got)
			}
		})
	}
}

func TestAccess_DecisionNormalized(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.access.t.policy.decision":         " BYPASS ",
		"labelgate.access.t.policy.include.everyone": "",
	})
	if got.Access[0].Policies[0].Decision != model.AccessDecisionBypass {
		t.Errorf("got %q", got.Access[0].Policies[0].Decision)
	}
}

func TestAccess_Selectors(t *testing.T) {
	tests := []struct {
		selector, value string
		want            []string
	}{
		{"emails", "admin@example.com", []string{"admin@example.com"}},
		{"emails", "a@b.com, c@d.com, e@f.com", []string{"a@b.com", "c@d.com", "e@f.com"}},
		{"emails_ending_in", "example.com, corp.io", []string{"example.com", "corp.io"}},
		{"ip_ranges", "10.0.0.0/8, 192.168.0.0/16", []string{"10.0.0.0/8", "192.168.0.0/16"}},
		{"country", "US, CN, JP", []string{"US", "CN", "JP"}},
		{"everyone", "", []string{}},
		{"everyone", "ignored", []string{}},
		{"service_token", "token-uuid-123", []string{"token-uuid-123"}},
		{"access_groups", "group-1, group-2", []string{"group-1", "group-2"}},
		{"certificate", "", []string{}},
		{"login_methods", "method-uuid-1", []string{"method-uuid-1"}},
		{"emails", "a@b.com,,  , c@d.com", []string{"a@b.com", "c@d.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.selector+"="+tt.value, func(t *testing.T) {
			got := Parse("labelgate", L{
				"labelgate.access.test.policy.decision":               "bypass",
				"labelgate.access.test.policy.include." + tt.selector: tt.value,
			})
			if len(got.Errors) != 0 || len(got.Access) != 1 {
				t.Fatalf("unexpected: %+v", got)
			}
			rules := got.Access[0].Policies[0].Include
			if len(rules) != 1 || rules[0].Selector != tt.selector || !reflect.DeepEqual(rules[0].Values, tt.want) {
				t.Errorf("got %+v, want values %v", rules, tt.want)
			}
		})
	}
}

func TestAccess_SelectorErrors(t *testing.T) {
	tests := []struct{ name, key, value, msg string }{
		{"invalid include selector", "policy.include.invalid_thing", "v", "invalid include selector"},
		{"invalid require selector", "policy.require.nope", "v", "invalid require selector"},
		{"invalid exclude selector", "policy.exclude.nope", "v", "invalid exclude selector"},
		{"empty selector", "policy.include.", "v", "invalid include selector"},
		{"emails without value", "policy.include.emails", "  ,  ", "requires at least one value"},
		{"bad session duration", "session_duration", "forever", "invalid session_duration"},
		{"negative session duration", "session_duration", "-1h", "invalid session_duration"},
		{"bad decision", "policy.decision", "maybe", "invalid decision"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse("labelgate", L{
				"labelgate.access.test.policy.include.everyone": "",
				"labelgate.access.test." + tt.key:               tt.value,
			})
			if len(got.Access) != 0 || !hasErr(got, "labelgate.access.test."+tt.key, tt.msg) {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestAccess_UnknownPropertyKeepsDef(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.access.test.policy.include.everyone": "",
		"labelgate.access.test.policy.decision":         "bypass",
		"labelgate.access.test.policy.colour":           "red",
		"labelgate.access.test.bogus":                   "1",
	})
	if len(got.Access) != 1 ||
		!hasErr(got, "labelgate.access.test.policy.colour", "unknown property") ||
		!hasErr(got, "labelgate.access.test.bogus", "unknown property") {
		t.Fatalf("got %+v", got)
	}
}

func TestAccess_IncludeRequireExclude(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.access.test.policy.decision":          "allow",
		"labelgate.access.test.policy.include.emails":    "admin@example.com",
		"labelgate.access.test.policy.include.country":   "DE",
		"labelgate.access.test.policy.require.country":   "US",
		"labelgate.access.test.policy.exclude.ip_ranges": "10.0.0.0/8",
	})
	if len(got.Errors) != 0 || len(got.Access) != 1 {
		t.Fatalf("unexpected: %+v", got)
	}
	p := got.Access[0].Policies[0]
	if len(p.Include) != 2 || p.Include[0].Selector != "country" || p.Include[1].Selector != "emails" {
		t.Errorf("include rules must be sorted by selector: %+v", p.Include)
	}
	if len(p.Require) != 1 || p.Require[0].Selector != "country" {
		t.Errorf("require: %+v", p.Require)
	}
	if len(p.Exclude) != 1 || p.Exclude[0].Selector != "ip_ranges" {
		t.Errorf("exclude: %+v", p.Exclude)
	}
}

func TestAccess_SessionAndAppName(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.access.test.session_duration":        "12h",
		"labelgate.access.test.app_name":                " My App ",
		"labelgate.access.test.policy.decision":         "bypass",
		"labelgate.access.test.policy.include.everyone": "",
		"labelgate.access.other.app_name":               "  ",
		"labelgate.access.other.policy.decision":        "bypass",
	})
	if len(got.Errors) != 0 || len(got.Access) != 2 {
		t.Fatalf("unexpected: %+v", got)
	}
	other, test := got.Access[0], got.Access[1]
	if test.SessionDuration != "12h" || test.AppName != "My App" {
		t.Errorf("test: %+v", test)
	}
	if other.SessionDuration != "24h" || other.AppName != "labelgate-other" {
		t.Errorf("blank app_name keeps default: %+v", other)
	}
}

func TestAccess_MultiplePoliciesSorted(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.access.public.policy.decision":         "bypass",
		"labelgate.access.public.policy.include.everyone": "",
		"labelgate.access.internal.policy.decision":       "allow",
		"labelgate.access.internal.policy.include.emails": "admin@example.com",
	})
	if len(got.Errors) != 0 || len(got.Access) != 2 ||
		got.Access[0].Name != "internal" || got.Access[1].Name != "public" {
		t.Fatalf("got %+v", got)
	}
}

func TestAccess_MixedWithTunnelAndDNS(t *testing.T) {
	got := Parse("labelgate", L{
		"labelgate.access.internal.policy.decision":       "allow",
		"labelgate.access.internal.policy.include.emails": "admin@example.com",
		"labelgate.tunnel.web.hostname":                   "app.example.com",
		"labelgate.tunnel.web.service":                    "http://localhost:8080",
		"labelgate.tunnel.web.access":                     "internal",
		"labelgate.dns.api.hostname":                      "api.example.com",
	})
	if len(got.Errors) != 0 || len(got.Access) != 1 || len(got.Tunnels) != 1 || len(got.DNS) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got.Tunnels[0].Access != "internal" || got.DNS[0].Access != "" {
		t.Errorf("access refs: %q / %q", got.Tunnels[0].Access, got.DNS[0].Access)
	}
}
