package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/source"
)

// AgentOptions configures the agent-side process.
type AgentOptions struct {
	Connect  config.ConnectConfig
	Retry    config.RetryConfig
	Version  string
	Source   source.Source
	PublicIP model.PublicIPFunc
	Now      func() time.Time
	Log      *slog.Logger
}

// Agent reports local containers to a main instance.
//
// Required behavior (WP5):
//   - AgentID: Connect.AgentID or derived (see config.ConnectConfig.AgentID);
//     must satisfy model.ValidAgentID.
//   - Outbound: dial Connect.Endpoint (TLS verified via connect.tls.ca/system
//     roots; insecure only if explicitly set), handshake with ModeOutbound,
//     reconnect with backoff.
//   - Inbound: listen on Connect.Listen (TLS if connect.tls.cert/key), serve
//     PathWS and PathHealth; handshake with ModeInbound; a newly authenticated
//     main session replaces an older one.
//   - Verify mac_m before sending anything other than hello.
//   - Send a full Report on session start, on source change (Watch notify,
//     debounced 1s) and every Connect.HeartbeatInterval; on MsgRefresh.
//     Snapshot errors -> DockerOK=false (never an empty list).
type Agent struct{}

// NewAgent validates options. No I/O.
func NewAgent(opts AgentOptions) (*Agent, error) { return nil, errNotImplemented }

// Run runs until ctx is cancelled. Returns nil on cancellation.
func (a *Agent) Run(ctx context.Context) error { return errNotImplemented }
