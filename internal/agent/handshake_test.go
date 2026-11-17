package agent

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type mainResult struct {
	hello Hello
	err   error
}

// handshakeServer runs mainHandshake (+ welcome on success) for every connection.
func handshakeServer(t *testing.T, mode Mode, creds credentials) (string, <-chan mainResult) {
	t.Helper()
	results := make(chan mainResult, 4)
	up := newUpgrader(testTimings())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		hello, err := mainHandshake(ws, mode, creds, "dummy-key", testTimings())
		if err == nil {
			err = writeDirect(ws, MsgWelcome, Welcome{})
		}
		results <- mainResult{hello, err}
		_, _, _ = ws.ReadMessage() // hold the conn until the client closes
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), results
}

func staticCreds(m map[string]string) credentials {
	return func(id string) (string, bool) {
		tok, ok := m[id]
		return tok, ok
	}
}

func TestHandshakeMatrix(t *testing.T) {
	cases := []struct {
		name       string
		mainMode   Mode
		agentMode  Mode
		mainTokens map[string]string
		agentID    string
		agentToken string
		wantOK     bool
		wantAgent  error // errors.Is target or nil (when wantOK)
	}{
		{"outbound ok", ModeOutbound, ModeOutbound, map[string]string{"a1": "s3cret"}, "a1", "s3cret", true, nil},
		{"inbound ok", ModeInbound, ModeInbound, map[string]string{"a1": "s3cret"}, "a1", "s3cret", true, nil},
		{"agent has wrong token", ModeOutbound, ModeOutbound, map[string]string{"a1": "s3cret"}, "a1", "wrong", false, errAuth},
		{"main has wrong token", ModeInbound, ModeInbound, map[string]string{"a1": "wrong"}, "a1", "s3cret", false, errAuth},
		{"unknown id", ModeOutbound, ModeOutbound, map[string]string{}, "a1", "s3cret", false, errAuth},
		{"relay outbound->inbound", ModeOutbound, ModeInbound, map[string]string{"a1": "s3cret"}, "a1", "s3cret", false, errAuth},
		{"relay inbound->outbound", ModeInbound, ModeOutbound, map[string]string{"a1": "s3cret"}, "a1", "s3cret", false, errAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, results := handshakeServer(t, tc.mainMode, staticCreds(tc.mainTokens))
			ws := rawDial(t, url)

			err := agentHandshake(ws, tc.agentMode, tc.agentID, tc.agentToken, "v", testTimings())
			_ = ws.Close()

			if tc.wantOK {
				if err != nil {
					t.Fatalf("agent: %v", err)
				}
				if r := <-results; r.err != nil || r.hello.AgentID != tc.agentID {
					t.Fatalf("main: %+v", r)
				}
				return
			}
			if !errors.Is(err, tc.wantAgent) {
				t.Fatalf("agent err = %v, want %v", err, tc.wantAgent)
			}
			if r := <-results; r.err == nil {
				t.Fatal("main accepted a failed handshake")
			}
		})
	}
}

func TestMainHandshakeRejectsMalformedHello(t *testing.T) {
	good := func() Hello {
		return Hello{Proto: ProtocolVersion, AgentID: "a1", Nonce: bytes.Repeat([]byte{1}, NonceSize)}
	}
	cases := []struct {
		name string
		send func(*testing.T, *websocket.Conn)
		code ErrorCode
	}{
		{"wrong proto", func(t *testing.T, ws *websocket.Conn) {
			h := good()
			h.Proto = 2
			mustWrite(t, ws, MsgHello, h)
		}, ErrUnsupportedVersion},
		{"short nonce", func(t *testing.T, ws *websocket.Conn) {
			h := good()
			h.Nonce = []byte{1}
			mustWrite(t, ws, MsgHello, h)
		}, ErrBadRequest},
		{"auth before hello", func(t *testing.T, ws *websocket.Conn) {
			mustWrite(t, ws, MsgAuth, Auth{MAC: []byte{1}})
		}, ErrBadRequest},
		{"binary frame", func(t *testing.T, ws *websocket.Conn) {
			_ = ws.WriteMessage(websocket.BinaryMessage, []byte(`{"type":"hello"}`))
		}, ErrBadRequest},
		{"garbage", func(t *testing.T, ws *websocket.Conn) {
			_ = ws.WriteMessage(websocket.TextMessage, []byte(`not json`))
		}, ErrBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, results := handshakeServer(t, ModeOutbound, staticCreds(map[string]string{"a1": "s"}))
			ws := rawDial(t, url)

			tc.send(t, ws)

			expectErrorCode(t, ws, tc.code)
			expectClosed(t, ws)
			if r := <-results; r.err == nil {
				t.Fatal("main accepted malformed hello")
			}
		})
	}
}

func TestMainHandshakeUnknownIDIndistinguishableFromBadMAC(t *testing.T) {
	url, results := handshakeServer(t, ModeOutbound, staticCreds(map[string]string{"known": "s3cret"}))
	transcript := func(id string) (Challenge, ErrorMsg) {
		ws := rawDial(t, url)
		mustWrite(t, ws, MsgHello, Hello{Proto: ProtocolVersion, AgentID: id, Nonce: newNonce()})
		ch := mustPayload[Challenge](t, mustRead(t, ws), MsgChallenge)
		mustWrite(t, ws, MsgAuth, Auth{MAC: bytes.Repeat([]byte{7}, 32)})
		em := mustPayload[ErrorMsg](t, mustRead(t, ws), MsgError)
		<-results
		return ch, em
	}

	chKnown, errKnown := transcript("known")
	chUnknown, errUnknown := transcript("unknown")

	if len(chKnown.Nonce) != len(chUnknown.Nonce) || len(chKnown.MAC) != len(chUnknown.MAC) {
		t.Fatal("challenge shape differs between known and unknown IDs")
	}
	if errKnown != errUnknown || errKnown.Code != ErrUnauthorized {
		t.Fatalf("errors differ: %+v vs %+v", errKnown, errUnknown)
	}
}

func TestAgentHandshakeSendsNothingAfterBadChallenge(t *testing.T) {
	// A fake main that does not know the token: after a forged challenge the
	// agent must close without sending auth (or anything else).
	got := make(chan [][]byte, 1)
	up := newUpgrader(testTimings())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var frames [][]byte
		_ = ws.SetReadDeadline(time.Now().Add(waitFor))
		_, hello, _ := ws.ReadMessage()
		frames = append(frames, hello)
		_ = writeDirect(ws, MsgChallenge, Challenge{Nonce: newNonce(), MAC: bytes.Repeat([]byte{9}, 32)})
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				break
			}
			frames = append(frames, data)
		}
		got <- frames
	}))
	defer srv.Close()
	ws := rawDial(t, "ws"+strings.TrimPrefix(srv.URL, "http"))

	err := agentHandshake(ws, ModeInbound, "a1", "s3cret", "v", testTimings())
	_ = ws.Close()

	if !errors.Is(err, errAuth) {
		t.Fatalf("err = %v, want errAuth", err)
	}
	frames := <-got
	if len(frames) != 1 {
		t.Fatalf("agent sent %d frames, want only hello", len(frames))
	}
	if containsSecret(frames[0], "s3cret") || bytes.Contains(frames[0], []byte(`"mac"`)) {
		t.Fatalf("hello leaks secret material: %s", frames[0])
	}
}

func TestAgentHandshakeReportsPeerError(t *testing.T) {
	url, _ := handshakeServer(t, ModeOutbound, staticCreds(nil))
	ws := rawDial(t, url)

	err := agentHandshake(ws, ModeOutbound, "UPPER", "tok", "v", testTimings())

	var pe *PeerError
	if !errors.As(err, &pe) || pe.Code != ErrBadRequest {
		t.Fatalf("err = %v, want PeerError bad_request", err)
	}
	if !strings.Contains(pe.Error(), "bad_request") {
		t.Fatalf("PeerError text: %q", pe.Error())
	}
}
