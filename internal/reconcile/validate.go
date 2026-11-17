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

// agentReject returns the rejection reason for an agent-sourced DNS or tunnel
// claim, or "" when the claim satisfies the agent's policy.
func agentReject(c claim, allowedTunnels map[string]bool) string {
	id := c.Src.AgentID
	if !hostnameAllowed(c.Src.Policy.Hostnames, c.Res.Hostname) {
		return fmt.Sprintf("agent %s may not claim hostname %s", id, c.Res.Hostname)
	}
	if c.Res.Kind != model.KindTunnel {
		return ""
	}
	if !allowedTunnels[c.Res.TunnelID] {
		return fmt.Sprintf("agent %s may not use tunnel %s", id, c.Tunnel.Ref)
	}
	if err := checkAgentService(c.Res.Service, c.Src.Policy.Services); err != nil {
		return err.Error()
	}
	return ""
}
