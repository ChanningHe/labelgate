package config

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// agentToken meets the agent token rules (32 characters, 12 distinct).
const agentToken = "3f9a1c7e5b2d8046af13ce97b5d20864"

// validMain returns a valid main-mode config with a root credential and tunnel.
func validMain() Config {
	cfg := Defaults()
	cfg.Cloudflare.APIToken = "root-token"
	cfg.Cloudflare.AccountID = "acct"
	cfg.Cloudflare.TunnelID = "tunnel-1"
	return cfg
}

// validAgent returns a valid outbound agent-mode config.
func validAgent() Config {
	cfg := Defaults()
	cfg.Mode = ModeAgent
	cfg.Connect.Token = agentToken
	cfg.Connect.Endpoint = "wss://main.example.com:28112/ws"
	return cfg
}

type validateCase struct {
	name      string
	base      func() Config
	mutate    func(*Config)
	wantField string // "" = valid
	wantMsg   string // substring of the message
}

func runValidateCases(t *testing.T, cases []validateCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.base()
			tc.mutate(&cfg)
			err := Validate(cfg)
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want *ValidationError, got %T %v", err, err)
			}
			if ve.Field != tc.wantField || !strings.Contains(ve.Message, tc.wantMsg) {
				t.Fatalf("got field %q msg %q; want field %q msg containing %q", ve.Field, ve.Message, tc.wantField, tc.wantMsg)
			}
		})
	}
}

func TestValidate_Baselines(t *testing.T) {
	if err := Validate(validMain()); err != nil {
		t.Errorf("valid main config rejected: %v", err)
	}
	if err := Validate(validAgent()); err != nil {
		t.Errorf("valid agent config rejected: %v", err)
	}
	if err := Validate(Defaults()); err == nil {
		t.Error("defaults without a credential must fail in main mode")
	}
}

func TestValidate_Core(t *testing.T) {
	m := validMain
	runValidateCases(t, []validateCase{
		{"bad label prefix", m, func(c *Config) { c.LabelPrefix = "Label Gate" }, "label_prefix", "(default: labelgate)"},
		{"empty label prefix", m, func(c *Config) { c.LabelPrefix = "" }, "label_prefix", "is invalid: use lower-case"},
		{"custom label prefix ok", m, func(c *Config) { c.LabelPrefix = "my.prefix-1" }, "", ""},
		{"bad mode", m, func(c *Config) { c.Mode = "worker" }, "mode", "use main or agent"},
		{"empty mode", m, func(c *Config) { c.Mode = "" }, "mode", "invalid"},
		{"bad log level", m, func(c *Config) { c.LogLevel = "verbose" }, "log_level", "invalid"},
		{"bad log format", m, func(c *Config) { c.LogFormat = "xml" }, "log_format", "invalid"},
		{"json log format ok", m, func(c *Config) { c.LogFormat = "json"; c.LogLevel = "debug" }, "", ""},
		{"bad connect mode", m, func(c *Config) { c.Connect.Mode = "sideways" }, "connect.mode", "invalid"},
		{"bad instance id", m, func(c *Config) { c.InstanceID = "AB" }, "instance_id", "4-32"},
		{"good instance id", m, func(c *Config) { c.InstanceID = "abcd1234" }, "", ""},
	})
}

func TestValidate_Retry(t *testing.T) {
	m := validMain
	runValidateCases(t, []validateCase{
		{"attempts zero", m, func(c *Config) { c.Retry.Attempts = 0 }, "retry.attempts", ">= 1"},
		{"delay zero", m, func(c *Config) { c.Retry.Delay = 0 }, "retry.delay", "> 0"},
		{"delay negative", m, func(c *Config) { c.Retry.Delay = -time.Second }, "retry.delay", "> 0"},
		{"backoff below one", m, func(c *Config) { c.Retry.Backoff = 0.5 }, "retry.backoff", ">= 1"},
		{"backoff NaN", m, func(c *Config) { c.Retry.Backoff = math.NaN() }, "retry.backoff", ">= 1"},
		{"backoff one ok", m, func(c *Config) { c.Retry.Backoff = 1 }, "", ""},
		{"max delay below delay", m, func(c *Config) { c.Retry.Delay = time.Minute; c.Retry.MaxDelay = time.Second }, "retry.max_delay", ">= retry.delay"},
	})
}

func TestValidate_Docker(t *testing.T) {
	m := validMain
	runValidateCases(t, []validateCase{
		{"ssh endpoint", m, func(c *Config) { c.Docker.Endpoint = "ssh://user@host" }, "docker.endpoint", "removed in v0.3"},
		{"empty endpoint", m, func(c *Config) { c.Docker.Endpoint = "" }, "docker.endpoint", "not a valid endpoint"},
		{"http endpoint", m, func(c *Config) { c.Docker.Endpoint = "http://host:2375" }, "docker.endpoint", "unsupported scheme"},
		{"unparsable endpoint", m, func(c *Config) { c.Docker.Endpoint = "tcp://[::1" }, "docker.endpoint", "not a valid endpoint"},
		{"unix without path", m, func(c *Config) { c.Docker.Endpoint = "unix://" }, "docker.endpoint", "no socket path"},
		{"tcp without port", m, func(c *Config) { c.Docker.Endpoint = "tcp://host" }, "docker.endpoint", "host:port"},
		{"tcp without host", m, func(c *Config) { c.Docker.Endpoint = "tcp://:2375" }, "docker.endpoint", "host:port"},
		{"tcp with bad port", m, func(c *Config) { c.Docker.Endpoint = "tcp://host:99999" }, "docker.endpoint", "invalid port"},
		{"tcp ok", m, func(c *Config) { c.Docker.Endpoint = "tcp://docker:2376" }, "", ""},
		{"poll interval zero", m, func(c *Config) { c.Docker.PollInterval = 0 }, "docker.poll_interval", "> 0"},
		{"poll interval negative", m, func(c *Config) { c.Docker.PollInterval = -1 }, "docker.poll_interval", "> 0"},
		{"tls cert without key", m, func(c *Config) { c.Docker.TLS.Cert = "c.pem" }, "docker.tls.cert", "together"},
		{"tls key without cert", m, func(c *Config) { c.Docker.TLS.Key = "k.pem" }, "docker.tls.cert", "together"},
		{"tls pair ok", m, func(c *Config) { c.Docker.TLS = TLSConfig{CA: "ca", Cert: "c", Key: "k"} }, "", ""},
	})
}

func TestValidate_SyncAndDB(t *testing.T) {
	m := validMain
	runValidateCases(t, []validateCase{
		{"interval too small", m, func(c *Config) { c.Sync.Interval = 9 * time.Second }, "sync.interval", ">= 10s"},
		{"interval zero", m, func(c *Config) { c.Sync.Interval = 0 }, "sync.interval", ">= 10s"},
		{"interval minimum ok", m, func(c *Config) { c.Sync.Interval = 10 * time.Second }, "", ""},
		{"remove delay negative", m, func(c *Config) { c.Sync.RemoveDelay = -1 }, "sync.remove_delay", ">= 0"},
		{"remove delay zero ok", m, func(c *Config) { c.Sync.RemoveDelay = 0 }, "", ""},
		{"orphan ttl negative", m, func(c *Config) { c.Sync.OrphanTTL = -1 }, "sync.orphan_ttl", ">= 0"},
		{"agent grace negative", m, func(c *Config) { c.Sync.AgentGrace = -1 }, "sync.agent_grace", ">= 5m"},
		{"agent grace zero", m, func(c *Config) { c.Sync.AgentGrace = 0 }, "sync.agent_grace", ">= 5m"},
		{"agent grace below floor", m, func(c *Config) { c.Sync.AgentGrace = 4 * time.Minute }, "sync.agent_grace", ">= 5m"},
		{"agent grace floor ok", m, func(c *Config) { c.Sync.AgentGrace = MinAgentGrace }, "", ""},
		{"empty db path", m, func(c *Config) { c.DB.Path = "  " }, "db.path", "must not be empty"},
	})
}

func TestValidate_API(t *testing.T) {
	m := validMain
	runValidateCases(t, []validateCase{
		{"base path without slash", m, func(c *Config) { c.API.BasePath = "api" }, "api.base_path", "start with /"},
		{"empty base path", m, func(c *Config) { c.API.BasePath = "" }, "api.base_path", "start with /"},
		{"root base path", m, func(c *Config) { c.API.BasePath = "/" }, "api.base_path", "not end with /"},
		{"trailing slash", m, func(c *Config) { c.API.BasePath = "/api/" }, "api.base_path", "not end with /"},
		{"query in base path", m, func(c *Config) { c.API.BasePath = "/api?x" }, "api.base_path", "whitespace"},
		{"space in base path", m, func(c *Config) { c.API.BasePath = "/a b" }, "api.base_path", "whitespace"},
		{"dashboard base path", m, func(c *Config) { c.API.BasePath = "/dashboard" }, "api.base_path", "reserved"},
		{"under dashboard", m, func(c *Config) { c.API.BasePath = "/dashboard/api" }, "api.base_path", "reserved"},
		{"dashboard lookalike ok", m, func(c *Config) { c.API.BasePath = "/dashboards" }, "", ""},
		{"nested base path ok", m, func(c *Config) { c.API.BasePath = "/v1/lg" }, "", ""},
		{"bad address", m, func(c *Config) { c.API.Address = "28111" }, "api.address", "[host]:port"},
		{"bad address port", m, func(c *Config) { c.API.Address = ":http" }, "api.address", "invalid port"},
		{"disabled api skips checks", m, func(c *Config) { c.API.Enabled = false; c.API.Address = "x"; c.API.BasePath = "x" }, "", ""},
		{"empty token ok (api disabled at runtime)", m, func(c *Config) { c.API.Token = "" }, "", ""},
		{"short api token", m, func(c *Config) { c.API.Token = strings.Repeat("a", MinAPITokenLen-1) }, "api.token", "openssl rand -hex 32"},
		{"minimum api token ok", m, func(c *Config) { c.API.Token = strings.Repeat("a", MinAPITokenLen) }, "", ""},
		{"short api token ignored when api disabled", m, func(c *Config) { c.API.Enabled = false; c.API.Token = "x" }, "", ""},
	})
}

func TestValidate_Cloudflare(t *testing.T) {
	m := validMain
	creds := func(c *Config, name string, cc CredentialConfig) {
		c.Cloudflare.Credentials = map[string]CredentialConfig{name: cc}
	}
	tunnels := func(c *Config, name string, tc TunnelConfig) {
		c.Cloudflare.Tunnels = map[string]TunnelConfig{name: tc}
	}
	runValidateCases(t, []validateCase{
		{"no credential", m, func(c *Config) { c.Cloudflare = CloudflareConfig{} }, "cloudflare.api_token", "no Cloudflare API token is configured"},
		{"no credential but skipped", m, func(c *Config) { c.Cloudflare = CloudflareConfig{}; c.SkipCredentialValidation = true }, "", ""},
		{"only named credential ok", m, func(c *Config) {
			c.Cloudflare = CloudflareConfig{}
			creds(c, "corp", CredentialConfig{APIToken: agentToken})
		}, "", ""},
		{"tunnel id without account", m, func(c *Config) { c.Cloudflare.AccountID = "" }, "cloudflare.account_id", "required"},
		{"tunnel id without token", m, func(c *Config) {
			c.Cloudflare.APIToken = ""
			creds(c, "corp", CredentialConfig{APIToken: agentToken})
		}, "cloudflare.api_token", "required because cloudflare.tunnel_id is set"},
		{"credential named default", m, func(c *Config) { creds(c, "default", CredentialConfig{APIToken: agentToken}) }, "cloudflare.credentials.default", "reserved"},
		{"credential bad name", m, func(c *Config) { creds(c, "Corp Co", CredentialConfig{APIToken: agentToken}) }, "cloudflare.credentials.Corp Co", "is invalid: use lower-case"},
		{"credential empty token", m, func(c *Config) { creds(c, "corp", CredentialConfig{APIToken: " "}) }, "cloudflare.credentials.corp.api_token", "must not be empty"},
		{"credential bad zone", m, func(c *Config) { creds(c, "corp", CredentialConfig{APIToken: agentToken, Zones: []string{"[bad"}}) }, "cloudflare.credentials.corp.zones", "invalid zone pattern"},
		{"credential empty zone", m, func(c *Config) { creds(c, "corp", CredentialConfig{APIToken: agentToken, Zones: []string{""}}) }, "cloudflare.credentials.corp.zones", "invalid zone pattern"},
		{"credential zones ok", m, func(c *Config) {
			creds(c, "corp", CredentialConfig{APIToken: agentToken, Zones: []string{"example.com", "*.corp.io"}})
		}, "", ""},
		{"tunnel named default", m, func(c *Config) { tunnels(c, "default", TunnelConfig{AccountID: "a", TunnelID: "t"}) }, "cloudflare.tunnels.default", "reserved"},
		{"tunnel bad name", m, func(c *Config) { tunnels(c, "-x", TunnelConfig{AccountID: "a", TunnelID: "t"}) }, "cloudflare.tunnels.-x", "is invalid: use lower-case"},
		{"tunnel no account", m, func(c *Config) { tunnels(c, "sec", TunnelConfig{TunnelID: "t"}) }, "cloudflare.tunnels.sec.account_id", "must not be empty"},
		{"tunnel no id", m, func(c *Config) { tunnels(c, "sec", TunnelConfig{AccountID: "a"}) }, "cloudflare.tunnels.sec.tunnel_id", "must not be empty"},
		{"tunnel unknown credential", m, func(c *Config) {
			tunnels(c, "sec", TunnelConfig{AccountID: "a", TunnelID: "t", Credential: "nope"})
		}, "cloudflare.tunnels.sec.credential", "not defined"},
		{"tunnel root credential missing", m, func(c *Config) {
			c.Cloudflare.APIToken, c.Cloudflare.TunnelID, c.Cloudflare.AccountID = "", "", ""
			creds(c, "corp", CredentialConfig{APIToken: agentToken})
			tunnels(c, "sec", TunnelConfig{AccountID: "a", TunnelID: "t"})
		}, "cloudflare.tunnels.sec.credential", "cloudflare.api_token (env LABELGATE_CLOUDFLARE_API_TOKEN) is empty"},
		{"tunnel with named credential ok", m, func(c *Config) {
			creds(c, "corp", CredentialConfig{APIToken: agentToken})
			tunnels(c, "sec", TunnelConfig{AccountID: "a", TunnelID: "t", Credential: "corp"})
		}, "", ""},
		{"tunnel with explicit root credential ok", m, func(c *Config) {
			tunnels(c, "sec", TunnelConfig{AccountID: "a", TunnelID: "t", Credential: "default"})
		}, "", ""},
	})
}

func TestValidate_DefaultTunnel(t *testing.T) {
	withNamed := func() Config {
		c := validMain()
		c.Cloudflare.Tunnels = map[string]TunnelConfig{"sec": {AccountID: "a", TunnelID: "t2"}}
		return c
	}
	noRoot := func() Config {
		c := withNamed()
		c.Cloudflare.TunnelID, c.Cloudflare.AccountID = "", ""
		return c
	}
	runValidateCases(t, []validateCase{
		{"empty ok", withNamed, func(c *Config) {}, "", ""},
		{"default ok", withNamed, func(c *Config) { c.DefaultTunnel = "default" }, "", ""},
		{"named ok", withNamed, func(c *Config) { c.DefaultTunnel = "sec" }, "", ""},
		{"unknown", withNamed, func(c *Config) { c.DefaultTunnel = "nope" }, "default_tunnel", "unknown tunnel"},
		{"default without root tunnel", noRoot, func(c *Config) { c.DefaultTunnel = "default" }, "default_tunnel", "cloudflare.tunnel_id (env LABELGATE_CLOUDFLARE_TUNNEL_ID), which is empty"},
		{"empty without root tunnel ok", noRoot, func(c *Config) {}, "", ""},
	})
}

func TestValidate_AgentServer(t *testing.T) {
	base := func() Config {
		c := validMain()
		c.Cloudflare.Tunnels = map[string]TunnelConfig{"sec": {AccountID: "a", TunnelID: "t2"}}
		c.Agent.Enabled = true
		return c
	}
	entry := func(c *Config, id string, e AgentEntry) {
		c.Agent.Agents = map[string]AgentEntry{id: e}
	}
	runValidateCases(t, []validateCase{
		{"enabled defaults ok", base, func(c *Config) {}, "", ""},
		{"bad listen", base, func(c *Config) { c.Agent.Listen = "nope" }, "agent.listen", "[host]:port"},
		{"bad listen ignored when disabled", base, func(c *Config) { c.Agent.Enabled = false; c.Agent.Listen = "nope" }, "", ""},
		{"tls cert without key", base, func(c *Config) { c.Agent.TLS.Cert = "c" }, "agent.tls.cert", "together"},
		{"bad accept hostname glob", base, func(c *Config) { c.Agent.AcceptHostnames = []string{"*.ok.com", "[x"} }, "agent.accept_hostnames", "invalid pattern"},
		{"accept hostnames ok", base, func(c *Config) { c.Agent.AcceptHostnames = []string{"*.ok.com", "a.b.com"} }, "", ""},
		{"accept tunnels unknown", base, func(c *Config) { c.Agent.AcceptTunnels = []string{"nope"} }, "agent.accept_tunnels", "unknown tunnel"},
		{"accept tunnels by name, default and id", base, func(c *Config) { c.Agent.AcceptTunnels = []string{"sec", "default", "t2", "tunnel-1"} }, "", ""},
		{"agent invalid id", base, func(c *Config) { entry(c, "Bad ID", AgentEntry{Token: agentToken}) }, "agent.agents.Bad ID", "invalid agent ID"},
		{"agent empty token", base, func(c *Config) { entry(c, "web", AgentEntry{}) }, "agent.agents.web.token", "must not be empty"},
		{"agent short token", base, func(c *Config) { entry(c, "web", AgentEntry{Token: strings.Repeat("a", MinAgentTokenLen-1)}) }, "agent.agents.web.token", "openssl rand -hex 32"},
		{"agent multibyte token counts characters", base, func(c *Config) {
			entry(c, "web", AgentEntry{Token: strings.Repeat("àáâãäåæçèéêëìíîï", 2)})
		}, "", ""},
		{"agent low-variety token", base, func(c *Config) { entry(c, "web", AgentEntry{Token: strings.Repeat("ab", 16)}) }, "agent.agents.web.token", "distinct"},
		{"agent token 11 distinct", base, func(c *Config) { entry(c, "web", AgentEntry{Token: strings.Repeat("0123456789a", 3)}) }, "agent.agents.web.token", "at least 12 distinct"},
		{"agent token 12 distinct ok", base, func(c *Config) { entry(c, "web", AgentEntry{Token: strings.Repeat("0123456789ab", 3)}) }, "", ""},
		{"accept token low-variety", base, func(c *Config) { c.Agent.AcceptToken = strings.Repeat("k", 40) }, "agent.accept_token", "distinct"},
		{"short accept token", base, func(c *Config) { c.Agent.AcceptToken = "shared" }, "agent.accept_token", "openssl rand -hex 32"},
		{"accept token ok", base, func(c *Config) { c.Agent.AcceptToken = agentToken }, "", ""},
		{"agent dotted id ok", base, func(c *Config) { entry(c, "nas.lan", AgentEntry{Token: agentToken}) }, "", ""},
		{"agent whitespace token", base, func(c *Config) { entry(c, "web", AgentEntry{Token: "  "}) }, "agent.agents.web.token", "must not be empty"},
		{"agent bad connect_to", base, func(c *Config) { entry(c, "web", AgentEntry{Token: agentToken, ConnectTo: "http://h/ws"}) }, "agent.agents.web.connect_to", "ws:// or wss://"},
		{"agent connect_to without host", base, func(c *Config) { entry(c, "web", AgentEntry{Token: agentToken, ConnectTo: "ws://"}) }, "agent.agents.web.connect_to", "ws:// or wss://"},
		{"agent unknown default tunnel", base, func(c *Config) { entry(c, "web", AgentEntry{Token: agentToken, DefaultTunnel: "nope"}) }, "agent.agents.web.default_tunnel", "unknown tunnel"},
		{"agent unknown tunnel", base, func(c *Config) { entry(c, "web", AgentEntry{Token: agentToken, Tunnels: []string{"nope"}}) }, "agent.agents.web.tunnels", "unknown tunnel"},
		{"accept services ok", base, func(c *Config) {
			c.Agent.AcceptServices = []string{"web", "*.svc.internal", "10.0.0.0/8", "192.0.2.7", "fd00::/8", "::1"}
		}, "", ""},
		{"accept services bad cidr", base, func(c *Config) { c.Agent.AcceptServices = []string{"10.0.0.0/33"} }, "agent.accept_services", "invalid"},
		{"accept services inet_aton form", base, func(c *Config) { c.Agent.AcceptServices = []string{"127.1"} }, "agent.accept_services", "canonical"},
		{"accept services hex form", base, func(c *Config) { c.Agent.AcceptServices = []string{"0x7f000001"} }, "agent.accept_services", "canonical"},
		{"accept services empty entry", base, func(c *Config) { c.Agent.AcceptServices = []string{""} }, "agent.accept_services", "invalid"},
		{"agent bad services entry", base, func(c *Config) { entry(c, "web", AgentEntry{Token: agentToken, Services: []string{"a b"}}) }, "agent.agents.web.services", "invalid"},
		{"agent bad hostname glob", base, func(c *Config) { entry(c, "web", AgentEntry{Token: agentToken, Hostnames: []string{"a b"}}) }, "agent.agents.web.hostnames", "invalid pattern"},
		{"agent full ok", base, func(c *Config) {
			entry(c, "web-1", AgentEntry{
				Token: agentToken, Name: "Web", DefaultTunnel: "sec", Tunnels: []string{"sec"},
				Hostnames: []string{"*.example.com"}, ConnectTo: "wss://agent:28113/ws",
			})
		}, "", ""},
		{"agent deterministic first error", base, func(c *Config) {
			c.Agent.Agents = map[string]AgentEntry{"b": {}, "a": {}}
		}, "agent.agents.a.token", "must not be empty"},
	})
}

func TestValidate_AgentMode(t *testing.T) {
	a := validAgent
	runValidateCases(t, []validateCase{
		{"empty token", a, func(c *Config) { c.Connect.Token = "" }, "connect.token", "must not be empty"},
		{"short token", a, func(c *Config) { c.Connect.Token = "tok" }, "connect.token", "openssl rand -hex 32"},
		{"low-variety token", a, func(c *Config) { c.Connect.Token = strings.Repeat("0123", 10) }, "connect.token", "openssl rand -hex 32"},
		{"outbound missing endpoint", a, func(c *Config) { c.Connect.Endpoint = "" }, "connect.endpoint", "required"},
		{"outbound http endpoint", a, func(c *Config) { c.Connect.Endpoint = "https://main/ws" }, "connect.endpoint", "ws:// or wss://"},
		{"outbound endpoint without path", a, func(c *Config) { c.Connect.Endpoint = "wss://main.example.com:28112" }, "connect.endpoint", "did you mean wss://main.example.com:28112/ws?"},
		{"outbound endpoint root path", a, func(c *Config) { c.Connect.Endpoint = "ws://main:28112/" }, "connect.endpoint", "did you mean ws://main:28112/ws?"},
		{"outbound endpoint custom path ok", a, func(c *Config) { c.Connect.Endpoint = "wss://proxy.example.com/labelgate/ws" }, "", ""},
		{"outbound ws ok", a, func(c *Config) { c.Connect.Endpoint = "ws://main:28112/ws" }, "", ""},
		{"inbound missing listen", a, func(c *Config) { c.Connect.Mode = ConnectInbound }, "connect.listen", "required"},
		{"inbound bad listen", a, func(c *Config) { c.Connect.Mode = ConnectInbound; c.Connect.Listen = "x" }, "connect.listen", "[host]:port"},
		{"inbound ok without endpoint", a, func(c *Config) { c.Connect.Mode = ConnectInbound; c.Connect.Listen = ":9000" }, "", ""},
		{"bad agent id", a, func(c *Config) { c.Connect.AgentID = "Bad_ID!" }, "connect.agent_id", "is invalid: use up to 63"},
		{"good agent id", a, func(c *Config) { c.Connect.AgentID = "host-1" }, "", ""},
		{"heartbeat too small", a, func(c *Config) { c.Connect.HeartbeatInterval = 500 * time.Millisecond }, "connect.heartbeat_interval", ">= 1s"},
		{"connect tls cert without key", a, func(c *Config) { c.Connect.TLS.Key = "k" }, "connect.tls.cert", "together"},
		{"no cloudflare credential needed", a, func(c *Config) {}, "", ""},
		{"main-only sections not validated", a, func(c *Config) { c.Sync.Interval = 0; c.API.BasePath = "x" }, "", ""},
		{"docker still validated", a, func(c *Config) { c.Docker.Endpoint = "ssh://x" }, "docker.endpoint", "removed"},
	})
}

func TestValidate_DoesNotMutate(t *testing.T) {
	cfg := validMain()
	cfg.Cloudflare.Credentials = map[string]CredentialConfig{"corp": {APIToken: agentToken}}
	before := cfg.Cloudflare.Credentials["corp"].APIToken
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Cloudflare.Credentials["corp"].APIToken != before || cfg.Mode != ModeMain {
		t.Error("Validate mutated its input")
	}
}

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{Field: "a.b", Message: "bad"}
	if err.Error() != "config: a.b: bad" {
		t.Errorf("got %q", err.Error())
	}
}

func TestDefaults_ListenPorts(t *testing.T) {
	cfg := Defaults()
	if cfg.API.Address != ":28111" {
		t.Errorf("api.address = %q, want :28111", cfg.API.Address)
	}
	if cfg.Agent.Listen != ":28112" {
		t.Errorf("agent.listen = %q, want :28112", cfg.Agent.Listen)
	}
}

func TestKeyWithEnv(t *testing.T) {
	tests := []struct{ key, want string }{
		{"api.token", "api.token (env LABELGATE_API_TOKEN)"},
		{"connect.tls.ca", "connect.tls.ca (env LABELGATE_CONNECT_TLS_CA)"},
		{"db.retention", "db.retention (env LABELGATE_DB_RETENTION)"}, // removed, still read from env
		{"agent.agents.edge1.token", "agent.agents.edge1.token"},      // map section: file only
		{"cloudflare.credentials.corp.api_token", "cloudflare.credentials.corp.api_token"},
		{"bogus", "bogus"},
	}
	for _, tt := range tests {
		if got := KeyWithEnv(tt.key); got != tt.want {
			t.Errorf("KeyWithEnv(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestValidationError_NamesEnvAndFix(t *testing.T) {
	cfg := validMain()
	cfg.API.Token = "short"
	err := Validate(cfg)
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"api.token (env LABELGATE_API_TOKEN): ", "at least 16 characters", "openssl rand -hex 32"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not contain %q", err.Error(), want)
		}
	}
}

func TestValidationErrors_NameEnvPerLine(t *testing.T) {
	err := ValidationErrors{fail("api.token", "bad"), fail("agent.agents.a.token", "worse")}
	want := "config: 2 problems:\n  api.token (env LABELGATE_API_TOKEN): bad\n  agent.agents.a.token: worse"
	if err.Error() != want {
		t.Errorf("got %q\nwant %q", err.Error(), want)
	}
}

func TestValidate_TLSPairNamesBothKeys(t *testing.T) {
	cfg := validMain()
	cfg.Docker.TLS.Cert = "/c.pem"
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "docker.tls.key (env LABELGATE_DOCKER_TLS_KEY)") {
		t.Errorf("got %v", err)
	}
}
