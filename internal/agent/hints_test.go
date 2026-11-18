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

var toMain = dialTarget{urlKey: config.KeyWithEnv("connect.endpoint"), section: sectionConnect, peer: "the main instance"}

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
	return explainDial(err, toMain)
}

func TestDialHints(t *testing.T) {
	t.Run("untrusted certificate", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.NotFoundHandler())
		defer srv.Close()
		wantAll(t, dialErr(t, "wss"+strings.TrimPrefix(srv.URL, "https")+PathWS).Error(),
			"not trusted", "connect.tls.ca (env LABELGATE_CONNECT_TLS_CA)", "LABELGATE_CONNECT_TLS_INSECURE_SKIP_VERIFY")
	})
	t.Run("wrong path", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()
		wantAll(t, dialErr(t, "ws"+strings.TrimPrefix(srv.URL, "http")+"/nope").Error(),
			"HTTP 404", "connect.endpoint (env LABELGATE_CONNECT_ENDPOINT) must end in /ws")
	})
	t.Run("plain server, wss URL", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()
		wantAll(t, dialErr(t, "wss"+strings.TrimPrefix(srv.URL, "http")+PathWS).Error(), "does not use TLS: use ws://")
	})
	t.Run("connection refused", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		wantAll(t, dialErr(t, "ws://"+addr+PathWS).Error(), "nothing listens there", "agent.enabled=true")
	})
}

func TestAgentAuthHint(t *testing.T) {
	wantAll(t, agentAuthHint("edge1", errAuth),
		"connect.token (env LABELGATE_CONNECT_TOKEN)", "agent.agents.edge1.token", "agent.accept_token")
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
