package config

import (
	"os"
	"strings"

	"github.com/spf13/viper"
)

const (
	// EnvPrefix is the environment variable prefix
	EnvPrefix = "LABELGATE"

	// DefaultConfigName is the default config file name
	DefaultConfigName = "labelgate"
)

// Load loads configuration from environment variables and config file.
// Config file resolution priority: CLI flag > ENV > default search paths.
// Value priority: Environment variables > Config file > Defaults.
func Load(configPath string) (*Config, error) {
	cfg := DefaultConfig()
	v := viper.New()

	// Enable environment variable binding
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Set defaults in viper
	setDefaults(v, cfg)

	// Resolve config file path with priority: CLI flag > ENV > default search paths
	if err := resolveConfigFile(v, configPath); err != nil {
		return nil, err
	}

	// Unmarshal configuration
	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// Apply environment variable overrides for nested configs
	// that viper cannot auto-resolve (e.g. cloudflare flat fields + map children).
	applyEnvOverrides(cfg)

	// Validate configuration
	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// setDefaults sets default values in viper.
func setDefaults(v *viper.Viper, cfg *Config) {
	// Core
	v.SetDefault("label_prefix", cfg.LabelPrefix)
	v.SetDefault("log_level", cfg.LogLevel)
	v.SetDefault("log_format", cfg.LogFormat)
	v.SetDefault("mode", cfg.Mode)
	v.SetDefault("default_tunnel", cfg.DefaultTunnel)

	// Docker
	v.SetDefault("docker.endpoint", cfg.Docker.Endpoint)
	v.SetDefault("docker.poll_interval", cfg.Docker.PollInterval)
	v.SetDefault("docker.filter_label", cfg.Docker.FilterLabel)
	v.SetDefault("docker.ssh.key", cfg.Docker.SSH.Key)
	v.SetDefault("docker.ssh.key_passphrase", cfg.Docker.SSH.KeyPassphrase)
	v.SetDefault("docker.ssh.known_hosts", cfg.Docker.SSH.KnownHosts)
	v.SetDefault("docker.tls.ca", cfg.Docker.TLS.CA)
	v.SetDefault("docker.tls.cert", cfg.Docker.TLS.Cert)
	v.SetDefault("docker.tls.key", cfg.Docker.TLS.Key)

	// Cloudflare (flat defaults for the default credential/tunnel)
	v.SetDefault("cloudflare.api_token", cfg.Cloudflare.APIToken)
	v.SetDefault("cloudflare.account_id", cfg.Cloudflare.AccountID)
	v.SetDefault("cloudflare.tunnel_id", cfg.Cloudflare.TunnelID)

	// Sync
	v.SetDefault("sync.interval", cfg.Sync.Interval)
	v.SetDefault("sync.remove_delay", cfg.Sync.RemoveDelay)
	v.SetDefault("sync.orphan_ttl", cfg.Sync.OrphanTTL)

	// Database
	v.SetDefault("db.path", cfg.Db.Path)
	v.SetDefault("db.retention", cfg.Db.Retention)
	v.SetDefault("db.vacuum_interval", cfg.Db.VacuumInterval)

	// API server
	v.SetDefault("api.enabled", cfg.Api.Enabled)
	v.SetDefault("api.address", cfg.Api.Address)
	v.SetDefault("api.base_path", cfg.Api.BasePath)
	v.SetDefault("api.token", cfg.Api.Token)

	// Agent server (main instance)
	v.SetDefault("agent.enabled", cfg.Agent.Enabled)
	v.SetDefault("agent.listen", cfg.Agent.Listen)
	v.SetDefault("agent.accept_token", cfg.Agent.AcceptToken)
	v.SetDefault("agent.tls.ca", cfg.Agent.TLS.CA)
	v.SetDefault("agent.tls.cert", cfg.Agent.TLS.Cert)
	v.SetDefault("agent.tls.key", cfg.Agent.TLS.Key)

	// Agent connection (agent mode)
	v.SetDefault("connect.mode", cfg.Connect.Mode)
	v.SetDefault("connect.endpoint", cfg.Connect.Endpoint)
	v.SetDefault("connect.listen", cfg.Connect.Listen)
	v.SetDefault("connect.token", cfg.Connect.Token)
	v.SetDefault("connect.agent_id", cfg.Connect.AgentID)
	v.SetDefault("connect.heartbeat_interval", cfg.Connect.HeartbeatInterval)
	v.SetDefault("connect.tls.ca", cfg.Connect.TLS.CA)
	v.SetDefault("connect.tls.cert", cfg.Connect.TLS.Cert)
	v.SetDefault("connect.tls.key", cfg.Connect.TLS.Key)

	// Retry
	v.SetDefault("retry.attempts", cfg.Retry.Attempts)
	v.SetDefault("retry.delay", cfg.Retry.Delay)
	v.SetDefault("retry.max_delay", cfg.Retry.MaxDelay)
	v.SetDefault("retry.backoff", cfg.Retry.Backoff)

	// Skip credential validation
	v.SetDefault("skip_credential_validation", cfg.SkipCredentialValidation)
}

// applyEnvOverrides applies environment variable overrides for configs
// that viper doesn't automatically resolve well (mainly cloudflare flat fields
// coexisting with map-type children).
func applyEnvOverrides(cfg *Config) {
	// Cloudflare default credential / tunnel from ENV.
	// These are needed because viper has trouble with flat fields alongside map children.
	if token := os.Getenv(EnvPrefix + "_CLOUDFLARE_API_TOKEN"); token != "" {
		cfg.Cloudflare.APIToken = token
	}
	if accountID := os.Getenv(EnvPrefix + "_CLOUDFLARE_ACCOUNT_ID"); accountID != "" {
		cfg.Cloudflare.AccountID = accountID
	}
	if tunnelID := os.Getenv(EnvPrefix + "_CLOUDFLARE_TUNNEL_ID"); tunnelID != "" {
		cfg.Cloudflare.TunnelID = tunnelID
	}
}

// validate validates the configuration.
func validate(cfg *Config) error {
	// Validate mode
	if cfg.Mode != ModeMain && cfg.Mode != ModeAgent {
		cfg.Mode = ModeMain
	}

	// Validate log level
	validLevels := map[string]bool{
		"debug": true, "info": true, "warn": true, "error": true,
	}
	if !validLevels[cfg.LogLevel] {
		cfg.LogLevel = "info"
	}

	// Validate log format
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		cfg.LogFormat = "text"
	}

	// Agent mode requires connection config
	if cfg.Mode == ModeAgent {
		if cfg.Connect.Mode == ConnectOutbound && cfg.Connect.Endpoint == "" {
			return &ValidationError{Field: "connect.endpoint", Message: "endpoint is required in agent outbound mode"}
		}
	}

	// Main mode should have at least one Cloudflare credential
	if cfg.Mode == ModeMain {
		if cfg.Cloudflare.APIToken == "" && len(cfg.Cloudflare.Credentials) == 0 {
			if !cfg.SkipCredentialValidation {
				return &ValidationError{
					Field:   "cloudflare.api_token",
					Message: "at least one credential is required: set cloudflare.api_token or configure cloudflare.credentials",
				}
			}
		}
	}

	return nil
}

// ValidationError represents a configuration validation error.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return "config validation error: " + e.Field + ": " + e.Message
}

// resolveConfigFile resolves and loads the config file into viper.
// Priority: explicit path (CLI flag) > LABELGATE_CONFIG env > default search paths.
func resolveConfigFile(v *viper.Viper, configPath string) error {
	// 1. Explicit path from CLI flag
	if configPath != "" {
		v.SetConfigFile(configPath)
		return v.ReadInConfig()
	}

	// 2. Path from environment variable
	if envPath := os.Getenv(EnvPrefix + "_CONFIG"); envPath != "" {
		v.SetConfigFile(envPath)
		return v.ReadInConfig()
	}

	// 3. Default search paths: ./labelgate.yaml, /etc/labelgate/labelgate.yaml
	// Note: Do NOT call v.SetConfigType() here. When configType is set,
	// viper also matches extensionless files (e.g. the binary itself at
	// /app/labelgate in Docker). Without it, viper only matches files
	// with known extensions (.yaml, .yml, .json, etc.) which is what we want.
	v.SetConfigName(DefaultConfigName)
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/labelgate")

	if err := v.ReadInConfig(); err != nil {
		// Not finding a config file in default paths is fine;
		// the application can run purely from env vars and defaults.
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return nil
		}
		return err
	}
	return nil
}

// Reload reloads configuration from file.
// Uses the same resolution logic as Load with no explicit CLI path,
// so it will re-read from ENV or default search paths.
func Reload() (*Config, error) {
	return Load("")
}
