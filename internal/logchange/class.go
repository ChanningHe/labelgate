package logchange

import (
	"net/netip"
	"regexp"
	"strings"
)

// addrPlaceholder replaces an IP address (with or without port) in a Class.
const addrPlaceholder = "<addr>"

var (
	// cloudflareStatus matches a Cloudflare API error from its HTTP status
	// on: the status, codes and messages vary (401, 429, ...) while the
	// failing operation stays the same.
	cloudflareStatus = regexp.MustCompile(`cloudflare (\S+): HTTP \d+.*`)
	// addrToken matches candidate IP addresses: the characters of IPv4,
	// IPv6, ports and brackets. Candidates that do not parse are kept.
	addrToken = regexp.MustCompile(`[0-9A-Fa-f.:\[\]]+`)
)

// Class reduces a problem text to its cause, for change detection: IP
// addresses and their ports (e.g. the server a dial picked) are masked, and
// a Cloudflare API error is reduced to its operation. Two texts with the same
// Class are the same problem; the full text is still what gets logged.
func Class(text string) string {
	text = cloudflareStatus.ReplaceAllString(text, "cloudflare $1: HTTP error")
	return addrToken.ReplaceAllStringFunc(text, maskAddr)
}

// maskAddr replaces tok with addrPlaceholder when it is an IP address or
// address:port, keeping trailing punctuation that belongs to the sentence.
func maskAddr(tok string) string {
	core := strings.TrimRight(tok, ".:")
	if !isAddr(core) {
		return tok
	}
	return addrPlaceholder + tok[len(core):]
}

func isAddr(s string) bool {
	if _, err := netip.ParseAddrPort(s); err == nil {
		return true
	}
	_, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]"))
	return err == nil
}
