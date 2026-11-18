package config

import (
	"errors"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/viper"
)

// decodeFailedPrefix starts the wrapper message mapstructure puts around the
// joined per-field errors.
const decodeFailedPrefix = "decoding failed due to the following error(s):"

var (
	unusedKeysRegexp = regexp.MustCompile(`^'([^']*)' has invalid keys: (.+)$`)
	fieldErrRegexp   = regexp.MustCompile(`(?s)^'([^']*)' (.*)$`)
	mapIndexRegexp   = regexp.MustCompile(`\[([^\]]+)\]`)
)

// removedProblems reports every removed v0.2 key (or its parent section)
// present in the file or the environment, sorted, each once. A bare removed
// section (e.g. an empty docker.ssh block, or one holding only unknown keys)
// yields one problem per removed key under it, unless one of those keys is
// set explicitly, in which case only the set keys are reported.
func removedProblems(v *viper.Viper, env envLookup) ValidationErrors {
	keys := sortedKeys(RemovedKeys)
	present := map[string]bool{}
	for _, key := range keys {
		if _, inEnv := env(envName(key)); inEnv || v.IsSet(viperKey(key)) {
			present[key] = true
		}
	}
	reported := maps.Clone(present)
	for _, key := range keys {
		section := key[:strings.LastIndex(key, ".")]
		if v.IsSet(viperKey(section)) && !knownSection(section) && !anyUnder(present, section) {
			reported[key] = true
		}
	}
	var p ValidationErrors
	for _, key := range keys {
		if reported[key] {
			p.addf(key, "%s", RemovedKeys[key])
		}
	}
	return p
}

// anyUnder reports whether set holds a key below section.
func anyUnder(set map[string]bool, section string) bool {
	for key := range set {
		if strings.HasPrefix(key, section+".") {
			return true
		}
	}
	return false
}

// knownSection reports whether section is still a valid config section, so a
// file merely containing it does not imply a removed key.
func knownSection(section string) bool {
	for _, k := range leafKeys(reflect.TypeFor[Config](), "") {
		if strings.HasPrefix(k, section+".") {
			return true
		}
	}
	return false
}

// isRemovedPath reports whether key is a removed key or a section holding
// only removed keys; removedProblems already covers both.
func isRemovedPath(key string) bool {
	if _, ok := RemovedKeys[key]; ok {
		return true
	}
	for removed := range RemovedKeys {
		if strings.HasPrefix(removed, key+".") {
			return true
		}
	}
	return false
}

// decodeProblems splits a strict-decoding failure into unknown-key problems
// (sorted, removed keys excluded) and every other decode failure (sorted by
// field). mapstructure aggregates one error per field.
func decodeProblems(err error) (unknown, failures ValidationErrors) {
	if strings.HasPrefix(err.Error(), decodeFailedPrefix) {
		if inner := errors.Unwrap(err); inner != nil {
			err = inner
		}
	}
	for _, leaf := range leafErrors(err) {
		msg := leaf.Error()
		if m := unusedKeysRegexp.FindStringSubmatch(msg); m != nil {
			unknown = append(unknown, unknownKeys(m[1], m[2])...)
			continue
		}
		failures = append(failures, decodeFailure(msg))
	}
	byField := func(a, b *ValidationError) int {
		return strings.Compare(a.Field+"\x00"+a.Message, b.Field+"\x00"+b.Message)
	}
	slices.SortFunc(unknown, byField)
	slices.SortFunc(failures, byField)
	return unknown, failures
}

// unknownKeys expands "'section' has invalid keys: a, b" into one problem per
// key, skipping removed keys.
func unknownKeys(section, list string) ValidationErrors {
	var p ValidationErrors
	for _, name := range strings.Split(list, ",") {
		key := dotted(section + "." + strings.TrimSpace(name))
		if !isRemovedPath(key) {
			p.addf(key, "unknown key")
		}
	}
	return p
}

// decodeFailure turns "'field' message" into a problem; a message without a
// field name is attributed to "config".
func decodeFailure(msg string) *ValidationError {
	msg = strings.TrimSpace(strings.TrimPrefix(msg, decodeFailedPrefix))
	if m := fieldErrRegexp.FindStringSubmatch(msg); m != nil && m[1] != "" {
		return fail(dotted(m[1]), "%s", m[2])
	}
	return fail("config", "%s", msg)
}

// leafErrors flattens joined errors into their individual members.
func leafErrors(err error) []error {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []error{err}
	}
	var leaves []error
	for _, e := range joined.Unwrap() {
		leaves = append(leaves, leafErrors(e)...)
	}
	return leaves
}

// dotted rewrites mapstructure paths ("a.b[c].d", ".x") into dotted config keys.
func dotted(path string) string {
	return strings.Trim(mapIndexRegexp.ReplaceAllString(path, ".$1"), ".")
}
