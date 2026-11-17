package reconcile

import (
	"fmt"
	"net/netip"
	"net/url"
	"path"
	"strings"

	"github.com/channinghe/labelgate/internal/model"
)

// hostnameAllowed reports whether host matches one of the globs (path.Match
// on lower-case names). An empty list allows every hostname.
func hostnameAllowed(globs []string, host string) bool {
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

// agentServiceSchemes that never reach a network origin and are safe for agents.
var agentSafeSchemes = map[string]bool{"hello_world": true}

// agentDeniedSchemes reach local sockets or arbitrary destinations.
var agentDeniedSchemes = map[string]bool{"unix": true, "unix+tls": true, "bastion": true}

// checkAgentService is the SSRF guard for agent-sourced tunnel services
// (§6.3): no unix sockets, no bastion mode, no localhost names and no
// loopback, link-local, unspecified or multicast IP literals.
func checkAgentService(service string) error {
	s := strings.TrimSpace(service)
	lower := strings.ToLower(s)
	if agentSafeSchemes[lower] || strings.HasPrefix(lower, "http_status:") {
		return nil
	}
	if agentDeniedSchemes[lower] {
		return fmt.Errorf("service %q is not allowed for agents", s)
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("invalid service URL %q", s)
	}
	scheme := strings.ToLower(u.Scheme)
	if agentDeniedSchemes[scheme] {
		return fmt.Errorf("service scheme %q is not allowed for agents", scheme)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return fmt.Errorf("service URL %q has no host", s)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("service host %q is not allowed for agents", host)
	}
	if addr, err := netip.ParseAddr(host); err == nil && forbiddenAddr(addr) {
		return fmt.Errorf("service address %s is not allowed for agents", host)
	}
	return nil
}

func forbiddenAddr(a netip.Addr) bool {
	a = a.WithZone("").Unmap()
	return a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsUnspecified() || a.IsMulticast()
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
	if err := checkAgentService(c.Res.Service); err != nil {
		return err.Error()
	}
	return ""
}
