package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/source"
	"github.com/gorilla/websocket"
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
//   - AgentID: Connect.AgentID or derived (machine-id, else hostname);
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
type Agent struct {
	opts    AgentOptions
	id      string
	idSrc   agentIDSource
	log     *slog.Logger
	t       timings
	seq     atomic.Uint64
	pending *pendingLimiter

	mu  sync.Mutex
	cur *agentSession // current authenticated main session

	ipMu       sync.Mutex
	ipProblems ipTrackers // public IP detection failures, guarded by ipMu
}

// agentSession is one authenticated main connection.
type agentSession struct {
	conn    *wsConn
	cancel  context.CancelFunc
	refresh chan struct{} // cap 1
	changes chan struct{} // cap 1
}

// NewAgent validates options and resolves the agent ID (reads
// /etc/machine-id or the hostname when connect.agent_id is empty). No network I/O.
func NewAgent(opts AgentOptions) (*Agent, error) {
	if opts.Source == nil {
		return nil, errors.New("agent: source is required")
	}
	if err := validateConnect(opts.Connect); err != nil {
		return nil, err
	}
	if err := validateRetry(opts.Retry); err != nil {
		return nil, err
	}
	id, idSrc, err := resolveAgentID(opts.Connect.AgentID, readMachineID, os.Hostname)
	if err != nil {
		return nil, err
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	return &Agent{
		opts:    opts,
		id:      id,
		idSrc:   idSrc,
		log:     opts.Log.With("component", "agent", "agent_id", id),
		t:       defaultTimings(),
		pending: newPendingLimiter(MaxPendingHandshakes, MaxPendingPerIP),

		ipProblems: newIPTrackers(),
	}, nil
}

func validateConnect(c config.ConnectConfig) error {
	if c.Token == "" {
		return errors.New("agent: connect.token is empty")
	}
	if c.HeartbeatInterval <= 0 {
		return errors.New("agent: connect.heartbeat_interval must be > 0")
	}
	switch c.Mode {
	case config.ConnectOutbound:
		if err := validateWSURL(c.Endpoint); err != nil {
			return fmt.Errorf("agent: connect.endpoint: %w", err)
		}
	case config.ConnectInbound:
		if c.Listen == "" {
			return errors.New("agent: connect.listen is required in inbound mode")
		}
		if err := validateTLSPair(c.TLS); err != nil {
			return fmt.Errorf("agent: connect.%w", err)
		}
	default:
		return fmt.Errorf("agent: unknown connect.mode %q", c.Mode)
	}
	return nil
}

// ID returns the resolved agent ID.
func (a *Agent) ID() string { return a.id }

// Run runs until ctx is cancelled. Returns nil on cancellation; an error if
// the inbound listener cannot start or the source fails unrecoverably.
func (a *Agent) Run(ctx context.Context) error {
	// a.log already carries agent_id; the startup line names it itself.
	logAgentID(a.opts.Log.With("component", "agent"), a.id, a.idSrc, a.opts.Connect.Mode)
	if a.opts.Connect.Mode == config.ConnectInbound {
		ln, err := listen(a.opts.Connect.Listen, a.opts.Connect.TLS, sectionConnect)
		if err != nil {
			return err
		}
		a.log.Info("agent inbound listener started", "address", ln.Addr().String())
		return a.run(ctx, func(ctx context.Context) error { return a.serveInbound(ctx, ln) })
	}
	tlsCfg, err := clientTLSConfig(a.opts.Connect.TLS, sectionConnect, a.log)
	if err != nil {
		return err
	}
	warnPlaintext(a.log, a.opts.Connect.Endpoint)
	return a.run(ctx, func(ctx context.Context) error {
		a.runOutbound(ctx, a.dialer(tlsCfg))
		return nil
	})
}

// run runs the source watcher alongside transport until ctx is done or either fails.
func (a *Agent) run(ctx context.Context, transport func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watchErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := a.opts.Source.Watch(ctx, a.notifyChange)
		if err != nil && ctx.Err() == nil {
			watchErr <- err
			cancel()
		}
	}()
	err := transport(ctx)
	cancel()
	wg.Wait()
	select {
	case werr := <-watchErr:
		return fmt.Errorf("agent: source watch: %w", werr)
	default:
		return err
	}
}

// dialer returns a function dialing the main endpoint with a fresh private dialer.
func (a *Agent) dialer(tlsCfg *tls.Config) func(context.Context) (*websocket.Conn, error) {
	return func(ctx context.Context) (*websocket.Conn, error) {
		return dial(ctx, a.opts.Connect.Endpoint, tlsCfg, a.t.handshake)
	}
}

// runOutbound keeps a session to the main instance until ctx is done.
func (a *Agent) runOutbound(ctx context.Context, dialFn func(context.Context) (*websocket.Conn, error)) {
	target := redactURL(a.opts.Connect.Endpoint)
	where := dialTarget{url: a.opts.Connect.Endpoint, urlKey: config.KeyWithEnv("connect.endpoint"), section: sectionConnect, peer: "the main instance"}
	bo := newBackoff(a.opts.Retry)
	for {
		ws, err := dialFn(ctx)
		if err != nil {
			a.log.Warn("cannot connect to the main instance; retrying", "url", target, "error", explainDial(err, where))
		} else if a.runConn(ctx, ws, ModeOutbound, func() {}, target) {
			bo.reset()
		}
		if sleepCtx(ctx, bo.next()) != nil {
			return
		}
	}
}

// serveInbound serves PathWS/PathHealth on ln until ctx is done.
func (a *Agent) serveInbound(ctx context.Context, ln net.Listener) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	up := newUpgrader(a.t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+PathHealth, handleHealth)
	mux.HandleFunc("GET "+PathWS, func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1)
		defer wg.Done()
		ws, release, ok := upgrade(a.pending, up, w, r)
		if !ok {
			return
		}
		defer release()
		a.runConn(ctx, ws, ModeInbound, release, r.RemoteAddr)
	})
	return serveHTTP(ctx, ln, mux, a.log)
}

// runConn authenticates the main on ws and serves the session. It reports
// whether the main authenticated.
func (a *Agent) runConn(ctx context.Context, ws *websocket.Conn, mode Mode, release func(), remote string) bool {
	stop := context.AfterFunc(ctx, func() { _ = ws.Close() })
	defer stop()
	defer ws.Close()
	log := a.log.With("mode", string(mode), "remote", remote)
	if err := agentHandshake(ws, mode, a.id, a.opts.Connect.Token, a.opts.Version, a.t); err != nil {
		log.Warn("handshake with the main instance failed", "error", withHint(err, agentAuthHint(a.id, err)))
		return false
	}
	release()
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &agentSession{
		conn:    newWSConn(ws, a.t, MaxHandshakeMessage), // main only sends small frames
		cancel:  cancel,
		refresh: make(chan struct{}, 1),
		changes: make(chan struct{}, 1),
	}
	if a.setSession(s) {
		log.Info("replaced previous main session")
	}
	defer a.clearSession(s)
	log.Info("connected to main")
	a.serveSession(sctx, s, log)
	log.Info("disconnected from main")
	return true
}

// setSession installs s as the current session, cancelling any previous one.
func (a *Agent) setSession(s *agentSession) (replaced bool) {
	a.mu.Lock()
	prev := a.cur
	a.cur = s
	a.mu.Unlock()
	if prev != nil {
		prev.cancel()
	}
	return prev != nil
}

func (a *Agent) clearSession(s *agentSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur == s {
		a.cur = nil
	}
}

// notifyChange is the source Watch callback (never blocks).
func (a *Agent) notifyChange() {
	a.mu.Lock()
	s := a.cur
	a.mu.Unlock()
	if s != nil {
		signal(s.changes)
	}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// serveSession runs the reporter and the reader loop of one session.
func (a *Agent) serveSession(ctx context.Context, s *agentSession, log *slog.Logger) {
	s.conn.start(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.reportLoop(ctx, s, log)
	}()
	defer func() {
		s.cancel()
		s.conn.close()
		wg.Wait()
	}()
	for {
		env, err := s.conn.read()
		if err != nil {
			log.Debug("main session read ended", "error", err)
			return
		}
		switch env.Type {
		case MsgAck:
		case MsgRefresh:
			signal(s.refresh)
		case MsgError:
			perr := peerError(env)
			log.Warn("main instance ended the session with an error", "error", withHint(perr, agentAuthHint(a.id, perr)))
			return
		default:
			s.conn.fail(ErrBadRequest, "unexpected message type")
			return
		}
	}
}

// reportLoop sends a report at session start, on refresh, on heartbeat and
// after debounced source changes.
func (a *Agent) reportLoop(ctx context.Context, s *agentSession, log *slog.Logger) {
	heartbeat := time.NewTicker(a.opts.Connect.HeartbeatInterval)
	defer heartbeat.Stop()
	for {
		if err := s.conn.send(MsgReport, a.buildReport(ctx, log)); err != nil {
			return
		}
		if !a.waitReport(ctx, s, heartbeat.C) {
			return
		}
	}
}

// waitReport blocks until the next report is due; false when ctx is done.
// Change notifications are coalesced for the debounce period.
func (a *Agent) waitReport(ctx context.Context, s *agentSession, heartbeat <-chan time.Time) bool {
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return false
		case <-heartbeat:
			return true
		case <-s.refresh:
			return true
		case <-debounce:
			return true
		case <-s.changes:
			if debounce == nil {
				debounce = time.After(a.t.debounce)
			}
		}
	}
}

// buildReport takes a full snapshot. Snapshot failures are reported as
// DockerOK=false, never as an empty list.
func (a *Agent) buildReport(ctx context.Context, log *slog.Logger) Report {
	rep := Report{Seq: a.seq.Add(1)}
	var err4, err6 error
	rep.PublicIP, err4 = a.lookupIP(ctx, false)
	rep.PublicIPv6, err6 = a.lookupIP(ctx, true)
	a.logIPProblems(ctx, log, err4, err6)
	snap, err := a.opts.Source.Snapshot(ctx)
	switch {
	case err != nil:
		rep.DockerError = truncate(err.Error(), maxDockerErrorLen)
		log.Warn("cannot list local containers; the main instance keeps this agent's previous containers", "error", err)
	case len(snap.Containers) > MaxContainers:
		rep.DockerError = fmt.Sprintf("too many containers: %d > %d; set %s on the agent to report only labelled containers",
			len(snap.Containers), MaxContainers, config.KeyWithEnv("docker.filter_label"))
		log.Error(rep.DockerError, "containers", len(snap.Containers))
	default:
		rep.DockerOK = true
		rep.Containers = snap.Containers
		if rep.Containers == nil {
			rep.Containers = []model.Container{}
		}
	}
	return rep
}

// lookupIP returns this host's public address of one family, or "" without
// an error when no lookup is configured. A failed lookup is an error.
func (a *Agent) lookupIP(ctx context.Context, v6 bool) (string, error) {
	if a.opts.PublicIP == nil {
		return "", nil
	}
	addr, err := a.opts.PublicIP(ctx, v6)
	if err != nil {
		return "", err
	}
	addr = addr.Unmap()
	if (v6 && !addr.Is6()) || (!v6 && !addr.Is4()) {
		return "", fmt.Errorf("the lookup returned %q, not an %s address", addr, ipFamily(v6))
	}
	return addr.String(), nil
}

func ipFamily(v6 bool) string {
	if v6 {
		return "IPv6"
	}
	return "IPv4"
}
