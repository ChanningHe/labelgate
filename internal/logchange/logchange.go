// Package logchange decides which recurring problems are worth logging: a
// problem is reported when it appears or its cause (see Class) changes, once
// more when it is resolved, and optionally again at a low rate while it
// persists. Every report carries the latest full text.
//
// A Tracker is not safe for concurrent use; each owner serializes access.
package logchange

import (
	"sort"
	"time"
)

// DefaultRepeat is the suggested repeat interval: a persisting problem is
// logged again once an hour, so it stays visible without flooding the log.
const DefaultRepeat = time.Hour

// Kind classifies an Event.
type Kind int

const (
	// Raised: the problem is new, or its Class changed.
	Raised Kind = iota + 1
	// Repeated: the problem's Class is unchanged but the repeat interval
	// elapsed; Text is the latest text.
	Repeated
	// Resolved: the problem is gone; Text is its last known text.
	Resolved
)

// Event is one thing to log.
type Event struct {
	Kind Kind
	Key  string
	Text string
}

type entry struct {
	text     string // latest text
	class    string // Class(text) when it was last logged
	loggedAt time.Time
}

// Tracker remembers the problems seen by the previous Update.
type Tracker struct {
	repeat time.Duration
	seen   map[string]entry
}

// New returns a Tracker that re-reports a persisting problem every repeat;
// repeat <= 0 never does.
func New(repeat time.Duration) *Tracker {
	return &Tracker{repeat: repeat, seen: map[string]entry{}}
}

// Update takes the complete set of currently active problems (key -> text)
// and returns the events to log: Raised and Repeated sorted by key, then
// Resolved sorted by key.
func (t *Tracker) Update(now time.Time, active map[string]string) []Event {
	return t.update(now, active, true)
}

// UpdatePartial is Update for an incomplete observation (e.g. an aborted
// cycle): problems missing from active are not reported as resolved.
func (t *Tracker) UpdatePartial(now time.Time, active map[string]string) []Event {
	return t.update(now, active, false)
}

func (t *Tracker) update(now time.Time, active map[string]string, resolve bool) []Event {
	var events []Event
	for _, key := range sortedKeys(active) {
		text, class := active[key], Class(active[key])
		prev, known := t.seen[key]
		switch {
		case !known || prev.class != class:
			events = append(events, Event{Raised, key, text})
		case t.repeat > 0 && now.Sub(prev.loggedAt) >= t.repeat:
			events = append(events, Event{Repeated, key, text})
		default:
			t.seen[key] = entry{text: text, class: prev.class, loggedAt: prev.loggedAt}
			continue
		}
		t.seen[key] = entry{text: text, class: class, loggedAt: now}
	}
	if !resolve {
		return events
	}
	for _, key := range sortedKeys(t.seen) {
		if _, ok := active[key]; !ok {
			events = append(events, Event{Resolved, key, t.seen[key].text})
			delete(t.seen, key)
		}
	}
	return events
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
