package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/logchange"
)

// ipHarness drives buildReport with a controllable public IP lookup and clock.
type ipHarness struct {
	a     *Agent
	buf   *bytes.Buffer
	now   time.Time
	v4Err error
	v6Err error
	log   *slog.Logger
}

func newIPHarness(t *testing.T) *ipHarness {
	t.Helper()
	h := &ipHarness{buf: &bytes.Buffer{}, now: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	h.log = slog.New(slog.NewTextHandler(h.buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
	h.a = newTestAgent(t, outboundConnect("ws://x/ws"), &fakeSource{}, func(o *AgentOptions) {
		o.Now = func() time.Time { return h.now }
		o.PublicIP = func(_ context.Context, v6 bool) (netip.Addr, error) {
			if v6 {
				return netip.MustParseAddr("2001:db8::1"), h.v6Err
			}
			return netip.MustParseAddr("198.51.100.7"), h.v4Err
		}
	})
	return h
}

// report runs one report one heartbeat later and returns what it logged.
func (h *ipHarness) report() []string {
	h.now = h.now.Add(30 * time.Second)
	h.buf.Reset()
	h.a.buildReport(context.Background(), h.log)
	return strings.FieldsFunc(h.buf.String(), func(r rune) bool { return r == '\n' })
}

func TestIPFailureWarnsOnceThenStaysQuiet(t *testing.T) {
	h := newIPHarness(t)
	h.v4Err = errors.New("all endpoints failed")

	first := h.report()
	if len(first) != 1 {
		t.Fatalf("first failure: %q", first)
	}
	for _, want := range []string{"level=WARN", "IPv4", "target auto", "outbound internet access", "all endpoints failed"} {
		if !strings.Contains(first[0], want) {
			t.Errorf("warning lacks %q: %s", want, first[0])
		}
	}
	for i := range 5 {
		if got := h.report(); len(got) != 0 {
			t.Fatalf("heartbeat %d logged: %q", i, got)
		}
	}
}

func TestIPFailureWarnsAgainWhenErrorChanges(t *testing.T) {
	h := newIPHarness(t)
	h.v4Err = errors.New("timeout")
	h.report()

	h.v4Err = errors.New("unexpected status 503")
	got := h.report()
	if len(got) != 1 || !strings.Contains(got[0], "level=WARN") || !strings.Contains(got[0], "503") {
		t.Fatalf("changed error: %q", got)
	}
}

func TestIPRecoveryLoggedOnceAtInfo(t *testing.T) {
	h := newIPHarness(t)
	h.v4Err = errors.New("timeout")
	h.report()

	h.v4Err = nil
	got := h.report()
	if len(got) != 1 || !strings.Contains(got[0], "level=INFO") || !strings.Contains(got[0], "IPv4") || !strings.Contains(got[0], "recovered") {
		t.Fatalf("recovery: %q", got)
	}
	if got := h.report(); len(got) != 0 {
		t.Fatalf("recovery logged twice: %q", got)
	}
}

func TestIPFamiliesAreTrackedSeparately(t *testing.T) {
	h := newIPHarness(t)
	h.v6Err = errors.New("no route")
	got := h.report()
	if len(got) != 1 || !strings.Contains(got[0], "IPv6") {
		t.Fatalf("IPv6 failure: %q", got)
	}
	h.v4Err = errors.New("timeout")
	got = h.report()
	if len(got) != 1 || !strings.Contains(got[0], "IPv4") {
		t.Fatalf("a new IPv4 failure must not repeat the IPv6 one: %q", got)
	}
}

func TestIPFailureRepeatsAfterTheRepeatInterval(t *testing.T) {
	h := newIPHarness(t)
	h.v4Err = errors.New("timeout")
	h.report()

	h.now = h.now.Add(logchange.DefaultRepeat - 2*time.Minute)
	if got := h.report(); len(got) != 0 {
		t.Fatalf("before the interval: %q", got)
	}
	if got := h.report(); len(got) != 0 {
		t.Fatalf("still before the interval: %q", got)
	}
	h.now = h.now.Add(time.Minute)
	if got := h.report(); len(got) != 1 || !strings.Contains(got[0], "level=WARN") {
		t.Fatalf("at the interval: %q", got)
	}
}

func TestIPLookupCancelledByShutdownIsNotAFailure(t *testing.T) {
	h := newIPHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.a.opts.PublicIP = func(ctx context.Context, _ bool) (netip.Addr, error) { return netip.Addr{}, ctx.Err() }
	h.a.buildReport(ctx, h.log)
	if h.buf.Len() != 0 {
		t.Fatalf("logged on shutdown: %s", h.buf)
	}
}

func TestIPWrongFamilyIsAFailure(t *testing.T) {
	h := newIPHarness(t)
	h.a.opts.PublicIP = func(_ context.Context, v6 bool) (netip.Addr, error) {
		_ = v6 // both families answer with an IPv4 address
		return netip.MustParseAddr("198.51.100.7"), nil
	}
	got := h.report()
	if len(got) != 1 || !strings.Contains(got[0], "IPv6") || !strings.Contains(got[0], "not an IPv6 address") {
		t.Fatalf("wrong family: %q", got)
	}
}

func TestIPv6FailureIsInfoAndNeverRepeats(t *testing.T) {
	h := newIPHarness(t)
	h.v6Err = errors.New("dial tcp [2606:4700::6810:84e5]:443: connect: network is unreachable")
	got := h.report()
	if len(got) != 1 {
		t.Fatalf("first IPv6 failure: %q", got)
	}
	for _, want := range []string{"level=INFO", "IPv6", "network is unreachable", "no IPv6"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("IPv6 line lacks %q: %s", want, got[0])
		}
	}
	h.now = h.now.Add(3 * logchange.DefaultRepeat)
	if got := h.report(); len(got) != 0 {
		t.Fatalf("IPv6 failure repeated: %q", got)
	}
}

func TestIPv4FailureStaysWarnNextToIPv6Info(t *testing.T) {
	h := newIPHarness(t)
	h.v4Err = errors.New("timeout")
	h.v6Err = errors.New("no route")
	got := strings.Join(h.report(), "\n")
	if !strings.Contains(got, `level=WARN msg="cannot detect this host's public IPv4`) {
		t.Errorf("IPv4 must be WARN: %s", got)
	}
	if !strings.Contains(got, `level=INFO msg="cannot detect this host's public IPv6`) {
		t.Errorf("IPv6 must be INFO: %s", got)
	}
}
