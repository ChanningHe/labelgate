package reconcile

import (
	"fmt"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// agentSafeServices never reach a network origin and are safe for agents.
var agentSafeServices = map[string]bool{"hello_world": true}

// agentNetworkSchemes are the tunnel service schemes agents may use; unix
// sockets, bastion mode and anything else are refused.
var agentNetworkSchemes = map[string]bool{"http": true, "https": true, "tcp": true, "ssh": true, "rdp": true}

var (
	serviceLabelPattern = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)
	allDigits           = regexp.MustCompile(`^[0-9]+$`)
	ipv4Zero            = netip.MustParsePrefix("0.0.0.0/8")
	ipv4CGNAT           = netip.MustParsePrefix("100.64.0.0/10") // incl. 100.100.100.200 metadata
	ipv4Broadcast       = netip.MustParseAddr("255.255.255.255")
	ipv6SiteLocal       = netip.MustParsePrefix("fec0::/10")
	ipv6Compat          = netip.MustParsePrefix("::/96")
	ipv6Translated      = netip.MustParsePrefix("::ffff:0:0:0/96") // SIIT, RFC 2765
	ipv6NAT64           = netip.MustParsePrefix("64:ff9b::/96")
	ipv6NAT64Local      = netip.MustParsePrefix("64:ff9b:1::/48") // RFC 8215: any embedding layout
)

// checkAgentService is the SSRF guard for agent-sourced tunnel services
// (decisions addendum). The origin host must be either
//   - explicitly allowed by the agent's services allow-list (hostname globs,
//     IP addresses, CIDRs), or
//   - a strictly parsed IP literal that is not loopback, unspecified,
//     link-local, multicast, 0.0.0.0/8, CGNAT 100.64.0.0/10, broadcast,
//     IPv6 site-local, NAT64 local-use 64:ff9b:1::/48, zoned, or an
//     IPv4-mapped/compatible/translated/NAT64 embedding of such an address.
//
// Host names (which the agent host resolves, e.g. to 127.0.0.1 or a cloud
// metadata service) are refused unless allow-listed; inet_aton forms such
// as 127.1 or 0x7f000001 are never accepted.
//
// Every error names the agent, the offending value and, where an allow-list
// can fix it, the config key and the literal entry to add. It is stored as
// the resource's last_error, so it stays on one line.
func checkAgentService(id string, p AgentPolicy, service string) error {
	s := strings.TrimSpace(service)
	lower := strings.ToLower(s)
	if agentSafeServices[lower] || strings.HasPrefix(lower, "http_status:") {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || !agentNetworkSchemes[strings.ToLower(u.Scheme)] {
		return fmt.Errorf("agent %q: service %q is not allowed: agents may only use http, https, tcp, ssh or rdp URLs (no allow-list can permit other schemes)", id, s)
	}
	if u.User != nil {
		return fmt.Errorf("agent %q: service URL %q must not contain user info; remove it from the URL (no allow-list can permit it)", id, u.Redacted())
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("agent %q: service URL %q has no host; use a URL such as http://<ip>:<port>", id, s)
	}
	if serviceAllowed(host, p.Services) {
		return nil
	}
	return serviceHostError(id, p, host)
}

// serviceHostError explains why host, which is not allow-listed, is refused;
// nil when it is an IP literal that is fine by default.
func serviceHostError(id string, p AgentPolicy, host string) error {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		if !validServiceHost(host) {
			return fmt.Errorf("agent %q: service host %q is not a valid host name or canonical IP address (shorthand forms such as 127.1 are never accepted, not even allow-listed); use a DNS name or the full IP address", id, host)
		}
		return fmt.Errorf("agent %q: service host %q is a host name, which agents may only use if allow-listed; add %q to %s (or use the origin's IP address)", id, host, host, p.fixHint(id, "services"))
	}
	reason := forbiddenReason(addr)
	switch {
	case reason == "":
		return nil
	case addr.Zone() != "":
		return fmt.Errorf("agent %q: service target is the %s %s, which is never accepted; use an address without a zone", id, reason, host)
	}
	return fmt.Errorf("agent %q: service target is the %s %s, which agents may not use unless allow-listed; if intended, add %q (or a CIDR containing it) to %s", id, reason, host, host, p.fixHint(id, "services"))
}

// serviceAllowed reports whether host matches the allow-list: IP hosts by IP
// or CIDR entries, valid host names by glob entries.
func serviceAllowed(host string, allow []string) bool {
	addr, err := netip.ParseAddr(host)
	isIP := err == nil
	if (isIP && addr.Zone() != "") || (!isIP && !validServiceHost(host)) {
		return false
	}
	for _, e := range allow {
		e = strings.ToLower(strings.TrimSpace(e))
		if isIP && ipEntryMatches(e, addr) {
			return true
		}
		if !isIP && !isIPEntry(e) {
			if ok, err := path.Match(e, host); err == nil && ok {
				return true
			}
		}
	}
	return false
}

func isIPEntry(e string) bool {
	if _, err := netip.ParsePrefix(e); err == nil {
		return true
	}
	_, err := netip.ParseAddr(e)
	return err == nil
}

func ipEntryMatches(e string, addr netip.Addr) bool {
	addr = addr.Unmap()
	if p, err := netip.ParsePrefix(e); err == nil {
		return p.Masked().Contains(addr)
	}
	a, err := netip.ParseAddr(e)
	return err == nil && a.Unmap() == addr
}

// validServiceHost accepts DNS names (single labels such as compose service
// names included) and rejects trailing dots, empty labels and numeric
// last labels, which resolvers may treat as inet_aton IPv4 forms.
func validServiceHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		if !serviceLabelPattern.MatchString(l) {
			return false
		}
	}
	last := labels[len(labels)-1]
	return !allDigits.MatchString(last) && !strings.HasPrefix(last, "0x")
}

// forbiddenReason names the category of an IP literal that reaches the agent
// host itself, its link or a metadata service; "" when the address is fine.
func forbiddenReason(a netip.Addr) string {
	if a.Zone() != "" {
		return "zoned address"
	}
	a = a.Unmap()
	if a.Is4() {
		return forbiddenV4Reason(a)
	}
	if r := forbiddenV6Reason(a); r != "" {
		return r
	}
	if ipv6Compat.Contains(a) || ipv6Translated.Contains(a) || ipv6NAT64.Contains(a) {
		b := a.As16()
		if r := forbiddenV4Reason(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})); r != "" {
			return "IPv4-embedding " + r
		}
	}
	return ""
}

func forbiddenV6Reason(a netip.Addr) string {
	switch {
	case a.IsLoopback():
		return "loopback address"
	case a.IsUnspecified():
		return "unspecified address"
	case a.IsLinkLocalUnicast():
		return "link-local address"
	case a.IsMulticast():
		return "multicast address"
	case ipv6SiteLocal.Contains(a):
		return "site-local address"
	case ipv6NAT64Local.Contains(a):
		return "NAT64 local-use address"
	}
	return ""
}

func forbiddenV4Reason(a netip.Addr) string {
	switch {
	case a.IsLoopback():
		return "loopback address"
	case a.IsUnspecified(), ipv4Zero.Contains(a):
		return "unspecified (0.0.0.0/8) address"
	case a.IsLinkLocalUnicast():
		return "link-local address"
	case a.IsMulticast():
		return "multicast address"
	case ipv4CGNAT.Contains(a):
		return "carrier-grade NAT address"
	case a == ipv4Broadcast:
		return "broadcast address"
	}
	return ""
}
