package agent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/gorilla/websocket"
)

const waitFor = 5 * time.Second

func testTimings() timings {
	return timings{
		handshake: 2 * time.Second,
		write:     2 * time.Second,
		ping:      time.Hour,
		pongWait:  5 * time.Second,
		debounce:  20 * time.Millisecond,
	}
}

func testRetry() config.RetryConfig {
	return config.RetryConfig{Attempts: 1, Delay: 10 * time.Millisecond, MaxDelay: 50 * time.Millisecond, Backoff: 2}
}

// checkLeaks fails the test if goroutines started during it are still
// running at cleanup. Register it first so it runs last.
func checkLeaks(t *testing.T) {
	t.Helper()
	base := runtime.NumGoroutine()
	t.Cleanup(func() {
		deadline := time.Now().Add(waitFor)
		for runtime.NumGoroutine() > base {
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<20)
				n := runtime.Stack(buf, true)
				t.Errorf("goroutine leak: %d > %d\n%s", runtime.NumGoroutine(), base, buf[:n])
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// --- fake store ---

type recordedReport struct {
	rec  model.AgentRecord
	snap *model.Snapshot
}

type fakeStore struct {
	mu       sync.Mutex
	reports  []recordedReport
	changed  bool
	err      error
	failNext int // number of upcoming calls that fail
}

func (s *fakeStore) RecordAgentReport(_ context.Context, rec model.AgentRecord, snap *model.Snapshot) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	if s.failNext > 0 {
		s.failNext--
		return false, errors.New("store unavailable")
	}
	s.reports = append(s.reports, recordedReport{rec, snap})
	return s.changed, nil
}

func (s *fakeStore) set(changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changed, s.err = changed, err
}

func (s *fakeStore) all() []recordedReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedReport(nil), s.reports...)
}

func (s *fakeStore) count() int { return len(s.all()) }

// --- fake source ---

type fakeSource struct {
	mu         sync.Mutex
	containers []model.Container
	err        error
	watchErr   error
	notify     func()
	snapshots  int
}

func (f *fakeSource) Snapshot(context.Context) (model.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots++
	if f.err != nil {
		return model.Snapshot{}, f.err
	}
	return model.Snapshot{Complete: true, Containers: append([]model.Container(nil), f.containers...)}, nil
}

func (f *fakeSource) Watch(ctx context.Context, notify func()) error {
	f.mu.Lock()
	f.notify = notify
	werr := f.watchErr
	f.mu.Unlock()
	if werr != nil {
		return werr
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeSource) set(cs []model.Container, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.containers, f.err = cs, err
}

func (f *fakeSource) fire() {
	f.mu.Lock()
	n := f.notify
	f.mu.Unlock()
	if n != nil {
		n()
	}
}

func containers(names ...string) []model.Container {
	out := make([]model.Container, 0, len(names))
	for _, n := range names {
		out = append(out, model.Container{ID: "id-" + n, Name: n, Labels: map[string]string{"labelgate.dns.web.hostname": n + ".example.com"}, State: "running"})
	}
	return out
}

// --- raw protocol client (test-side agent or main) ---

func rawDial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	d := &websocket.Dialer{HandshakeTimeout: waitFor}
	ws, resp, err := d.Dial(url, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	_ = ws.SetReadDeadline(time.Now().Add(waitFor))
	return ws
}

func mustWrite(t *testing.T, ws *websocket.Conn, typ MsgType, v any) {
	t.Helper()
	if err := writeDirect(ws, typ, v); err != nil {
		t.Fatalf("write %s: %v", typ, err)
	}
}

func mustRead(t *testing.T, ws *websocket.Conn) Envelope {
	t.Helper()
	mt, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	env, err := decodeEnvelope(mt, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env
}

func mustPayload[T any](t *testing.T, env Envelope, want MsgType) T {
	t.Helper()
	var v T
	if env.Type != want {
		t.Fatalf("got message %q (%s), want %q", env.Type, env.Payload, want)
	}
	if err := decodePayload(env, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// expectErrorCode reads until an error message arrives and checks its code.
func expectErrorCode(t *testing.T, ws *websocket.Conn, code ErrorCode) {
	t.Helper()
	m := mustPayload[ErrorMsg](t, mustRead(t, ws), MsgError)
	if m.Code != code {
		t.Fatalf("error code %q, want %q", m.Code, code)
	}
}

// expectClosed asserts the peer closes without sending any data frame.
func expectClosed(t *testing.T, ws *websocket.Conn) {
	t.Helper()
	mt, data, err := ws.ReadMessage()
	if err == nil {
		t.Fatalf("expected close, got frame type %d: %s", mt, data)
	}
}

// connectAgent performs a full agent-side handshake as id with token.
func connectAgent(t *testing.T, url, id, token string) *websocket.Conn {
	t.Helper()
	ws := rawDial(t, url)
	if err := agentHandshake(ws, ModeOutbound, id, token, "test", testTimings()); err != nil {
		t.Fatalf("agent handshake as %s: %v", id, err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(waitFor))
	_ = ws.SetWriteDeadline(time.Time{})
	return ws
}

func sendReport(t *testing.T, ws *websocket.Conn, rep Report) Ack {
	t.Helper()
	mustWrite(t, ws, MsgReport, rep)
	return mustPayload[Ack](t, mustRead(t, ws), MsgAck)
}

func containsSecret(data []byte, token string) bool {
	return bytes.Contains(data, []byte(token)) ||
		strings.Contains(string(data), base64.StdEncoding.EncodeToString([]byte(token)))
}

// --- TLS fixtures ---

type tlsFiles struct{ cert, key string }

// selfSignedCert writes a self-signed certificate valid for 127.0.0.1.
func selfSignedCert(t *testing.T) tlsFiles {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "labelgate-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := tlsFiles{cert: filepath.Join(dir, "cert.pem"), key: filepath.Join(dir, "key.pem")}
	writePEM(t, f.cert, "CERTIFICATE", der)
	writePEM(t, f.key, "EC PRIVATE KEY", kder)
	return f
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}
