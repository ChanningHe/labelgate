// Package config provides configuration management for labelgate.
package config

import "time"

// RunMode represents the running mode of labelgate.
type RunMode string

const (
	// ModeMain is the main instance mode that manages Cloudflare resources.
	ModeMain RunMode = "main"
	// ModeAgent is the agent mode that reports to a main instance.
	ModeAgent RunMode = "agent"
)

// ConnectMode represents how agent connects to main instance.
type ConnectMode string

const (
	// ConnectOutbound means agent connects to main instance.
	ConnectOutbound ConnectMode = "outbound"
	// ConnectInbound means main instance connects to agent.
	ConnectInbound ConnectMode = "inbound"
)

// Config holds all configuration for labelgate.
type Config struct {
	// LabelPrefix is the prefix for container labels (default: "labelgate")
	LabelPrefix string `mapstructure:"label_prefix"`

	// LogLevel is the logging level (debug, info, warn, error)
	LogLevel string `mapstructure:"log_level"`

	// LogFormat is the logging format (json, text)
	LogFormat string `mapstructure:"log_format"`

	// Mode is the running mode (main, agent)
	Mode RunMode `mapstructure:"mode"`

	// DefaultTunnel is the default tunnel name to use
	DefaultTunnel string `mapstructure:"default_tunnel"`

	// Docker configuration
	Docker DockerConfig `mapstructure:"docker"`

	// Cloudflare configuration
	Cloudflare CloudflareConfig `mapstructure:"cloudflare"`

	// Sync configuration (reconciliation + lifecycle)
	Sync SyncConfig `mapstructure:"sync"`

	// Db configuration (SQLite database)
	Db DbConfig `mapstructure:"db"`

	// Api configuration (HTTP API server)
	Api ApiConfig `mapstructure:"api"`

	// Agent configuration (for main instance)
	Agent AgentServerConfig `mapstructure:"agent"`

	// Connect configuration (for agent mode)
	Connect ConnectConfig `mapstructure:"connect"`

	// Retry configuration (general retry policy for API calls and reconnection)
	Retry RetryConfig `mapstructure:"retry"`

	// SkipCredentialValidation skips credential validation on startup
	SkipCredentialValidation bool `mapstructure:"skip_credential_validation"`
}

// DockerConfig holds Docker provider configuration.
type DockerConfig struct {
	// Endpoint is the Docker endpoint (unix://, tcp://, ssh://)
	Endpoint string `mapstructure:"endpoint"`

	// PollInterval is the interval for polling containers
	PollInterval time.Duration `mapstructure:"poll_interval"`

	// FilterLabel is the label to filter containers (optional)
	FilterLabel string `mapstructure:"filter_label"`

	// SSH configuration for ssh:// endpoint
	SSH SSHConfig `mapstructure:"ssh"`

	// TLS configuration for tcp:// endpoint with TLS
	TLS TLSConfig `mapstructure:"tls"`
}

// SSHConfig holds SSH connection configuration.
type SSHConfig struct {
	// Key is the path to SSH private key
	Key string `mapstructure:"key"`

	// KeyPassphrase is the passphrase for the SSH key
	KeyPassphrase string `mapstructure:"key_passphrase"`

	// KnownHosts is the path to known_hosts file.
	// TODO: not yet implemented — SSH currently uses InsecureIgnoreHostKey.
	KnownHosts string `mapstructure:"known_hosts"`
}

// TLSConfig holds TLS configuration.
type TLSConfig struct {
	// CA is the path to CA certificate.
	// TODO: not yet implemented — TLS does not load CA for mTLS verification.
	CA string `mapstructure:"ca"`

	// Cert is the path to client/server certificate
	Cert string `mapstructure:"cert"`

	// Key is the path to client/server key
	Key string `mapstructure:"key"`
}

// CloudflareConfig holds Cloudflare API configuration.
// Default credential and tunnel are flattened at root level for 1:1 ENV mapping.
type CloudflareConfig struct {
	// APIToken is the default Cloudflare API token
	APIToken string `mapstructure:"api_token"`

	// AccountID is the default Cloudflare account ID
	AccountID string `mapstructure:"account_id"`

	// TunnelID is the default Cloudflare Tunnel ID
	TunnelID string `mapstructure:"tunnel_id"`

	// Credentials is additional named credentials (config file only)
	Credentials map[string]CredentialConfig `mapstructure:"credentials"`

	// Tunnels is additional named tunnels (config file only)
	Tunnels map[string]TunnelConfig `mapstructure:"tunnels"`
}

// CredentialConfig holds a single Cloudflare credential.
type CredentialConfig struct {
	// APIToken is the Cloudflare API token
	APIToken string `mapstructure:"api_token"`

	// Zones is the list of zones this credential can manage
	Zones []string `mapstructure:"zones"`
}

// TunnelConfig holds a single named Cloudflare Tunnel configuration.
type TunnelConfig struct {
	// AccountID is the Cloudflare account ID
	AccountID string `mapstructure:"account_id"`

	// TunnelID is the Cloudflare Tunnel ID
	TunnelID string `mapstructure:"tunnel_id"`

	// Credential is the name of the credential to use for API calls
	Credential string `mapstructure:"credential"`
}

// SyncConfig holds sync and resource lifecycle configuration.
// Merges the old cleanup + reconcile configs.
type SyncConfig struct {
	// Interval is the interval for periodic reconciliation with Cloudflare
	Interval time.Duration `mapstructure:"interval"`

	// RemoveDelay is the delay before removing resources after container stops
	RemoveDelay time.Duration `mapstructure:"remove_delay"`

	// OrphanTTL is the TTL for orphaned resources (0 = never auto cleanup)
	OrphanTTL time.Duration `mapstructure:"orphan_ttl"`
}

// DbConfig holds database configuration.
type DbConfig struct {
	// Path is the path to SQLite database
	Path string `mapstructure:"path"`

	// Retention is how long to keep deleted records for audit.
	// TODO: not yet implemented — CleanupDeletedResources() exists but is not scheduled.
	Retention time.Duration `mapstructure:"retention"`

	// VacuumInterval is the interval for database vacuum.
	// TODO: not yet implemented — Vacuum() exists but is not scheduled.
	VacuumInterval time.Duration `mapstructure:"vacuum_interval"`
}

// ApiConfig holds HTTP API server configuration.
type ApiConfig struct {
	// Enabled controls whether the API server is enabled
	Enabled bool `mapstructure:"enabled"`

	// Address is the listen address
	Address string `mapstructure:"address"`

	// BasePath is the API base path
	BasePath string `mapstructure:"base_path"`

	// Token is the optional Bearer token for API authentication.
	// If empty, no authentication is required.
	Token string `mapstructure:"token"`
}

// AgentServerConfig holds agent server configuration (main instance).
type AgentServerConfig struct {
	// Enabled controls whether agent server is enabled
	Enabled bool `mapstructure:"enabled"`

	// Listen is the listen address for agent connections
	Listen string `mapstructure:"listen"`

	// AcceptToken is a shared token that allows any agent to connect.
	// When set, any agent presenting this token is accepted regardless
	// of whether it's pre-configured in the Agents map.
	AcceptToken string `mapstructure:"accept_token"`

	// TLS configuration for agent server
	TLS TLSConfig `mapstructure:"tls"`

	// Agents is a map of pre-configured agent entries (agentID -> config).
	Agents map[string]AgentEntryConfig `mapstructure:"agents"`
}

// AgentEntryConfig holds configuration for a single agent.
type AgentEntryConfig struct {
	// Token is the authentication token for this agent
	Token string `mapstructure:"token"`

	// DefaultTunnel is the default tunnel for this agent
	DefaultTunnel string `mapstructure:"default_tunnel"`

	// ConnectTo is the agent's WebSocket endpoint for inbound mode.
	ConnectTo string `mapstructure:"connect_to"`
}

// ConnectConfig holds agent connection configuration.
type ConnectConfig struct {
	// Mode is the connection mode (outbound, inbound)
	Mode ConnectMode `mapstructure:"mode"`

	// Endpoint is the main instance endpoint (for outbound mode)
	Endpoint string `mapstructure:"endpoint"`

	// Listen is the listen address (for inbound mode)
	Listen string `mapstructure:"listen"`

	// Token is the agent authentication token
	Token string `mapstructure:"token"`

	// AgentID is the unique identifier for this agent
	AgentID string `mapstructure:"agent_id"`

	// HeartbeatInterval is the interval for agent heartbeat
	HeartbeatInterval time.Duration `mapstructure:"heartbeat_interval"`

	// TLS configuration for agent connection
	TLS TLSConfig `mapstructure:"tls"`
}

// RetryConfig holds retry configuration for API calls and reconnection.
type RetryConfig struct {
	// Attempts is the maximum number of retry attempts
	Attempts int `mapstructure:"attempts"`

	// Delay is the initial retry delay
	Delay time.Duration `mapstructure:"delay"`

	// MaxDelay is the maximum retry delay after backoff
	MaxDelay time.Duration `mapstructure:"max_delay"`

	// Backoff is the delay multiplier between retries
	Backoff float64 `mapstructure:"backoff"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		LabelPrefix:   "labelgate",
		LogLevel:      "info",
		LogFormat:     "text",
		Mode:          ModeMain,
		DefaultTunnel: "default",
		Docker: DockerConfig{
			Endpoint:     "unix:///var/run/docker.sock",
			PollInterval: 2 * time.Minute,
		},
		Cloudflare: CloudflareConfig{
			Credentials: make(map[string]CredentialConfig),
			Tunnels:     make(map[string]TunnelConfig),
		},
		Sync: SyncConfig{
			Interval:    time.Hour,
			RemoveDelay: 30 * time.Minute,
			OrphanTTL:   0,
		},
		Db: DbConfig{
			Path:           "/app/config/labelgate.db",
			Retention:      7 * 24 * time.Hour,
			VacuumInterval: 24 * time.Hour,
		},
		Api: ApiConfig{
			Enabled:  true,
			Address:  ":8080",
			BasePath: "/api",
		},
		Agent: AgentServerConfig{
			Enabled: false,
			Listen:  ":8081",
		},
		Connect: ConnectConfig{
			Mode:              ConnectOutbound,
			HeartbeatInterval: 30 * time.Second,
		},
		Retry: RetryConfig{
			Attempts: 3,
			Delay:    time.Second,
			MaxDelay: 30 * time.Second,
			Backoff:  2,
		},
	}
}
