package labels

import (
	"testing"

	"github.com/channinghe/labelgate/internal/types"
)

func TestParser_Parse_DNS(t *testing.T) {
	parser := NewParser("labelgate")

	tests := []struct {
		name    string
		labels  map[string]string
		want    int    // expected DNS services count
		wantErr bool
	}{
		{
			name: "single DNS service",
			labels: map[string]string{
				"labelgate.dns.web.hostname": "web.example.com",
				"labelgate.dns.web.type":     "A",
				"labelgate.dns.web.target":   "auto",
			},
			want:    1,
			wantErr: false,
		},
		{
			name: "multiple DNS services",
			labels: map[string]string{
				"labelgate.dns.web.hostname": "web.example.com",
				"labelgate.dns.api.hostname": "api.example.com",
				"labelgate.dns.api.type":     "CNAME",
				"labelgate.dns.api.target":   "web.example.com",
			},
			want:    2,
			wantErr: false,
		},
		{
			name: "DNS service with defaults",
			labels: map[string]string{
				"labelgate.dns.default.proxied":    "false",
				"labelgate.dns.default.credential": "company",
				"labelgate.dns.web.hostname":       "web.example.com",
			},
			want:    1,
			wantErr: false,
		},
		{
			name: "missing hostname",
			labels: map[string]string{
				"labelgate.dns.web.type":   "A",
				"labelgate.dns.web.target": "auto",
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "invalid service name",
			labels: map[string]string{
				"labelgate.dns.Web_Service.hostname": "web.example.com",
			},
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.Parse(tt.labels)

			if tt.wantErr && len(result.Errors) == 0 {
				t.Error("expected errors but got none")
			}
			if !tt.wantErr && len(result.Errors) > 0 {
				t.Errorf("unexpected errors: %v", result.Errors)
			}
			if len(result.DNSServices) != tt.want {
				t.Errorf("got %d DNS services, want %d", len(result.DNSServices), tt.want)
			}
		})
	}
}

func TestParser_Parse_Tunnel(t *testing.T) {
	parser := NewParser("labelgate")

	tests := []struct {
		name    string
		labels  map[string]string
		want    int
		wantErr bool
	}{
		{
			name: "single Tunnel service",
			labels: map[string]string{
				"labelgate.tunnel.web.hostname": "app.example.com",
				"labelgate.tunnel.web.service":  "http://localhost:8080",
			},
			want:    1,
			wantErr: false,
		},
		{
			name: "multiple Tunnel services",
			labels: map[string]string{
				"labelgate.tunnel.web.hostname": "app.example.com",
				"labelgate.tunnel.web.service":  "http://localhost:8080",
				"labelgate.tunnel.api.hostname": "api.example.com",
				"labelgate.tunnel.api.service":  "http://localhost:3000",
			},
			want:    2,
			wantErr: false,
		},
		{
			name: "Tunnel with origin config",
			labels: map[string]string{
				"labelgate.tunnel.web.hostname":              "app.example.com",
				"labelgate.tunnel.web.service":               "http://localhost:8080",
				"labelgate.tunnel.web.origin.connect_timeout": "30s",
				"labelgate.tunnel.web.origin.no_tls_verify":  "true",
			},
			want:    1,
			wantErr: false,
		},
		{
			name: "missing service URL",
			labels: map[string]string{
				"labelgate.tunnel.web.hostname": "app.example.com",
			},
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.Parse(tt.labels)

			if tt.wantErr && len(result.Errors) == 0 {
				t.Error("expected errors but got none")
			}
			if !tt.wantErr && len(result.Errors) > 0 {
				t.Errorf("unexpected errors: %v", result.Errors)
			}
			if len(result.TunnelServices) != tt.want {
				t.Errorf("got %d Tunnel services, want %d", len(result.TunnelServices), tt.want)
			}
		})
	}
}

func TestParser_HostnameConflict(t *testing.T) {
	parser := NewParser("labelgate")

	// Test conflict detection
	labels := map[string]string{
		"labelgate.dns.web.hostname":    "app.example.com",
		"labelgate.dns.web.type":        "A",
		"labelgate.tunnel.app.hostname": "app.example.com",
		"labelgate.tunnel.app.service":  "http://localhost:8080",
	}

	result := parser.Parse(labels)
	err := parser.CheckHostnameConflict(result)

	if err == nil {
		t.Error("expected hostname conflict error")
	}

	if _, ok := err.(*HostnameConflictError); !ok {
		t.Errorf("expected HostnameConflictError, got %T", err)
	}
}

func TestParser_CustomPrefix(t *testing.T) {
	parser := NewParser("myprefix")

	labels := map[string]string{
		"myprefix.dns.web.hostname": "web.example.com",
		"labelgate.dns.api.hostname": "api.example.com", // Should be ignored
	}

	result := parser.Parse(labels)

	if len(result.DNSServices) != 1 {
		t.Errorf("got %d DNS services, want 1", len(result.DNSServices))
	}
	if result.DNSServices[0].Hostname != "web.example.com" {
		t.Errorf("got hostname %s, want web.example.com", result.DNSServices[0].Hostname)
	}
}

func TestParser_DNSDefaults(t *testing.T) {
	parser := NewParser("labelgate")

	labels := map[string]string{
		"labelgate.dns.default.proxied":    "false",
		"labelgate.dns.default.credential": "company",
		"labelgate.dns.web.hostname":       "web.example.com",
		"labelgate.dns.api.hostname":       "api.example.com",
		"labelgate.dns.api.proxied":        "true", // Override default
	}

	result := parser.Parse(labels)

	if len(result.DNSServices) != 2 {
		t.Fatalf("got %d DNS services, want 2", len(result.DNSServices))
	}

	// Find services by hostname
	var webService, apiService *types.DNSService
	for _, svc := range result.DNSServices {
		if svc.Hostname == "web.example.com" {
			webService = svc
		} else if svc.Hostname == "api.example.com" {
			apiService = svc
		}
	}

	// web should inherit defaults
	if webService.Proxied != false {
		t.Error("web service should inherit proxied=false from defaults")
	}
	if webService.Credential != "company" {
		t.Error("web service should inherit credential=company from defaults")
	}

	// api should override proxied
	if apiService.Proxied != true {
		t.Error("api service should override proxied to true")
	}
}

func TestParseBool(t *testing.T) {
	tests := []struct {
		input string
		def   bool
		want  bool
	}{
		{"true", false, true},
		{"True", false, true},
		{"TRUE", false, true},
		{"1", false, true},
		{"yes", false, true},
		{"on", false, true},
		{"false", true, false},
		{"False", true, false},
		{"0", true, false},
		{"no", true, false},
		{"off", true, false},
		{"invalid", true, true},   // Returns default
		{"invalid", false, false}, // Returns default
		{"", true, true},          // Returns default
	}

	for _, tt := range tests {
		got := parseBool(tt.input, tt.def)
		if got != tt.want {
			t.Errorf("parseBool(%q, %v) = %v, want %v", tt.input, tt.def, got, tt.want)
		}
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input string
		def   int
		want  int
	}{
		{"123", 0, 123},
		{"0", 100, 0},
		{"-1", 0, -1},
		{"invalid", 42, 42},
		{"", 42, 42},
		{" 100 ", 0, 100}, // Trimmed
	}

	for _, tt := range tests {
		got := parseInt(tt.input, tt.def)
		if got != tt.want {
			t.Errorf("parseInt(%q, %v) = %v, want %v", tt.input, tt.def, got, tt.want)
		}
	}
}

// --- Access Policy Parsing Tests ---

func TestParser_Parse_Access_BasicAllow(t *testing.T) {
	parser := NewParser("labelgate")

	labels := map[string]string{
		"labelgate.access.internal.policy.decision":       "allow",
		"labelgate.access.internal.policy.name":           "Allow Internal",
		"labelgate.access.internal.policy.include.emails": "admin@example.com",
	}

	result := parser.Parse(labels)

	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(result.AccessPolicies) != 1 {
		t.Fatalf("got %d access policies, want 1", len(result.AccessPolicies))
	}

	pol, ok := result.AccessPolicies["internal"]
	if !ok {
		t.Fatal("expected access policy 'internal' not found")
	}
	if pol.Name != "internal" {
		t.Errorf("policy def name = %q, want %q", pol.Name, "internal")
	}
	if len(pol.Policies) != 1 {
		t.Fatalf("got %d sub-policies, want 1", len(pol.Policies))
	}
	p := pol.Policies[0]
	if p.Decision != "allow" {
		t.Errorf("decision = %q, want %q", p.Decision, "allow")
	}
	if p.Name != "Allow Internal" {
		t.Errorf("policy name = %q, want %q", p.Name, "Allow Internal")
	}
	if len(p.Include) != 1 {
		t.Fatalf("got %d include rules, want 1", len(p.Include))
	}
	if p.Include[0].Selector != "emails" {
		t.Errorf("include selector = %q, want %q", p.Include[0].Selector, "emails")
	}
	if len(p.Include[0].Values) != 1 || p.Include[0].Values[0] != "admin@example.com" {
		t.Errorf("include values = %v, want [admin@example.com]", p.Include[0].Values)
	}
}

func TestParser_Parse_Access_Decisions(t *testing.T) {
	parser := NewParser("labelgate")

	tests := []struct {
		name     string
		decision string
		extra    map[string]string // additional labels
		wantErr  bool
	}{
		{
			name:     "allow requires include",
			decision: "allow",
			extra: map[string]string{
				"labelgate.access.test.policy.include.emails": "a@b.com",
			},
		},
		{
			name:     "block requires include",
			decision: "block",
			extra: map[string]string{
				"labelgate.access.test.policy.include.ip_ranges": "10.0.0.0/8",
			},
		},
		{
			name:     "bypass with everyone",
			decision: "bypass",
			extra: map[string]string{
				"labelgate.access.test.policy.include.everyone": "",
			},
		},
		{
			name:     "service_auth with service_token",
			decision: "service_auth",
			extra: map[string]string{
				"labelgate.access.test.policy.include.service_token": "token-uuid-123",
			},
		},
		{
			name:     "invalid decision",
			decision: "invalid_decision",
			wantErr:  true,
		},
		{
			name:     "allow without include fails",
			decision: "allow",
			wantErr:  true,
		},
		{
			name:     "block without include fails",
			decision: "block",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := map[string]string{
				"labelgate.access.test.policy.decision": tt.decision,
			}
			for k, v := range tt.extra {
				labels[k] = v
			}

			result := parser.Parse(labels)

			if tt.wantErr {
				if len(result.Errors) == 0 {
					t.Error("expected errors but got none")
				}
				return
			}
			if len(result.Errors) > 0 {
				t.Fatalf("unexpected errors: %v", result.Errors)
			}
			if len(result.AccessPolicies) != 1 {
				t.Fatalf("got %d access policies, want 1", len(result.AccessPolicies))
			}
			pol := result.AccessPolicies["test"]
			if pol.Policies[0].Decision != tt.decision {
				t.Errorf("decision = %q, want %q", pol.Policies[0].Decision, tt.decision)
			}
		})
	}
}

func TestParser_Parse_Access_Selectors(t *testing.T) {
	parser := NewParser("labelgate")

	tests := []struct {
		name          string
		selector      string
		value         string
		wantValues    []string
		wantSelector  string
	}{
		{
			name:         "emails single",
			selector:     "emails",
			value:        "admin@example.com",
			wantValues:   []string{"admin@example.com"},
			wantSelector: "emails",
		},
		{
			name:         "emails multiple comma-separated",
			selector:     "emails",
			value:        "a@b.com, c@d.com, e@f.com",
			wantValues:   []string{"a@b.com", "c@d.com", "e@f.com"},
			wantSelector: "emails",
		},
		{
			name:         "emails_ending_in",
			selector:     "emails_ending_in",
			value:        "example.com, corp.io",
			wantValues:   []string{"example.com", "corp.io"},
			wantSelector: "emails_ending_in",
		},
		{
			name:         "ip_ranges",
			selector:     "ip_ranges",
			value:        "10.0.0.0/8, 192.168.0.0/16",
			wantValues:   []string{"10.0.0.0/8", "192.168.0.0/16"},
			wantSelector: "ip_ranges",
		},
		{
			name:         "country",
			selector:     "country",
			value:        "US, CN, JP",
			wantValues:   []string{"US", "CN", "JP"},
			wantSelector: "country",
		},
		{
			name:         "everyone empty value",
			selector:     "everyone",
			value:        "",
			wantValues:   []string{},
			wantSelector: "everyone",
		},
		{
			name:         "service_token",
			selector:     "service_token",
			value:        "token-uuid-123",
			wantValues:   []string{"token-uuid-123"},
			wantSelector: "service_token",
		},
		{
			name:         "access_groups",
			selector:     "access_groups",
			value:        "group-1, group-2",
			wantValues:   []string{"group-1", "group-2"},
			wantSelector: "access_groups",
		},
		{
			name:         "certificate",
			selector:     "certificate",
			value:        "",
			wantValues:   []string{},
			wantSelector: "certificate",
		},
		{
			name:         "login_methods",
			selector:     "login_methods",
			value:        "method-uuid-1",
			wantValues:   []string{"method-uuid-1"},
			wantSelector: "login_methods",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := map[string]string{
				"labelgate.access.test.policy.decision":                       "bypass",
				"labelgate.access.test.policy.include." + tt.selector: tt.value,
			}

			result := parser.Parse(labels)

			if len(result.Errors) > 0 {
				t.Fatalf("unexpected errors: %v", result.Errors)
			}

			pol := result.AccessPolicies["test"]
			if pol == nil {
				t.Fatal("expected access policy 'test' not found")
			}

			rules := pol.Policies[0].Include
			// Find the rule matching our selector
			var found bool
			for _, rule := range rules {
				if rule.Selector == tt.wantSelector {
					found = true
					if len(rule.Values) != len(tt.wantValues) {
						t.Errorf("got %d values, want %d", len(rule.Values), len(tt.wantValues))
						break
					}
					for i, v := range tt.wantValues {
						if rule.Values[i] != v {
							t.Errorf("value[%d] = %q, want %q", i, rule.Values[i], v)
						}
					}
				}
			}
			if !found {
				t.Errorf("selector %q not found in include rules", tt.wantSelector)
			}
		})
	}
}

func TestParser_Parse_Access_InvalidSelector(t *testing.T) {
	parser := NewParser("labelgate")

	labels := map[string]string{
		"labelgate.access.test.policy.decision":              "allow",
		"labelgate.access.test.policy.include.invalid_thing": "value",
	}

	result := parser.Parse(labels)

	if len(result.Errors) == 0 {
		t.Error("expected error for invalid selector")
	}
}

func TestParser_Parse_Access_IncludeRequireExclude(t *testing.T) {
	parser := NewParser("labelgate")

	labels := map[string]string{
		"labelgate.access.test.policy.decision":          "allow",
		"labelgate.access.test.policy.include.emails":    "admin@example.com",
		"labelgate.access.test.policy.require.country":   "US",
		"labelgate.access.test.policy.exclude.ip_ranges": "10.0.0.0/8",
	}

	result := parser.Parse(labels)

	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}

	pol := result.AccessPolicies["test"]
	if pol == nil {
		t.Fatal("expected access policy 'test' not found")
	}

	p := pol.Policies[0]

	if len(p.Include) != 1 {
		t.Errorf("got %d include rules, want 1", len(p.Include))
	}
	if p.Include[0].Selector != "emails" {
		t.Errorf("include selector = %q, want %q", p.Include[0].Selector, "emails")
	}

	if len(p.Require) != 1 {
		t.Errorf("got %d require rules, want 1", len(p.Require))
	}
	if p.Require[0].Selector != "country" {
		t.Errorf("require selector = %q, want %q", p.Require[0].Selector, "country")
	}

	if len(p.Exclude) != 1 {
		t.Errorf("got %d exclude rules, want 1", len(p.Exclude))
	}
	if p.Exclude[0].Selector != "ip_ranges" {
		t.Errorf("exclude selector = %q, want %q", p.Exclude[0].Selector, "ip_ranges")
	}
}

func TestParser_Parse_Access_SessionDuration(t *testing.T) {
	parser := NewParser("labelgate")

	labels := map[string]string{
		"labelgate.access.test.session_duration":      "12h",
		"labelgate.access.test.app_name":              "My App",
		"labelgate.access.test.policy.decision":       "bypass",
		"labelgate.access.test.policy.include.everyone": "",
	}

	result := parser.Parse(labels)

	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}

	pol := result.AccessPolicies["test"]
	if pol == nil {
		t.Fatal("expected access policy 'test' not found")
	}

	if pol.SessionDuration != "12h" {
		t.Errorf("session_duration = %q, want %q", pol.SessionDuration, "12h")
	}
	if pol.AppName != "My App" {
		t.Errorf("app_name = %q, want %q", pol.AppName, "My App")
	}
}

func TestParser_Parse_Access_DefaultSessionDuration(t *testing.T) {
	parser := NewParser("labelgate")

	labels := map[string]string{
		"labelgate.access.test.policy.decision":         "bypass",
		"labelgate.access.test.policy.include.everyone": "",
	}

	result := parser.Parse(labels)

	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}

	pol := result.AccessPolicies["test"]
	if pol.SessionDuration != "24h" {
		t.Errorf("default session_duration = %q, want %q", pol.SessionDuration, "24h")
	}
}

func TestParser_Parse_Access_MultiplePolicies(t *testing.T) {
	parser := NewParser("labelgate")

	labels := map[string]string{
		// Policy "internal"
		"labelgate.access.internal.policy.decision":       "allow",
		"labelgate.access.internal.policy.include.emails": "admin@example.com",
		// Policy "public"
		"labelgate.access.public.policy.decision":         "bypass",
		"labelgate.access.public.policy.include.everyone": "",
	}

	result := parser.Parse(labels)

	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(result.AccessPolicies) != 2 {
		t.Fatalf("got %d access policies, want 2", len(result.AccessPolicies))
	}

	if _, ok := result.AccessPolicies["internal"]; !ok {
		t.Error("expected access policy 'internal' not found")
	}
	if _, ok := result.AccessPolicies["public"]; !ok {
		t.Error("expected access policy 'public' not found")
	}
}

func TestParser_Parse_Access_DefaultPolicySkipped(t *testing.T) {
	parser := NewParser("labelgate")

	labels := map[string]string{
		// "default" policy name should be skipped
		"labelgate.access.default.policy.decision":         "bypass",
		"labelgate.access.default.policy.include.everyone": "",
	}

	result := parser.Parse(labels)

	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(result.AccessPolicies) != 0 {
		t.Fatalf("got %d access policies, want 0 (default should be skipped)", len(result.AccessPolicies))
	}
}

func TestParser_Parse_TunnelWithAccessRef(t *testing.T) {
	parser := NewParser("labelgate")

	labels := map[string]string{
		"labelgate.tunnel.web.hostname": "app.example.com",
		"labelgate.tunnel.web.service":  "http://localhost:8080",
		"labelgate.tunnel.web.access":   "internal",
	}

	result := parser.Parse(labels)

	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(result.TunnelServices) != 1 {
		t.Fatalf("got %d tunnel services, want 1", len(result.TunnelServices))
	}
	if result.TunnelServices[0].Access != "internal" {
		t.Errorf("tunnel access ref = %q, want %q", result.TunnelServices[0].Access, "internal")
	}
}

func TestParser_Parse_DNSWithAccessRef(t *testing.T) {
	parser := NewParser("labelgate")

	labels := map[string]string{
		"labelgate.dns.web.hostname": "web.example.com",
		"labelgate.dns.web.access":   "public",
	}

	result := parser.Parse(labels)

	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(result.DNSServices) != 1 {
		t.Fatalf("got %d dns services, want 1", len(result.DNSServices))
	}
	if result.DNSServices[0].Access != "public" {
		t.Errorf("dns access ref = %q, want %q", result.DNSServices[0].Access, "public")
	}
}

func TestParser_Parse_Access_MixedWithTunnelAndDNS(t *testing.T) {
	parser := NewParser("labelgate")

	labels := map[string]string{
		// Access policy definition
		"labelgate.access.internal.policy.decision":       "allow",
		"labelgate.access.internal.policy.include.emails": "admin@example.com",
		// Tunnel referencing the policy
		"labelgate.tunnel.web.hostname": "app.example.com",
		"labelgate.tunnel.web.service":  "http://localhost:8080",
		"labelgate.tunnel.web.access":   "internal",
		// DNS (no access)
		"labelgate.dns.api.hostname": "api.example.com",
		"labelgate.dns.api.type":     "A",
		"labelgate.dns.api.target":   "auto",
	}

	result := parser.Parse(labels)

	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(result.AccessPolicies) != 1 {
		t.Errorf("got %d access policies, want 1", len(result.AccessPolicies))
	}
	if len(result.TunnelServices) != 1 {
		t.Errorf("got %d tunnel services, want 1", len(result.TunnelServices))
	}
	if len(result.DNSServices) != 1 {
		t.Errorf("got %d dns services, want 1", len(result.DNSServices))
	}
	if result.TunnelServices[0].Access != "internal" {
		t.Errorf("tunnel access ref = %q, want %q", result.TunnelServices[0].Access, "internal")
	}
	if result.DNSServices[0].Access != "" {
		t.Errorf("dns access ref = %q, want empty", result.DNSServices[0].Access)
	}
}
