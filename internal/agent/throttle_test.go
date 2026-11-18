package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLogThrottleSuppressesWithinWindow(t *testing.T) {
	th := newLogThrottle(time.Minute, 10)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if ok, n := th.allow(t0, "a"); !ok || n != 0 {
		t.Fatalf("first: ok=%v n=%d", ok, n)
	}
	for i := range 3 {
		if ok, _ := th.allow(t0.Add(time.Duration(i+1)*time.Second), "a"); ok {
			t.Fatalf("repeat %d within the window was allowed", i)
		}
	}
	if ok, _ := th.allow(t0.Add(time.Second), "b"); !ok {
		t.Fatal("another key must be independent")
	}
	ok, n := th.allow(t0.Add(time.Minute), "a")
	if !ok || n != 3 {
		t.Fatalf("after the window: ok=%v suppressed=%d, want true 3", ok, n)
	}
	if ok, _ := th.allow(t0.Add(time.Minute+time.Second), "a"); ok {
		t.Fatal("the window restarts after a logged failure")
	}
}

func TestLogThrottleIsBounded(t *testing.T) {
	const max = 4
	th := newLogThrottle(time.Hour, max)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 100 {
		th.allow(t0.Add(time.Duration(i)*time.Second), fmt.Sprintf("k%d", i))
	}
	if n := th.size(); n != max {
		t.Fatalf("size = %d, want %d", n, max)
	}
	// The newest keys survive; the oldest were evicted and log again.
	if ok, _ := th.allow(t0.Add(100*time.Second), "k99"); ok {
		t.Error("newest key was evicted")
	}
	if ok, _ := th.allow(t0.Add(100*time.Second), "k0"); !ok {
		t.Error("oldest key was not evicted")
	}
}

// fakeClock is a concurrency-safe settable clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestHubThrottlesFailedHandshakeLogs(t *testing.T) {
	var buf lockedBuffer
	clock := &fakeClock{now: fixedNow}
	hh := startHub(t, namedServer(), func(o *HubOptions) {
		o.Log = slog.New(slog.NewTextHandler(&buf, nil))
		o.Now = clock.Now
	}, nil)
	fail := func(id string) {
		ws := rawDial(t, hh.url)
		_ = agentHandshake(ws, ModeOutbound, id, "bad", "v", testTimings())
		_ = ws.Close()
		eventually(t, "handshake slot released", func() bool { return hh.hub.pending.count() == 0 })
	}
	lines := func(id string) []string {
		var out []string
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.Contains(l, "agent handshake failed") && strings.Contains(l, `\"`+id+`\"`) {
				out = append(out, l)
			}
		}
		return out
	}

	for range 3 {
		fail("stranger")
	}
	fail("other")
	if got := lines("stranger"); len(got) != 1 {
		t.Fatalf("want one line for 3 failures, got %d:\n%s", len(got), buf.String())
	}
	if got := lines("other"); len(got) != 1 {
		t.Fatalf("another claimed ID must log on its own: %s", buf.String())
	}

	clock.add(handshakeLogWindow)
	fail("stranger")
	got := lines("stranger")
	if len(got) != 2 || !strings.Contains(got[1], "2 more failed handshakes from this address with this agent ID") {
		t.Fatalf("after the window: %q", got)
	}
}

func TestInboundAgentThrottlesFailedHandshakeLogs(t *testing.T) {
	var buf lockedBuffer
	clock := &fakeClock{now: fixedNow}
	a := newTestAgent(t, inboundConnect(), &fakeSource{}, func(o *AgentOptions) {
		o.Log = slog.New(slog.NewTextHandler(&buf, nil))
		o.Now = clock.Now
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runAgent(t, a, func(ctx context.Context) error { return a.serveInbound(ctx, ln) })
	fail := func() {
		ws, err := dialAsMain(t, ln.Addr().String(), "wrong-token")
		if err == nil {
			t.Fatal("main with wrong token accepted")
		}
		_ = ws.Close()
		eventually(t, "handshake slot released", func() bool { return a.pending.count() == 0 })
	}
	lines := func() []string {
		var out []string
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.Contains(l, "handshake with the main instance failed") {
				out = append(out, l)
			}
		}
		return out
	}

	for range 3 {
		fail()
	}
	if got := lines(); len(got) != 1 {
		t.Fatalf("want one line for 3 failures, got %d:\n%s", len(got), buf.String())
	}

	clock.add(handshakeLogWindow)
	fail()
	got := lines()
	if len(got) != 2 || !strings.Contains(got[1], "2 more failed handshakes from this address") {
		t.Fatalf("after the window: %q", got)
	}
}
