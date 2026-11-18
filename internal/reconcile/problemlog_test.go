package reconcile

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

// captureLog routes the reconciler's log into the returned buffer without timestamps.
func captureLog(h *harness) *bytes.Buffer {
	var buf bytes.Buffer
	h.r.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
	return &buf
}

// cycleLog runs one cycle one minute later and returns what it logged.
func cycleLog(h *harness, buf *bytes.Buffer) []string {
	h.clk.advance(time.Minute)
	buf.Reset()
	h.cycle()
	return strings.FieldsFunc(buf.String(), func(r rune) bool { return r == '\n' })
}

func TestProblemLoggedOnFirstAppearanceOnly(t *testing.T) {
	h := newHarness(t)
	buf := captureLog(h)
	h.src.setErr(errBoom)

	first := cycleLog(h, buf)
	if len(first) != 1 || !strings.Contains(first[0], `level=WARN msg="reconcile problem"`) ||
		!strings.Contains(first[0], `subject="local Docker"`) || !strings.Contains(first[0], "boom") {
		t.Fatalf("first cycle: %q", first)
	}
	for i := range 3 {
		if got := cycleLog(h, buf); len(got) != 0 {
			t.Fatalf("unchanged cycle %d logged: %q", i, got)
		}
	}
}

func TestProblemLoggedAgainWhenTextChanges(t *testing.T) {
	h := newHarness(t)
	buf := captureLog(h)
	h.src.setErr(errBoom)
	cycleLog(h, buf)

	h.src.setErr(errors.New("socket gone"))
	got := cycleLog(h, buf)
	if len(got) != 1 || !strings.Contains(got[0], "reconcile problem\"") || !strings.Contains(got[0], "socket gone") {
		t.Fatalf("changed text: %q", got)
	}
}

func TestProblemResolvedLoggedOnceAtInfo(t *testing.T) {
	h := newHarness(t)
	buf := captureLog(h)
	h.src.setErr(errBoom)
	cycleLog(h, buf)

	h.src.set()
	got := cycleLog(h, buf)
	if len(got) != 1 || !strings.Contains(got[0], `level=INFO msg="reconcile problem resolved"`) ||
		!strings.Contains(got[0], `subject="local Docker"`) || !strings.Contains(got[0], "boom") {
		t.Fatalf("resolved: %q", got)
	}
	if got := cycleLog(h, buf); len(got) != 0 {
		t.Fatalf("resolution logged twice: %q", got)
	}
}

func TestPersistentProblemIsRepeatedHourly(t *testing.T) {
	h := newHarness(t)
	buf := captureLog(h)
	h.src.setErr(errBoom)
	cycleLog(h, buf)

	h.clk.advance(problemRepeatInterval - 2*time.Minute)
	if got := cycleLog(h, buf); len(got) != 0 {
		t.Fatalf("before the interval: %q", got)
	}
	got := cycleLog(h, buf)
	if len(got) != 1 || !strings.Contains(got[0], "level=WARN") || !strings.Contains(got[0], "still_present=true") {
		t.Fatalf("at the interval: %q", got)
	}
}

func TestDistinctProblemsAreTrackedIndependently(t *testing.T) {
	h := newHarness(t)
	buf := captureLog(h)
	bad := model.ParsedLabels{Errors: []model.LabelError{{Key: "labelgate.dns.web.type", Message: "bad type"}}}
	h.src.set(ctr("a", bad), ctr("b", bad))
	if got := cycleLog(h, buf); len(got) != 2 {
		t.Fatalf("two problems, want two lines: %q", got)
	}

	h.src.set(ctr("a", bad))
	got := cycleLog(h, buf)
	if len(got) != 1 || !strings.Contains(got[0], "reconcile problem resolved") || !strings.Contains(got[0], "container b") {
		t.Fatalf("only b is resolved: %q", got)
	}
}

func TestAbortedCycleDoesNotResolveProblems(t *testing.T) {
	h := newHarness(t)
	buf := captureLog(h)
	h.src.setErr(errBoom)
	cycleLog(h, buf)

	h.st.failNext("AgentSnapshots", errBoom) // aborts before the Docker state is collected
	got := cycleLog(h, buf)
	if len(got) != 1 || !strings.Contains(got[0], `subject="reconcile cycle"`) {
		t.Fatalf("aborted cycle must log only the abort: %q", got)
	}
	// The next full cycle resolves the abort; the unchanged Docker problem stays silent.
	got = cycleLog(h, buf)
	if len(got) != 1 || !strings.Contains(got[0], "reconcile problem resolved") || !strings.Contains(got[0], `subject="reconcile cycle"`) {
		t.Fatalf("after the abort: %q", got)
	}
}
