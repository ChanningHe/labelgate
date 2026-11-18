package agent

import (
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// The actionable Docker hints are longer than a socket error; 1024 bytes
// keep them whole while still bounding what a peer can put into the log.
const wantMaxDockerErrorLen = 1024

type lockedBuffer struct {
	mu sync.Mutex
	sb strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.String()
}

func TestMaxDockerErrorLen(t *testing.T) {
	if maxDockerErrorLen != wantMaxDockerErrorLen {
		t.Fatalf("maxDockerErrorLen = %d, want %d", maxDockerErrorLen, wantMaxDockerErrorLen)
	}
}

func TestDockerErrorFitsEveryReadLimit(t *testing.T) {
	frame, err := encode(MsgReport, Report{DockerError: strings.Repeat("x", maxDockerErrorLen)})
	if err != nil {
		t.Fatal(err)
	}
	// Even before authentication, where the limit is smallest, a maximal
	// error leaves ample room; after it, the limit is orders of magnitude larger.
	if len(frame)*10 > MaxHandshakeMessage {
		t.Fatalf("a %d-byte report frame is not comfortably below the %d-byte handshake read limit", len(frame), MaxHandshakeMessage)
	}
	if len(frame)*1000 > MaxMessage {
		t.Fatalf("a %d-byte report frame is not comfortably below the %d-byte read limit", len(frame), MaxMessage)
	}
}

func TestAgentKeepsDockerErrorUpToTheBound(t *testing.T) {
	long := strings.Repeat("a", 3*maxDockerErrorLen)
	cases := []struct {
		name    string
		message string
		wantLen int
	}{
		{"below the bound is kept whole", strings.Repeat("a", maxDockerErrorLen-1), maxDockerErrorLen - 1},
		{"at the bound is kept whole", strings.Repeat("a", maxDockerErrorLen), maxDockerErrorLen},
		{"beyond the bound is cut to it", long, maxDockerErrorLen},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAgent(t, outboundConnect("ws://x/ws"), &fakeSource{err: errors.New(tc.message)}, nil)
			rep := a.buildReport(t.Context(), slog.New(slog.DiscardHandler))
			if len(rep.DockerError) != tc.wantLen {
				t.Fatalf("DockerError is %d bytes, want %d", len(rep.DockerError), tc.wantLen)
			}
		})
	}
}

func TestHubLogsDockerErrorUpToTheBound(t *testing.T) {
	logs := &lockedBuffer{}
	hh := startHub(t, namedServer(), func(o *HubOptions) {
		o.Log = slog.New(slog.NewTextHandler(logs, nil))
	}, nil)
	ws := connectAgent(t, hh.url, "named", "named-token")
	marker := "END"
	msg := strings.Repeat("a", maxDockerErrorLen-len(marker)) + marker + strings.Repeat("b", 2*maxDockerErrorLen)

	sendReport(t, ws, Report{Seq: 1, DockerOK: false, DockerError: msg})

	out := logs.String()
	if !strings.Contains(out, marker) {
		t.Fatalf("the hint was cut below the bound:\n%s", out)
	}
	if strings.Contains(out, "bb") {
		t.Fatalf("the error was not bounded:\n%s", out)
	}
}
