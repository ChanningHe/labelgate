package model

import (
	"errors"
	"regexp"
	"strings"
)

// Sentinel errors shared across packages. Wrap them with fmt.Errorf("...: %w").
var (
	// ErrNotFound: the requested entity does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict: the entity exists but is not owned by this instance, or a
	// claim lost conflict resolution.
	ErrConflict = errors.New("conflict")
	// ErrInvalid: input failed validation.
	ErrInvalid = errors.New("invalid")
)

// Error message prefixes stored in Resource.LastError. The API surfaces them verbatim.
const (
	// ConflictNotOwned is used when Cloudflare already has an object labelgate does not own.
	ConflictNotOwned = "conflict: existing record not owned by labelgate"
)

var (
	agentIDPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	instanceIDPattern = regexp.MustCompile(`^[a-z0-9]{4,32}$`)
	hostLabelPattern  = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)
)

// ValidAgentID reports whether id is a valid agent ID.
func ValidAgentID(id string) bool { return agentIDPattern.MatchString(id) }

// ValidInstanceID reports whether id is a valid instance ID (lower-case alphanumeric, 4-32).
func ValidInstanceID(id string) bool { return instanceIDPattern.MatchString(id) }

// NormalizeHostname lower-cases, trims and removes one trailing dot.
func NormalizeHostname(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// ValidHostname reports whether h (already normalized) is a valid DNS name.
// A leading "*." wildcard label is allowed; underscores are allowed (SRV/TXT names).
func ValidHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return false
	}
	for i, l := range labels {
		if i == 0 && l == "*" {
			continue
		}
		if !hostLabelPattern.MatchString(l) {
			return false
		}
	}
	return true
}

// DNSMarker returns the ownership marker placed at the start of DNS comments.
func DNSMarker(instanceID string) string { return "labelgate:" + instanceID }

// DNSComment builds the comment for an owned record: marker, then the user comment.
func DNSComment(instanceID, userComment string) string {
	m := DNSMarker(instanceID)
	if userComment == "" {
		return m
	}
	return m + " " + userComment
}

// IsOwnedComment reports whether a live DNS comment carries this instance's marker.
// "labelgate:abcd1234" and "labelgate:abcd1234 text" match; "labelgate:abcd12345" does not.
func IsOwnedComment(instanceID, comment string) bool {
	m := DNSMarker(instanceID)
	return comment == m || strings.HasPrefix(comment, m+" ")
}

// AccessTag returns the Access tag marking applications owned by this instance.
func AccessTag(instanceID string) string { return "labelgate-" + instanceID }
