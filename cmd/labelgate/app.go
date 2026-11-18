package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/channinghe/labelgate/internal/agent"
	"github.com/channinghe/labelgate/internal/api"
	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/publicip"
	"github.com/channinghe/labelgate/internal/reconcile"
	"github.com/channinghe/labelgate/internal/source"
	"github.com/channinghe/labelgate/internal/source/docker"
	"github.com/channinghe/labelgate/internal/store"
	"github.com/channinghe/labelgate/internal/version"
)

// runner is a long-running component that stops when its context ends.
type runner interface {
	Run(ctx context.Context) error
}

// dockerSource is the local container source plus its cleanup.
type dockerSource interface {
	source.Source
	Close() error
}

// hub is the main-side agent hub.
type hub interface {
	runner
	Connected(agentID string) bool
}

// builders are the constructors cmd wires together. Production values call the
// real packages; tests substitute fakes.
type builders struct {
	openStore     func(ctx context.Context, path string) (store.Store, error)
	newCloudflare func(opts cloudflare.Options) (cloudflare.API, error)
	newDocker     func(cfg config.DockerConfig, retry config.RetryConfig, log *slog.Logger) (dockerSource, error)
	newReconciler func(d reconcile.Deps) (reconcile.Engine, error)
	newHub        func(opts agent.HubOptions) (hub, error)
	newAPI        func(d api.Deps) (runner, error)
	newAgent      func(opts agent.AgentOptions) (runner, error)
	publicIP      func() model.PublicIPFunc
}

// app carries everything run needs from the outside world.
type app struct {
	load func(path string) (config.Config, error)
	b    builders
	now  func() time.Time
}

func newApp() *app {
	return &app{load: config.Load, b: realBuilders(), now: time.Now}
}

func realBuilders() builders {
	return builders{
		openStore: func(ctx context.Context, path string) (store.Store, error) {
			s, err := store.Open(ctx, path, nil)
			if err != nil {
				return nil, err
			}
			return s, nil
		},
		newCloudflare: func(opts cloudflare.Options) (cloudflare.API, error) {
			c, err := cloudflare.New(opts)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		newDocker: func(cfg config.DockerConfig, retry config.RetryConfig, log *slog.Logger) (dockerSource, error) {
			s, err := docker.New(cfg, retry, log)
			if err != nil {
				return nil, err
			}
			return s, nil
		},
		newReconciler: func(d reconcile.Deps) (reconcile.Engine, error) {
			r, err := reconcile.New(d)
			if err != nil {
				return nil, err
			}
			return r, nil
		},
		newHub: func(opts agent.HubOptions) (hub, error) {
			h, err := agent.NewHub(opts)
			if err != nil {
				return nil, err
			}
			return h, nil
		},
		newAPI: func(d api.Deps) (runner, error) {
			s, err := api.New(d)
			if err != nil {
				return nil, err
			}
			return s, nil
		},
		newAgent: func(opts agent.AgentOptions) (runner, error) {
			a, err := agent.NewAgent(opts)
			if err != nil {
				return nil, err
			}
			return a, nil
		},
		publicIP: func() model.PublicIPFunc {
			return publicip.New(nil, nil, nil, 0, nil).Lookup
		},
	}
}

// runAgent runs agent mode: local Docker source reported to a main instance.
func (a *app) runAgent(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	src, err := a.b.newDocker(cfg.Docker, cfg.Retry, log)
	if err != nil {
		return fmt.Errorf("docker source: %w", err)
	}
	defer closeLogged(src.Close, "docker source", log)

	ag, err := a.b.newAgent(agent.AgentOptions{
		Connect:  cfg.Connect,
		Retry:    cfg.Retry,
		Version:  version.Version,
		Source:   src,
		PublicIP: a.b.publicIP(),
		Log:      log,
	})
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	return ag.Run(ctx)
}

// runMain runs the main instance. Components are supervised in two groups so
// shutdown follows the required order: edge (API, agent hub) stops accepting
// first, then the reconciler, and only then the store closes.
func (a *app) runMain(ctx context.Context, cfg config.Config, log *slog.Logger) (err error) {
	st, err := a.b.openStore(ctx, cfg.DB.Path)
	if err != nil {
		return fmt.Errorf("open store %q: %w%s", cfg.DB.Path, err, storeHint(err))
	}
	defer func() { err = errors.Join(err, st.Close()) }()

	rec, hubInst, apiInst, err := a.buildMain(ctx, cfg, st, log)
	if err != nil {
		return err
	}
	return a.supervise(ctx, log, rec, hubInst, apiInst)
}

// buildMain constructs every main-mode component; nothing runs yet.
func (a *app) buildMain(ctx context.Context, cfg config.Config, st store.Store, log *slog.Logger) (reconcile.Engine, hub, runner, error) {
	instanceID, err := resolveInstanceID(ctx, cfg, st)
	if err != nil {
		return nil, nil, nil, err
	}
	log.Info("Storage ready", "path", cfg.DB.Path, "instance_id", instanceID)

	cf, err := a.b.newCloudflare(cloudflare.Options{
		Cloudflare:    cfg.Cloudflare,
		DefaultTunnel: cfg.DefaultTunnel,
		Retry:         cfg.Retry,
		Log:           log,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cloudflare: %w", err)
	}
	if !cfg.SkipCredentialValidation {
		if err := cf.Verify(ctx); err != nil {
			return nil, nil, nil, fmt.Errorf("cloudflare credential validation failed; to skip this startup check set %s to true: %w",
				config.KeyWithEnv("skip_credential_validation"), err)
		}
	}

	src, err := a.b.newDocker(cfg.Docker, cfg.Retry, log)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("docker source: %w", err)
	}
	live := &presence{}
	settings := settingsFromConfig(cfg, instanceID)
	settings.Connected = live.connected
	rec, err := a.b.newReconciler(reconcile.Deps{
		Settings:   settings,
		Cloudflare: cf,
		Store:      st,
		Local:      src,
		PublicIP:   a.b.publicIP(),
		Now:        a.now,
		Log:        log,
	})
	if err != nil {
		closeLogged(src.Close, "docker source", log)
		return nil, nil, nil, fmt.Errorf("reconciler: %w", err)
	}

	h, err := a.buildHub(cfg, st, rec, log)
	if err != nil {
		closeLogged(src.Close, "docker source", log)
		return nil, nil, nil, err
	}
	live.set(h)
	srv, err := a.buildAPI(cfg, st, rec, h, cf, log)
	if err != nil {
		closeLogged(src.Close, "docker source", log)
		return nil, nil, nil, err
	}
	return &closingEngine{Engine: rec, close: src.Close, log: log}, h, srv, nil
}

func resolveInstanceID(ctx context.Context, cfg config.Config, st store.Store) (string, error) {
	if cfg.InstanceID != "" {
		if err := st.SetInstanceID(ctx, cfg.InstanceID); err != nil {
			return "", fmt.Errorf("set instance_id: %w", err)
		}
		return cfg.InstanceID, nil
	}
	id, err := st.InstanceID(ctx)
	if err != nil {
		return "", fmt.Errorf("load instance_id: %w", err)
	}
	return id, nil
}

func settingsFromConfig(cfg config.Config, instanceID string) reconcile.Settings {
	return reconcile.Settings{
		InstanceID:  instanceID,
		LabelPrefix: cfg.LabelPrefix,
		Interval:    cfg.Sync.Interval,
		RemoveDelay: cfg.Sync.RemoveDelay,
		OrphanTTL:   cfg.Sync.OrphanTTL,
		AgentGrace:  cfg.Sync.AgentGrace,
		Agents:      reconcile.PoliciesFromConfig(cfg),
	}
}

// buildHub returns nil when the agent server is disabled.
func (a *app) buildHub(cfg config.Config, st store.Store, rec reconcile.Engine, log *slog.Logger) (hub, error) {
	if !cfg.Agent.Enabled {
		return nil, nil
	}
	h, err := a.b.newHub(agent.HubOptions{
		Server:   cfg.Agent,
		Retry:    cfg.Retry,
		Version:  version.Version,
		Store:    st,
		OnChange: rec.Trigger,
		Now:      a.now,
		Log:      log,
	})
	if err != nil {
		return nil, fmt.Errorf("agent hub: %w", err)
	}
	return h, nil
}

// buildAPI returns nil (not an error) when the API is disabled or has no
// token: an unauthenticated API is never started, but the reconciler keeps running.
func (a *app) buildAPI(cfg config.Config, st store.Store, rec reconcile.Engine, h hub, cf cloudflare.API, log *slog.Logger) (runner, error) {
	switch {
	case !cfg.API.Enabled:
		log.Info("API and dashboard disabled by " + config.KeyWithEnv("api.enabled") + "=false")
		return nil, nil
	case cfg.API.Token == "":
		log.Warn(apiTokenMissing())
		return nil, nil
	}
	deps := api.Deps{
		Config:      cfg.API,
		Store:       st,
		Sync:        rec,
		Cloudflare:  cf,
		Version:     version.Version,
		LabelPrefix: cfg.LabelPrefix,
		StartedAt:   a.now(),
		AgentGrace:  cfg.Sync.AgentGrace,
		Now:         a.now,
		Log:         log,
	}
	if h != nil {
		deps.Agents = h // avoid a typed-nil interface when the hub is disabled
	}
	srv, err := a.b.newAPI(deps)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	return srv, nil
}

// apiTokenMissing is logged when the API is enabled but has no token.
func apiTokenMissing() string {
	return fmt.Sprintf("API and dashboard disabled: api.token is empty; set %s to a secret of at least %d characters "+
		"(generate one with: openssl rand -hex 32). DNS, tunnel and Access sync keep running",
		config.KeyWithEnv("api.token"), config.MinAPITokenLen)
}

// closingEngine releases the Docker source once the reconciler has stopped.
type closingEngine struct {
	reconcile.Engine
	close func() error
	log   *slog.Logger
}

func (c *closingEngine) Run(ctx context.Context) error {
	defer closeLogged(c.close, "docker source", c.log)
	return c.Engine.Run(ctx)
}

func closeLogged(closeFn func() error, what string, log *slog.Logger) {
	if err := closeFn(); err != nil {
		log.Warn("close failed", "component", what, "error", err)
	}
}
