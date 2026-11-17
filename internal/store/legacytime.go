package store

import (
	"regexp"
	"strings"
	"time"
)

// monotonicSuffix is the " m=+0.123456789" tail that time.Time.String() adds
// when the value carries a monotonic clock reading.
var monotonicSuffix = regexp.MustCompile(`\s+m=[+-][0-9.]+$`)

// legacyTimeLayouts are the formats found in v0.2 databases, in the order
// they are tried:
//   - Go time.Time.String(), written by modernc.org/sqlite for time.Time
//     arguments (zone offset of the writing process, e.g. "+0800 CST");
//   - SQLite CURRENT_TIMESTAMP defaults (UTC, no zone);
//   - RFC 3339 variants.
var legacyTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999 -0700 -0700",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04:05.999999999",
	time.RFC3339Nano,
}

// parseLegacyTime converts a v0.2 text timestamp to unix seconds. Numeric
// zone offsets in the text are honoured, so the result does not depend on the
// local time zone. ok is false when s matches no known layout.
func parseLegacyTime(s string) (sec int64, ok bool) {
	s = strings.TrimSpace(monotonicSuffix.ReplaceAllString(strings.TrimSpace(s), ""))
	if s == "" {
		return 0, false
	}
	for _, layout := range legacyTimeLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.Unix(), true
		}
	}
	return 0, false
}
