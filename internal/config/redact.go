package config

import (
	"net/url"
	"strings"
)

// redactedUserinfo replaces the userinfo of a URL that cannot be parsed.
const redactedUserinfo = "***"

// redactURL returns raw safe to echo in a message: the password of a parsable
// URL is masked, and for an unparsable one everything before the last "@" is
// dropped, because it may be userinfo.
func redactURL(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Redacted()
	}
	at := strings.LastIndex(raw, "@")
	if at < 0 {
		return raw
	}
	scheme := ""
	if i := strings.Index(raw, "://"); i >= 0 && i < at {
		scheme = raw[:i+len("://")]
	}
	return scheme + redactedUserinfo + raw[at:]
}
