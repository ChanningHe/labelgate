package agent

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func captureIDLog(src agentIDSource) string {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logAgentID(log, "abc123", src)
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
