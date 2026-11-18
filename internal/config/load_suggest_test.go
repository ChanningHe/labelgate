package config

import (
	"strings"
	"testing"
)

func TestLoad_UnknownKeySuggestions(t *testing.T) {
	tests := []struct {
		name, body, field, want string
	}{
		{"misplaced known key", "  skip_credential_validation: true\n", "cloudflare.skip_credential_validation",
			"did you mean skip_credential_validation?"},
		{"typo within section", "sync:\n  remove_dely: 1m\n", "sync.remove_dely", "did you mean sync.remove_delay?"},
		{"typo at top level", "log_levl: debug\n", "log_levl", "did you mean log_level?"},
		{"typo in credential entry", "  credentials:\n    c:\n      api_tokn: x\n", "cloudflare.credentials.c.api_tokn",
			"did you mean cloudflare.credentials.<name>.api_token?"},
		{"typo in agent entry with dotted id", "agent:\n  agents:\n    nas.lan:\n      tokn: x\n", "agent.agents.nas.lan.tokn",
			"did you mean agent.agents.<name>.token?"},
		{"typo in tunnel entry", "  tunnels:\n    t:\n      acount_id: a\n", "cloudflare.tunnels.t.acount_id",
			"did you mean cloudflare.tunnels.<name>.account_id?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := loadYAML(t, tt.body)
			msg := messageFor(t, err, tt.field)
			if !strings.HasPrefix(msg, "unknown key") || !strings.Contains(msg, tt.want) {
				t.Errorf("message %q: want unknown key ... %q", msg, tt.want)
			}
		})
	}
}

func TestLoad_UnknownKeyWithoutCloseMatchKeepsGenericAdvice(t *testing.T) {
	err := loadYAML(t, "webhook:\n  url: x\nsync:\n  zzzzzzzz: 1\n")

	for _, field := range []string{"webhook", "sync.zzzzzzzz"} {
		msg := messageFor(t, err, field)
		if strings.Contains(msg, "did you mean") || !strings.Contains(msg, "check the spelling") {
			t.Errorf("%s: unexpected message %q", field, msg)
		}
	}
}

func TestSuggestKey(t *testing.T) {
	tests := []struct{ section, name, want string }{
		{"cloudflare", "skip_credential_validation", "skip_credential_validation"},
		{"sync", "remove_dely", "sync.remove_delay"},
		{"sync", "orphan_tt", "sync.orphan_ttl"},
		{"db", "ph", ""},    // short names never match by distance
		{"sync", "xyz", ""}, // nothing close
		{"retry", "delays", "retry.delay"},
		{"sync", "heartbeat_interval", "connect.heartbeat_interval"},
		{"cloudflare.credentials.*", "api_tokn", "cloudflare.credentials.<name>.api_token"},
		{"", "log_levl", "log_level"},
	}
	for _, tt := range tests {
		t.Run(tt.section+"/"+tt.name, func(t *testing.T) {
			if got := suggestKey(tt.section, tt.name); got != tt.want {
				t.Errorf("suggestKey(%q, %q) = %q, want %q", tt.section, tt.name, got, tt.want)
			}
		})
	}
}

func TestSuggestKey_AmbiguousLastSegmentListsAllCandidates(t *testing.T) {
	got := suggestKey("sync", "listen")

	for _, want := range []string{"agent.listen", "connect.listen"} {
		if !strings.Contains(got, want) {
			t.Errorf("suggestKey = %q, want it to mention %s", got, want)
		}
	}
}

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{{"", "abc", 3}, {"abc", "abc", 0}, {"kitten", "sitting", 3}, {"delay", "dely", 1}, {"ab", "ba", 2}}
	for _, tt := range tests {
		if got := editDistance(tt.a, tt.b); got != tt.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// messageFor returns the message of the problem on field.
func messageFor(t *testing.T, err error, field string) string {
	t.Helper()
	for _, ve := range problemsOf(t, err) {
		if ve.Field == field {
			return ve.Message
		}
	}
	t.Fatalf("no problem for %s in: %v", field, err)
	return ""
}

func problemsOf(t *testing.T, err error) ValidationErrors {
	t.Helper()
	switch e := err.(type) {
	case *ValidationError:
		return ValidationErrors{e}
	case ValidationErrors:
		return e
	}
	t.Fatalf("want validation problems, got %T: %v", err, err)
	return nil
}
