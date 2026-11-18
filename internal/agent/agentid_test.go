package agent

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
)

func captureIDLog(src agentIDSource) string {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logAgentID(log, "abc123", src, config.ConnectOutbound)
	return buf.String()
}

func TestLogAgentID_WarnsOnlyOnHostnameFallback(t *testing.T) {
	for _, src := range []agentIDSource{idExplicit, idFromMachineID} {
		out := captureIDLog(src)
		if strings.Contains(out, "level=WARN") {
			t.Errorf("source %q: unexpected warning: %s", src, out)
		}
		if strings.Count(out, "level=INFO") != 1 || !strings.Contains(out, "source="+string(src)) {
			t.Errorf("source %q: want one INFO line naming the source, got: %s", src, out)
		}
	}

	out := captureIDLog(idFromHostname)
	if strings.Count(out, "level=WARN") != 1 {
		t.Fatalf("want exactly one warning, got: %s", out)
	}
	for _, want := range []string{
		`agent ID abc123 comes from the container hostname`,
		"changes when the container is recreated",
		"sync.agent_grace (env LABELGATE_SYNC_AGENT_GRACE)",
		"/etc/machine-id:/etc/machine-id:ro",
		"connect.agent_id (env LABELGATE_CONNECT_AGENT_ID)",
		"takes priority",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("warning does not contain %q: %s", want, out)
		}
	}
}

func TestLogAgentID_NamesResolvedIDSourceAndMode(t *testing.T) {
	out := captureIDLog(idFromMachineID)
	for _, want := range []string{`msg="Agent mode"`, "agent_id=abc123", "agent_id_source=machine-id", "connect_mode=outbound"} {
		if !strings.Contains(out, want) {
			t.Errorf("startup line lacks %q: %s", want, out)
		}
	}
}

func TestRunLogsResolvedAgentIDOnce(t *testing.T) {
	var buf lockedBuffer
	c := outboundConnect("ws://127.0.0.1:1/ws")
	c.AgentID = ""
	a := newTestAgent(t, c, &fakeSource{}, func(o *AgentOptions) { o.Log = slog.New(slog.NewTextHandler(&buf, nil)) })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = a.Run(ctx)

	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, `msg="Agent mode"`) {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no startup line: %s", buf.String())
	}
	if !strings.Contains(line, "agent_id="+a.ID()) || strings.Contains(line, `agent_id=""`) {
		t.Errorf("startup line lacks the resolved ID %q: %s", a.ID(), line)
	}
	if n := strings.Count(line, "agent_id="); n != 1 {
		t.Errorf("agent_id appears %d times: %s", n, line)
	}
}
