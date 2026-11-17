package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/gorilla/websocket"
)

func edgeServer() config.AgentServerConfig {
	return config.AgentServerConfig{
		Agents: map[string]config.AgentEntry{"edge-1": {Token: agentToken, Name: "Edge"}},
	}
}

func TestEndToEndOutbound(t *testing.T) {
	checkLeaks(t)
	hh := startHub(t, edgeServer(), withClock, nil)
	src := &fakeSource{containers: containers("web")}
	a := newTestAgent(t, outboundConnect(hh.url), src, nil)
	stopAgent := runAgent(t, a, outbound(a))

	eventually(t, "first report", func() bool { return hh.store.count() >= 1 })
	src.set(containers("web", "api"), nil)
	src.fire()
	eventually(t, "change report", func() bool {
		r := hh.store.all()
		return len(r[len(r)-1].snap.Containers) == 2
	})

	if !hh.hub.Connected("edge-1") || hh.changes.get() < 1 {
		t.Fatal("agent not connected / no OnChange")
	}
	if err := stopAgent(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "disconnect", func() bool { return !hh.hub.Connected("edge-1") })
	if hh.store.count() < 2 {
		t.Fatal("stored reports lost on disconnect")
	}
	hh.stop()
}

func TestEndToEndInbound(t *testing.T) {
	checkLeaks(t)
	_, addr := startInboundAgent(t, &fakeSource{containers: containers("web")})
	srv := edgeServer()
	srv.Agents["edge-1"] = config.AgentEntry{Token: agentToken, ConnectTo: "ws://" + addr + PathWS}
	hh := startHub(t, srv, nil, nil)

	eventually(t, "inbound report", func() bool { return hh.store.count() >= 1 })

	if !hh.hub.Connected("edge-1") {
		t.Fatal("inbound agent not connected")
	}
	if r := hh.store.all()[0]; r.snap == nil || r.snap.Containers[0].Name != "web" {
		t.Fatalf("report: %+v", r)
	}
	hh.stop()
}

func TestEndToEndInboundWrongTokenNeverConnects(t *testing.T) {
	_, addr := startInboundAgent(t, &fakeSource{})
	srv := edgeServer()
	srv.Agents["edge-1"] = config.AgentEntry{Token: "wrong", ConnectTo: "ws://" + addr + PathWS}
	hh := startHub(t, srv, nil, nil)

	time.Sleep(200 * time.Millisecond) // several dial attempts

	if hh.hub.Connected("edge-1") || hh.store.count() != 0 {
		t.Fatal("main with wrong token got data")
	}
}

func TestEndToEndInboundDialFailureRetries(t *testing.T) {
	checkLeaks(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	srv := edgeServer()
	srv.Agents["edge-1"] = config.AgentEntry{Token: agentToken, ConnectTo: "ws://" + addr + PathWS}
	hh := startHub(t, srv, nil, nil)

	time.Sleep(100 * time.Millisecond)

	hh.stop() // must return promptly despite the backoff loop
}

func TestCrossDirectionRelayWithRealPeers(t *testing.T) {
	// Attacker: dials the inbound agent (which greets with its hello) and
	// relays the hello into the main's outbound listener, then relays the
	// main's challenge back. The agent must reject it (mode mismatch).
	hh := startHub(t, edgeServer(), nil, nil)
	_, agentAddr := startInboundAgent(t, &fakeSource{containers: containers("web")})
	toAgent := rawDial(t, "ws://"+agentAddr+PathWS)
	toMain := rawDial(t, hh.url)

	_, hello, err := toAgent.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	_ = toMain.WriteMessage(websocket.TextMessage, hello)
	_, challenge, err := toMain.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	_ = toAgent.WriteMessage(websocket.TextMessage, challenge)

	expectClosed(t, toAgent) // no auth was sent to the attacker
	if hh.hub.Connected("edge-1") {
		t.Fatal("relay authenticated")
	}
}

// --- TLS ---

func tlsWrap(files tlsFiles) func(net.Listener) net.Listener {
	return func(ln net.Listener) net.Listener {
		cert, err := tls.LoadX509KeyPair(files.cert, files.key)
		if err != nil {
			panic(err)
		}
		return tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	}
}

func TestTLSVerification(t *testing.T) {
	cert := selfSignedCert(t)
	cases := []struct {
		name      string
		tls       config.TLSConfig
		wantConn  bool
		wantError bool
	}{
		{"self-signed rejected by default", config.TLSConfig{}, false, false},
		{"accepted with configured CA", config.TLSConfig{CA: cert.cert}, true, false},
		{"accepted with explicit insecure flag", config.TLSConfig{InsecureSkipVerify: true}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkLeaks(t)
			defaultBefore := *websocket.DefaultDialer
			hh := startHub(t, edgeServer(), nil, tlsWrap(cert))
			c := outboundConnect(hh.url)
			c.TLS = tc.tls
			a := newTestAgent(t, c, &fakeSource{}, nil)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- a.Run(ctx) }()

			if tc.wantConn {
				eventually(t, "TLS session", func() bool { return hh.hub.Connected("edge-1") })
			} else {
				time.Sleep(200 * time.Millisecond)
				if hh.hub.Connected("edge-1") || hh.store.count() != 0 {
					t.Fatal("connected to an untrusted certificate")
				}
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			hh.stop()
			if websocket.DefaultDialer.TLSClientConfig != defaultBefore.TLSClientConfig ||
				websocket.DefaultDialer.HandshakeTimeout != defaultBefore.HandshakeTimeout {
				t.Fatal("websocket.DefaultDialer was mutated")
			}
		})
	}
}

func TestTLSInboundListenerAndHubCA(t *testing.T) {
	checkLeaks(t)
	cert := selfSignedCert(t)
	c := inboundConnect()
	c.TLS = config.TLSConfig{Cert: cert.cert, Key: cert.key}
	a := newTestAgent(t, c, &fakeSource{containers: containers("web")}, nil)
	ln, err := listen("127.0.0.1:0", c.TLS)
	if err != nil {
		t.Fatal(err)
	}
	runAgent(t, a, func(ctx context.Context) error { return a.serveInbound(ctx, ln) })
	srv := edgeServer()
	srv.TLS.CA = cert.cert
	srv.Agents["edge-1"] = config.AgentEntry{Token: agentToken, ConnectTo: "wss://" + ln.Addr().String() + PathWS}
	hh := startHub(t, srv, nil, nil)

	eventually(t, "inbound TLS report", func() bool { return hh.store.count() >= 1 })
	hh.stop()
}

func TestHubRejectsAgentWhileReplacedConnectionCloses(t *testing.T) {
	// Disconnect of a rejected duplicate must not unregister the live session.
	hh := startHub(t, edgeServer(), nil, nil)
	live := connectAgent(t, hh.url, "edge-1", agentToken)
	sendReport(t, live, Report{Seq: 1, DockerOK: true})

	for range 5 {
		dup := rawDial(t, hh.url)
		var pe *PeerError
		if err := agentHandshake(dup, ModeOutbound, "edge-1", agentToken, "v", testTimings()); !errors.As(err, &pe) {
			t.Fatalf("dup err = %v", err)
		}
		_ = dup.Close()
	}

	if !hh.hub.Connected("edge-1") {
		t.Fatal("live session lost")
	}
	sendReport(t, live, Report{Seq: 2, DockerOK: true})
}
