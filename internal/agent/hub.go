package agent

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/gorilla/websocket"
)

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
//   - Credential lookup by agent_id: named entry token, else accept_token,
//     else unauthorized. Same error for every auth failure.
//   - Duplicate live agent ID -> ErrDuplicateAgent after auth, new conn closed;
//     the existing session is untouched.
//   - Reports: validated, last_seen = Now() (never agent-supplied),
//     RecordAgentReport, ack, then OnChange if changed or first report of the session.
//   - Disconnect only removes the live session (never data).
//   - One writer goroutine per connection, SetReadLimit before the first
//     read, deadlines refreshed by every frame and pong.
//   - Inbound dialing: TLS verified (agent.tls.ca or system roots;
//     insecure_skip_verify only if set, with a warning); a private dialer per
//     dial (websocket.DefaultDialer is never touched); reconnect with backoff.
//   - Tokens are never logged.
type Hub struct {
	opts     HubOptions
	log      *slog.Logger
	now      func() time.Time
	t        timings
	dummyKey string // challenge key for IDs without credentials
	pending  *pendingLimiter

	mu       sync.Mutex
	sessions map[string]*hubSession
}

// hubSession is one authenticated agent connection. reported is only
// touched by the session's reader goroutine.
type hubSession struct {
	id       string
	mode     Mode
	conn     *wsConn
	reported bool
}

// NewHub validates options. No I/O.
func NewHub(opts HubOptions) (*Hub, error) {
	if opts.Store == nil {
		return nil, errors.New("agent: hub store is required")
	}
	if err := validateServer(opts.Server); err != nil {
		return nil, err
	}
	if err := validateRetry(opts.Retry); err != nil {
		return nil, err
	}
	if opts.OnChange == nil {
		opts.OnChange = func() {}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	return &Hub{
		opts:     opts,
		log:      opts.Log.With("component", "agent-hub"),
		now:      opts.Now,
		t:        defaultTimings(),
		dummyKey: hex.EncodeToString(newNonce()),
		pending:  newPendingLimiter(MaxPendingHandshakes, MaxPendingPerIP),
		sessions: make(map[string]*hubSession),
	}, nil
}

func validateServer(s config.AgentServerConfig) error {
	if err := validateTLSPair(s.TLS); err != nil {
		return fmt.Errorf("agent: agent.%w", err)
	}
	for id, e := range s.Agents {
		if !model.ValidAgentID(id) {
			return fmt.Errorf("agent: agent.agents: invalid agent id %q", id)
		}
		if e.Token == "" {
			return fmt.Errorf("agent: agent.agents.%s.token is empty", id)
		}
		if e.ConnectTo == "" {
			continue
		}
		if err := validateWSURL(e.ConnectTo); err != nil {
			return fmt.Errorf("agent: agent.agents.%s.connect_to: %w", id, err)
		}
	}
	return nil
}

// Run serves agent.listen (TLS if agent.tls.cert/key set; no listener if
// listen is empty) and runs inbound dialers until ctx is cancelled, then
// closes all sessions. Returns nil on cancellation; an error if the listener
// cannot start.
func (h *Hub) Run(ctx context.Context) error {
	var ln net.Listener
	if h.opts.Server.Listen != "" {
		var err error
		if ln, err = listen(h.opts.Server.Listen, h.opts.Server.TLS, sectionAgent); err != nil {
			return err
		}
		h.log.Info("agent listener started", "address", ln.Addr().String())
	}
	return h.run(ctx, ln)
}

// run is Run with an injected listener (nil = inbound dialers only).
func (h *Hub) run(ctx context.Context, ln net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait() // every dialer and session has exited
	}()
	if err := h.startDialers(ctx, &wg); err != nil {
		if ln != nil {
			_ = ln.Close()
		}
		return err
	}
	if ln == nil {
		<-ctx.Done()
		return nil
	}
	return serveHTTP(ctx, ln, h.handler(ctx, &wg), h.log)
}

// Connected implements Presence.
func (h *Hub) Connected(agentID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.sessions[agentID]
	return ok
}

func (h *Hub) handler(ctx context.Context, wg *sync.WaitGroup) http.Handler {
	up := newUpgrader(h.t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+PathHealth, handleHealth)
	mux.HandleFunc("GET "+PathWS, func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1) // before hijack: Shutdown waits for this handler until then
		defer wg.Done()
		ws, release, ok := upgrade(h.pending, up, w, r)
		if !ok {
			return
		}
		defer release()
		h.runConn(ctx, ws, ModeOutbound, h.outboundCredentials, release, r.RemoteAddr)
	})
	return mux
}

// outboundCredentials: a named entry's token, else accept_token. A named ID
// can never authenticate with accept_token.
func (h *Hub) outboundCredentials(id string) (string, bool) {
	if e, ok := h.opts.Server.Agents[id]; ok {
		return e.Token, true
	}
	if t := h.opts.Server.AcceptToken; t != "" {
		return t, true
	}
	return "", false
}

// runConn authenticates ws, registers the session and serves it until it
// ends. It reports whether the peer authenticated and was registered.
func (h *Hub) runConn(ctx context.Context, ws *websocket.Conn, mode Mode, creds credentials, release func(), remote string) bool {
	stop := context.AfterFunc(ctx, func() { _ = ws.Close() })
	defer stop()
	defer ws.Close()
	log := h.log.With("mode", string(mode), "remote", remote)

	hello, err := mainHandshake(ws, mode, creds, h.dummyKey, h.t)
	if err != nil {
		log.Warn("agent handshake failed", "error", h.explainAuth(err, mode))
		return false
	}
	log = log.With("agent_id", hello.AgentID)
	s := &hubSession{id: hello.AgentID, mode: mode}
	if !h.register(s) {
		rejectDirect(ws, ErrDuplicateAgent, "agent already connected", h.t.write)
		log.Warn("rejected a second connection for an agent ID that is already connected; existing session kept " +
			"(if two hosts share this ID, give each its own connect.agent_id)")
		return false
	}
	defer h.unregister(s)
	s.conn = newWSConn(ws, h.t, MaxMessage)
	if err := writeDirect(ws, MsgWelcome, Welcome{}); err != nil {
		log.Warn("agent welcome failed", "error", err)
		return false
	}
	release()
	log.Info("agent connected", "version", truncate(hello.Version, maxVersionLen))
	h.serveSession(ctx, s, log)
	log.Info("agent disconnected")
	return true
}

// register adds s unless a live session with the same ID exists.
func (h *Hub) register(s *hubSession) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, dup := h.sessions[s.id]; dup {
		return false
	}
	h.sessions[s.id] = s
	return true
}

// unregister removes s only if it is still the registered session. Stored
// agent data is never touched.
func (h *Hub) unregister(s *hubSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[s.id] == s {
		delete(h.sessions, s.id)
	}
}

// serveSession is the reader loop of an authenticated session.
func (h *Hub) serveSession(ctx context.Context, s *hubSession, log *slog.Logger) {
	s.conn.start(ctx)
	defer s.conn.close()
	for {
		env, err := s.conn.read()
		if err != nil {
			if errors.Is(err, errProtocol) {
				s.conn.fail(ErrBadRequest, "malformed message")
			}
			log.Debug("agent session read ended", "error", err)
			return
		}
		switch env.Type {
		case MsgReport:
			if !h.handleReport(ctx, s, env, log) {
				return
			}
		case MsgError:
			log.Warn("agent sent error", "error", peerError(env))
			return
		default:
			s.conn.fail(ErrBadRequest, "unexpected message type")
			return
		}
	}
}

// startDialers launches one reconnecting dialer per inbound agent entry.
func (h *Hub) startDialers(ctx context.Context, wg *sync.WaitGroup) error {
	var ids []string
	for id, e := range h.opts.Server.Agents {
		if e.ConnectTo != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	tlsCfg, err := clientTLSConfig(h.opts.Server.TLS, sectionAgent, h.log)
	if err != nil {
		return err
	}
	for _, id := range ids {
		warnPlaintext(h.log, h.opts.Server.Agents[id].ConnectTo)
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.dialLoop(ctx, id, h.opts.Server.Agents[id], tlsCfg)
		}()
	}
	return nil
}

// dialLoop keeps an inbound agent connected. Only the configured ID with its
// own token is accepted on this connection (never accept_token).
func (h *Hub) dialLoop(ctx context.Context, id string, e config.AgentEntry, tlsCfg *tls.Config) {
	creds := func(got string) (string, bool) {
		if got != id {
			return "", false
		}
		return e.Token, true
	}
	target := redactURL(e.ConnectTo)
	where := dialTarget{url: e.ConnectTo, urlKey: "agent.agents." + id + ".connect_to", section: sectionAgent, peer: "the agent"}
	bo := newBackoff(h.opts.Retry)
	for {
		ws, err := dial(ctx, e.ConnectTo, tlsCfg, h.t.handshake)
		if err != nil {
			h.log.Warn("cannot connect to inbound agent; retrying", "agent_id", id, "url", target, "error", explainDial(err, where))
		} else if h.runConn(ctx, ws, ModeInbound, creds, func() {}, target) {
			bo.reset()
		}
		if sleepCtx(ctx, bo.next()) != nil {
			return
		}
	}
}
