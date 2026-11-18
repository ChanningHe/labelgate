package config

import (
	"reflect"
	"strings"
)

const (
	// maxSuggestDistance is the largest edit distance still taken as a typo.
	maxSuggestDistance = 2
	// maxSuggestions caps the candidates of an ambiguous misplaced key.
	maxSuggestions = 3
	// entryWildcard stands for a map key (credential, tunnel or agent name) in
	// a section path; entryPlaceholder is how it is shown in a suggestion.
	entryWildcard    = "*"
	entryPlaceholder = "<name>"
)

// keySchema indexes every valid config key. Sections are addressed by their
// mapstructure path with map entries as "*" ("cloudflare.credentials.*").
type keySchema struct {
	// children maps a section to the names of the keys directly in it.
	children map[string][]string
	// byName maps a key's last segment to its full keys, as shown to users.
	byName map[string][]string
}

func buildKeySchema(t reflect.Type) keySchema {
	s := keySchema{children: map[string][]string{}, byName: map[string][]string{}}
	s.add(t, "", "")
	return s
}

// add records the keys of struct type t found at section (path) and shown
// as display. Map fields recurse into their entry type.
func (s keySchema) add(t reflect.Type, section, display string) {
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("mapstructure"), ",")
		if name == "" || name == "-" {
			continue
		}
		path, shown := joinKey(section, name), joinKey(display, name)
		s.children[section] = append(s.children[section], name)
		s.byName[name] = append(s.byName[name], shown)
		ft := t.Field(i).Type
		switch {
		case ft.Kind() == reflect.Struct:
			s.add(ft, path, shown)
		case ft.Kind() == reflect.Map && ft.Elem().Kind() == reflect.Struct:
			s.add(ft.Elem(), joinKey(path, entryWildcard), joinKey(shown, entryPlaceholder))
		}
	}
}

func joinKey(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// suggestKey proposes the key a user most likely meant by the unknown key
// name inside section (a mapstructure path, map entries as "*"). A valid key
// elsewhere with the same last segment wins (a misplaced key); otherwise the
// closest sibling within maxSuggestDistance. It returns "" when nothing fits.
func suggestKey(section, name string) string {
	schema := buildKeySchema(reflect.TypeFor[Config]())
	if keys := schema.byName[name]; len(keys) > 0 && len(keys) <= maxSuggestions {
		return strings.Join(keys, " or ")
	}
	best, bestDist := "", maxSuggestDistance+1
	for _, sibling := range schema.children[section] {
		d := editDistance(name, sibling)
		if d < bestDist && d < len(name) {
			best, bestDist = sibling, d
		}
	}
	if best == "" {
		return ""
	}
	return joinKey(displaySection(section), best)
}

// displaySection renders a section path for users ("*" becomes "<name>").
func displaySection(section string) string {
	return strings.ReplaceAll(section, entryWildcard, entryPlaceholder)
}

// editDistance is the Levenshtein distance between a and b (in bytes; config
// keys are ASCII).
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// sectionPattern turns a mapstructure section path ("cloudflare.credentials[c]")
// into its schema form ("cloudflare.credentials.*").
func sectionPattern(section string) string {
	return strings.Trim(mapIndexRegexp.ReplaceAllString(section, "."+entryWildcard), ".")
}
