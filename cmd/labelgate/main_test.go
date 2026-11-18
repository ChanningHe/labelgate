package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/store"
)

// runMainAsync starts execute in the background and returns a stop function
// that cancels it and returns the result.
func runMainAsync(t *testing.T, h *harness, cfg config.Config, log *syncBuffer) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	logger, err := newLogger("debug", "text", log)
	if err != nil {
		t.Fatal(err)
	}
	go func() { done <- h.app.execute(ctx, cfg, logger) }()
	t.Cleanup(cancel)
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("execute did not return after cancel")
			return nil
		}
	}
}

func TestRunVersionAndUsage(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{"version", []string{"--version"}, exitOK, "labelgate ", ""},
		{"help", []string{"--help"}, exitOK, "Usage:", ""},
		{"help shorthand", []string{"-h"}, exitOK, "--config", ""},
		{"unknown flag", []string{"--nope"}, exitUsage, "", "unknown flag"},
		{"unknown command", []string{"frobnicate"}, exitUsage, "", "unknown command"},
		{"extra arguments", []string{"healthcheck", "extra"}, exitUsage, "", "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.app.load = func(string) (config.Config, error) { t.Fatal("config must not be loaded"); return config.Config{}, nil }
			var out, errOut bytes.Buffer
			if got := h.app.run(tt.args, &out, &errOut); got != tt.wantCode {
				t.Fatalf("exit = %d, want %d", got, tt.wantCode)
			}
			if !strings.Contains(out.String(), tt.wantOut) || !strings.Contains(errOut.String(), tt.wantErr) ||
				(tt.wantErr == "" && errOut.Len() > 0) {
				t.Fatalf("stdout=%q stderr=%q", out.String(), errOut.String())
			}
		})
	}
}

func TestRunBadConfigExitsNonZero(t *testing.T) {
	h := newHarness(t)
	var gotPath string
	h.app.load = func(p string) (config.Config, error) {
		gotPath = p
		return config.Config{}, errors.New("config: mode: invalid")
	}
	var out, errOut bytes.Buffer
	if got := h.app.run([]string{"-c", "/etc/lg.yaml"}, &out, &errOut); got != exitFatal {
		t.Fatalf("exit = %d", got)
	}
	if gotPath != "/etc/lg.yaml" || !strings.Contains(errOut.String(), "config: mode: invalid") {
		t.Fatalf("path=%q stderr=%q", gotPath, errOut.String())
	}
	// healthcheck with a broken config is unhealthy too
	if got := h.app.run([]string{"healthcheck"}, &out, &errOut); got != exitFatal {
		t.Fatalf("healthcheck exit = %d", got)
	}
}

func TestRunRejectsBadLogSettings(t *testing.T) {
	h := newHarness(t)
	cfg := mainConfig()
	cfg.LogLevel = "chatty"
	h.app.load = func(string) (config.Config, error) { return cfg, nil }
	var errOut bytes.Buffer
	if got := h.app.run(nil, &bytes.Buffer{}, &errOut); got != exitFatal || !strings.Contains(errOut.String(), "log_level") {
		t.Fatalf("exit=%d stderr=%q", got, errOut.String())
	}
}

func TestRunSignalsAndExitCodes(t *testing.T) {
	t.Run("SIGHUP is logged as unsupported, SIGTERM shuts down cleanly with exit 0", func(t *testing.T) {
		h := newHarness(t)
		cfg := mainConfig()
		h.app.load = func(string) (config.Config, error) { return cfg, nil }
		var errOut syncBuffer
		code := make(chan int, 1)
		go func() { code <- h.app.run(nil, &bytes.Buffer{}, &errOut) }()

		h.ev.waitFor(t, "reconciler started")
		h.ev.waitFor(t, "api started")
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
		waitForLog(t, &errOut, "reload is not supported")
		if strings.Contains(errOut.String(), "Shutdown requested") {
			t.Fatal("SIGHUP must not stop the process")
		}
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		if got := <-code; got != exitOK {
			t.Fatalf("exit = %d; log:\n%s", got, errOut.String())
		}
		if !strings.Contains(errOut.String(), "Labelgate stopped") {
			t.Fatalf("log:\n%s", errOut.String())
		}
	})

	t.Run("fatal component error exits 1", func(t *testing.T) {
		h := newHarness(t)
		h.api.runErr = errBoom
		cfg := mainConfig()
		h.app.load = func(string) (config.Config, error) { return cfg, nil }
		var errOut syncBuffer
		if got := h.app.run(nil, &bytes.Buffer{}, &errOut); got != exitFatal {
			t.Fatalf("exit = %d", got)
		}
		if !strings.Contains(errOut.String(), "labelgate: api: boom") {
			t.Fatalf("log:\n%s", errOut.String())
		}
	})
}

func TestServeSetsConfiguredLoggerAsDefaultBeforeStoreOpen(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := newHarness(t)
	cfg := mainConfig()
	cfg.LogFormat = "json"
	cfg.LogLevel = "warn"
	h.app.load = func(string) (config.Config, error) { return cfg, nil }
	open := h.app.b.openStore
	h.app.b.openStore = func(ctx context.Context, path string) (store.Store, error) {
		slog.Info("below-level-line")
		slog.Warn("migration-line")
		return open(ctx, path)
	}
	var errOut syncBuffer
	code := make(chan int, 1)
	go func() { code <- h.app.run(nil, &bytes.Buffer{}, &errOut) }()

	h.ev.waitFor(t, "reconciler started")
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	if got := <-code; got != exitOK {
		t.Fatalf("exit = %d; log:\n%s", got, errOut.String())
	}
	out := errOut.String()
	if !strings.Contains(out, `"msg":"migration-line"`) {
		t.Errorf("default logger must use log_format=json; log:\n%s", out)
	}
	if strings.Contains(out, "below-level-line") {
		t.Errorf("default logger must honour log_level=warn; log:\n%s", out)
	}
}

func waitForLog(t *testing.T, b *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(b.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in log:\n%s", want, b.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMainWiringPassesConfigToComponents(t *testing.T) {
	h := newHarness(t)
	cfg := mainConfig()
	cfg.LabelPrefix = "lg"
	cfg.InstanceID = "recov123"
	cfg.DefaultTunnel = "edge"
	cfg.Sync.Interval = 7 * time.Minute
	cfg.Sync.RemoveDelay = 11 * time.Minute
	cfg.Sync.OrphanTTL = 13 * time.Minute
	cfg.Sync.AgentGrace = 17 * time.Hour
	cfg.Cloudflare.APIToken = "cf-token"

	var log syncBuffer
	stop := runMainAsync(t, h, cfg, &log)
	h.ev.waitFor(t, "reconciler started")
	h.ev.waitFor(t, "hub started")
	h.ev.waitFor(t, "api started")
	if err := stop(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if h.store.setID != "recov123" {
		t.Errorf("SetInstanceID = %q", h.store.setID)
	}
	s := h.recDeps.Settings
	if s.InstanceID != "recov123" || s.LabelPrefix != "lg" || s.Interval != 7*time.Minute || s.RemoveDelay != 11*time.Minute ||
		s.OrphanTTL != 13*time.Minute || s.AgentGrace != 17*time.Hour || s.Agents == nil || s.Connected == nil {
		t.Errorf("settings = %+v", s)
	}
	if h.recDeps.Cloudflare != h.cf || h.recDeps.Store != h.store || h.recDeps.Local == nil || h.recDeps.Now == nil || h.recDeps.Log == nil {
		t.Errorf("reconciler deps = %+v", h.recDeps)
	}
	if h.cfOpts.Cloudflare.APIToken != "cf-token" || h.cfOpts.DefaultTunnel != "edge" || h.cfOpts.Retry != cfg.Retry {
		t.Errorf("cloudflare options = %+v", h.cfOpts)
	}
	if h.cf.verified.Load() != 1 {
		t.Errorf("Verify calls = %d", h.cf.verified.Load())
	}
	if h.apiDeps.Config.Token != "tok" || h.apiDeps.Sync == nil || h.apiDeps.Agents == nil || h.apiDeps.Cloudflare != h.cf ||
		h.apiDeps.LabelPrefix != "lg" || h.apiDeps.AgentGrace != 17*time.Hour || h.apiDeps.StartedAt.IsZero() {
		t.Errorf("api deps = %+v", h.apiDeps)
	}
	if h.hubOpts.Store != h.store || h.hubOpts.OnChange == nil || h.hubOpts.Server.Enabled != true {
		t.Errorf("hub options = %+v", h.hubOpts)
	}
	h.hubOpts.OnChange()
	if h.rec.triggered.Load() != 1 {
		t.Error("hub OnChange must trigger the reconciler")
	}
}

func TestMainUsesPersistedInstanceIDWhenNotOverridden(t *testing.T) {
	h := newHarness(t)
	cfg := mainConfig()
	var log syncBuffer
	stop := runMainAsync(t, h, cfg, &log)
	h.ev.waitFor(t, "reconciler started")
	_ = stop()
	if h.recDeps.Settings.InstanceID != "abcd1234" || h.store.setID != "" {
		t.Fatalf("instance id = %q, set = %q", h.recDeps.Settings.InstanceID, h.store.setID)
	}
}

func TestMainShutdownOrder(t *testing.T) {
	h := newHarness(t)
	var log syncBuffer
	stop := runMainAsync(t, h, mainConfig(), &log)
	h.ev.waitFor(t, "reconciler started")
	h.ev.waitFor(t, "hub started")
	h.ev.waitFor(t, "api started")
	if err := stop(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	order := []string{"api stopped", "hub stopped", "reconciler stopped", "docker closed", "store closed"}
	last := -1
	for i, e := range order {
		idx := h.ev.index(e)
		if idx < 0 {
			t.Fatalf("missing event %q in %v", e, h.ev.snapshot())
		}
		// api and hub stop concurrently; both must precede everything after them.
		if i >= 2 && idx < last {
			t.Fatalf("%q came before an earlier stage in %v", e, h.ev.snapshot())
		}
		if i == 2 && (idx < h.ev.index("api stopped") || idx < h.ev.index("hub stopped")) {
			t.Fatalf("reconciler stopped before the edge: %v", h.ev.snapshot())
		}
		last = max(last, idx)
	}
}

func TestMainWithoutTokenKeepsReconcilerRunning(t *testing.T) {
	h := newHarness(t)
	cfg := mainConfig()
	cfg.API.Token = ""
	var log syncBuffer
	stop := runMainAsync(t, h, cfg, &log)
	h.ev.waitFor(t, "reconciler started")
	if err := stop(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if h.builds["api"] != 0 {
		t.Fatal("API must not be constructed without a token")
	}
	for _, want := range []string{"level=WARN", "API and dashboard disabled: api.token is empty",
		"LABELGATE_API_TOKEN", "openssl rand -hex 32", "sync keep running"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("API-disabled warning lacks %q:\n%s", want, log.String())
		}
	}
}

func TestMainWithAPIDisabledAndAgentServerDisabled(t *testing.T) {
	h := newHarness(t)
	cfg := mainConfig()
	cfg.API.Enabled = false
	cfg.Agent.Enabled = false
	var log syncBuffer
	stop := runMainAsync(t, h, cfg, &log)
	h.ev.waitFor(t, "reconciler started")
	if err := stop(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if h.builds["api"] != 0 || h.builds["hub"] != 0 {
		t.Fatalf("builds = %v", h.builds)
	}
}

func TestMainAPIWithoutHubHasNoAgentPresence(t *testing.T) {
	h := newHarness(t)
	cfg := mainConfig()
	cfg.Agent.Enabled = false
	var log syncBuffer
	stop := runMainAsync(t, h, cfg, &log)
	h.ev.waitFor(t, "api started")
	_ = stop()
	if h.apiDeps.Agents != nil {
		t.Fatal("api.Deps.Agents must be a true nil interface when the hub is disabled")
	}
}

func TestMainSkipCredentialValidation(t *testing.T) {
	h := newHarness(t)
	cfg := mainConfig()
	cfg.SkipCredentialValidation = true
	var log syncBuffer
	stop := runMainAsync(t, h, cfg, &log)
	h.ev.waitFor(t, "reconciler started")
	_ = stop()
	if h.cf.verified.Load() != 0 {
		t.Fatal("Verify must be skipped")
	}
}

func TestMainStartupFailures(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*harness)
		wantErr    string
		wantStore  bool // store was opened and must be closed
		wantNoDock bool
	}{
		{"store open", func(h *harness) { h.fail["store"] = errBoom }, "open store", false, true},
		{"instance id set", func(h *harness) { h.store.setErr = errBoom }, "set instance_id", true, true},
		{"instance id load", func(h *harness) { h.store.idErr = errBoom }, "load instance_id", true, true},
		{"cloudflare construct", func(h *harness) { h.fail["cloudflare"] = errBoom }, "cloudflare:", true, true},
		{"credential validation", func(h *harness) { h.cf.verifyErr = errBoom }, "credential validation failed", true, true},
		{"docker", func(h *harness) { h.fail["docker"] = errBoom }, "docker source", true, false},
		{"reconciler", func(h *harness) { h.fail["reconciler"] = errBoom }, "reconciler:", true, false},
		{"hub", func(h *harness) { h.fail["hub"] = errBoom }, "agent hub:", true, false},
		{"api", func(h *harness) { h.fail["api"] = errBoom }, "api:", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			cfg := mainConfig()
			cfg.InstanceID = "recov123"
			if tt.name == "instance id load" {
				cfg.InstanceID = ""
			}
			tt.setup(h)
			err := h.app.execute(context.Background(), cfg, quietLogger())
			if err == nil || !errors.Is(err, errBoom) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q wrapping boom", err, tt.wantErr)
			}
			if tt.wantStore && !h.ev.has("store closed") {
				t.Error("store must be closed after a startup failure")
			}
			if !tt.wantNoDock && h.builds["docker"] == 1 && tt.name != "docker" && !h.ev.has("docker closed") {
				t.Error("docker source must be closed when later construction fails")
			}
			if h.ev.has("reconciler started") {
				t.Error("nothing may run after a startup failure")
			}
		})
	}
}

func TestMainStoreCloseErrorIsReported(t *testing.T) {
	h := newHarness(t)
	h.store.closeErr = errBoom
	var log syncBuffer
	stop := runMainAsync(t, h, mainConfig(), &log)
	h.ev.waitFor(t, "reconciler started")
	if err := stop(); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
}

func TestMainComponentFailureShutsEverythingDown(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*harness)
		want  string
	}{
		{"api error", func(h *harness) { h.api.runErr = errBoom }, "api: boom"},
		{"hub error", func(h *harness) { h.hub.runErr = errBoom }, "agent hub: boom"},
		{"reconciler error", func(h *harness) { h.rec.runErr = errBoom }, "reconciler: boom"},
		{"api exits unexpectedly with nil", func(h *harness) { h.api.retNil = true }, "api: exited unexpectedly"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			tt.setup(h)
			var log syncBuffer
			logger, _ := newLogger("info", "text", &log)
			done := make(chan error, 1)
			go func() { done <- h.app.execute(context.Background(), mainConfig(), logger) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("err = %v, want %q", err, tt.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("process did not shut down after a component failed")
			}
			if !h.ev.has("store closed") {
				t.Error("store must be closed")
			}
			// Healthy components were stopped in order: edge before reconciler before store.
			if h.ev.has("reconciler stopped") && h.ev.has("api stopped") && h.ev.index("api stopped") > h.ev.index("reconciler stopped") {
				t.Errorf("order violated: %v", h.ev.snapshot())
			}
		})
	}
}

func TestMainErrorsAreNotCanceled(t *testing.T) {
	h := newHarness(t)
	var log syncBuffer
	stop := runMainAsync(t, h, mainConfig(), &log)
	h.ev.waitFor(t, "reconciler started")
	err := stop()
	if err != nil {
		t.Fatalf("clean shutdown must not produce an error, got %v", err)
	}
}

func TestAgentMode(t *testing.T) {
	cfg := config.Defaults()
	cfg.Mode = config.ModeAgent
	cfg.Connect.Token = "t"
	cfg.Connect.AgentID = "nas"

	t.Run("runs the agent and closes the docker source", func(t *testing.T) {
		h := newHarness(t)
		var log syncBuffer
		stop := runMainAsync(t, h, cfg, &log)
		h.ev.waitFor(t, "agent started")
		if err := stop(); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		if h.agentOpt.Connect.AgentID != "nas" || h.agentOpt.Source == nil || h.agentOpt.Version == "" {
			t.Errorf("agent options = %+v", h.agentOpt)
		}
		if !h.ev.has("docker closed") {
			t.Error("docker source must be closed")
		}
		if h.builds["store"] != 0 || h.builds["cloudflare"] != 0 {
			t.Errorf("agent mode must not open a store or Cloudflare client: %v", h.builds)
		}
	})
	t.Run("docker failure", func(t *testing.T) {
		h := newHarness(t)
		h.fail["docker"] = errBoom
		if err := h.app.execute(context.Background(), cfg, quietLogger()); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("agent construction failure", func(t *testing.T) {
		h := newHarness(t)
		h.fail["agent"] = errBoom
		err := h.app.execute(context.Background(), cfg, quietLogger())
		if !errors.Is(err, errBoom) || !h.ev.has("docker closed") {
			t.Fatalf("err = %v, events %v", err, h.ev.snapshot())
		}
	})
	t.Run("agent run failure propagates", func(t *testing.T) {
		h := newHarness(t)
		h.agent.runErr = errBoom
		if err := h.app.execute(context.Background(), cfg, quietLogger()); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestExecuteUnknownMode(t *testing.T) {
	h := newHarness(t)
	cfg := mainConfig()
	cfg.Mode = "turbo"
	if err := h.app.execute(context.Background(), cfg, quietLogger()); err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewLogger(t *testing.T) {
	tests := []struct {
		level, format string
		wantErr       string
		check         func(out string) bool
	}{
		{"info", "text", "", func(o string) bool { return strings.Contains(o, "level=INFO") && !strings.Contains(o, "level=DEBUG") }},
		{"debug", "text", "", func(o string) bool { return strings.Contains(o, "level=DEBUG") }},
		{"warn", "json", "", func(o string) bool {
			return strings.Contains(o, `"level":"WARN"`) && !strings.Contains(o, `"level":"INFO"`)
		}},
		{"error", "text", "", func(o string) bool { return !strings.Contains(o, "level=WARN") && strings.Contains(o, "level=ERROR") }},
		{"loud", "text", "invalid log_level", nil},
		{"", "text", "invalid log_level", nil},
		{"info", "xml", "invalid log_format", nil},
	}
	for _, tt := range tests {
		t.Run(tt.level+"/"+tt.format, func(t *testing.T) {
			var buf bytes.Buffer
			log, err := newLogger(tt.level, tt.format, &buf)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			log.Debug("d")
			log.Info("i")
			log.Warn("w")
			log.Error("e")
			if !tt.check(buf.String()) {
				t.Fatalf("output:\n%s", buf.String())
			}
		})
	}
}

func TestNewAppUsesRealBuilders(t *testing.T) {
	a := newApp()
	if a.load == nil || a.now == nil || a.b.openStore == nil || a.b.newCloudflare == nil || a.b.newDocker == nil ||
		a.b.newReconciler == nil || a.b.newHub == nil || a.b.newAPI == nil || a.b.newAgent == nil || a.b.publicIP == nil {
		t.Fatal("newApp must populate every builder")
	}
	if a.b.publicIP() == nil {
		t.Fatal("publicIP builder must return a resolver")
	}
}
