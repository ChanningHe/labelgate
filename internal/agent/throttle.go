package agent

import (
	"sync"
	"time"
)

// Failed handshakes are unauthenticated, so anyone who reaches the agent
// port can cause them. Each remote address and claimed agent ID logs at most
// one warning per window; the map is capped so spoofed IDs cannot grow it.
const (
	handshakeLogWindow  = 5 * time.Minute
	maxHandshakeLogKeys = 1024
)

// logThrottle allows one log line per key and window, counting the
// suppressed ones. Safe for concurrent use.
type logThrottle struct {
	window time.Duration
	max    int

	mu   sync.Mutex
	seen map[string]throttleEntry
}

type throttleEntry struct {
	loggedAt   time.Time
	suppressed int
}

func newLogThrottle(window time.Duration, max int) *logThrottle {
	return &logThrottle{window: window, max: max, seen: make(map[string]throttleEntry)}
}

// allow reports whether an event for key at now should be logged and, if
// so, how many events for key were suppressed since it was last logged.
func (l *logThrottle) allow(now time.Time, key string) (ok bool, suppressed int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	prev, known := l.seen[key]
	if known && now.Sub(prev.loggedAt) < l.window {
		l.seen[key] = throttleEntry{loggedAt: prev.loggedAt, suppressed: prev.suppressed + 1}
		return false, 0
	}
	if !known && len(l.seen) >= l.max {
		l.evictOldest()
	}
	l.seen[key] = throttleEntry{loggedAt: now}
	return true, prev.suppressed
}

// evictOldest drops the entry logged longest ago. Callers hold mu.
func (l *logThrottle) evictOldest() {
	var (
		oldest string
		at     time.Time
		found  bool
	)
	for k, e := range l.seen {
		if !found || e.loggedAt.Before(at) {
			oldest, at, found = k, e.loggedAt, true
		}
	}
	delete(l.seen, oldest)
}

// size returns the number of tracked keys.
func (l *logThrottle) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.seen)
}
