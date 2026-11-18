package reconcile

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/channinghe/labelgate/internal/model"
)

// hostnameAllowed reports whether host matches one of the globs (path.Match
// on lower-case names). An empty list allows every hostname except
// wildcards: a wildcard hostname ("*.x") is allowed only by an entry equal
// to it, never by a glob match or an unrestricted list.
func hostnameAllowed(globs []string, host string) bool {
	if strings.HasPrefix(host, "*") {
		return slices.ContainsFunc(globs, func(g string) bool { return strings.ToLower(strings.TrimSpace(g)) == host })
	}
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		if ok, err := path.Match(strings.ToLower(strings.TrimSpace(g)), host); err == nil && ok {
			return true
		}
	}
	return false
}

// tunnelDomain is the zone of tunnel CNAME targets (<tunnel-id>.cfargotunnel.com).
const tunnelDomain = "cfargotunnel.com"

// agentDNSReject refuses agent CNAMEs into Cloudflare tunnels: tunnels are
// only reachable through tunnel claims, which the tunnel allow-list governs.
func agentDNSReject(c claim) string {
	t := model.NormalizeHostname(c.Res.Content)
	if c.Res.RecordType == model.DNSTypeCNAME && (t == tunnelDomain || strings.HasSuffix(t, "."+tunnelDomain)) {
		return fmt.Sprintf("agent %q: a CNAME to %s is not allowed (no allow-list can permit it, because it would bypass the tunnel allow-list); use a tunnel label (labelgate.tunnel.<name>.hostname/service) instead", c.Src.AgentID, t)
	}
	return ""
}

// agentHostnameReject explains a hostname the agent's allow-list refuses. The
// message names the key to extend in the main instance config and the literal
// entry that fixes it.
func agentHostnameReject(id string, p AgentPolicy, host string) string {
	fix := p.fixHint(id, "hostnames")
	if strings.HasPrefix(host, "*") {
		return fmt.Sprintf("agent %q: wildcard hostname %q is only allowed by an identical entry (globs such as \"*\" never match it, and an empty list does not allow it); add %q to %s", id, host, host, fix)
	}
	return fmt.Sprintf("agent %q: hostname %q does not match the agent's hostnames allow-list; add %q (or a glob matching it) to %s", id, host, host, fix)
}

// agentReject returns the rejection reason for an agent-sourced DNS or tunnel
// claim, or "" when the claim satisfies the agent's policy. Every reason is a
// single line naming the agent, the rejected value and how to fix it.
func agentReject(c claim, allowedTunnels map[string]bool) string {
	id, p := c.Src.AgentID, c.Src.Policy
	if !hostnameAllowed(p.Hostnames, c.Res.Hostname) {
		return agentHostnameReject(id, p, c.Res.Hostname)
	}
	if c.Res.Kind == model.KindDNS {
		return agentDNSReject(c)
	}
	if c.Res.Kind != model.KindTunnel {
		return ""
	}
	if !allowedTunnels[c.Res.TunnelID] {
		return fmt.Sprintf("agent %q: tunnel %q is not in the agent's allowed tunnels; add %q to %s", id, c.Tunnel.Ref, c.Tunnel.Ref, p.fixHint(id, "tunnels"))
	}
	if err := checkAgentService(id, p, c.Res.Service); err != nil {
		return err.Error()
	}
	return ""
}
