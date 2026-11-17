package main

// Process-level end-to-end harness. Each test runs the real cmd wiring
// (app.execute -> buildMain/runAgent -> supervise) with the real cloudflare
// client (against cfServer), SQLite store, reconciler, agent Hub/Agent over
// real WebSockets and the real HTTP API. Only the Docker source (scriptSource)
// and the public IP lookup are faked. Wall-clock waits are bounded polls;
// remove_delay is crossed with an injected clock offset, not by sleeping.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/reconcile"
)

const (
	e2eCFToken    = "cf-e2e-root-token"
	e2eAccount    = "acct1"
	e2eZoneID     = "zone1"
	e2eTunnelID   = "0d3c8b1e-5f2a-4c7e-9b1d-2a6f4e8c0001"
	e2eInstanceID = "e2etest1"
	e2eAPIToken   = "api-token-0123456789abcdef"
	e2eAgentToken = "agent-token-0123456789abcdef0123456789"
	e2ePublicIP   = "203.0.113.10"
	e2eWait       = 5 * time.Second
	e2ePoll       = 2 * time.Millisecond
	// e2eDebounce replaces the 2s production debounce so cycles follow triggers quickly.
	e2eDebounce = 10 * time.Millisecond
)

// e2eUnmanagedTunnelConfig is a live tunnel configuration written by a human:
// one unmanaged rule, a custom catch-all and global settings.
const e2eUnmanagedTunnelConfig = `{"ingress":[` +
	`{"hostname":"manual.example.com","service":"http://manual:80","originRequest":{"noTLSVerify":true}},` +
	`{"service":"http_status:418"}],` +
	`"originRequest":{"connectTimeout":10},"warp-routing":{"enabled":true}}`

// offsetClock is wall time plus an adjustable offset (advanced by tests to
// cross remove_delay without sleeping).
type offsetClock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *offsetClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.off)
}

func (c *offsetClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.off += d
}

// scriptSource is a scriptable Docker source: a fixed container list,
// notifications on demand and an optional gate that blocks Snapshot.
type scriptSource struct {
	now      func() time.Time
	watching chan struct{}

	mu         sync.Mutex
	containers []model.Container
	notify     func()
	snaps      []time.Time // clock time of every Snapshot call
	gate       chan struct{}
	watchOnce  sync.Once
}

func newScriptSource(now func() time.Time, cs ...model.Container) *scriptSource {
	return &scriptSource{now: now, watching: make(chan struct{}), containers: cs}
}

func (s *scriptSource) Snapshot(ctx context.Context) (model.Snapshot, error) {
	s.mu.Lock()
	gate := s.gate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return model.Snapshot{}, ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snaps = append(s.snaps, s.now())
	cs := make([]model.Container, len(s.containers))
	copy(cs, s.containers)
	return model.Snapshot{AgentID: model.LocalAgentID, Complete: true, Containers: cs}, nil
}

func (s *scriptSource) Watch(ctx context.Context, notify func()) error {
	s.mu.Lock()
	s.notify = notify
	s.mu.Unlock()
	s.watchOnce.Do(func() { close(s.watching) })
	<-ctx.Done()
	return ctx.Err()
}

func (s *scriptSource) Close() error { return nil }

func (s *scriptSource) set(cs ...model.Container) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.containers = cs
}

// hold makes Snapshot block until the returned release func is called.
func (s *scriptSource) hold() (release func()) {
	gate := make(chan struct{})
	s.mu.Lock()
	s.gate = gate
	s.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

func (s *scriptSource) snapshotCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.snaps)
}

func (s *scriptSource) snapshotAt(i int) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.snaps) {
		return time.Time{}, false
	}
	return s.snaps[i], true
}

// poke simulates a Docker event.
func (s *scriptSource) poke(t *testing.T) {
	t.Helper()
	select {
	case <-s.watching:
	case <-time.After(e2eWait):
		t.Fatal("source Watch never started")
	}
	s.mu.Lock()
	notify := s.notify
	s.mu.Unlock()
	notify()
}

func fixedPublicIP() model.PublicIPFunc {
	return func(_ context.Context, v6 bool) (netip.Addr, error) {
		if v6 {
			return netip.Addr{}, errors.New("no IPv6 in e2e")
		}
		return netip.MustParseAddr(e2ePublicIP), nil
	}
}

// e2eEnv is one fake Cloudflare account shared by the processes of a test.
type e2eEnv struct {
	t     *testing.T
	cf    *cfServer
	clock *offsetClock
}

// newEnv seeds four zones (two pages at the fake's page size of 2) and the
// root tunnel with an unmanaged configuration.
func newEnv(t *testing.T) *e2eEnv {
	t.Helper()
	cf := newCFServer(t, e2eCFToken)
	cf.addZone(e2eCFToken, "zone-a", "alpha.net", e2eAccount)
	cf.addZone(e2eCFToken, "zone-b", "beta.org", e2eAccount)
	cf.addZone(e2eCFToken, e2eZoneID, "example.com", e2eAccount)
	cf.addZone(e2eCFToken, "zone-g", "gamma.io", e2eAccount)
	cf.setTunnelConfig(e2eTunnelID, e2eUnmanagedTunnelConfig)
	return &e2eEnv{t: t, cf: cf, clock: &offsetClock{}}
}

// mainConfig is a validated-shape main config: root credential + tunnel, API
// with token on a free port, fast retries, DB in a temp dir.
func (e *e2eEnv) mainConfig() config.Config {
	cfg := config.Defaults()
	cfg.LogLevel = "debug"
	cfg.InstanceID = e2eInstanceID
	cfg.DB.Path = filepath.Join(e.t.TempDir(), "labelgate.db")
	cfg.Cloudflare = config.CloudflareConfig{APIToken: e2eCFToken, AccountID: e2eAccount, TunnelID: e2eTunnelID}
	cfg.API.Address = freeAddr(e.t)
	cfg.API.Token = e2eAPIToken
	cfg.Retry = fastRetry()
	return cfg
}

func fastRetry() config.RetryConfig {
	return config.RetryConfig{Attempts: 2, Delay: 5 * time.Millisecond, MaxDelay: 20 * time.Millisecond, Backoff: 2}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// proc is one running labelgate process (main or agent).
type proc struct {
	t    *testing.T
	cfg  config.Config
	src  *scriptSource
	logs *syncBuffer

	cancel  context.CancelFunc
	done    chan error
	stopped sync.Once
	err     error

	mu     sync.Mutex
	engine reconcile.Engine
}

// startMain runs a main instance against env with src as its Docker source.
func (e *e2eEnv) startMain(cfg config.Config, src *scriptSource) *proc {
	e.t.Helper()
	p := &proc{t: e.t, cfg: cfg, src: src}
	b := realBuilders()
	realCF, realRec := b.newCloudflare, b.newReconciler
	b.newCloudflare = func(o cloudflare.Options) (cloudflare.API, error) {
		if o.BaseURL != "" || o.HTTPClient != nil {
			e.t.Errorf("production wiring must not set a base URL or HTTP client: %+v", o)
		}
		o.BaseURL, o.HTTPClient = e.cf.srv.URL, e.cf.srv.Client()
		return realCF(o)
	}
	b.newReconciler = func(d reconcile.Deps) (reconcile.Engine, error) {
		d.Settings.Debounce, d.Settings.MaxDebounce = e2eDebounce, 5*e2eDebounce
		eng, err := realRec(d)
		p.mu.Lock()
		p.engine = eng
		p.mu.Unlock()
		return eng, err
	}
	p.run(b, e.clock.Now)
	return p
}

func (p *proc) run(b builders, now func() time.Time) {
	p.t.Helper()
	if err := config.Validate(p.cfg); err != nil {
		p.t.Fatalf("config invalid: %v", err)
	}
	src := p.src
	b.newDocker = func(config.DockerConfig, config.RetryConfig, *slog.Logger) (dockerSource, error) { return src, nil }
	b.publicIP = fixedPublicIP
	p.logs = &syncBuffer{}
	log, err := newLogger(p.cfg.LogLevel, p.cfg.LogFormat, p.logs)
	if err != nil {
		p.t.Fatal(err)
	}
	a := &app{load: config.Load, b: b, now: now}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan error, 1)
	go func() { p.done <- a.execute(ctx, p.cfg, log) }()
	p.t.Cleanup(func() {
		if err := p.stop(); err != nil {
			p.t.Errorf("process exited with error: %v", err)
		}
		if p.t.Failed() {
			p.t.Logf("process log (%s):\n%s", p.cfg.Mode, p.logs.String())
		}
	})
}

// stop cancels the process and returns its exit error (idempotent).
func (p *proc) stop() error {
	p.stopped.Do(func() {
		p.cancel()
		select {
		case p.err = <-p.done:
		case <-time.After(e2eWait):
			p.err = errors.New("process did not stop")
		}
	})
	return p.err
}

func (p *proc) rec() reconcile.Engine {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.engine
}

// waitCycleAfter waits until a cycle that started with Snapshot call n has
// completed, and returns the resulting status.
func (p *proc) waitCycleAfter(n int) reconcile.SyncStatus {
	p.t.Helper()
	var st reconcile.SyncStatus
	eventually(p.t, fmt.Sprintf("reconcile cycle #%d", n+1), func() bool {
		started, ok := p.src.snapshotAt(n)
		if !ok || p.rec() == nil {
			return false
		}
		st = p.rec().Status()
		return st.State != reconcile.SyncPending && st.LastSync.After(started)
	})
	return st
}

// firstCycle waits for the initial cycle.
func (p *proc) firstCycle() reconcile.SyncStatus {
	p.t.Helper()
	return p.waitCycleAfter(0)
}

// cycle triggers a cycle through a Docker event and waits for it.
func (p *proc) cycle() reconcile.SyncStatus {
	p.t.Helper()
	n := p.src.snapshotCount()
	p.src.poke(p.t)
	return p.waitCycleAfter(n)
}

func (p *proc) waitLog(what string) {
	p.t.Helper()
	eventually(p.t, "log line "+what, func() bool { return strings.Contains(p.logs.String(), what) })
}

// apiGet performs GET {base}{path}; token "" sends no Authorization header.
func (p *proc) apiGet(path, token string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, "http://"+p.cfg.API.Address+p.cfg.API.BasePath+path, nil)
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// waitAPI waits until the unauthenticated health endpoint answers.
func (p *proc) waitAPI() {
	p.t.Helper()
	eventually(p.t, "API health", func() bool {
		code, _, err := p.apiGet("/health", "")
		return err == nil && code == http.StatusOK
	})
}

// api GETs path with the right token, requires 200 and decodes into v.
func (p *proc) api(path string, v any) {
	p.t.Helper()
	code, body, err := p.apiGet(path, e2eAPIToken)
	if err != nil || code != http.StatusOK {
		p.t.Fatalf("GET %s: code=%d err=%v body=%s", path, code, err, body)
	}
	if err := json.Unmarshal(body, v); err != nil {
		p.t.Fatalf("GET %s: decode: %v (%s)", path, err, body)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(e2eWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(e2ePoll)
	}
}
