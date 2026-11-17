// Command labelgate manages Cloudflare DNS, Tunnel ingress and Access from
// Docker container labels.
//
//	labelgate [--config PATH] [--version]      run (mode from config)
//	labelgate [--config PATH] healthcheck      container HEALTHCHECK
//
// Exit codes: 0 clean shutdown (SIGINT/SIGTERM) or healthy, 1 fatal error /
// unhealthy / bad configuration, 2 usage error.
// Shutdown order: API + agent hub stop accepting -> reconciler stops -> store closes.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	flag "github.com/spf13/pflag"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/version"
)

// Exit codes.
const (
	exitOK    = 0
	exitFatal = 1
	exitUsage = 2
)

const healthcheckCommand = "healthcheck"

func main() {
	os.Exit(newApp().run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses args, loads config, wires components and blocks until shutdown.
// It returns the process exit code.
func (a *app) run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("labelgate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.StringP("config", "c", "", "path to configuration file")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "labelgate: %v\n", err)
		return exitUsage
	}
	if *showVersion {
		fmt.Fprintf(stdout, "labelgate %s\n", version.Version)
		return exitOK
	}
	command := fs.Arg(0)
	if command != "" && command != healthcheckCommand || fs.NArg() > 1 {
		fmt.Fprintf(stderr, "labelgate: unknown command %q (want %q or no command)\n", fs.Arg(0), healthcheckCommand)
		return exitUsage
	}

	cfg, err := a.load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "labelgate: %v\n", err)
		return exitFatal
	}
	if command == healthcheckCommand {
		return a.healthcheck(cfg, stderr)
	}
	return a.serve(cfg, stderr)
}

// serve runs the configured mode until SIGINT/SIGTERM or a fatal error.
func (a *app) serve(cfg config.Config, stderr io.Writer) int {
	log, err := newLogger(cfg.LogLevel, cfg.LogFormat, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "labelgate: %v\n", err)
		return exitFatal
	}
	log.Info("Starting labelgate", "version", version.Version, "mode", string(cfg.Mode))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go logReloadUnsupported(ctx, hup, log)

	err = a.execute(ctx, cfg, log)
	if ctx.Err() != nil {
		stop() // a second signal now terminates immediately
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error("labelgate failed", "error", err)
		return exitFatal
	}
	log.Info("Labelgate stopped")
	return exitOK
}

// logReloadUnsupported answers SIGHUP honestly: configuration is read once.
func logReloadUnsupported(ctx context.Context, hup <-chan os.Signal, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			log.Warn("SIGHUP received: configuration reload is not supported; restart labelgate to apply changes")
		}
	}
}

// execute runs main or agent mode.
func (a *app) execute(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	switch cfg.Mode {
	case config.ModeMain:
		return a.runMain(ctx, cfg, log)
	case config.ModeAgent:
		return a.runAgent(ctx, cfg, log)
	default:
		return fmt.Errorf("unknown mode %q", cfg.Mode)
	}
}
