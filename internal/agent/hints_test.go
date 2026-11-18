package agent

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
)

func wantAll(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%q\nlacks %q", got, w)
		}
	}
}

// toMain is the dial target of an agent whose connect.endpoint is url.
func toMain(url string) dialTarget {
	return dialTarget{url: url, urlKey: config.KeyWithEnv("connect.endpoint"), section: sectionConnect, peer: "the main instance"}
}

func dialErr(t *testing.T, url string) error {
	t.Helper()
	tlsCfg, err := clientTLSConfig(config.TLSConfig{}, sectionConnect, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = dial(ctx, url, tlsCfg, 2*time.Second)
	if err == nil {
		t.Fatal("dial succeeded")
	}
	return explainDial(err, toMain(url))
}

// statusServer answers every request with status.
func statusServer(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestDialHints(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.NotFoundHandler())
	defer tlsSrv.Close()
	tlsHost := strings.TrimPrefix(tlsSrv.URL, "https")
	refused := func() string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		return ln.Addr().String()
	}()

	tests := []struct {
		name   string
		url    string
		wants  []string
		absent []string
	}{
		{"untrusted certificate", "wss" + tlsHost + PathWS,
			[]string{"not trusted", "connect.tls.ca (env LABELGATE_CONNECT_TLS_CA)", "LABELGATE_CONNECT_TLS_INSECURE_SKIP_VERIFY"}, nil},
		{"wrong path", statusServer(t, http.StatusNotFound) + "/nope",
			[]string{"HTTP 404", "connect.endpoint (env LABELGATE_CONNECT_ENDPOINT) must end in /ws"}, nil},
		{"404 on /ws: not the agent listener", statusServer(t, http.StatusNotFound) + PathWS,
			[]string{"HTTP 404", "not labelgate's agent listener", "dashboard/API port", "28111", "agent.listen", "28112"},
			[]string{"must end in /ws"}},
		{"ws:// against a TLS listener", "ws" + tlsHost + PathWS,
			[]string{"HTTP 400", "expects TLS", "use wss:// in connect.endpoint"}, nil},
		{"502 from a reverse proxy", statusServer(t, http.StatusBadGateway) + PathWS,
			[]string{"HTTP 502", "reverse proxy in front of the main instance cannot reach it"}, nil},
		{"503 from a reverse proxy", statusServer(t, http.StatusServiceUnavailable) + PathWS,
			[]string{"HTTP 503", "reverse proxy in front of the main instance cannot reach it", "too many pending connections"}, nil},
		{"504 from a reverse proxy", statusServer(t, http.StatusGatewayTimeout) + PathWS,
			[]string{"HTTP 504", "reverse proxy in front of the main instance cannot reach it"}, nil},
		{"other status", statusServer(t, http.StatusTeapot) + PathWS,
			[]string{"HTTP 418", "does not belong to labelgate's agent port"}, nil},
		{"plain server, wss URL", "wss" + strings.TrimPrefix(statusServer(t, http.StatusNotFound), "ws") + PathWS,
			[]string{"does not use TLS: use ws://"}, nil},
		{"connection refused", "ws://" + refused + PathWS,
			[]string{"nothing listens there", "agent.enabled=true"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dialErr(t, tt.url).Error()
			wantAll(t, got, tt.wants...)
			for _, a := range tt.absent {
				if strings.Contains(got, a) {
					t.Errorf("%q\nmust not contain %q", got, a)
				}
			}
		})
	}
}

func TestStatusHintOnlySuggestsTLSForPlainURLs(t *testing.T) {
	se := &statusError{err: errors.New("websocket: bad handshake"), status: http.StatusBadRequest}
	if got := dialHint(se, toMain("wss://main:28112/ws")); strings.Contains(got, "expects TLS") {
		t.Errorf("wss:// URL got a TLS hint: %q", got)
	}
	wantAll(t, dialHint(se, toMain("ws://main:28112/ws")), "expects TLS", "wss://")
}

func TestStatusHintForInboundAgentTarget(t *testing.T) {
	se := &statusError{err: errors.New("websocket: bad handshake"), status: http.StatusNotFound}
	toAgent := dialTarget{url: "ws://edge:28113/ws", urlKey: "agent.agents.edge.connect_to", section: sectionAgent, peer: "the agent"}
	got := dialHint(se, toAgent)
	wantAll(t, got, "not labelgate's agent listener", "connect.listen")
	if strings.Contains(got, "28111") {
		t.Errorf("main-side hint must not name the dashboard port: %q", got)
	}
}

func TestMalformedResponseHintNeedsPlainURL(t *testing.T) {
	err := errors.New(`malformed HTTP response "\x15\x03\x01"`)
	wantAll(t, dialHint(err, toMain("ws://main:28112/ws")), "expects TLS", "wss://")
	if got := dialHint(err, toMain("wss://main:28112/ws")); got != "" {
		t.Errorf("wss:// URL got hint %q", got)
	}
}

func TestAgentAuthHint(t *testing.T) {
	for _, err := range []error{errAuth, &PeerError{Code: ErrUnauthorized}} {
		got := agentAuthHint("edge1", err)
		// Both causes carry equal weight: the agent cannot tell them apart.
		wantAll(t, got,
			"main rejected this agent: either",
			`agent "edge1" is not configured on the main instance`,
			"add agent.agents.edge1",
			"agent.accept_token, env LABELGATE_AGENT_ACCEPT_TOKEN",
			"or the tokens do not match: connect.token (env LABELGATE_CONNECT_TOKEN) on this agent must equal agent.agents.edge1.token")
		if strings.Contains(got, "if \"edge1\" is not listed") {
			t.Errorf("the unconfigured-agent case must not be a parenthetical: %q", got)
		}
	}
	wantAll(t, agentAuthHint("edge1", &PeerError{Code: ErrDuplicateAgent}),
		"already connected", "connect.agent_id (env LABELGATE_CONNECT_AGENT_ID)")
	wantAll(t, agentAuthHint("edge1", &PeerError{Code: ErrUnsupportedVersion}), "same release")
	if got := agentAuthHint("edge1", errors.New("EOF")); got != "" {
		t.Errorf("unrelated error got hint %q", got)
	}
}

func TestMainAuthHint(t *testing.T) {
	closed := errors.New("websocket: close 1006")
	tests := []struct {
		name  string
		f     *authFailure
		mode  Mode
		named bool
		wants []string
	}{
		{"unknown outbound", &authFailure{agentID: "edge1"}, ModeOutbound, false,
			[]string{`agent "edge1" is not configured`, "agent.agents.edge1", "agent.accept_token (env LABELGATE_AGENT_ACCEPT_TOKEN)"}},
		{"unknown inbound", &authFailure{agentID: "other"}, ModeInbound, false,
			[]string{`reports ID "other"`, "connect.agent_id (env LABELGATE_CONNECT_AGENT_ID)"}},
		{"named, agent hung up", &authFailure{agentID: "edge1", known: true, cause: closed}, ModeOutbound, true,
			[]string{"closed the connection", "agent.agents.edge1.token"}},
		{"accept token, wrong MAC", &authFailure{agentID: "edge1", known: true}, ModeOutbound, false,
			[]string{"wrong token proof", "agent.accept_token (env LABELGATE_AGENT_ACCEPT_TOKEN)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { wantAll(t, mainAuthHint(tt.f, tt.mode, tt.named), tt.wants...) })
	}
}

func TestAuthFailureUnwraps(t *testing.T) {
	if err := error(&authFailure{agentID: "a"}); !errors.Is(err, errAuth) {
		t.Errorf("wrong MAC must be errAuth: %v", err)
	}
	cause := errors.New("closed")
	if err := error(&authFailure{agentID: "a", cause: cause}); !errors.Is(err, cause) {
		t.Errorf("hang-up must unwrap to its cause: %v", err)
	}
}

func TestListenNamesKey(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, err = listen(ln.Addr().String(), config.TLSConfig{}, sectionAgent)
	if err == nil {
		t.Fatal("second listener on the same port succeeded")
	}
	wantAll(t, err.Error(), "agent.listen (env LABELGATE_AGENT_LISTEN)")
}
