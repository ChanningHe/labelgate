package agent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/gorilla/websocket"
)

const agentToken = "agent-secret-token"

func outboundConnect(endpoint string) config.ConnectConfig {
	return config.ConnectConfig{
		Mode: config.ConnectOutbound, Endpoint: endpoint, Token: agentToken,
		AgentID: "edge-1", HeartbeatInterval: time.Hour,
	}
}

func inboundConnect() config.ConnectConfig {
	return config.ConnectConfig{
		Mode: config.ConnectInbound, Listen: "127.0.0.1:0", Token: agentToken,
		AgentID: "edge-1", HeartbeatInterval: time.Hour,
	}
}

func newTestAgent(t *testing.T, c config.ConnectConfig, src *fakeSource, mutate func(*AgentOptions)) *Agent {
	t.Helper()
	opts := AgentOptions{
		Connect: c, Retry: testRetry(), Version: "test", Source: src,
		PublicIP: func(_ context.Context, v6 bool) (netip.Addr, error) {
			if v6 {
				return netip.MustParseAddr("2001:db8::1"), nil
			}
			return netip.MustParseAddr("::ffff:198.51.100.7"), nil
		},
	}
	if mutate != nil {
		mutate(&opts)
	}
	a, err := NewAgent(opts)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	a.t = testTimings()
	return a
}

// runAgent runs transport under a.run and stops it at cleanup.
func runAgent(t *testing.T, a *Agent, transport func(context.Context) error) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.run(ctx, transport) }()
	var once sync.Once
	var err error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case err = <-done:
			case <-time.After(waitFor):
				err = errors.New("agent did not stop")
			}
		})
		return err
	}
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Errorf("agent stop: %v", err)
		}
	})
	return stop
}

// fakeMain authenticates agents (mode outbound) and hands sessions to the test.
type fakeMain struct {
	url      string
	sessions chan *websocket.Conn
	frames   chan []byte // first frame of every connection (the hello)
}

func startFakeMain(t *testing.T, token string) *fakeMain {
	t.Helper()
	fm := &fakeMain{sessions: make(chan *websocket.Conn, 4), frames: make(chan []byte, 4)}
	up := newUpgrader(testTimings())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		creds := staticCreds(map[string]string{"edge-1": token})
		if _, err := mainHandshake(ws, ModeOutbound, creds, "dummy", testTimings()); err != nil {
			_ = ws.Close()
			return
		}
		_ = writeDirect(ws, MsgWelcome, Welcome{})
		_ = ws.SetReadDeadline(time.Now().Add(waitFor))
		_ = ws.SetWriteDeadline(time.Time{})
		fm.sessions <- ws
	}))
	t.Cleanup(srv.Close)
	fm.url = "ws" + strings.TrimPrefix(srv.URL, "http") + PathWS
	return fm
}

func (fm *fakeMain) session(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case ws := <-fm.sessions:
		t.Cleanup(func() { _ = ws.Close() })
		return ws
	case <-time.After(waitFor):
		t.Fatal("no agent session")
		return nil
	}
}

func readReport(t *testing.T, ws *websocket.Conn) Report {
	t.Helper()
	return mustPayload[Report](t, mustRead(t, ws), MsgReport)
}

func outbound(a *Agent) func(context.Context) error {
	return func(ctx context.Context) error {
		a.runOutbound(ctx, a.dialer(nil))
		return nil
	}
}

func TestAgentOutboundReports(t *testing.T) {
	checkLeaks(t)
	fm := startFakeMain(t, agentToken)
	src := &fakeSource{containers: containers("web")}
	a := newTestAgent(t, outboundConnect(fm.url), src, nil)
	stop := runAgent(t, a, outbound(a))
	ws := fm.session(t)

	first := readReport(t, ws) // on session start
	mustWrite(t, ws, MsgAck, Ack{Seq: first.Seq})
	mustWrite(t, ws, MsgRefresh, Refresh{})
	second := readReport(t, ws) // on refresh
	src.set(containers("web", "api"), nil)
	src.fire()
	src.fire() // coalesced by the debounce
	third := readReport(t, ws)

	if !first.DockerOK || len(first.Containers) != 1 || first.PublicIP != "198.51.100.7" || first.PublicIPv6 != "2001:db8::1" {
		t.Fatalf("first report: %+v", first)
	}
	if second.Seq <= first.Seq || third.Seq <= second.Seq || len(third.Containers) != 2 {
		t.Fatalf("reports out of order: %d %d %d (%d containers)", first.Seq, second.Seq, third.Seq, len(third.Containers))
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentHeartbeatReports(t *testing.T) {
	fm := startFakeMain(t, agentToken)
	c := outboundConnect(fm.url)
	c.HeartbeatInterval = 30 * time.Millisecond
	a := newTestAgent(t, c, &fakeSource{}, nil)
	runAgent(t, a, outbound(a))
	ws := fm.session(t)

	for range 3 {
		if rep := readReport(t, ws); !rep.DockerOK || rep.Containers == nil {
			t.Fatalf("heartbeat report: %+v", rep)
		}
	}
}

func TestAgentSnapshotErrorReportsDockerNotOK(t *testing.T) {
	fm := startFakeMain(t, agentToken)
	src := &fakeSource{err: errors.New("docker socket unavailable")}
	a := newTestAgent(t, outboundConnect(fm.url), src, func(o *AgentOptions) { o.PublicIP = nil })
	runAgent(t, a, outbound(a))

	rep := readReport(t, fm.session(t))

	if rep.DockerOK || rep.Containers != nil || !strings.Contains(rep.DockerError, "unavailable") || rep.PublicIP != "" {
		t.Fatalf("report: %+v", rep)
	}
}

func TestAgentTooManyContainersReportsDockerNotOK(t *testing.T) {
	fm := startFakeMain(t, agentToken)
	src := &fakeSource{containers: make([]model.Container, MaxContainers+1)}
	a := newTestAgent(t, outboundConnect(fm.url), src, nil)
	runAgent(t, a, outbound(a))

	rep := readReport(t, fm.session(t))

	if rep.DockerOK || !strings.Contains(rep.DockerError, "too many") {
		t.Fatalf("report: %+v", rep)
	}
}

func TestAgentLookupIPFamilies(t *testing.T) {
	cases := []struct {
		name  string
		fn    model.PublicIPFunc
		v4    string
		v6    string
		fails bool
	}{
		{"nil func", nil, "", "", false},
		{"error", func(context.Context, bool) (netip.Addr, error) { return netip.Addr{}, errors.New("x") }, "", "", true},
		{"wrong families", func(_ context.Context, v6 bool) (netip.Addr, error) {
			if v6 {
				return netip.MustParseAddr("1.2.3.4"), nil
			}
			return netip.MustParseAddr("2001:db8::2"), nil
		}, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAgent(t, outboundConnect("ws://x/ws"), &fakeSource{}, func(o *AgentOptions) { o.PublicIP = tc.fn })
			got4, err4 := a.lookupIP(context.Background(), false)
			got6, err6 := a.lookupIP(context.Background(), true)
			if got4 != tc.v4 || got6 != tc.v6 {
				t.Fatalf("v4 = %q, v6 = %q", got4, got6)
			}
			if (err4 != nil) != tc.fails || (err6 != nil) != tc.fails {
				t.Fatalf("errors = %v, %v; want failure: %v", err4, err6, tc.fails)
			}
		})
	}
}

func TestAgentEndsSessionOnMainErrorAndReconnects(t *testing.T) {
	fm := startFakeMain(t, agentToken)
	a := newTestAgent(t, outboundConnect(fm.url), &fakeSource{}, nil)
	runAgent(t, a, outbound(a))
	ws := fm.session(t)
	readReport(t, ws)

	mustWrite(t, ws, MsgError, ErrorMsg{Code: ErrInternal, Message: "boom"})

	expectClosed(t, ws)
	readReport(t, fm.session(t)) // reconnected with backoff
}

func TestAgentRejectsUnexpectedMessage(t *testing.T) {
	fm := startFakeMain(t, agentToken)
	a := newTestAgent(t, outboundConnect(fm.url), &fakeSource{}, nil)
	runAgent(t, a, outbound(a))
	ws := fm.session(t)
	readReport(t, ws)

	mustWrite(t, ws, MsgChallenge, Challenge{})

	for {
		env := mustRead(t, ws)
		if env.Type == MsgError {
			if m := mustPayload[ErrorMsg](t, env, MsgError); m.Code != ErrBadRequest {
				t.Fatalf("code %q", m.Code)
			}
			break
		}
	}
	expectClosed(t, ws)
}

func TestAgentWrongMainTokenSendsOnlyHello(t *testing.T) {
	// The main does not know the agent's token: the agent must not send auth.
	var mu sync.Mutex
	var frames [][]byte
	up := newUpgrader(testTimings())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_ = ws.SetReadDeadline(time.Now().Add(waitFor))
		var h Hello
		if readExpect(ws, MsgHello, &h) != nil {
			return
		}
		nm := newNonce()
		_ = writeDirect(ws, MsgChallenge, Challenge{Nonce: nm, MAC: MAC("guess", RoleMain, ModeOutbound, h.AgentID, h.Nonce, nm)})
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			mu.Lock()
			frames = append(frames, data)
			mu.Unlock()
		}
	}))
	defer srv.Close()
	a := newTestAgent(t, outboundConnect("ws"+strings.TrimPrefix(srv.URL, "http")), &fakeSource{}, nil)
	stop := runAgent(t, a, outbound(a))

	time.Sleep(200 * time.Millisecond) // several attempts with 10-50ms backoff
	_ = stop()

	mu.Lock()
	defer mu.Unlock()
	if len(frames) != 0 {
		t.Fatalf("agent sent %d frames after an unverified challenge: %s", len(frames), frames[0])
	}
}

func TestAgentBackoffRespectsCancel(t *testing.T) {
	checkLeaks(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens: every dial fails
	a := newTestAgent(t, outboundConnect("ws://"+addr+PathWS), &fakeSource{}, func(o *AgentOptions) {
		o.Retry = config.RetryConfig{Attempts: 1, Delay: time.Hour, MaxDelay: time.Hour, Backoff: 2}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	time.Sleep(50 * time.Millisecond) // first dial failed, now sleeping 30-60 minutes

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitFor):
		t.Fatal("Run ignored ctx during backoff")
	}
}

func TestAgentWatchErrorStopsRun(t *testing.T) {
	checkLeaks(t)
	fm := startFakeMain(t, agentToken)
	src := &fakeSource{watchErr: errors.New("cannot subscribe")}
	a := newTestAgent(t, outboundConnect(fm.url), src, nil)

	err := a.Run(context.Background())

	if err == nil || !strings.Contains(err.Error(), "cannot subscribe") {
		t.Fatalf("err = %v", err)
	}
}

// --- inbound mode (main dials the agent) ---

func startInboundAgent(t *testing.T, src *fakeSource) (*Agent, string) {
	t.Helper()
	a := newTestAgent(t, inboundConnect(), src, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runAgent(t, a, func(ctx context.Context) error { return a.serveInbound(ctx, ln) })
	return a, ln.Addr().String()
}

// dialAsMain authenticates to an inbound agent the way the hub does.
func dialAsMain(t *testing.T, addr, token string) (*websocket.Conn, error) {
	t.Helper()
	ws := rawDial(t, "ws://"+addr+PathWS)
	if _, err := mainHandshake(ws, ModeInbound, staticCreds(map[string]string{"edge-1": token}), "dummy", testTimings()); err != nil {
		return ws, err
	}
	if err := writeDirect(ws, MsgWelcome, Welcome{}); err != nil {
		return ws, err
	}
	_ = ws.SetReadDeadline(time.Now().Add(waitFor))
	_ = ws.SetWriteDeadline(time.Time{})
	return ws, nil
}

func TestAgentInboundSessionReplacement(t *testing.T) {
	checkLeaks(t)
	_, addr := startInboundAgent(t, &fakeSource{containers: containers("web")})
	first, err := dialAsMain(t, addr, agentToken)
	if err != nil {
		t.Fatal(err)
	}
	readReport(t, first)

	second, err := dialAsMain(t, addr, agentToken)
	if err != nil {
		t.Fatal(err)
	}

	readReport(t, second)
	for { // the older session is closed (any queued frames, then close)
		if _, _, err := first.ReadMessage(); err != nil {
			break
		}
	}
	mustWrite(t, second, MsgRefresh, Refresh{})
	readReport(t, second)
}

func TestAgentInboundRejectsMainWithWrongToken(t *testing.T) {
	_, addr := startInboundAgent(t, &fakeSource{})

	ws, err := dialAsMain(t, addr, "wrong-token")

	if err == nil {
		t.Fatal("main with wrong token accepted")
	}
	expectClosed(t, ws)
}

func TestInboundAttackerLearnsNothing(t *testing.T) {
	_, addr := startInboundAgent(t, &fakeSource{containers: containers("secret-app")})
	ws := rawDial(t, "ws://"+addr+PathWS)

	_, hello, err := ws.ReadMessage() // the agent speaks first
	if err != nil {
		t.Fatal(err)
	}
	var h Hello
	env, _ := decodeEnvelope(websocket.TextMessage, hello)
	_ = decodePayload(env, &h)
	mustWrite(t, ws, MsgChallenge, Challenge{Nonce: newNonce(), MAC: make([]byte, 32)})
	mt, rest, err := ws.ReadMessage()

	if env.Type != MsgHello || h.AgentID != "edge-1" || len(h.Nonce) != NonceSize {
		t.Fatalf("unexpected hello: %s", hello)
	}
	for _, leak := range []string{agentToken, "secret-app", `"mac"`, `"containers"`} {
		if containsSecret(hello, leak) {
			t.Fatalf("hello leaks %q: %s", leak, hello)
		}
	}
	if err == nil {
		t.Fatalf("agent answered a forged challenge with frame type %d: %s", mt, rest)
	}
}

func TestInboundAttackerHandshakeTimeout(t *testing.T) {
	a := newTestAgent(t, inboundConnect(), &fakeSource{}, nil)
	a.t.handshake = 100 * time.Millisecond
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runAgent(t, a, func(ctx context.Context) error { return a.serveInbound(ctx, ln) })
	ws := rawDial(t, "ws://"+ln.Addr().String()+PathWS)
	mustRead(t, ws) // hello

	start := time.Now()
	_, _, err = ws.ReadMessage() // never answer

	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("no handshake timeout: %v after %v", err, time.Since(start))
	}
}

func TestAgentInboundHealthBody(t *testing.T) {
	_, addr := startInboundAgent(t, &fakeSource{})
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	resp, err := client.Get("http://" + addr + PathHealth)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != `{"status":"ok"}` || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("health: %q %v", body, resp.Header)
	}
}

func TestAgentRunInboundListenError(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	c := inboundConnect()
	c.Listen = busy.Addr().String()
	a := newTestAgent(t, c, &fakeSource{}, nil)

	if err := a.Run(context.Background()); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestAgentRunInboundStopsOnCancel(t *testing.T) {
	checkLeaks(t)
	a := newTestAgent(t, inboundConnect(), &fakeSource{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := a.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunOutboundBadCA(t *testing.T) {
	c := outboundConnect("wss://127.0.0.1:1/ws")
	c.TLS.CA = "/nonexistent/ca.pem"
	a := newTestAgent(t, c, &fakeSource{}, nil)

	if err := a.Run(context.Background()); err == nil {
		t.Fatal("expected CA error")
	}
}

func TestNewAgentValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*AgentOptions)
	}{
		{"nil source", func(o *AgentOptions) { o.Source = nil }},
		{"empty token", func(o *AgentOptions) { o.Connect.Token = "" }},
		{"zero heartbeat", func(o *AgentOptions) { o.Connect.HeartbeatInterval = 0 }},
		{"bad endpoint", func(o *AgentOptions) { o.Connect.Endpoint = "http://main/ws" }},
		{"unparsable endpoint", func(o *AgentOptions) { o.Connect.Endpoint = "ws://[::1" }},
		{"unknown mode", func(o *AgentOptions) { o.Connect.Mode = "sideways" }},
		{"inbound without listen", func(o *AgentOptions) { o.Connect.Mode, o.Connect.Listen = config.ConnectInbound, "" }},
		{"inbound cert without key", func(o *AgentOptions) {
			o.Connect.Mode, o.Connect.Listen, o.Connect.TLS.Cert = config.ConnectInbound, ":0", "c.pem"
		}},
		{"invalid explicit id", func(o *AgentOptions) { o.Connect.AgentID = "Not Valid" }},
		{"bad retry", func(o *AgentOptions) { o.Retry.Backoff = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := AgentOptions{Connect: outboundConnect("ws://main/ws"), Retry: testRetry(), Source: &fakeSource{}}
			tc.mutate(&o)
			if _, err := NewAgent(o); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	a, err := NewAgent(AgentOptions{Connect: outboundConnect("ws://main/ws"), Retry: testRetry(), Source: &fakeSource{}})
	if err != nil || a.ID() != "edge-1" {
		t.Fatalf("valid options: %v", err)
	}
}

func TestResolveAgentID(t *testing.T) {
	ok := func(s string) func() (string, error) { return func() (string, error) { return s, nil } }
	fail := func() (string, error) { return "", errors.New("no") }
	cases := []struct {
		name      string
		explicit  string
		machine   func() (string, error)
		host      func() (string, error)
		want      string
		wantSrc   agentIDSource
		wantError bool
	}{
		{"explicit", "edge-1", fail, fail, "edge-1", idExplicit, false},
		{"explicit wins over machine id", "edge-1", ok("0123456789abcdef"), ok("h"), "edge-1", idExplicit, false},
		{"explicit invalid", "Edge 1", ok("0123456789abcdef"), ok("h"), "", "", true},
		{"machine id", "", ok("0123456789ABCDEF0123\n"), ok("h"), "0123456789ab", idFromMachineID, false},
		{"machine id not hex", "", ok("zzzzzzzzzzzzzzzz"), ok("My.Host"), "my.host", idFromHostname, false},
		{"machine id short", "", ok("abc"), ok("host_1"), "host_1", idFromHostname, false},
		{"hostname sanitized", "", fail, ok("--Web Server.local"), "web-server.local", idFromHostname, false},
		{"hostname lower-cased, dots kept", "", fail, ok("NAS.lan"), "nas.lan", idFromHostname, false},
		{"hostname truncated", "", fail, ok(strings.Repeat("a", 80)), strings.Repeat("a", 63), idFromHostname, false},
		{"nothing usable", "", fail, ok("..."), "", "", true},
		{"all fail", "", fail, fail, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, src, err := resolveAgentID(tc.explicit, tc.machine, tc.host)
			if (err != nil) != tc.wantError || got != tc.want || src != tc.wantSrc {
				t.Fatalf("got %q (%q), %v; want %q (%q) (error %v)", got, src, err, tc.want, tc.wantSrc, tc.wantError)
			}
		})
	}
}
