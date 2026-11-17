// Package agent implements the main<->agent WebSocket protocol: the
// main-side Hub (agent listener + inbound dialers) and the agent-side Agent
// (outbound dialer or inbound listener).
//
// protocol.go is a frozen contract: wire types, limits and the handshake MAC.
// Both directions (outbound: agent dials main; inbound: main dials agent) use
// the same message sequence on an established WebSocket:
//
//	agent -> main  hello      {proto, agent_id, version, nonce_a}
//	main  -> agent challenge  {nonce_m, mac_m = MAC(token, RoleMain,  mode, agent_id, nonce_a, nonce_m)}
//	agent -> main  auth       {mac_a = MAC(token, RoleAgent, mode, agent_id, nonce_a, nonce_m)}
//	main  -> agent welcome    {}
//
// The agent verifies mac_m before sending anything but its hello; the main
// verifies mac_a before accepting any report. The token never travels on the
// wire. mode is the direction the TCP/TLS connection was established in, as
// known locally by each side, which defeats relaying a session between the
// two directions. Failures are answered with an error message and a close.
package agent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

// ProtocolVersion is the protocol version spoken by this release.
const ProtocolVersion = 1

// HTTP paths served by the main's agent listener and the agent's inbound listener.
const (
	PathWS     = "/ws"
	PathHealth = "/health" // unauthenticated; body exactly {"status":"ok"}
)

// Limits and timings.
const (
	NonceSize            = 32
	MaxHandshakeMessage  = 64 << 10 // read limit until authenticated
	MaxMessage           = 8 << 20  // read limit after authentication
	MaxContainers        = 4096     // per report; more -> error too_large
	HandshakeTimeout     = 5 * time.Second
	WriteWait            = 10 * time.Second
	PingInterval         = 20 * time.Second
	PongWait             = 60 * time.Second // read deadline, refreshed by every frame incl. pong
	SendQueueSize        = 16               // per-connection outbound queue (single writer goroutine)
	MaxPendingHandshakes = 64               // concurrent unauthenticated connections per listener
	MaxPendingPerIP      = 4                // ... per remote IPv4 address or IPv6 /64
)

// MsgType is the envelope type.
type MsgType string

const (
	MsgHello     MsgType = "hello"
	MsgChallenge MsgType = "challenge"
	MsgAuth      MsgType = "auth"
	MsgWelcome   MsgType = "welcome"
	MsgReport    MsgType = "report"  // agent -> main, full snapshot
	MsgAck       MsgType = "ack"     // main -> agent, report accepted
	MsgRefresh   MsgType = "refresh" // main -> agent, send a report now
	MsgError     MsgType = "error"   // either direction, followed by close
)

// Envelope is the JSON text frame carrying every message.
type Envelope struct {
	Type    MsgType         `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Hello opens the handshake (agent -> main).
type Hello struct {
	Proto   int    `json:"proto"`
	AgentID string `json:"agent_id"`
	Version string `json:"version"` // labelgate build version, informational
	Nonce   []byte `json:"nonce"`   // NonceSize random bytes (base64 in JSON)
}

// Challenge proves the main knows the token (main -> agent).
type Challenge struct {
	Nonce []byte `json:"nonce"`
	MAC   []byte `json:"mac"`
}

// Auth proves the agent knows the token (agent -> main).
type Auth struct {
	MAC []byte `json:"mac"`
}

// Welcome completes the handshake (main -> agent).
type Welcome struct{}

// Report is a complete snapshot of the agent host (agent -> main).
// DockerOK=false means the agent could not list containers: Containers is
// ignored and the main keeps the previous snapshot (resources are held).
type Report struct {
	Seq         uint64            `json:"seq"`
	PublicIP    string            `json:"public_ip,omitempty"`
	PublicIPv6  string            `json:"public_ipv6,omitempty"`
	DockerOK    bool              `json:"docker_ok"`
	DockerError string            `json:"docker_error,omitempty"`
	Containers  []model.Container `json:"containers"`
}

// Ack acknowledges a report by sequence number.
type Ack struct {
	Seq uint64 `json:"seq"`
}

// Refresh asks the agent to send a report immediately.
type Refresh struct{}

// ErrorCode classifies protocol errors. Authentication failures always use
// ErrUnauthorized regardless of cause (unknown ID, bad MAC) to avoid oracles.
type ErrorCode string

const (
	ErrUnauthorized       ErrorCode = "unauthorized"
	ErrDuplicateAgent     ErrorCode = "duplicate_agent" // sent only after successful auth
	ErrUnsupportedVersion ErrorCode = "unsupported_version"
	ErrBadRequest         ErrorCode = "bad_request"
	ErrTooLarge           ErrorCode = "too_large"
	ErrInternal           ErrorCode = "internal"
)

// ErrorMsg reports a protocol error. Message must never contain secrets.
type ErrorMsg struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message,omitempty"`
}

// Role identifies which side computes a MAC.
type Role string

const (
	RoleMain  Role = "main"
	RoleAgent Role = "agent"
)

// Mode is the direction the connection was established in.
type Mode string

const (
	ModeOutbound Mode = "outbound" // agent dialed main
	ModeInbound  Mode = "inbound"  // main dialed agent
)

// MAC computes HMAC-SHA256(key=token) over the transcript
//
//	"labelgate/1|" role "|" mode "|" agentID "|" hex(agentNonce) "|" hex(mainNonce)
func MAC(token string, role Role, mode Mode, agentID string, agentNonce, mainNonce []byte) []byte {
	h := hmac.New(sha256.New, []byte(token))
	transcript := "labelgate/1|" + string(role) + "|" + string(mode) + "|" + agentID + "|" +
		hex.EncodeToString(agentNonce) + "|" + hex.EncodeToString(mainNonce)
	h.Write([]byte(transcript)) // hash.Hash.Write never returns an error
	return h.Sum(nil)
}

// VerifyMAC reports whether mac is valid for the transcript (constant time).
// Nonces of the wrong size never verify.
func VerifyMAC(token string, role Role, mode Mode, agentID string, agentNonce, mainNonce, mac []byte) bool {
	if token == "" || len(agentNonce) != NonceSize || len(mainNonce) != NonceSize {
		return false
	}
	return hmac.Equal(mac, MAC(token, role, mode, agentID, agentNonce, mainNonce))
}
