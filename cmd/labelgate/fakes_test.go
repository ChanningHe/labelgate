package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/agent"
	"github.com/channinghe/labelgate/internal/api"
	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/reconcile"
	"github.com/channinghe/labelgate/internal/store"
)

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// events is an ordered, goroutine-safe event log.
type events struct {
	mu  sync.Mutex
	log []string
}

func (e *events) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.log = append(e.log, s)
}

func (e *events) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.log...)
}

func (e *events) index(s string) int {
	for i, v := range e.snapshot() {
		if v == s {
			return i
		}
	}
	return -1
}

func (e *events) has(s string) bool { return e.index(s) >= 0 }

func (e *events) waitFor(t *testing.T, s string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !e.has(s) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q; have %v", s, e.snapshot())
		}
		time.Sleep(time.Millisecond)
	}
}

type fakeStore struct {
	store.Store // unimplemented methods panic if the wiring calls them unexpectedly
	ev          *events
	id          string
	setID       string
	setErr      error
	idErr       error
	closeErr    error
}

func (f *fakeStore) Close() error { f.ev.add("store closed"); return f.closeErr }
func (f *fakeStore) InstanceID(context.Context) (string, error) {
	return f.id, f.idErr
}
func (f *fakeStore) SetInstanceID(_ context.Context, id string) error {
	f.setID = id
	return f.setErr
}

type fakeCF struct {
	cloudflare.API
	verifyErr error
	verified  atomic.Int32
}

func (f *fakeCF) Verify(context.Context) error { f.verified.Add(1); return f.verifyErr }
func (f *fakeCF) Health() cloudflare.Health    { return cloudflare.Health{Reachable: true} }

type fakeDocker struct{ ev *events }

func (f *fakeDocker) Snapshot(context.Context) (model.Snapshot, error) { return model.Snapshot{}, nil }
func (f *fakeDocker) Watch(ctx context.Context, _ func()) error        { <-ctx.Done(); return ctx.Err() }
func (f *fakeDocker) Close() error                                     { f.ev.add("docker closed"); return nil }

// fakeComponent runs until ctx is done, then records a stop event after stopDelay.
type fakeComponent struct {
	name      string
	ev        *events
	stopDelay time.Duration
	runErr    error // returned immediately when set
	retNil    bool  // return nil immediately (unexpected exit)
	stopErr   error // returned after ctx is done
	triggered atomic.Int32
}

func (c *fakeComponent) Run(ctx context.Context) error {
	c.ev.add(c.name + " started")
	if c.runErr != nil {
		return c.runErr
	}
	if c.retNil {
		return nil
	}
	<-ctx.Done()
	time.Sleep(c.stopDelay)
	c.ev.add(c.name + " stopped")
	if c.stopErr != nil {
		return c.stopErr
	}
	return ctx.Err()
}

func (c *fakeComponent) Trigger() { c.triggered.Add(1) }
func (c *fakeComponent) Status() reconcile.SyncStatus {
	return reconcile.SyncStatus{State: reconcile.SyncPending}
}
func (c *fakeComponent) Connected(string) bool { return false }

// harness wires an app with fakes and records what the constructors received.
type harness struct {
	app   *app
	ev    *events
	store *fakeStore
	cf    *fakeCF
	rec   *fakeComponent
	hub   *fakeComponent
	api   *fakeComponent
	agent *fakeComponent

	cfOpts   cloudflare.Options
	recDeps  reconcile.Deps
	hubOpts  agent.HubOptions
	apiDeps  api.Deps
	agentOpt agent.AgentOptions
	builds   map[string]int
	fail     map[string]error // constructor name -> error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ev := &events{}
	h := &harness{
		ev:     ev,
		store:  &fakeStore{ev: ev, id: "abcd1234"},
		cf:     &fakeCF{},
		rec:    &fakeComponent{name: "reconciler", ev: ev},
		hub:    &fakeComponent{name: "hub", ev: ev, stopDelay: 20 * time.Millisecond},
		api:    &fakeComponent{name: "api", ev: ev, stopDelay: 20 * time.Millisecond},
		agent:  &fakeComponent{name: "agent", ev: ev},
		builds: map[string]int{},
		fail:   map[string]error{},
	}
	step := func(name string) error { h.builds[name]++; return h.fail[name] }
	h.app = &app{
		now: func() time.Time { return time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC) },
		b: builders{
			openStore: func(context.Context, string) (store.Store, error) {
				if err := step("store"); err != nil {
					return nil, err
				}
				return h.store, nil
			},
			newCloudflare: func(o cloudflare.Options) (cloudflare.API, error) {
				h.cfOpts = o
				if err := step("cloudflare"); err != nil {
					return nil, err
				}
				return h.cf, nil
			},
			newDocker: func(config.DockerConfig, config.RetryConfig, *slog.Logger) (dockerSource, error) {
				if err := step("docker"); err != nil {
					return nil, err
				}
				return &fakeDocker{ev: ev}, nil
			},
			newReconciler: func(d reconcile.Deps) (reconcile.Engine, error) {
				h.recDeps = d
				if err := step("reconciler"); err != nil {
					return nil, err
				}
				return h.rec, nil
			},
			newHub: func(o agent.HubOptions) (hub, error) {
				h.hubOpts = o
				if err := step("hub"); err != nil {
					return nil, err
				}
				return h.hub, nil
			},
			newAPI: func(d api.Deps) (runner, error) {
				h.apiDeps = d
				if err := step("api"); err != nil {
					return nil, err
				}
				return h.api, nil
			},
			newAgent: func(o agent.AgentOptions) (runner, error) {
				h.agentOpt = o
				if err := step("agent"); err != nil {
					return nil, err
				}
				return h.agent, nil
			},
			publicIP: func() model.PublicIPFunc { return nil },
		},
	}
	return h
}

// mainConfig is a main-mode config with API and agent server enabled.
func mainConfig() config.Config {
	cfg := config.Defaults()
	cfg.Mode = config.ModeMain
	cfg.API.Token = "tok"
	cfg.Agent.Enabled = true
	cfg.DB.Path = "/tmp/lg-test.db"
	return cfg
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

var errBoom = errors.New("boom")
