package agent

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/channinghe/labelgate/internal/model"
	"github.com/gorilla/websocket"
)

// errAuth means the peer failed to prove knowledge of the token.
var errAuth = errors.New("agent: peer authentication failed")

// credentials returns the token for an agent ID (ok=false: no key).
type credentials func(agentID string) (token string, ok bool)

func newNonce() []byte {
	b := make([]byte, NonceSize)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (it aborts the process instead)
	return b
}

// beginHandshake applies the pre-auth read limit and the overall deadline.
// Must be called before the first read on a fresh connection.
func beginHandshake(ws *websocket.Conn, t timings) {
	ws.SetReadLimit(MaxHandshakeMessage)
	deadline := time.Now().Add(t.handshake)
	_ = ws.SetReadDeadline(deadline)
	_ = ws.SetWriteDeadline(deadline)
}

// readExpect reads one handshake message of type want into v. A MsgError is
// returned as *PeerError.
func readExpect(ws *websocket.Conn, want MsgType, v any) error {
	mt, data, err := ws.ReadMessage()
	if err != nil {
		return err
	}
	env, err := decodeEnvelope(mt, data)
	if err != nil {
		return err
	}
	if env.Type == MsgError {
		return peerError(env)
	}
	if env.Type != want {
		return fmt.Errorf("%w: expected %s", errProtocol, want)
	}
	return decodePayload(env, v)
}

// checkHello validates a hello; it returns the error code to answer with.
func checkHello(h Hello) (ErrorCode, error) {
	switch {
	case h.Proto != ProtocolVersion:
		return ErrUnsupportedVersion, fmt.Errorf("%w: unsupported protocol %d", errProtocol, h.Proto)
	case !model.ValidAgentID(h.AgentID):
		return ErrBadRequest, fmt.Errorf("%w: invalid agent id", errProtocol)
	case len(h.Nonce) != NonceSize:
		return ErrBadRequest, fmt.Errorf("%w: invalid nonce", errProtocol)
	}
	return "", nil
}

// mainHandshake runs the main side up to (not including) welcome. The main
// proves itself first (challenge MAC), then verifies the agent's MAC. For IDs
// without a key the challenge is computed with dummyKey so unknown IDs and
// bad MACs are indistinguishable (both end in ErrUnauthorized). On failure an
// error message has been sent; the caller closes ws.
func mainHandshake(ws *websocket.Conn, mode Mode, creds credentials, dummyKey string, t timings) (Hello, error) {
	beginHandshake(ws, t)
	var hello Hello
	if err := readExpect(ws, MsgHello, &hello); err != nil {
		rejectDirect(ws, ErrBadRequest, "expected hello", t.write)
		return Hello{}, err
	}
	if code, err := checkHello(hello); err != nil {
		rejectDirect(ws, code, err.Error(), t.write)
		return Hello{}, err
	}
	token, known := creds(hello.AgentID)
	if !known || token == "" {
		known, token = false, dummyKey
	}
	nm := newNonce()
	mac := MAC(token, RoleMain, mode, hello.AgentID, hello.Nonce, nm)
	if err := writeDirect(ws, MsgChallenge, Challenge{Nonce: nm, MAC: mac}); err != nil {
		return Hello{}, err
	}
	var auth Auth
	if err := readExpect(ws, MsgAuth, &auth); err != nil {
		rejectDirect(ws, ErrBadRequest, "expected auth", t.write)
		return Hello{}, &authFailure{agentID: hello.AgentID, known: known, cause: err}
	}
	ok := VerifyMAC(token, RoleAgent, mode, hello.AgentID, hello.Nonce, nm, auth.MAC)
	if !known || !ok {
		rejectDirect(ws, ErrUnauthorized, "authentication failed", t.write)
		return Hello{}, &authFailure{agentID: hello.AgentID, known: known}
	}
	return hello, nil
}

// agentHandshake runs the agent side to completion (welcome received). The
// agent sends its hello, verifies the main's MAC and only then sends its own
// MAC; nothing else is ever sent to an unverified peer. On failure the caller
// closes ws.
func agentHandshake(ws *websocket.Conn, mode Mode, agentID, token, version string, t timings) error {
	beginHandshake(ws, t)
	na := newNonce()
	hello := Hello{Proto: ProtocolVersion, AgentID: agentID, Version: version, Nonce: na}
	if err := writeDirect(ws, MsgHello, hello); err != nil {
		return err
	}
	var ch Challenge
	if err := readExpect(ws, MsgChallenge, &ch); err != nil {
		return err
	}
	if !VerifyMAC(token, RoleMain, mode, agentID, na, ch.Nonce, ch.MAC) {
		return errAuth
	}
	mac := MAC(token, RoleAgent, mode, agentID, na, ch.Nonce)
	if err := writeDirect(ws, MsgAuth, Auth{MAC: mac}); err != nil {
		return err
	}
	return readExpect(ws, MsgWelcome, &Welcome{})
}
