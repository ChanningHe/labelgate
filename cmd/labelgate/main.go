// Package main provides the entry point for labelgate.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	flag "github.com/spf13/pflag"

	"github.com/channinghe/labelgate/internal/agent"
	"github.com/channinghe/labelgate/internal/api"
	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/config"
	accessop "github.com/channinghe/labelgate/internal/operator/access"
	dnsop "github.com/channinghe/labelgate/internal/operator/dns"
	tunnelop "github.com/channinghe/labelgate/internal/operator/tunnel"
	"github.com/channinghe/labelgate/internal/provider/docker"
	"github.com/channinghe/labelgate/internal/reconciler"
	"github.com/channinghe/labelgate/internal/storage"
	"github.com/channinghe/labelgate/internal/version"
)

func main() {
	// Parse CLI flags
	configPath := flag.StringP("config", "c", "", "Path to configuration file")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("labelgate %s\n", version.Version)
		os.Exit(0)
	}

	// Setup logger
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	// Configure log level
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	// Configure log format
	if cfg.LogFormat == "text" {
		log.Logger = log.Output(zerolog.ConsoleWriter{
			Out:        os.Stderr,
			TimeFormat: "3:04:05PM",
		})
	}

	log.Info().
		Str("version", version.Version).
		Str("mode", string(cfg.Mode)).
		Msg("Starting labelgate")

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGHUP:
				log.Info().Msg("Received SIGHUP, reloading configuration...")
				// TODO: Implement hot reload
			case syscall.SIGINT, syscall.SIGTERM:
				log.Info().Msg("Received shutdown signal, gracefully shutting down...")
				cancel()
				return
			}
		}
	}()

	// Run based on mode
	var runErr error
	switch cfg.Mode {
	case config.ModeMain:
		runErr = runMain(ctx, cfg)
	case config.ModeAgent:
		runErr = runAgent(ctx, cfg)
	default:
		log.Fatal().Str("mode", string(cfg.Mode)).Msg("Unknown mode")
	}

	if runErr != nil && runErr != context.Canceled {
		log.Error().Err(runErr).Msg("Runtime error")
	}

	log.Info().Msg("Labelgate stopped")
}

// runMain runs labelgate in main instance mode.
func runMain(ctx context.Context, cfg *config.Config) error {
	// Initialize storage
	store, err := storage.NewSQLiteStorage(cfg.Db.Path)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.Initialize(ctx); err != nil {
		return err
	}
	log.Info().Str("path", cfg.Db.Path).Msg("Storage initialized")

	// Initialize credential manager
	credManager, err := cloudflare.NewCredentialManager(cfg)
	if err != nil {
		return err
	}

	// Validate credentials if not skipped
	if !cfg.SkipCredentialValidation {
		if err := credManager.Validate(ctx); err != nil {
			log.Warn().Err(err).Msg("Credential validation failed")
		}
	}

	// Initialize Docker provider
	dockerProvider := docker.NewDockerProvider(&cfg.Docker)
	if err := dockerProvider.Connect(ctx); err != nil {
		return err
	}
	defer dockerProvider.Close()
	log.Info().Str("endpoint", cfg.Docker.Endpoint).Msg("Docker connected")

	// Initialize operators
	dnsOperator := dnsop.NewDNSOperator(credManager, store)
	tunnelOperator := tunnelop.NewTunnelOperator(credManager, store)
	accessOperator := accessop.NewAccessOperator(credManager, store)

	// Probe Access API permissions at startup (non-blocking)
	if err := accessOperator.CheckPermissions(ctx); err != nil {
		log.Warn().Err(err).
			Msg("Access API permission check failed — Zero Trust Access features will not work. " +
				"Ensure the API token has 'Account > Access: Apps and Policies > Edit' permission. " +
				"DNS and Tunnel features will continue to operate normally.")
	} else {
		log.Info().Msg("Access API permission check passed")
	}

	// Initialize reconciler
	rec := reconciler.NewReconciler(&reconciler.Config{
		Provider:     dockerProvider,
		Storage:      store,
		LabelPrefix:  cfg.LabelPrefix,
		DNSOperator:  dnsOperator,
		TunnelOp:     tunnelOperator,
		AccessOp:     accessOperator,
		PollInterval: cfg.Docker.PollInterval,
		OrphanTTL:    cfg.Sync.OrphanTTL,
		RemoveDelay:  cfg.Sync.RemoveDelay,
	})

	// Start agent server if enabled
	var agentServer *agent.Server
	if cfg.Agent.Enabled {
		agentConfigs := buildAgentConfigs(cfg)
		agentServer = agent.NewServer(&cfg.Agent, agentConfigs, rec, store, cfg.LabelPrefix)
		go func() {
			if err := agentServer.Start(ctx); err != nil && err != context.Canceled {
				log.Error().Err(err).Msg("Agent server error")
			}
		}()

		// Start inbound connections to agents that have connect_to configured
		agentServer.ConnectToInboundAgents(ctx)
	}

	// Start API server if enabled
	if cfg.Api.Enabled {
		apiServer := api.NewServer(&api.Config{
			Address:     cfg.Api.Address,
			BasePath:    cfg.Api.BasePath,
			Token:       cfg.Api.Token,
			Storage:     store,
			Reconciler:  rec,
			AgentServer: agentServer,
			CredManager: credManager,
			Version:     version.Version,
		})
		go func() {
			if err := apiServer.Start(ctx); err != nil && err != context.Canceled {
				log.Error().Err(err).Msg("API server error")
			}
		}()
	}

	// Run reconciler
	return rec.Run(ctx)
}

// buildAgentConfigs builds agent config entries from configuration.
func buildAgentConfigs(cfg *config.Config) map[string]*agent.AgentConfigEntry {
	result := make(map[string]*agent.AgentConfigEntry)

	// Load named agents from config
	for id, entry := range cfg.Agent.Agents {
		result[id] = &agent.AgentConfigEntry{
			Token:         entry.Token,
			DefaultTunnel: entry.DefaultTunnel,
			ConnectTo:     entry.ConnectTo,
		}
	}

	count := len(result)
	if cfg.Agent.AcceptToken != "" {
		log.Info().Msg("Agent accept token configured (dynamic agent registration enabled)")
	}
	if count > 0 {
		log.Info().Int("count", count).Msg("Pre-configured agents loaded")
	}

	return result
}

// runAgent runs labelgate in agent mode.
func runAgent(ctx context.Context, cfg *config.Config) error {
	// Initialize Docker provider
	dockerProvider := docker.NewDockerProvider(&cfg.Docker)

	switch cfg.Connect.Mode {
	case config.ConnectInbound:
		// Inbound mode: agent starts WebSocket server, waits for Main to connect
		log.Info().Msg("Running agent in inbound mode")
		listener := agent.NewInboundListener(cfg, dockerProvider)
		return listener.Run(ctx)

	default:
		// Outbound mode (default): agent connects to Main's WebSocket server
		log.Info().Msg("Running agent in outbound mode")
		client := agent.NewClient(cfg, dockerProvider)

		retryDelay := cfg.Retry.Delay
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			err := client.Run(ctx)
			if err == context.Canceled {
				return err
			}

			log.Error().Err(err).Dur("delay", retryDelay).Msg("Agent run failed, retrying...")
			time.Sleep(retryDelay)

			// Apply exponential backoff for outer restart loop
			nextDelay := time.Duration(float64(retryDelay) * cfg.Retry.Backoff)
			if cfg.Retry.MaxDelay > 0 && nextDelay > cfg.Retry.MaxDelay {
				nextDelay = cfg.Retry.MaxDelay
			}
			retryDelay = nextDelay
		}
	}
}
