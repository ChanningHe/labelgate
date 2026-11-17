// Package publicip discovers this host's public IPv4/IPv6 address.
package publicip

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"time"
)

// DefaultTTL is how long a successful lookup is cached.
const DefaultTTL = 5 * time.Minute

// Default endpoints; each must return the bare address as text/plain.
var (
	DefaultV4Endpoints = []string{"https://api.ipify.org", "https://ipv4.icanhazip.com"}
	DefaultV6Endpoints = []string{"https://api6.ipify.org", "https://ipv6.icanhazip.com"}
)

// Resolver looks up and caches public addresses. Safe for concurrent use.
type Resolver struct {
	client *http.Client
	v4, v6 []string
	ttl    time.Duration
	now    func() time.Time
}

// New returns a Resolver. client nil = http.Client with a 5s timeout;
// v4/v6 nil = defaults; ttl <= 0 = DefaultTTL; now nil = time.Now.
func New(client *http.Client, v4, v6 []string, ttl time.Duration, now func() time.Time) *Resolver {
	return &Resolver{client: client, v4: v4, v6: v6, ttl: ttl, now: now}
}

// Lookup returns the cached or freshly fetched public address of the requested
// family. Endpoints are tried in order; bodies are limited to 64 bytes; the
// result must parse as an address of the requested family. Errors are wrapped
// with the last endpoint failure. Matches model.PublicIPFunc.
func (r *Resolver) Lookup(ctx context.Context, v6 bool) (netip.Addr, error) {
	return netip.Addr{}, errors.New("publicip.Lookup: not implemented")
}
