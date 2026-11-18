package config

import (
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
	// MinAgentGrace is the smallest allowed sync.agent_grace: shorter windows
	// would orphan agents between two heartbeats.
	MinAgentGrace = 5 * time.Minute
	// MinAgentTokenLen is the smallest allowed agent token (named agents,
	// agent.accept_token, connect.token). Tokens are guessable offline from a
	// captured handshake, so they must carry real entropy.
	MinAgentTokenLen = 32
	// MinAgentTokenDistinct is the smallest number of distinct characters in
	// an agent token: rejects repeated or patterned placeholders.
	MinAgentTokenDistinct = 12
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

// Validate checks cfg and returns every problem it finds: a single
// *ValidationError when there is one, ValidationErrors (whose elements are all
// *ValidationError) when there are several, in check order. It never modifies
// cfg (no silent fallbacks).
//
// Checks are independent: one failing field does not hide another. A check
// stops at the first problem of its own field, and is skipped when it depends
// on a rule that already failed (retry.max_delay needs a valid retry.delay,
// mode-specific sections need a valid mode, and so on). Rules, in check order:
//   - label_prefix pattern; mode, log_level, log_format, connect.mode
//     enumerations; instance_id empty or model.ValidInstanceID.
//   - retry.attempts >= 1, retry.delay > 0, retry.backoff >= 1,
//     retry.max_delay >= retry.delay.
//   - docker.endpoint unix:// or tcp:// (ssh:// -> removal message);
//     docker.poll_interval > 0; TLS cert and key set together.
//   - main mode: sync.interval >= 10s, remove_delay/orphan_ttl >= 0, agent_grace >= 5m,
//     db.path set, api.address and api.base_path shape, cloudflare credentials
//     and tunnels (names, tokens, references), default_tunnel resolvable, at
//     least one credential unless skip_credential_validation, agent listener
//     and agent entries (IDs, tokens, connect_to, globs, tunnel references,
//     services allow-lists: CIDR, IP or hostname glob, no inet_aton forms).
//     Tokens: api.token >= MinAPITokenLen when set (enabled API); named agent
//     tokens and agent.accept_token (when set) >= MinAgentTokenLen characters
//     with >= MinAgentTokenDistinct distinct characters.
//   - agent mode: connect.token like agent tokens, outbound needs a ws(s):// endpoint,
//     inbound needs connect.listen, connect.agent_id valid when set,
//     heartbeat_interval >= 1s.
func Validate(cfg Config) error {
	var p ValidationErrors
	checkCore(&p, cfg)
	checkRetry(&p, cfg)
	checkDocker(&p, cfg)
	switch cfg.Mode {
	case ModeAgent:
		checkConnect(&p, cfg)
	case ModeMain:
		checkSyncDB(&p, cfg)
		checkAPI(&p, cfg)
		checkCloudflare(&p, cfg)
		checkAgentServer(&p, cfg)
	}
	return p.err()
}

func checkCore(p *ValidationErrors, cfg Config) {
	if !labelPrefixPattern.MatchString(cfg.LabelPrefix) {
		p.addf("label_prefix", "%q must match %s", cfg.LabelPrefix, labelPrefixPattern)
	}
	if cfg.Mode != ModeMain && cfg.Mode != ModeAgent {
		p.addf("mode", "%q is invalid (use main or agent)", cfg.Mode)
	}
	if !slices.Contains(logLevels, cfg.LogLevel) {
		p.addf("log_level", "%q is invalid (use %s)", cfg.LogLevel, strings.Join(logLevels, ", "))
	}
	if !slices.Contains(logFormats, cfg.LogFormat) {
		p.addf("log_format", "%q is invalid (use %s)", cfg.LogFormat, strings.Join(logFormats, ", "))
	}
	if !validConnectMode(cfg.Connect.Mode) {
		p.addf("connect.mode", "%q is invalid (use outbound or inbound)", cfg.Connect.Mode)
	}
	if cfg.InstanceID != "" && !model.ValidInstanceID(cfg.InstanceID) {
		p.addf("instance_id", "%q must be 4-32 lower-case alphanumeric characters", cfg.InstanceID)
	}
}

func validConnectMode(m ConnectMode) bool { return m == ConnectOutbound || m == ConnectInbound }

func checkRetry(p *ValidationErrors, cfg Config) {
	r := cfg.Retry
	if r.Attempts < 1 {
		p.addf("retry.attempts", "must be >= 1, got %d", r.Attempts)
	}
	if r.Delay <= 0 {
		p.addf("retry.delay", "must be > 0, got %s", r.Delay)
	}
	if !(r.Backoff >= 1) { // also rejects NaN
		p.addf("retry.backoff", "must be >= 1, got %v", r.Backoff)
	}
	// max_delay is only comparable to a valid delay.
	if r.Delay > 0 && r.MaxDelay < r.Delay {
		p.addf("retry.max_delay", "must be >= retry.delay (%s), got %s", r.Delay, r.MaxDelay)
	}
}

func checkDocker(p *ValidationErrors, cfg Config) {
	d := cfg.Docker
	p.add(checkDockerEndpoint(d.Endpoint))
	if d.PollInterval <= 0 {
		p.addf("docker.poll_interval", "must be > 0, got %s", d.PollInterval)
	}
	p.add(checkTLSPair("docker.tls", d.TLS))
}

func checkDockerEndpoint(endpoint string) *ValidationError {
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
		return checkHostPort(field, u.Host)
	default:
		return fail(field, "unsupported scheme in %q (use unix:// or tcp://)", endpoint)
	}
	return nil
}

// checkHostPort verifies hostport is host:port with a non-empty host and a numeric port.
func checkHostPort(field, hostport string) *ValidationError {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil || host == "" {
		return fail(field, "%q must be host:port", hostport)
	}
	return checkPort(field, port)
}

func checkPort(field, port string) *ValidationError {
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > maxPort {
		return fail(field, "invalid port %q", port)
	}
	return nil
}

// checkListen verifies a listen address ("[host]:port"; host may be empty).
func checkListen(field, addr string) *ValidationError {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fail(field, "%q must be [host]:port", addr)
	}
	return checkPort(field, port)
}

func checkTLSPair(prefix string, t TLSConfig) *ValidationError {
	if (t.Cert == "") != (t.Key == "") {
		return fail(prefix+".cert", "cert and key must be set together")
	}
	return nil
}

func checkSyncDB(p *ValidationErrors, cfg Config) {
	s := cfg.Sync
	if s.Interval < MinSyncInterval {
		p.addf("sync.interval", "must be >= %s, got %s", MinSyncInterval, s.Interval)
	}
	if s.RemoveDelay < 0 {
		p.addf("sync.remove_delay", "must be >= 0, got %s", s.RemoveDelay)
	}
	if s.OrphanTTL < 0 {
		p.addf("sync.orphan_ttl", "must be >= 0, got %s", s.OrphanTTL)
	}
	if s.AgentGrace < MinAgentGrace {
		p.addf("sync.agent_grace", "must be >= 5m, got %s", s.AgentGrace)
	}
	if strings.TrimSpace(cfg.DB.Path) == "" {
		p.addf("db.path", "must not be empty")
	}
}

func checkAPI(p *ValidationErrors, cfg Config) {
	a := cfg.API
	if !a.Enabled {
		return
	}
	p.add(checkListen("api.address", a.Address))
	p.add(checkTokenLen("api.token", a.Token, MinAPITokenLen, true))
	p.add(checkBasePath(a.BasePath))
}

func checkBasePath(p string) *ValidationError {
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

func validGlob(p string) bool {
	if p == "" || strings.ContainsAny(p, " \t") {
		return false
	}
	_, err := path.Match(p, "a.b")
	return err == nil
}

func checkGlobs(field string, globs []string) *ValidationError {
	for _, g := range globs {
		if !validGlob(g) {
			return fail(field, "invalid pattern %q", g)
		}
	}
	return nil
}

// checkTokenLen enforces a minimum token length (in characters). An empty
// token passes when optional is true (the feature is disabled).
func checkTokenLen(field, token string, minLen int, optional bool) *ValidationError {
	if token == "" && optional {
		return nil
	}
	if n := utf8.RuneCountInString(token); n < minLen {
		return fail(field, "must be at least %d characters, got %d (generate one with: openssl rand -hex 32)", minLen, n)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
