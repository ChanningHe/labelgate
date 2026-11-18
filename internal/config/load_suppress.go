package config

import "strings"

// agentEntryPrefix starts the fields of one agent entry. Agent IDs may contain
// dots, so such fields are matched by prefix and suffix, never split on ".".
const agentEntryPrefix = "agent.agents."

// dependents lists, per decode-failed field, the validation problems that
// would only be noise because the check relies on the failed value. A pattern
// matches the field itself, everything below it, or, with one "*", any field
// with the given prefix and suffix.
var dependents = map[string][]string{
	// A failed mode leaves the default in cfg, so mode-specific checks would
	// validate the wrong section.
	"mode": {"connect", "sync", "db", "api", "cloudflare", "agent", "default_tunnel"},
	// max_delay is only comparable to a valid delay.
	"retry.delay":  {"retry.max_delay"},
	"connect.mode": {"connect.listen", "connect.endpoint"},
	"api.enabled":  {"api"},
	// The checks below resolve references to tunnels and credentials.
	"agent.enabled":          {"agent.listen"},
	"cloudflare.tunnel_id":   {"default_tunnel", "agent.accept_tunnels", agentEntryPrefix + "*.default_tunnel", agentEntryPrefix + "*.tunnels"},
	"cloudflare.tunnels":     {"default_tunnel", "agent.accept_tunnels", agentEntryPrefix + "*.default_tunnel", agentEntryPrefix + "*.tunnels"},
	"cloudflare.credentials": {"cloudflare.api_token", "cloudflare.tunnels"},
	"cloudflare.api_token":   {"cloudflare.tunnels"},
}

// dropCovered removes the problems that a decode failure already explains: a
// problem on a failed field or below it, or on a field listed in dependents.
// The failed field's cfg value is the default, so validating it would add a
// misleading second error ("must be >= 10s, got 5m") after "expected a
// duration".
func dropCovered(found, failures ValidationErrors) ValidationErrors {
	if len(failures) == 0 {
		return found
	}
	var kept ValidationErrors
	for _, ve := range found {
		if !coveredBy(ve.Field, failures) {
			kept = append(kept, ve)
		}
	}
	return kept
}

func coveredBy(field string, failures ValidationErrors) bool {
	for _, f := range failures {
		if inSubtree(field, f.Field) {
			return true
		}
		if dependsOnFailed(field, f.Field) {
			return true
		}
	}
	return false
}

// dependsOnFailed reports whether field is a dependent of failed or of one of
// its ancestors (a failed map entry also fails the section it belongs to).
func dependsOnFailed(field, failed string) bool {
	for key := failed; key != ""; {
		for _, pattern := range dependents[key] {
			if matchesField(pattern, field) {
				return true
			}
		}
		i := strings.LastIndex(key, ".")
		if i < 0 {
			break
		}
		key = key[:i]
	}
	return false
}

// inSubtree reports whether field is root or lies below it.
func inSubtree(field, root string) bool {
	return field == root || strings.HasPrefix(field, root+".")
}

// matchesField matches field against a dependents pattern.
func matchesField(pattern, field string) bool {
	prefix, suffix, wildcard := strings.Cut(pattern, "*")
	if !wildcard {
		return inSubtree(field, pattern)
	}
	return len(field) >= len(prefix)+len(suffix) &&
		strings.HasPrefix(field, prefix) && strings.HasSuffix(field, suffix)
}
