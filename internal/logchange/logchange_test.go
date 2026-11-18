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

func TestTrackerIgnoresAlternatingCloudflareStatus(t *testing.T) {
	tr := New(time.Hour)
	unauthorized := "dns example.com: cannot create DNS record: cloudflare dns.create: HTTP 401 (codes [10000]): Authentication error; check the token"
	limited := "dns example.com: cannot create DNS record: cloudflare dns.create: HTTP 429 (codes [971]): Please wait and consider throttling"
	if got := tr.Update(t0, map[string]string{"a": unauthorized}); len(got) != 1 {
		t.Fatalf("first: %v", got)
	}
	for i, text := range []string{limited, unauthorized, limited} {
		if got := tr.Update(t0.Add(time.Duration(i+1)*time.Minute), map[string]string{"a": text}); got != nil {
			t.Fatalf("alternation %d re-logged: %v", i, got)
		}
	}
	// The hourly repeat and the resolution carry the latest full text.
	got := tr.Update(t0.Add(time.Hour), map[string]string{"a": limited})
	if want := []Event{ev(Repeated, "a", limited)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("repeat: got %v, want %v", got, want)
	}
	tr.Update(t0.Add(time.Hour+time.Minute), map[string]string{"a": unauthorized})
	got = tr.Update(t0.Add(time.Hour+2*time.Minute), nil)
	if want := []Event{ev(Resolved, "a", unauthorized)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolve: got %v, want %v", got, want)
	}
}

func TestTrackerIgnoresVaryingDialAddresses(t *testing.T) {
	tr := New(time.Hour)
	texts := []string{
		`Get "https://api64.ipify.org": dial tcp [2606:4700::6810:84e5]:443: connect: network is unreachable`,
		`Get "https://api64.ipify.org": dial tcp [2606:4700::6810:85e5]:443: connect: network is unreachable`,
		`Get "https://api64.ipify.org": dial tcp 2606:4700::6810:86e5: connect: network is unreachable`,
	}
	tr.Update(t0, map[string]string{"a": texts[0]})
	for i, text := range texts[1:] {
		if got := tr.Update(t0.Add(time.Duration(i+1)*time.Minute), map[string]string{"a": text}); got != nil {
			t.Fatalf("address change %d re-logged: %v", i, got)
		}
	}
	tr4 := New(time.Hour)
	tr4.Update(t0, map[string]string{"a": "read tcp 172.18.0.2:41234->104.16.1.1:443: i/o timeout"})
	if got := tr4.Update(t0.Add(time.Minute), map[string]string{"a": "read tcp 172.18.0.2:50110->104.16.2.9:443: i/o timeout"}); got != nil {
		t.Fatalf("IPv4 address change re-logged: %v", got)
	}
}

func TestTrackerReportsADifferentCause(t *testing.T) {
	tr := New(time.Hour)
	tr.Update(t0, map[string]string{"a": "dial tcp 10.0.0.1:443: connect: connection refused"})
	got := tr.Update(t0.Add(time.Minute), map[string]string{"a": "dial tcp 10.0.0.1:443: i/o timeout"})
	if want := []Event{ev(Raised, "a", "dial tcp 10.0.0.1:443: i/o timeout")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got = tr.Update(t0.Add(2*time.Minute), map[string]string{"a": "cloudflare dns.update: HTTP 429 (codes [971]): wait"})
	if len(got) != 1 || got[0].Kind != Raised {
		t.Fatalf("another Cloudflare operation must be raised: %v", got)
	}
}

func TestClass(t *testing.T) {
	tests := []struct{ in, want string }{
		{"dial tcp [2606:4700::1]:443: refused", "dial tcp <addr>: refused"},
		{"dial tcp 1.2.3.4:443: refused", "dial tcp <addr>: refused"},
		{"lookup x on 127.0.0.11:53: no such host", "lookup x on <addr>: no such host"},
		{"x: cloudflare dns.create: HTTP 401 (codes [10000]): bad", "x: cloudflare dns.create: HTTP error"},
		{"web1.example.com conflicts with 2 records", "web1.example.com conflicts with 2 records"},
		{"took 12:30:45", "took 12:30:45"},
	}
	for _, tt := range tests {
		if got := Class(tt.in); got != tt.want {
			t.Errorf("Class(%q) = %q, want %q", tt.in, got, tt.want)
		}
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
