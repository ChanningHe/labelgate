package config

import (
	"net/netip"
	"net/url"
	"strings"

	"github.com/channinghe/labelgate/internal/model"
)

// checkServices validates a tunnel-origin allow-list: CIDRs and IP
// addresses must parse strictly; anything else is a hostname glob whose last
// label must not be numeric (inet_aton forms such as 127.1 are no IPs here).
func checkServices(field string, entries []string) *ValidationError {
	for _, e := range entries {
		e = strings.ToLower(strings.TrimSpace(e))
		if strings.Contains(e, "/") {
			if _, err := netip.ParsePrefix(e); err != nil {
				return fail(field, "invalid CIDR %q", e)
			}
			continue
		}
		if _, err := netip.ParseAddr(e); err == nil {
			continue
		}
		if !validGlob(e) {
			return fail(field, "invalid entry %q (use a hostname glob, an IP address or a CIDR)", e)
		}
		labels := strings.Split(e, ".")
		last := labels[len(labels)-1]
		if strings.HasPrefix(last, "0x") || strings.Trim(last, "0123456789") == "" {
			return fail(field, "%q is not a canonical IP address or hostname (write IPv4 as a.b.c.d)", e)
		}
	}
	return nil
}

// checkAgentToken enforces length and character variety of an agent token
// (an HMAC key that a captured handshake lets attackers brute-force offline).
func checkAgentToken(field, token string, optional bool) *ValidationError {
	if ve := checkTokenLen(field, token, MinAgentTokenLen, optional); ve != nil || token == "" {
		return ve
	}
	distinct := map[rune]bool{}
	for _, r := range token {
		distinct[r] = true
	}
	if len(distinct) < MinAgentTokenDistinct {
		return fail(field, "must contain at least %d distinct characters, got %d (generate one with: openssl rand -hex 32)",
			MinAgentTokenDistinct, len(distinct))
	}
	return nil
}

// checkRequiredAgentToken reports a blank token as "must not be empty" and
// otherwise applies the strength rules, so one field yields one problem.
func checkRequiredAgentToken(field, token, emptyMsg string) *ValidationError {
	if strings.TrimSpace(token) == "" {
		return fail(field, "%s", emptyMsg)
	}
	return checkAgentToken(field, token, false)
}

func checkAgentServer(p *ValidationErrors, cfg Config) {
	a := cfg.Agent
	p.add(checkAgentToken("agent.accept_token", a.AcceptToken, true))
	p.add(checkTLSPair("agent.tls", a.TLS))
	if a.Enabled {
		p.add(checkListen("agent.listen", a.Listen))
	}
	p.add(checkGlobs("agent.accept_hostnames", a.AcceptHostnames))
	p.add(checkTunnelRefs(cfg.Cloudflare, "agent.accept_tunnels", a.AcceptTunnels))
	p.add(checkServices("agent.accept_services", a.AcceptServices))
	for _, id := range sortedKeys(a.Agents) {
		checkAgentEntry(p, cfg.Cloudflare, id, a.Agents[id])
	}
}

func checkAgentEntry(p *ValidationErrors, cf CloudflareConfig, id string, e AgentEntry) {
	field := "agent.agents." + id
	if !model.ValidAgentID(id) {
		p.addf(field, "invalid agent ID %q (must match ^[a-z0-9][a-z0-9._-]{0,62}$)", id)
	}
	p.add(checkRequiredAgentToken(field+".token", e.Token, "must not be empty"))
	if e.ConnectTo != "" {
		p.add(checkWebSocketURL(field+".connect_to", e.ConnectTo))
	}
	if e.DefaultTunnel != "" && !resolvableTunnel(cf, e.DefaultTunnel) {
		p.addf(field+".default_tunnel", "unknown tunnel %q", e.DefaultTunnel)
	}
	p.add(checkTunnelRefs(cf, field+".tunnels", e.Tunnels))
	p.add(checkGlobs(field+".hostnames", e.Hostnames))
	p.add(checkServices(field+".services", e.Services))
}

func checkWebSocketURL(field, raw string) *ValidationError {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return fail(field, "%q must be a ws:// or wss:// URL", raw)
	}
	return nil
}

func checkConnect(p *ValidationErrors, cfg Config) {
	c := cfg.Connect
	p.add(checkRequiredAgentToken("connect.token", c.Token, "must not be empty in agent mode"))
	if c.AgentID != "" && !model.ValidAgentID(c.AgentID) {
		p.addf("connect.agent_id", "%q must match ^[a-z0-9][a-z0-9._-]{0,62}$", c.AgentID)
	}
	if c.HeartbeatInterval < MinHeartbeatInterval {
		p.addf("connect.heartbeat_interval", "must be >= %s, got %s", MinHeartbeatInterval, c.HeartbeatInterval)
	}
	p.add(checkTLSPair("connect.tls", c.TLS))
	// Listen vs endpoint depends on the mode; an invalid mode is reported by checkCore.
	switch c.Mode {
	case ConnectInbound:
		p.add(checkConnectListen(c.Listen))
	case ConnectOutbound:
		p.add(checkConnectEndpoint(c.Endpoint))
	}
}

func checkConnectListen(listen string) *ValidationError {
	if listen == "" {
		return fail("connect.listen", "required in inbound mode")
	}
	return checkListen("connect.listen", listen)
}

func checkConnectEndpoint(endpoint string) *ValidationError {
	const field = "connect.endpoint"
	if endpoint == "" {
		return fail(field, "required in outbound mode")
	}
	if ve := checkWebSocketURL(field, endpoint); ve != nil {
		return ve
	}
	if u, _ := url.Parse(endpoint); strings.Trim(u.Path, "/") == "" {
		return fail(field, "%q has no path (did you mean %s/ws?)", endpoint, strings.TrimRight(endpoint, "/"))
	}
	return nil
}
