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
func checkAgentService(service string, allow []string) error {
	s := strings.TrimSpace(service)
	lower := strings.ToLower(s)
	if agentSafeServices[lower] || strings.HasPrefix(lower, "http_status:") {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || !agentNetworkSchemes[strings.ToLower(u.Scheme)] {
		return fmt.Errorf("service %q is not allowed for agents (use http, https, tcp, ssh or rdp)", s)
	}
	if u.User != nil {
		return fmt.Errorf("service URL %q must not contain user info", s)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("service URL %q has no host", s)
	}
	if serviceAllowed(host, allow) {
		return nil
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("service host %q is not allowed for agents: use an IP address or add it to the agent's services allow-list", host)
	}
	if forbiddenAddr(addr) {
		return fmt.Errorf("service address %s is not allowed for agents", host)
	}
	return nil
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

// forbiddenAddr reports whether an IP literal reaches the agent host itself,
// its link or a metadata service.
func forbiddenAddr(a netip.Addr) bool {
	if a.Zone() != "" {
		return true
	}
	a = a.Unmap()
	if a.Is4() {
		return forbiddenV4(a)
	}
	if ipv6Compat.Contains(a) || ipv6Translated.Contains(a) || ipv6NAT64.Contains(a) {
		b := a.As16()
		if forbiddenV4(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})) {
			return true
		}
	}
	return a.IsLoopback() || a.IsUnspecified() || a.IsLinkLocalUnicast() || a.IsMulticast() ||
		ipv6SiteLocal.Contains(a) || ipv6NAT64Local.Contains(a)
}

func forbiddenV4(a netip.Addr) bool {
	return a.IsLoopback() || a.IsUnspecified() || a.IsLinkLocalUnicast() || a.IsMulticast() ||
		ipv4Zero.Contains(a) || ipv4CGNAT.Contains(a) || a == ipv4Broadcast
}
