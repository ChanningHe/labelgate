package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// Bounds for peer-supplied text before it reaches logs.
const (
	maxPeerErrorLen   = 256
	maxDockerErrorLen = 512
)

// truncate cuts s to at most n bytes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

var (
	errClosed   = errors.New("agent: connection closed")
	errProtocol = errors.New("agent: protocol violation")
)

// PeerError is an error message received from the peer.
type PeerError struct {
	Code    ErrorCode
	Message string
}

func (e *PeerError) Error() string {
	if e.Message == "" {
		return "peer error: " + string(e.Code)
	}
	return "peer error: " + string(e.Code) + ": " + e.Message
}

// encode builds a text frame for msg type t with payload v (nil = no payload).
func encode(t MsgType, v any) ([]byte, error) {
	env := Envelope{Type: t}
	if v != nil {
		p, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("agent: encode %s: %w", t, err)
		}
		env.Payload = p
	}
	return json.Marshal(env)
}

// decodeEnvelope parses one frame. Only text frames carrying a typed envelope are valid.
func decodeEnvelope(mt int, data []byte) (Envelope, error) {
	if mt != websocket.TextMessage {
		return Envelope{}, fmt.Errorf("%w: non-text frame", errProtocol)
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Type == "" {
		return Envelope{}, fmt.Errorf("%w: malformed envelope", errProtocol)
	}
	return env, nil
}

// decodePayload unmarshals env.Payload into v; an absent payload is "{}".
func decodePayload(env Envelope, v any) error {
	p := []byte(env.Payload)
	if len(p) == 0 {
		p = []byte("{}")
	}
	if err := json.Unmarshal(p, v); err != nil {
		return fmt.Errorf("%w: malformed %s payload", errProtocol, env.Type)
	}
	return nil
}

// peerError converts a received MsgError envelope into a *PeerError.
func peerError(env Envelope) error {
	var m ErrorMsg
	_ = decodePayload(env, &m) // a malformed error payload still ends the session
	return &PeerError{Code: ErrorCode(truncate(string(m.Code), maxPeerErrorLen)), Message: truncate(m.Message, maxPeerErrorLen)}
}

// writeDirect writes one message synchronously. Only valid while a single
// goroutine owns the connection (handshake phase, before wsConn.start).
func writeDirect(ws *websocket.Conn, t MsgType, v any) error {
	data, err := encode(t, v)
	if err != nil {
		return err
	}
	return ws.WriteMessage(websocket.TextMessage, data)
}

// rejectDirect sends an error message and a close frame (handshake phase).
func rejectDirect(ws *websocket.Conn, code ErrorCode, msg string, wait time.Duration) {
	_ = writeDirect(ws, MsgError, ErrorMsg{Code: code, Message: msg})
	_ = ws.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, string(code)),
		time.Now().Add(wait))
}

// outFrame is one queued text frame; last=true ends the session after it is
// written (data may be nil for a bare close).
type outFrame struct {
	data []byte
	last bool
}

// wsConn is an authenticated connection with exactly one writer goroutine.
// All data frames go through out; pings are written by the writer as well.
// The reader is the goroutine that calls read (one at a time).
type wsConn struct {
	ws   *websocket.Conn
	t    timings
	out  chan outFrame
	done chan struct{} // closed when the writer has exited and ws is closed
}

func newWSConn(ws *websocket.Conn, t timings, readLimit int64) *wsConn {
	ws.SetReadLimit(readLimit)
	c := &wsConn{ws: ws, t: t, out: make(chan outFrame, SendQueueSize), done: make(chan struct{})}
	ws.SetPongHandler(func(string) error { return c.extendRead() })
	return c
}

// start arms the read deadline and launches the writer. The writer exits (and
// closes the socket) when ctx is done, after a last frame, or on write error.
func (c *wsConn) start(ctx context.Context) {
	_ = c.ws.SetWriteDeadline(time.Time{})
	_ = c.extendRead()
	go c.writeLoop(ctx)
}

func (c *wsConn) extendRead() error {
	return c.ws.SetReadDeadline(time.Now().Add(c.t.pongWait))
}

func (c *wsConn) writeLoop(ctx context.Context) {
	defer close(c.done)
	defer c.ws.Close()
	ping := time.NewTicker(c.t.ping)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			c.writeClose(websocket.CloseGoingAway)
			return
		case f := <-c.out:
			if f.data != nil && c.writeFrame(f.data) != nil {
				return
			}
			if f.last {
				c.writeClose(websocket.CloseNormalClosure)
				return
			}
		case <-ping.C:
			if c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(c.t.write)) != nil {
				return
			}
		}
	}
}

func (c *wsConn) writeFrame(data []byte) error {
	_ = c.ws.SetWriteDeadline(time.Now().Add(c.t.write))
	return c.ws.WriteMessage(websocket.TextMessage, data)
}

func (c *wsConn) writeClose(code int) {
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, ""),
		time.Now().Add(c.t.write))
}

func (c *wsConn) enqueue(f outFrame) error {
	select { // prefer reporting a dead writer over queueing into the void
	case <-c.done:
		return errClosed
	default:
	}
	select {
	case c.out <- f:
		return nil
	case <-c.done:
		return errClosed
	}
}

// send queues a message. It blocks while the queue is full (bounded by the
// writer's write deadline) and fails once the writer has exited.
func (c *wsConn) send(t MsgType, v any) error {
	data, err := encode(t, v)
	if err != nil {
		return err
	}
	return c.enqueue(outFrame{data: data})
}

// fail queues an error message as the last frame; the writer then closes.
func (c *wsConn) fail(code ErrorCode, msg string) {
	data, err := encode(MsgError, ErrorMsg{Code: code, Message: msg})
	if err != nil {
		data = nil
	}
	_ = c.enqueue(outFrame{data: data, last: true})
}

// close asks the writer to send a close frame and exit, and waits for it.
func (c *wsConn) close() {
	_ = c.enqueue(outFrame{last: true})
	<-c.done
}

// read returns the next envelope and refreshes the read deadline.
func (c *wsConn) read() (Envelope, error) {
	mt, data, err := c.ws.ReadMessage()
	if err != nil {
		return Envelope{}, err
	}
	_ = c.extendRead()
	return decodeEnvelope(mt, data)
}
