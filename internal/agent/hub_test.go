package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/gorilla/websocket"
)

var fixedNow = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func namedServer() config.AgentServerConfig {
	return config.AgentServerConfig{
		Listen:      "127.0.0.1:0",
		AcceptToken: "shared-token",
		Agents: map[string]config.AgentEntry{
			"named": {Token: "named-token", Name: "Named Host", DefaultTunnel: "t1"},
		},
	}
}

func withClock(o *HubOptions) { o.Now = func() time.Time { return fixedNow } }

func TestHubOutboundReportUsesServerClock(t *testing.T) {
	checkLeaks(t)
	hh := startHub(t, namedServer(), withClock, nil)
	ws := connectAgent(t, hh.url, "named", "named-token")

	ack := sendReport(t, ws, Report{Seq: 7, PublicIP: "203.0.113.5", DockerOK: true, Containers: containers("web")})

	if ack.Seq != 7 {
		t.Fatalf("ack seq %d", ack.Seq)
	}
	if !hh.hub.Connected("named") {
		t.Fatal("named not connected")
	}
	r := hh.store.all()[0]
	if !r.rec.LastSeen.Equal(fixedNow) || !r.snap.TakenAt.Equal(fixedNow) {
		t.Fatalf("timestamps not from server clock: %+v", r)
	}
	if r.rec.Name != "Named Host" || r.rec.DefaultTunnel != "t1" || r.rec.PublicIP != "203.0.113.5" {
		t.Fatalf("record: %+v", r.rec)
	}
	if !r.snap.Complete || r.snap.AgentID != "named" || len(r.snap.Containers) != 1 {
		t.Fatalf("snapshot: %+v", r.snap)
	}
	_ = ws.Close()
	hh.stop()
}

func TestHubAuthFailures(t *testing.T) {
	cases := []struct{ name, id, token string }{
		{"wrong token for named", "named", "bad"},
		{"named id with accept_token", "named", "shared-token"},
		{"unknown id with wrong shared token", "stranger", "bad"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hh := startHub(t, namedServer(), nil, nil)
			ws := rawDial(t, hh.url)

			err := agentHandshake(ws, ModeOutbound, tc.id, tc.token, "v", testTimings())

			if !errors.Is(err, errAuth) {
				t.Fatalf("err = %v, want errAuth (agent rejects main's MAC)", err)
			}
			if hh.hub.Connected(tc.id) {
				t.Fatal("unauthenticated agent registered")
			}
		})
	}
}

func TestHubAcceptTokenAgent(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)

	ws := connectAgent(t, hh.url, "guest-1", "shared-token")
	sendReport(t, ws, Report{Seq: 1, DockerOK: true})

	if !hh.hub.Connected("guest-1") {
		t.Fatal("accept_token agent not connected")
	}
	if r := hh.store.all()[0]; r.rec.Name != "" || r.snap == nil || len(r.snap.Containers) != 0 {
		t.Fatalf("unexpected record %+v", r)
	}
}

func TestHubNoAcceptTokenRejectsUnknownAgent(t *testing.T) {
	srv := namedServer()
	srv.AcceptToken = ""
	hh := startHub(t, srv, nil, nil)
	ws := rawDial(t, hh.url)
	mustWrite(t, ws, MsgHello, Hello{Proto: ProtocolVersion, AgentID: "stranger", Nonce: newNonce()})
	mustPayload[Challenge](t, mustRead(t, ws), MsgChallenge)

	mustWrite(t, ws, MsgAuth, Auth{MAC: MAC("", RoleAgent, ModeOutbound, "stranger", nil, nil)})

	expectErrorCode(t, ws, ErrUnauthorized)
	expectClosed(t, ws)
}

func TestHubRejectsReplayedHandshake(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)
	// Capture a legitimate handshake.
	ws := rawDial(t, hh.url)
	na := newNonce()
	hello := Hello{Proto: ProtocolVersion, AgentID: "named", Nonce: na}
	mustWrite(t, ws, MsgHello, hello)
	ch := mustPayload[Challenge](t, mustRead(t, ws), MsgChallenge)
	auth := Auth{MAC: MAC("named-token", RoleAgent, ModeOutbound, "named", na, ch.Nonce)}
	mustWrite(t, ws, MsgAuth, auth)
	mustPayload[Welcome](t, mustRead(t, ws), MsgWelcome)
	_ = ws.Close()
	eventually(t, "session end", func() bool { return !hh.hub.Connected("named") })

	// Replay the captured hello+auth on a new connection.
	replay := rawDial(t, hh.url)
	mustWrite(t, replay, MsgHello, hello)
	ch2 := mustPayload[Challenge](t, mustRead(t, replay), MsgChallenge)
	mustWrite(t, replay, MsgAuth, auth)

	if bytes.Equal(ch.Nonce, ch2.Nonce) {
		t.Fatal("main reused its nonce")
	}
	expectErrorCode(t, replay, ErrUnauthorized)
	if hh.hub.Connected("named") {
		t.Fatal("replayed handshake accepted")
	}
}

func TestHubOversizedPreAuthFrameCloses(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)
	ws := rawDial(t, hh.url)
	big := `{"type":"hello","payload":{"agent_id":"` + strings.Repeat("a", MaxHandshakeMessage) + `"}}`

	_ = ws.WriteMessage(websocket.TextMessage, []byte(big))

	_, _, err := ws.ReadMessage()
	var ce *websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != websocket.CloseMessageTooBig {
		// A reset before the close frame is acceptable too, as long as the conn dies.
		if err == nil {
			t.Fatal("oversized frame accepted")
		}
	}
}

func TestHubDuplicateIDRejectedOriginalIntact(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)
	orig := connectAgent(t, hh.url, "named", "named-token")
	sendReport(t, orig, Report{Seq: 1, DockerOK: true})

	dup := rawDial(t, hh.url)
	err := agentHandshake(dup, ModeOutbound, "named", "named-token", "v", testTimings())

	var pe *PeerError
	if !errors.As(err, &pe) || pe.Code != ErrDuplicateAgent {
		t.Fatalf("err = %v, want duplicate_agent", err)
	}
	expectClosed(t, dup)
	if ack := sendReport(t, orig, Report{Seq: 2, DockerOK: true}); ack.Seq != 2 {
		t.Fatal("original session disturbed")
	}
	if !hh.hub.Connected("named") {
		t.Fatal("original session unregistered")
	}
}

func TestHubRejectsInvalidAgentIDs(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)
	for _, id := range []string{"", "UPPER", "-lead", "_lead", "a/b", "a b", "a.b", strings.Repeat("a", 64), "ä"} {
		t.Run(id, func(t *testing.T) {
			ws := rawDial(t, hh.url)

			err := agentHandshake(ws, ModeOutbound, id, "shared-token", "v", testTimings())

			var pe *PeerError
			if !errors.As(err, &pe) || pe.Code != ErrBadRequest {
				t.Fatalf("err = %v, want bad_request", err)
			}
			if hh.hub.Connected(id) {
				t.Fatal("invalid id registered")
			}
		})
	}
}

func TestHubReportSpamUnderRace(t *testing.T) {
	checkLeaks(t)
	hh := startHub(t, namedServer(), func(o *HubOptions) { o.Store.(*fakeStore).changed = true }, nil)
	ws := connectAgent(t, hh.url, "named", "named-token")
	const n = 300

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= n; i++ {
			if writeDirect(ws, MsgReport, Report{Seq: uint64(i), DockerOK: true, Containers: containers("a", "b")}) != nil {
				return
			}
		}
	}()
	for i := 1; i <= n; i++ {
		if ack := mustPayload[Ack](t, mustRead(t, ws), MsgAck); ack.Seq != uint64(i) {
			t.Fatalf("ack %d, want %d", ack.Seq, i)
		}
	}
	wg.Wait()

	if hh.store.count() != n || hh.changes.get() != n {
		t.Fatalf("stored %d, onChange %d", hh.store.count(), hh.changes.get())
	}
	_ = ws.Close()
	hh.stop()
}

func TestHubOnChangeSemantics(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)
	ws := connectAgent(t, hh.url, "named", "named-token")

	sendReport(t, ws, Report{Seq: 1, DockerOK: true}) // first report of the session
	sendReport(t, ws, Report{Seq: 2, DockerOK: true}) // unchanged
	hh.store.set(true, nil)
	sendReport(t, ws, Report{Seq: 3, DockerOK: true}) // changed

	if got := hh.changes.get(); got != 2 {
		t.Fatalf("OnChange called %d times, want 2", got)
	}
}

func TestHubDockerNotOKStoresNilSnapshot(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)
	ws := connectAgent(t, hh.url, "named", "named-token")

	sendReport(t, ws, Report{Seq: 1, DockerOK: false, DockerError: "socket gone", Containers: containers("x")})

	if r := hh.store.all()[0]; r.snap != nil || r.rec.LastSeen.IsZero() {
		t.Fatalf("got %+v, want nil snapshot with last_seen", r)
	}
}

func TestHubStoreErrorSkipsAckKeepsSession(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)
	ws := connectAgent(t, hh.url, "named", "named-token")
	hh.store.mu.Lock()
	hh.store.failNext = 1
	hh.store.mu.Unlock()

	mustWrite(t, ws, MsgReport, Report{Seq: 1, DockerOK: true})
	ack := sendReport(t, ws, Report{Seq: 2, DockerOK: true})

	if ack.Seq != 2 || hh.changes.get() != 1 {
		t.Fatalf("ack %d, onChange %d", ack.Seq, hh.changes.get())
	}
}

func TestHubRejectsInvalidReports(t *testing.T) {
	tooMany := make([]model.Container, MaxContainers+1)
	cases := []struct {
		name string
		rep  any
		code ErrorCode
	}{
		{"too many containers", Report{DockerOK: true, Containers: tooMany}, ErrTooLarge},
		{"bad ipv4", Report{DockerOK: true, PublicIP: "::1"}, ErrBadRequest},
		{"bad ipv6", Report{DockerOK: true, PublicIPv6: "1.2.3.4"}, ErrBadRequest},
		{"mapped ipv6", Report{DockerOK: true, PublicIPv6: "::ffff:1.2.3.4"}, ErrBadRequest},
		{"bad container name", Report{DockerOK: true, Containers: []model.Container{{Name: "/web"}}}, ErrBadRequest},
		{"duplicate container", Report{DockerOK: true, Containers: containers("web", "web")}, ErrBadRequest},
		{"empty label key", Report{DockerOK: true, Containers: []model.Container{{Name: "web", Labels: map[string]string{"": "x"}}}}, ErrBadRequest},
		{"malformed payload", map[string]any{"seq": "x"}, ErrBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hh := startHub(t, namedServer(), nil, nil)
			ws := connectAgent(t, hh.url, "named", "named-token")

			mustWrite(t, ws, MsgReport, tc.rep)

			expectErrorCode(t, ws, tc.code)
			expectClosed(t, ws)
			if hh.store.count() != 0 {
				t.Fatal("invalid report stored")
			}
		})
	}
}

func TestHubRejectsUnexpectedMessages(t *testing.T) {
	cases := map[string]func(*websocket.Conn) error{
		"unknown type": func(ws *websocket.Conn) error { return writeDirect(ws, MsgWelcome, Welcome{}) },
		"not json":     func(ws *websocket.Conn) error { return ws.WriteMessage(websocket.TextMessage, []byte("{")) },
	}
	for name, send := range cases {
		t.Run(name, func(t *testing.T) {
			hh := startHub(t, namedServer(), nil, nil)
			ws := connectAgent(t, hh.url, "named", "named-token")

			_ = send(ws)

			expectErrorCode(t, ws, ErrBadRequest)
			eventually(t, "unregister", func() bool { return !hh.hub.Connected("named") })
		})
	}
}

func TestHubAgentErrorEndsSession(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)
	ws := connectAgent(t, hh.url, "named", "named-token")

	mustWrite(t, ws, MsgError, ErrorMsg{Code: ErrInternal})

	expectClosed(t, ws)
	eventually(t, "unregister", func() bool { return !hh.hub.Connected("named") })
}

func TestHubDisconnectKeepsStoredState(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)
	ws := connectAgent(t, hh.url, "named", "named-token")
	sendReport(t, ws, Report{Seq: 1, DockerOK: true, Containers: containers("web")})

	_ = ws.Close()

	eventually(t, "disconnect", func() bool { return !hh.hub.Connected("named") })
	if r := hh.store.all(); len(r) != 1 || r[0].snap == nil {
		t.Fatalf("stored state changed on disconnect: %+v", r)
	}
	// Reconnecting is a new session: OnChange fires for its first report.
	ws2 := connectAgent(t, hh.url, "named", "named-token")
	sendReport(t, ws2, Report{Seq: 1, DockerOK: true})
	if hh.changes.get() != 2 {
		t.Fatalf("onChange %d, want 2", hh.changes.get())
	}
}

func TestHubHandshakeTimeout(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil, func(tm *timings) { tm.handshake = 100 * time.Millisecond })
	ws := rawDial(t, hh.url)

	start := time.Now()
	_, _, err := ws.ReadMessage() // send nothing

	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("handshake not timed out: err=%v after %v", err, time.Since(start))
	}
}

func TestHubReadDeadlineWithoutTraffic(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil, func(tm *timings) { tm.pongWait = 150 * time.Millisecond })
	ws := connectAgent(t, hh.url, "named", "named-token")

	// The client neither sends nor reads (so it never answers pings).
	eventually(t, "connected", func() bool { return hh.hub.Connected("named") })
	eventually(t, "deadline disconnect", func() bool { return !hh.hub.Connected("named") })
	_ = ws.Close()
}

func TestHubPingsKeepSessionAlive(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil, func(tm *timings) {
		tm.ping = 20 * time.Millisecond
		tm.pongWait = 200 * time.Millisecond
	})
	ws := connectAgent(t, hh.url, "named", "named-token")
	pings := make(chan struct{}, 64)
	ws.SetPingHandler(func(data string) error {
		signal(pings)
		return ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})
	go func() {
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	time.Sleep(600 * time.Millisecond) // > 2x pongWait

	if !hh.hub.Connected("named") {
		t.Fatal("session dropped although pongs were answered")
	}
	if len(pings) == 0 {
		t.Fatal("no pings received")
	}
	_ = ws.Close()
}

func TestHubHealthBody(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	resp, err := client.Get(hh.base + PathHealth)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK || string(body) != `{"status":"ok"}` {
		t.Fatalf("health: %d %q", resp.StatusCode, body)
	}
}

func TestHubPendingHandshakeLimit(t *testing.T) {
	hh := startHub(t, namedServer(), nil, nil)
	for range MaxPendingHandshakes {
		rawDial(t, hh.url) // never sends hello
	}
	eventually(t, "slots taken", func() bool { return len(hh.hub.pending) == MaxPendingHandshakes })

	d := &websocket.Dialer{HandshakeTimeout: waitFor}
	_, resp, err := d.Dial(hh.url, nil)

	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got err=%v resp=%v", err, resp)
	}
	_ = resp.Body.Close()
}

func TestNewHubValidation(t *testing.T) {
	good := func() HubOptions { return newHubOpts(namedServer(), &fakeStore{}, &counter{}) }
	cases := []struct {
		name   string
		mutate func(*HubOptions)
	}{
		{"nil store", func(o *HubOptions) { o.Store = nil }},
		{"invalid id", func(o *HubOptions) { o.Server.Agents = map[string]config.AgentEntry{"Bad": {Token: "x"}} }},
		{"empty token", func(o *HubOptions) { o.Server.Agents = map[string]config.AgentEntry{"a": {}} }},
		{"bad connect_to", func(o *HubOptions) {
			o.Server.Agents = map[string]config.AgentEntry{"a": {Token: "x", ConnectTo: "http://a/ws"}}
		}},
		{"connect_to no host", func(o *HubOptions) {
			o.Server.Agents = map[string]config.AgentEntry{"a": {Token: "x", ConnectTo: "ws:///ws"}}
		}},
		{"tls cert without key", func(o *HubOptions) { o.Server.TLS.Cert = "c.pem" }},
		{"zero delay", func(o *HubOptions) { o.Retry.Delay = 0 }},
		{"max < delay", func(o *HubOptions) { o.Retry.MaxDelay = time.Millisecond }},
		{"backoff < 1", func(o *HubOptions) { o.Retry.Backoff = 0.5 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := good()
			tc.mutate(&o)
			if _, err := NewHub(o); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	o := good()
	o.OnChange, o.Now, o.Log = nil, nil, nil
	if _, err := NewHub(o); err != nil {
		t.Fatalf("defaults: %v", err)
	}
}

func TestHubRunListenerErrors(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	cases := map[string]config.AgentServerConfig{
		"address in use": {Listen: busy.Addr().String()},
		"missing cert":   {Listen: "127.0.0.1:0", TLS: config.TLSConfig{Cert: "/nonexistent", Key: "/nonexistent"}},
		"bad ca":         {Agents: map[string]config.AgentEntry{"a": {Token: "x", ConnectTo: "wss://127.0.0.1:1/ws"}}, TLS: config.TLSConfig{CA: "/nonexistent"}},
	}
	for name, srv := range cases {
		t.Run(name, func(t *testing.T) {
			checkLeaks(t)
			h, err := NewHub(newHubOpts(srv, &fakeStore{}, &counter{}))
			if err != nil {
				t.Fatal(err)
			}
			if err := h.Run(context.Background()); err == nil {
				t.Fatal("expected Run error")
			}
		})
	}
}

func TestHubRunWithoutListenerStopsOnCancel(t *testing.T) {
	checkLeaks(t)
	h, err := NewHub(newHubOpts(config.AgentServerConfig{}, &fakeStore{}, &counter{}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitFor):
		t.Fatal("Run did not return")
	}
}

func TestHubRunRealListener(t *testing.T) {
	checkLeaks(t)
	h, err := NewHub(newHubOpts(config.AgentServerConfig{Listen: "127.0.0.1:0"}, &fakeStore{}, &counter{}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := h.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

// --- hub harness ---

type hubHarness struct {
	hub     *Hub
	store   *fakeStore
	url     string // ws://.../ws
	base    string // http://...
	changes *counter
	stop    func()
}

type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) inc() { c.mu.Lock(); c.n++; c.mu.Unlock() }
func (c *counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func newHubOpts(srv config.AgentServerConfig, store *fakeStore, changes *counter) HubOptions {
	return HubOptions{
		Server:   srv,
		Retry:    testRetry(),
		Version:  "test",
		Store:    store,
		OnChange: changes.inc,
	}
}

// startHub runs a Hub on a loopback listener (wrapped by wrap, if non-nil).
func startHub(t *testing.T, srv config.AgentServerConfig, mutate func(*HubOptions), wrap func(net.Listener) net.Listener, tune ...func(*timings)) *hubHarness {
	t.Helper()
	store, changes := &fakeStore{}, &counter{}
	opts := newHubOpts(srv, store, changes)
	if mutate != nil {
		mutate(&opts)
	}
	h, err := NewHub(opts)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	h.t = testTimings()
	for _, f := range tune {
		f(&h.t)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	scheme := "ws"
	if wrap != nil {
		ln, scheme = wrap(ln), "wss"
	}
	return runHub(t, h, store, changes, ln, scheme)
}

func runHub(t *testing.T, h *Hub, store *fakeStore, changes *counter, ln net.Listener, scheme string) *hubHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx, ln) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("hub run: %v", err)
				}
			case <-time.After(waitFor):
				t.Errorf("hub did not stop")
			}
		})
	}
	t.Cleanup(stop)
	addr := ln.Addr().String()
	httpScheme := map[string]string{"ws": "http", "wss": "https"}[scheme]
	return &hubHarness{hub: h, store: store, url: scheme + "://" + addr + PathWS, base: httpScheme + "://" + addr, changes: changes, stop: stop}
}
