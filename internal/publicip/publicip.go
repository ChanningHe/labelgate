// Package publicip discovers this host's public IPv4/IPv6 address.
package publicip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// DefaultTTL is how long a successful lookup is cached.
const DefaultTTL = 5 * time.Minute

const (
	// maxBodyBytes bounds an endpoint response; a bare address is far shorter.
	maxBodyBytes = 64
	// defaultTimeout applies to the fallback HTTP client.
	defaultTimeout = 5 * time.Second
	userAgent      = "labelgate"
)

// Default endpoints; each must return the bare address as text/plain.
var (
	DefaultV4Endpoints = []string{"https://api.ipify.org", "https://ipv4.icanhazip.com"}
	DefaultV6Endpoints = []string{"https://api6.ipify.org", "https://ipv6.icanhazip.com"}
)

// cacheEntry is one cached lookup result.
type cacheEntry struct {
	addr    netip.Addr
	expires time.Time
}

// Resolver looks up and caches public addresses. Safe for concurrent use.
type Resolver struct {
	client *http.Client
	v4, v6 []string
	ttl    time.Duration
	now    func() time.Time

	mu    sync.Mutex
	cache [2]cacheEntry // index 0: IPv4, 1: IPv6
}

// New returns a Resolver. client nil = http.Client with a 5s timeout;
// v4/v6 nil = defaults; ttl <= 0 = DefaultTTL; now nil = time.Now.
func New(client *http.Client, v4, v6 []string, ttl time.Duration, now func() time.Time) *Resolver {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	if v4 == nil {
		v4 = DefaultV4Endpoints
	}
	if v6 == nil {
		v6 = DefaultV6Endpoints
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if now == nil {
		now = time.Now
	}
	return &Resolver{client: client, v4: v4, v6: v6, ttl: ttl, now: now}
}

// Lookup returns the cached or freshly fetched public address of the requested
// family. Endpoints are tried in order; bodies are limited to 64 bytes; the
// result must parse as an address of the requested family. Errors are wrapped
// with the last endpoint failure. Matches model.PublicIPFunc.
func (r *Resolver) Lookup(ctx context.Context, v6 bool) (netip.Addr, error) {
	idx, endpoints := 0, r.v4
	if v6 {
		idx, endpoints = 1, r.v6
	}
	if addr, ok := r.cached(idx); ok {
		return addr, nil
	}
	addr, err := r.fetchAny(ctx, endpoints, v6)
	if err != nil {
		return netip.Addr{}, err
	}
	r.mu.Lock()
	r.cache[idx] = cacheEntry{addr: addr, expires: r.now().Add(r.ttl)}
	r.mu.Unlock()
	return addr, nil
}

func (r *Resolver) cached(idx int) (netip.Addr, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.cache[idx]
	if e.addr.IsValid() && r.now().Before(e.expires) {
		return e.addr, true
	}
	return netip.Addr{}, false
}

// fetchAny tries each endpoint in order and returns the first valid address.
func (r *Resolver) fetchAny(ctx context.Context, endpoints []string, v6 bool) (netip.Addr, error) {
	family := "IPv4"
	if v6 {
		family = "IPv6"
	}
	if len(endpoints) == 0 {
		return netip.Addr{}, fmt.Errorf("publicip: no %s endpoints configured", family)
	}
	var lastErr error
	for _, ep := range endpoints {
		addr, err := r.fetch(ctx, ep, v6)
		if err == nil {
			return addr, nil
		}
		if ctx.Err() != nil {
			return netip.Addr{}, fmt.Errorf("publicip: %s lookup: %w", family, ctx.Err())
		}
		lastErr = err
	}
	return netip.Addr{}, fmt.Errorf("publicip: all %d %s endpoints failed: %w", len(endpoints), family, lastErr)
}

// fetch queries one endpoint and validates the answer.
func (r *Resolver) fetch(ctx context.Context, endpoint string, v6 bool) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%s: %w", endpoint, err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.client.Do(req)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("%s: unexpected status %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%s: read body: %w", endpoint, err)
	}
	if len(body) > maxBodyBytes {
		return netip.Addr{}, fmt.Errorf("%s: response exceeds %d bytes", endpoint, maxBodyBytes)
	}
	addr, err := parseAddr(strings.TrimSpace(string(body)), v6)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%s: %w", endpoint, err)
	}
	return addr, nil
}

// parseAddr parses s as a usable public address of the requested family.
func parseAddr(s string, v6 bool) (netip.Addr, error) {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid address %q", s)
	}
	if addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("address %q has a zone", s)
	}
	if (v6 && (!addr.Is6() || addr.Is4In6())) || (!v6 && !addr.Is4()) {
		return netip.Addr{}, fmt.Errorf("address %q is not of the requested family", s)
	}
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsMulticast() || addr.IsLinkLocalUnicast() {
		return netip.Addr{}, errors.New("address " + s + " is not a public address")
	}
	return addr, nil
}
