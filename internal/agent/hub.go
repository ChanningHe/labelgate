package agent

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/gorilla/websocket"
)

var errNotImplemented = errors.New("agent: not implemented")

// HubOptions configures the main-side Hub.
type HubOptions struct {
	Server  config.AgentServerConfig
	Retry   config.RetryConfig
	Version string
	Store   HubStore
	// OnChange is called (must not block) after a report changed an agent's
	// stored snapshot, and when an agent (re)connects. Typically Reconciler.Trigger.
	OnChange func()
	Now      func() time.Time
	Log      *slog.Logger
}

// Hub accepts outbound agents on agent.listen and dials inbound agents
// (entries with connect_to).
//
// Required behavior (WP5):
//   - Credential lookup by agent_id: named entry token, else accept_token,
//     else unauthorized. Same error for every auth failure.
//   - Duplicate live agent ID -> ErrDuplicateAgent after auth, new conn closed;
//     the existing session is untouched.
//   - Reports: validate (<= MaxContainers, container names/labels sane),
//     last_seen = Now() (never agent-supplied), RecordAgentReport, ack, then
//     OnChange if changed or first report of the session.
//   - Disconnect only removes the live session (never data).
//   - One writer goroutine per connection (all writes through a channel),
//     SetReadLimit before the first read, deadlines refreshed by pong.
//   - Inbound dialing: TLS verified (agent.tls.ca or system roots;
//     insecure_skip_verify only if set, warn once); custom websocket.Dialer per
//     Hub (never mutate websocket.DefaultDialer); reconnect with backoff.
//   - Tokens are never logged.
type Hub struct {
	upgrader websocket.Upgrader
	dialer   *websocket.Dialer // per-Hub dialer for inbound agents
}

// NewHub validates options. No I/O.
func NewHub(opts HubOptions) (*Hub, error) { return nil, errNotImplemented }

// Run serves agent.listen (TLS if agent.tls.cert/key set) and runs inbound
// dialers until ctx is cancelled, then closes all sessions. Returns nil on
// cancellation; an error if the listener cannot start.
func (h *Hub) Run(ctx context.Context) error { return errNotImplemented }

// Connected implements Presence.
func (h *Hub) Connected(agentID string) bool { return false }
