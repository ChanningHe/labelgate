package config

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/channinghe/labelgate/internal/model"
)

// Validation limits.
const (
	// MinSyncInterval is the smallest allowed sync.interval.
	MinSyncInterval = 10 * time.Second
	// MinHeartbeatInterval is the smallest allowed connect.heartbeat_interval.
	MinHeartbeatInterval = time.Second
	// MinAgentTokenLen is the smallest allowed agent token (named agents,
	// agent.accept_token, connect.token). Tokens are guessable offline from a
	// captured handshake, so they must carry real entropy.
	MinAgentTokenLen = 32
	// MinAPITokenLen is the smallest allowed api.token.
	MinAPITokenLen = 16

	dashboardPath = "/dashboard"
	maxPort       = 65535
)

var (
	labelPrefixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	resourceNamePat    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	logLevels          = []string{"debug", "info", "warn", "error"}
	logFormats         = []string{"text", "json"}
)

// fail builds a *ValidationError.
func fail(field, format string, args ...any) error {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Validate checks cfg and returns the first problem as *ValidationError.
// It never modifies cfg (no silent fallbacks). Rules, in check order:
//   - label_prefix pattern; mode, log_level, log_format, connect.mode
//     enumerations; instance_id empty or model.ValidInstanceID.
//   - retry.attempts >= 1, retry.delay > 0, retry.backoff >= 1,
//     retry.max_delay >= retry.delay.
//   - docker.endpoint unix:// or tcp:// (ssh:// -> removal message);
//     docker.poll_interval > 0; TLS cert and key set together.
//   - main mode: sync.interval >= 10s, remove_delay/orphan_ttl/agent_grace >= 0,
//     db.path set, api.address and api.base_path shape, cloudflare credentials
//     and tunnels (names, tokens, references), default_tunnel resolvable, at
//     least one credential unless skip_credential_validation, agent listener
//     and agent entries (IDs, tokens, connect_to, globs, tunnel references).
//     Tokens: api.token >= MinAPITokenLen when set (enabled API); named agent
//     tokens and agent.accept_token (when set) >= MinAgentTokenLen.
//   - agent mode: connect.token >= MinAgentTokenLen, outbound needs a ws(s):// endpoint,
//     inbound needs connect.listen, connect.agent_id valid when set,
//     heartbeat_interval >= 1s.
func Validate(cfg Config) error {
	checks := []func(Config) error{checkCore, checkRetry, checkDocker}
	if cfg.Mode == ModeAgent {
		checks = append(checks, checkConnect)
	} else {
		checks = append(checks, checkSyncDB, checkAPI, checkCloudflare, checkAgentServer)
	}
	for _, check := range checks {
		if err := check(cfg); err != nil {
			return err
		}
	}
	return nil
}

func checkCore(cfg Config) error {
	switch {
	case !labelPrefixPattern.MatchString(cfg.LabelPrefix):
		return fail("label_prefix", "%q must match %s", cfg.LabelPrefix, labelPrefixPattern)
	case cfg.Mode != ModeMain && cfg.Mode != ModeAgent:
		return fail("mode", "%q is invalid (use main or agent)", cfg.Mode)
	case !slices.Contains(logLevels, cfg.LogLevel):
		return fail("log_level", "%q is invalid (use %s)", cfg.LogLevel, strings.Join(logLevels, ", "))
	case !slices.Contains(logFormats, cfg.LogFormat):
		return fail("log_format", "%q is invalid (use %s)", cfg.LogFormat, strings.Join(logFormats, ", "))
	case cfg.Connect.Mode != ConnectOutbound && cfg.Connect.Mode != ConnectInbound:
		return fail("connect.mode", "%q is invalid (use outbound or inbound)", cfg.Connect.Mode)
	case cfg.InstanceID != "" && !model.ValidInstanceID(cfg.InstanceID):
		return fail("instance_id", "%q must be 4-32 lower-case alphanumeric characters", cfg.InstanceID)
	}
	return nil
}

func checkRetry(cfg Config) error {
	r := cfg.Retry
	switch {
	case r.Attempts < 1:
		return fail("retry.attempts", "must be >= 1, got %d", r.Attempts)
	case r.Delay <= 0:
		return fail("retry.delay", "must be > 0, got %s", r.Delay)
	case !(r.Backoff >= 1): // also rejects NaN
		return fail("retry.backoff", "must be >= 1, got %v", r.Backoff)
	case r.MaxDelay < r.Delay:
		return fail("retry.max_delay", "must be >= retry.delay (%s), got %s", r.Delay, r.MaxDelay)
	}
	return nil
}

func checkDocker(cfg Config) error {
	d := cfg.Docker
	if err := checkDockerEndpoint(d.Endpoint); err != nil {
		return err
	}
	if d.PollInterval <= 0 {
		return fail("docker.poll_interval", "must be > 0, got %s", d.PollInterval)
	}
	return checkTLSPair("docker.tls", d.TLS)
}

func checkDockerEndpoint(endpoint string) error {
	const field = "docker.endpoint"
	u, err := url.Parse(endpoint)
	if err != nil || endpoint == "" {
		return fail(field, "%q is not a valid endpoint (use unix:///path or tcp://host:port)", endpoint)
	}
	switch u.Scheme {
	case "ssh":
		return fail(field, "ssh:// Docker endpoints were removed in v0.3; use a unix socket or tcp:// with TLS")
	case "unix":
		if u.Path == "" {
			return fail(field, "%q has no socket path", endpoint)
		}
	case "tcp":
		if err := checkHostPort(field, u.Host); err != nil {
			return err
		}
	default:
		return fail(field, "unsupported scheme in %q (use unix:// or tcp://)", endpoint)
	}
	return nil
}

// checkHostPort verifies hostport is host:port with a non-empty host and a numeric port.
func checkHostPort(field, hostport string) error {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil || host == "" {
		return fail(field, "%q must be host:port", hostport)
	}
	return checkPort(field, port)
}

func checkPort(field, port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > maxPort {
		return fail(field, "invalid port %q", port)
	}
	return nil
}

// checkListen verifies a listen address ("[host]:port"; host may be empty).
func checkListen(field, addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fail(field, "%q must be [host]:port", addr)
	}
	return checkPort(field, port)
}

func checkTLSPair(prefix string, t TLSConfig) error {
	if (t.Cert == "") != (t.Key == "") {
		return fail(prefix+".cert", "cert and key must be set together")
	}
	return nil
}

func checkSyncDB(cfg Config) error {
	s := cfg.Sync
	switch {
	case s.Interval < MinSyncInterval:
		return fail("sync.interval", "must be >= %s, got %s", MinSyncInterval, s.Interval)
	case s.RemoveDelay < 0:
		return fail("sync.remove_delay", "must be >= 0, got %s", s.RemoveDelay)
	case s.OrphanTTL < 0:
		return fail("sync.orphan_ttl", "must be >= 0, got %s", s.OrphanTTL)
	case s.AgentGrace < 0:
		return fail("sync.agent_grace", "must be >= 0, got %s", s.AgentGrace)
	case strings.TrimSpace(cfg.DB.Path) == "":
		return fail("db.path", "must not be empty")
	}
	return nil
}

func checkAPI(cfg Config) error {
	a := cfg.API
	if !a.Enabled {
		return nil
	}
	if err := checkListen("api.address", a.Address); err != nil {
		return err
	}
	if err := checkTokenLen("api.token", a.Token, MinAPITokenLen, true); err != nil {
		return err
	}
	return checkBasePath(a.BasePath)
}

func checkBasePath(p string) error {
	const field = "api.base_path"
	switch {
	case !strings.HasPrefix(p, "/"):
		return fail(field, "%q must start with /", p)
	case strings.HasSuffix(p, "/"):
		return fail(field, "%q must not end with /", p)
	case strings.ContainsAny(p, " \t?#"):
		return fail(field, "%q must not contain whitespace, ? or #", p)
	case p == dashboardPath || strings.HasPrefix(p, dashboardPath+"/"):
		return fail(field, "%q is reserved for the dashboard", p)
	}
	return nil
}

func checkCloudflare(cfg Config) error {
	cf := cfg.Cloudflare
	if cf.TunnelID != "" && cf.AccountID == "" {
		return fail("cloudflare.account_id", "required when cloudflare.tunnel_id is set")
	}
	if cf.TunnelID != "" && cf.APIToken == "" {
		return fail("cloudflare.api_token", "required: cloudflare.tunnel_id uses the root credential")
	}
	if err := checkCredentials(cf.Credentials); err != nil {
		return err
	}
	if err := checkTunnels(cf); err != nil {
		return err
	}
	if err := checkDefaultTunnel(cfg); err != nil {
		return err
	}
	if cf.APIToken == "" && len(cf.Credentials) == 0 && !cfg.SkipCredentialValidation {
		return fail("cloudflare.api_token",
			"at least one credential is required: set cloudflare.api_token or configure cloudflare.credentials")
	}
	return nil
}

func checkName(field, name string) error {
	if name == RootCredential || name == RootTunnel {
		return fail(field, "name %q is reserved for the root entry", name)
	}
	if !resourceNamePat.MatchString(name) {
		return fail(field, "name %q must match %s", name, resourceNamePat)
	}
	return nil
}

func checkCredentials(creds map[string]CredentialConfig) error {
	for _, name := range sortedKeys(creds) {
		field := "cloudflare.credentials." + name
		if err := checkName(field, name); err != nil {
			return err
		}
		c := creds[name]
		if strings.TrimSpace(c.APIToken) == "" {
			return fail(field+".api_token", "must not be empty")
		}
		for _, z := range c.Zones {
			if !validGlob(z) {
				return fail(field+".zones", "invalid zone pattern %q", z)
			}
		}
	}
	return nil
}

func checkTunnels(cf CloudflareConfig) error {
	for _, name := range sortedKeys(cf.Tunnels) {
		field := "cloudflare.tunnels." + name
		if err := checkName(field, name); err != nil {
			return err
		}
		t := cf.Tunnels[name]
		switch {
		case strings.TrimSpace(t.AccountID) == "":
			return fail(field+".account_id", "must not be empty")
		case strings.TrimSpace(t.TunnelID) == "":
			return fail(field+".tunnel_id", "must not be empty")
		}
		if err := checkTunnelCredential(field+".credential", t.Credential, cf); err != nil {
			return err
		}
	}
	return nil
}

func checkTunnelCredential(field, ref string, cf CloudflareConfig) error {
	if ref != "" && ref != RootCredential {
		if _, ok := cf.Credentials[ref]; !ok {
			return fail(field, "credential %q is not defined in cloudflare.credentials", ref)
		}
		return nil
	}
	if cf.APIToken == "" {
		return fail(field, "uses the root credential but cloudflare.api_token is empty")
	}
	return nil
}

// checkDefaultTunnel validates default_tunnel: "" or "default" (root tunnel)
// or the name of an entry in cloudflare.tunnels.
func checkDefaultTunnel(cfg Config) error {
	ref := cfg.DefaultTunnel
	switch {
	case ref == "":
		return nil
	case ref == RootTunnel:
		if cfg.Cloudflare.TunnelID == "" {
			return fail("default_tunnel", "%q requires cloudflare.tunnel_id", ref)
		}
	default:
		if _, ok := cfg.Cloudflare.Tunnels[ref]; !ok {
			return fail("default_tunnel", "unknown tunnel %q (use default or a name from cloudflare.tunnels)", ref)
		}
	}
	return nil
}

// resolvableTunnel reports whether ref names the root tunnel, a configured
// tunnel, or the ID of either.
func resolvableTunnel(cf CloudflareConfig, ref string) bool {
	if ref == RootTunnel {
		return cf.TunnelID != ""
	}
	if cf.TunnelID != "" && ref == cf.TunnelID {
		return true
	}
	if _, ok := cf.Tunnels[ref]; ok {
		return true
	}
	for _, t := range cf.Tunnels {
		if t.TunnelID == ref {
			return true
		}
	}
	return false
}

func checkTunnelRefs(cf CloudflareConfig, field string, refs []string) error {
	for _, ref := range refs {
		if !resolvableTunnel(cf, ref) {
			return fail(field, "unknown tunnel %q", ref)
		}
	}
	return nil
}

func validGlob(p string) bool {
	if p == "" || strings.ContainsAny(p, " \t") {
		return false
	}
	_, err := path.Match(p, "a.b")
	return err == nil
}

func checkGlobs(field string, globs []string) error {
	for _, g := range globs {
		if !validGlob(g) {
			return fail(field, "invalid pattern %q", g)
		}
	}
	return nil
}

// checkTokenLen enforces a minimum token length (in characters). An empty
// token passes when optional is true (the feature is disabled).
func checkTokenLen(field, token string, minLen int, optional bool) error {
	if token == "" && optional {
		return nil
	}
	if n := utf8.RuneCountInString(token); n < minLen {
		return fail(field, "must be at least %d characters, got %d (generate one with: openssl rand -hex 32)", minLen, n)
	}
	return nil
}

func checkAgentServer(cfg Config) error {
	a := cfg.Agent
	if err := checkTokenLen("agent.accept_token", a.AcceptToken, MinAgentTokenLen, true); err != nil {
		return err
	}
	if err := checkTLSPair("agent.tls", a.TLS); err != nil {
		return err
	}
	if a.Enabled {
		if err := checkListen("agent.listen", a.Listen); err != nil {
			return err
		}
	}
	if err := checkGlobs("agent.accept_hostnames", a.AcceptHostnames); err != nil {
		return err
	}
	if err := checkTunnelRefs(cfg.Cloudflare, "agent.accept_tunnels", a.AcceptTunnels); err != nil {
		return err
	}
	for _, id := range sortedKeys(a.Agents) {
		if err := checkAgentEntry(cfg.Cloudflare, id, a.Agents[id]); err != nil {
			return err
		}
	}
	return nil
}

func checkAgentEntry(cf CloudflareConfig, id string, e AgentEntry) error {
	field := "agent.agents." + id
	if !model.ValidAgentID(id) {
		return fail(field, "invalid agent ID %q (must match ^[a-z0-9][a-z0-9._-]{0,62}$)", id)
	}
	if strings.TrimSpace(e.Token) == "" {
		return fail(field+".token", "must not be empty")
	}
	if err := checkTokenLen(field+".token", e.Token, MinAgentTokenLen, false); err != nil {
		return err
	}
	if e.ConnectTo != "" {
		if err := checkWebSocketURL(field+".connect_to", e.ConnectTo); err != nil {
			return err
		}
	}
	if e.DefaultTunnel != "" && !resolvableTunnel(cf, e.DefaultTunnel) {
		return fail(field+".default_tunnel", "unknown tunnel %q", e.DefaultTunnel)
	}
	if err := checkTunnelRefs(cf, field+".tunnels", e.Tunnels); err != nil {
		return err
	}
	return checkGlobs(field+".hostnames", e.Hostnames)
}

func checkWebSocketURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return fail(field, "%q must be a ws:// or wss:// URL", raw)
	}
	return nil
}

func checkConnect(cfg Config) error {
	c := cfg.Connect
	switch {
	case strings.TrimSpace(c.Token) == "":
		return fail("connect.token", "must not be empty in agent mode")
	case c.AgentID != "" && !model.ValidAgentID(c.AgentID):
		return fail("connect.agent_id", "%q must match ^[a-z0-9][a-z0-9._-]{0,62}$", c.AgentID)
	case c.HeartbeatInterval < MinHeartbeatInterval:
		return fail("connect.heartbeat_interval", "must be >= %s, got %s", MinHeartbeatInterval, c.HeartbeatInterval)
	}
	if err := checkTokenLen("connect.token", c.Token, MinAgentTokenLen, false); err != nil {
		return err
	}
	if err := checkTLSPair("connect.tls", c.TLS); err != nil {
		return err
	}
	if c.Mode == ConnectInbound {
		if c.Listen == "" {
			return fail("connect.listen", "required in inbound mode")
		}
		return checkListen("connect.listen", c.Listen)
	}
	if c.Endpoint == "" {
		return fail("connect.endpoint", "required in outbound mode")
	}
	return checkWebSocketURL("connect.endpoint", c.Endpoint)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
