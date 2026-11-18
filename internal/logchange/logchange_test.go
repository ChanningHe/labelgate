package logchange

import (
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func ev(k Kind, key, text string) Event { return Event{Kind: k, Key: key, Text: text} }

func TestTrackerUpdate(t *testing.T) {
	tr := New(time.Hour)
	steps := []struct {
		name   string
		at     time.Duration
		active map[string]string
		want   []Event
	}{
		{"first appearance is reported", 0, map[string]string{"a": "boom"}, []Event{ev(Raised, "a", "boom")}},
		{"unchanged problem is silent", time.Minute, map[string]string{"a": "boom"}, nil},
		{"unchanged problem is silent again", 2 * time.Minute, map[string]string{"a": "boom"}, nil},
		{"changed text is reported", 3 * time.Minute, map[string]string{"a": "bang"}, []Event{ev(Raised, "a", "bang")}},
		{"resolved problem reports its last text", 4 * time.Minute, nil, []Event{ev(Resolved, "a", "bang")}},
		{"resolution is reported once", 5 * time.Minute, nil, nil},
		{"a resolved problem that returns is new", 6 * time.Minute, map[string]string{"a": "bang"}, []Event{ev(Raised, "a", "bang")}},
	}
	for _, s := range steps {
		got := tr.Update(t0.Add(s.at), s.active)
		if !reflect.DeepEqual(got, s.want) {
			t.Fatalf("%s: got %v, want %v", s.name, got, s.want)
		}
	}
}

func TestTrackerRepeatsAfterInterval(t *testing.T) {
	tr := New(time.Hour)
	active := map[string]string{"a": "boom"}
	tr.Update(t0, active)
	if got := tr.Update(t0.Add(time.Hour-time.Second), active); got != nil {
		t.Fatalf("before the interval: %v", got)
	}
	if got, want := tr.Update(t0.Add(time.Hour), active), []Event{ev(Repeated, "a", "boom")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("at the interval: got %v, want %v", got, want)
	}
	if got := tr.Update(t0.Add(time.Hour+time.Minute), active); got != nil {
		t.Fatalf("the interval restarts after a repeat: %v", got)
	}
}

func TestTrackerChangeRestartsRepeatInterval(t *testing.T) {
	tr := New(time.Hour)
	tr.Update(t0, map[string]string{"a": "one"})
	tr.Update(t0.Add(50*time.Minute), map[string]string{"a": "two"})
	if got := tr.Update(t0.Add(70*time.Minute), map[string]string{"a": "two"}); got != nil {
		t.Fatalf("the change was logged 20m ago: %v", got)
	}
}

func TestTrackerZeroIntervalNeverRepeats(t *testing.T) {
	tr := New(0)
	active := map[string]string{"a": "boom"}
	tr.Update(t0, active)
	if got := tr.Update(t0.Add(1000*time.Hour), active); got != nil {
		t.Fatalf("got %v", got)
	}
}

func TestTrackerEventsAreSortedByKey(t *testing.T) {
	tr := New(time.Hour)
	got := tr.Update(t0, map[string]string{"b": "2", "a": "1", "c": "3"})
	want := []Event{ev(Raised, "a", "1"), ev(Raised, "b", "2"), ev(Raised, "c", "3")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("raised: got %v, want %v", got, want)
	}
	got = tr.Update(t0.Add(time.Minute), map[string]string{"b": "2"})
	want = []Event{ev(Resolved, "a", "1"), ev(Resolved, "c", "3")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved: got %v, want %v", got, want)
	}
}

func TestTrackerPartialUpdateDoesNotResolve(t *testing.T) {
	tr := New(time.Hour)
	tr.Update(t0, map[string]string{"a": "1", "b": "2"})
	got := tr.UpdatePartial(t0.Add(time.Minute), map[string]string{"b": "2", "c": "3"})
	if want := []Event{ev(Raised, "c", "3")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("partial: got %v, want %v", got, want)
	}
	if got := tr.Update(t0.Add(2*time.Minute), map[string]string{"a": "1", "b": "2", "c": "3"}); got != nil {
		t.Fatalf("a missing problem must stay known after a partial update: %v", got)
	}
}
