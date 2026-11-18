package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/gorilla/websocket"
)

func TestBackoffGrowsCapsAndResets(t *testing.T) {
	b := newBackoff(config.RetryConfig{Delay: 100 * time.Millisecond, MaxDelay: time.Second, Backoff: 2})
	steps := []time.Duration{100, 200, 400, 800, 1000, 1000}
	for i, step := range steps {
		step *= time.Millisecond
		d := b.next()
		if d < step/2 || d > step {
			t.Fatalf("attempt %d: %v not in [%v, %v]", i, d, step/2, step)
		}
	}
	b.reset()
	if d := b.next(); d > 100*time.Millisecond {
		t.Fatalf("after reset: %v", d)
	}
}

func TestBackoffOverflowClampsToMax(t *testing.T) {
	b := newBackoff(config.RetryConfig{Delay: time.Second, MaxDelay: time.Duration(math.MaxInt64), Backoff: 1e18})
	b.next()
	if d := b.next(); d <= 0 {
		t.Fatalf("overflowed delay %v", d)
	}
}

func TestSleepCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("elapsed: %v", err)
	}
}

// wsPair returns a connected (server, client) WebSocket pair.
func wsPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	srvConn := make(chan *websocket.Conn, 1)
	up := &websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err == nil {
			srvConn <- ws
		}
	}))
	t.Cleanup(srv.Close)
	client := rawDial(t, "ws"+strings.TrimPrefix(srv.URL, "http"))
	server := <-srvConn
	t.Cleanup(func() { _ = server.Close() })
	return server, client
}

func TestWSConnConcurrentSendsSingleWriter(t *testing.T) {
	checkLeaks(t)
	server, client := wsPair(t)
	c := newWSConn(server, testTimings(), MaxMessage)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.start(ctx)
	const senders, each = 8, 50
	got := make(chan int, 1)
	go func() {
		n := 0
		for n < senders*each {
			if _, _, err := client.ReadMessage(); err != nil {
				break
			}
			n++
		}
		got <- n
	}()

	var wg sync.WaitGroup
	for i := range senders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range each {
				if i%2 == 0 {
					_ = c.send(MsgAck, Ack{Seq: uint64(j)})
				} else {
					_ = c.send(MsgRefresh, Refresh{})
				}
			}
		}()
	}
	wg.Wait()

	if n := <-got; n != senders*each {
		t.Fatalf("received %d frames, want %d", n, senders*each)
	}
	c.close()
	if err := c.send(MsgAck, Ack{}); !errors.Is(err, errClosed) {
		t.Fatalf("send after close: %v", err)
	}
}

func TestWSConnFailSendsErrorThenCloses(t *testing.T) {
	server, client := wsPair(t)
	c := newWSConn(server, testTimings(), MaxMessage)
	c.start(context.Background())

	c.fail(ErrTooLarge, "nope")

	expectErrorCode(t, client, ErrTooLarge)
	expectClosed(t, client)
	<-c.done
}

func TestWSConnStopsOnContextCancel(t *testing.T) {
	server, client := wsPair(t)
	c := newWSConn(server, testTimings(), MaxMessage)
	ctx, cancel := context.WithCancel(context.Background())
	c.start(ctx)

	cancel()

	<-c.done
	_, _, err := client.ReadMessage()
	var ce *websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != websocket.CloseGoingAway {
		t.Fatalf("err = %v, want going-away close", err)
	}
}

func TestWSConnReadLimitAfterAuth(t *testing.T) {
	server, client := wsPair(t)
	c := newWSConn(server, testTimings(), 1024)
	c.start(context.Background())
	defer c.close()

	_ = client.WriteMessage(websocket.TextMessage, bytes.Repeat([]byte("x"), 2048))

	if _, err := c.read(); err == nil {
		t.Fatal("oversized frame accepted")
	}
}

func TestDecodeEdgeCases(t *testing.T) {
	if _, err := decodeEnvelope(websocket.TextMessage, []byte(`{"payload":{}}`)); !errors.Is(err, errProtocol) {
		t.Fatalf("untyped envelope: %v", err)
	}
	if err := decodePayload(Envelope{Type: MsgWelcome}, &Welcome{}); err != nil {
		t.Fatalf("absent payload: %v", err)
	}
	if _, err := encode(MsgReport, make(chan int)); err == nil {
		t.Fatal("unencodable payload accepted")
	}
	if got := (&PeerError{Code: ErrInternal}).Error(); got != "peer error: internal" {
		t.Fatalf("PeerError: %q", got)
	}
}

func TestValidateWSURL(t *testing.T) {
	cases := map[string]bool{
		"ws://main:28112/ws": true,
		"wss://main/ws":      true,
		"http://main/ws":     false,
		"ws:///ws":           false,
		"ws://[::1":          false,
		"main:28112":         false,
		"wss://user:pw@h/ws": true,
	}
	for raw, ok := range cases {
		if err := validateWSURL(raw); (err == nil) != ok {
			t.Errorf("validateWSURL(%q) = %v", raw, err)
		}
	}
	if got := redactURL("wss://user:pw@h/ws"); strings.Contains(got, "pw") {
		t.Errorf("redactURL leaks password: %q", got)
	}
	if got := redactURL("ws://[::1"); got != "<invalid url>" {
		t.Errorf("redactURL invalid: %q", got)
	}
}

func TestClientTLSConfig(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	notPEM := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(notPEM, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	cert := selfSignedCert(t)

	if _, err := clientTLSConfig(config.TLSConfig{CA: notPEM}, sectionConnect, log); err == nil {
		t.Fatal("non-PEM CA accepted")
	}
	if _, err := clientTLSConfig(config.TLSConfig{CA: "/nonexistent"}, sectionConnect, log); err == nil {
		t.Fatal("missing CA accepted")
	}
	cfg, err := clientTLSConfig(config.TLSConfig{CA: cert.cert}, sectionConnect, log)
	if err != nil || cfg.RootCAs == nil || cfg.InsecureSkipVerify {
		t.Fatalf("CA config: %+v, %v", cfg, err)
	}
	if logs.Len() != 0 {
		t.Fatalf("unexpected warning: %s", logs.String())
	}
	cfg, err = clientTLSConfig(config.TLSConfig{InsecureSkipVerify: true}, sectionConnect, log)
	if err != nil || !cfg.InsecureSkipVerify || !strings.Contains(logs.String(), "DISABLED") {
		t.Fatalf("insecure config: %+v, %v, log=%q", cfg, err, logs.String())
	}
}

func TestListenTLSPair(t *testing.T) {
	if _, err := listen("127.0.0.1:0", config.TLSConfig{Key: "k.pem"}, sectionConnect); err == nil {
		t.Fatal("key without cert accepted")
	}
	cert := selfSignedCert(t)
	ln, err := listen("127.0.0.1:0", config.TLSConfig{Cert: cert.cert, Key: cert.key}, sectionConnect)
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
}

func TestTruncate(t *testing.T) {
	if truncate("abcdef", 3) != "abc" || truncate("ab", 3) != "ab" {
		t.Fatal("truncate")
	}
}
