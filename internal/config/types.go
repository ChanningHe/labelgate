// Package config loads and validates labelgate configuration.
//
// types.go is a frozen contract (types, defaults, constants). Loading lives in
// load.go and validation in validate.go.
//
// Sources, highest priority first: environment (LABELGATE_<PATH>, dots ->
// underscores, upper-case), config file (YAML/JSON/TOML; --config flag, then
// LABELGATE_CONFIG, then ./labelgate.yaml, /etc/labelgate/labelgate.yaml),
// Defaults(). Map-valued sections (cloudflare.credentials, cloudflare.tunnels,
// agent.agents) are config-file only.
package config

import "time"

// EnvPrefix is the environment variable prefix.
const EnvPrefix = "LABELGATE"

// Mode is the process run mode.
type Mode string

const (
	ModeMain  Mode = "main"
	ModeAgent Mode = "agent"
)

// ConnectMode is how an agent and the main instance establish a session.
type ConnectMode string

const (
	// ConnectOutbound: the agent dials the main instance (agent.listen on main).
	ConnectOutbound ConnectMode = "outbound"
	// ConnectInbound: the main instance dials the agent (agent.agents.<id>.connect_to).
	ConnectInbound ConnectMode = "inbound"
)

// RootCredential is the name of the credential configured at cloudflare.api_token.
// A label credential=default selects it explicitly.
const RootCredential = "default"

// RootTunnel is the reference name of the tunnel configured at cloudflare.tunnel_id.
const RootTunnel = "default"

// Config is the complete configuration. Field names map 1:1 to yaml keys via mapstructure tags.
type Config struct {
	LabelPrefix string `mapstructure:"label_prefix"`
	LogLevel    string `mapstructure:"log_level"`  // debug|info|warn|error
	LogFormat   string `mapstructure:"log_format"` // text|json
	Mode        Mode   `mapstructure:"mode"`
	// InstanceID overrides the persisted random instance ID (DB-loss recovery). "" = use DB.
	InstanceID string `mapstructure:"instance_id"`
	// DefaultTunnel names the tunnel used when a label omits `tunnel`:
	// "" or "default" = cloudflare.tunnel_id, otherwise a key of cloudflare.tunnels.
	DefaultTunnel            string `mapstructure:"default_tunnel"`
	SkipCredentialValidation bool   `mapstructure:"skip_credential_validation"`

	Docker     DockerConfig      `mapstructure:"docker"`
	Cloudflare CloudflareConfig  `mapstructure:"cloudflare"`
	Sync       SyncConfig        `mapstructure:"sync"`
	DB         DBConfig          `mapstructure:"db"`
	API        APIConfig         `mapstructure:"api"`
	Agent      AgentServerConfig `mapstructure:"agent"`
	Connect    ConnectConfig     `mapstructure:"connect"`
	Retry      RetryConfig       `mapstructure:"retry"`
}

// DockerConfig configures the local Docker source.
type DockerConfig struct {
	// Endpoint: unix:///path or tcp://host:port. ssh:// is rejected.
	Endpoint string `mapstructure:"endpoint"`
	// PollInterval: periodic full re-list in addition to the event stream.
	PollInterval time.Duration `mapstructure:"poll_interval"`
	// FilterLabel: only containers carrying this label ("key" or "key=value").
	FilterLabel string    `mapstructure:"filter_label"`
	TLS         TLSConfig `mapstructure:"tls"` // client TLS for tcp:// (cert+key = client cert)
}

// TLSConfig is a TLS file set. Meaning of Cert/Key depends on the section:
//   - docker.tls: client certificate for the Docker daemon.
//   - agent.tls: server certificate of the main's agent listener; CA verifies
//     inbound agents when the main dials them.
//   - connect.tls: server certificate of the agent's inbound listener; CA
//     verifies the main when the agent dials it.
type TLSConfig struct {
	CA   string `mapstructure:"ca"`
	Cert string `mapstructure:"cert"`
	Key  string `mapstructure:"key"`
	// InsecureSkipVerify disables peer certificate verification when dialing.
	// Explicit opt-in only; logged as a warning at startup.
	InsecureSkipVerify bool `mapstructure:"insecure_skip_verify"`
}

// CloudflareConfig holds credentials and tunnels.
type CloudflareConfig struct {
	APIToken  string `mapstructure:"api_token"`  // root credential ("default")
	AccountID string `mapstructure:"account_id"` // account of the root tunnel
	TunnelID  string `mapstructure:"tunnel_id"`  // root tunnel ("default")
	// Credentials are additional named credentials. Name "default" is reserved.
	Credentials map[string]CredentialConfig `mapstructure:"credentials"`
	// Tunnels are additional named tunnels. Name "default" is reserved.
	Tunnels map[string]TunnelConfig `mapstructure:"tunnels"`
}

// CredentialConfig is one named API token.
type CredentialConfig struct {
	APIToken string `mapstructure:"api_token"`
	// Zones optionally restricts which zones (visible to the token) this
	// credential is used for in automatic selection: exact zone names or
	// "*.example.com" patterns. Empty = every zone the token can list.
	Zones []string `mapstructure:"zones"`
}

// TunnelConfig is one named tunnel.
type TunnelConfig struct {
	AccountID  string `mapstructure:"account_id"`
	TunnelID   string `mapstructure:"tunnel_id"`
	Credential string `mapstructure:"credential"` // "" = root credential
}

// SyncConfig controls reconciliation and resource lifecycle.
type SyncConfig struct {
	// Interval between periodic full reconcile cycles (in addition to event triggers).
	Interval time.Duration `mapstructure:"interval"`
	// RemoveDelay: an orphaned resource with cleanup=true is deleted this long after it stopped being desired.
	RemoveDelay time.Duration `mapstructure:"remove_delay"`
	// OrphanTTL: orphaned rows with cleanup=false are dropped from the DB (not
	// Cloudflare) after this long. 0 = never.
	OrphanTTL time.Duration `mapstructure:"orphan_ttl"`
	// AgentGrace: an agent's last reported containers stay desired while its
	// last_seen is within this window. Disconnects alone never orphan.
	AgentGrace time.Duration `mapstructure:"agent_grace"`
}

// DBConfig configures SQLite storage.
type DBConfig struct {
	Path string `mapstructure:"path"`
}

// APIConfig configures the HTTP API and dashboard.
type APIConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Address  string `mapstructure:"address"`
	BasePath string `mapstructure:"base_path"` // "/api"; must start with "/", no trailing "/"
	// Token is the Bearer token. Empty = API and dashboard disabled (logged as error).
	Token string `mapstructure:"token"`
}

// AgentServerConfig configures agent handling on the main instance.
type AgentServerConfig struct {
	// Enabled starts the agent listener (outbound agents) and inbound dialers.
	Enabled bool   `mapstructure:"enabled"`
	Listen  string `mapstructure:"listen"`
	// AcceptToken lets agents not listed in Agents connect with a shared token.
	AcceptToken string `mapstructure:"accept_token"`
	// AcceptHostnames restricts hostnames of accept_token agents (globs, path.Match
	// syntax on lower-case hostnames). Empty = unrestricted.
	AcceptHostnames []string `mapstructure:"accept_hostnames"`
	// AcceptTunnels lists tunnel references accept_token agents may use. Empty = only the default tunnel.
	AcceptTunnels []string              `mapstructure:"accept_tunnels"`
	TLS           TLSConfig             `mapstructure:"tls"`
	Agents        map[string]AgentEntry `mapstructure:"agents"`
}

// AgentEntry is one named agent. The map key is the agent ID.
type AgentEntry struct {
	Token string `mapstructure:"token"` // required, per-agent shared secret
	Name  string `mapstructure:"name"`  // optional display name
	// DefaultTunnel is used when this agent's labels omit `tunnel`. "" = global default.
	DefaultTunnel string `mapstructure:"default_tunnel"`
	// Tunnels this agent may target (references). Empty = only its default tunnel.
	Tunnels []string `mapstructure:"tunnels"`
	// Hostnames this agent may claim (globs). Empty = unrestricted.
	Hostnames []string `mapstructure:"hostnames"`
	// ConnectTo enables inbound mode: the main dials this ws:// or wss:// URL.
	ConnectTo string `mapstructure:"connect_to"`
}

// ConnectConfig configures agent mode.
type ConnectConfig struct {
	Mode     ConnectMode `mapstructure:"mode"`
	Endpoint string      `mapstructure:"endpoint"` // outbound: ws(s)://main:8081/ws
	Listen   string      `mapstructure:"listen"`   // inbound: listen address
	Token    string      `mapstructure:"token"`
	// AgentID: "" = derived from /etc/machine-id (first 12 hex chars), else hostname (sanitized).
	AgentID           string        `mapstructure:"agent_id"`
	HeartbeatInterval time.Duration `mapstructure:"heartbeat_interval"` // full report period
	TLS               TLSConfig     `mapstructure:"tls"`
}

// RetryConfig is the retry/backoff policy for Cloudflare calls and reconnects.
type RetryConfig struct {
	Attempts int           `mapstructure:"attempts"`
	Delay    time.Duration `mapstructure:"delay"`
	MaxDelay time.Duration `mapstructure:"max_delay"`
	Backoff  float64       `mapstructure:"backoff"`
}

// Defaults returns the default configuration.
func Defaults() Config {
	return Config{
		LabelPrefix: "labelgate",
		LogLevel:    "info",
		LogFormat:   "text",
		Mode:        ModeMain,
		Docker: DockerConfig{
			Endpoint:     "unix:///var/run/docker.sock",
			PollInterval: 2 * time.Minute,
		},
		Sync: SyncConfig{
			Interval:    5 * time.Minute,
			RemoveDelay: 30 * time.Minute,
			AgentGrace:  24 * time.Hour,
		},
		DB:  DBConfig{Path: "/app/config/labelgate.db"},
		API: APIConfig{Enabled: true, Address: ":8080", BasePath: "/api"},
		Agent: AgentServerConfig{
			Listen: ":8081",
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

// RemovedKeys are v0.2 keys that now fail validation, with the reason shown to the user.
var RemovedKeys = map[string]string{
	"docker.ssh.key":            "ssh:// Docker endpoints were removed in v0.3; use a unix socket or tcp:// with TLS",
	"docker.ssh.key_passphrase": "ssh:// Docker endpoints were removed in v0.3; use a unix socket or tcp:// with TLS",
	"docker.ssh.known_hosts":    "ssh:// Docker endpoints were removed in v0.3; use a unix socket or tcp:// with TLS",
	"db.retention":              "removed in v0.3 (deleted rows are no longer retained); delete this key",
	"db.vacuum_interval":        "removed in v0.3; delete this key",
}

// ValidationError is returned by Validate and Load for invalid configuration.
type ValidationError struct {
	Field   string // yaml path, e.g. "sync.remove_delay"
	Message string
}

func (e *ValidationError) Error() string {
	return "config: " + e.Field + ": " + e.Message
}
