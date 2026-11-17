package agent

import (
	"net"
	"net/netip"
	"sync"
)

// ipv6Bucket is the prefix length grouping IPv6 peers: a single host
// usually controls a whole /64.
const ipv6Bucket = 64

// pendingLimiter bounds concurrent unauthenticated connections, globally and
// per remote address (IPv4 address or IPv6 /64), so one peer cannot hold
// every pre-auth slot. Safe for concurrent use.
type pendingLimiter struct {
	max, perIP int

	mu    sync.Mutex
	total int
	byIP  map[string]int
}

func newPendingLimiter(max, perIP int) *pendingLimiter {
	return &pendingLimiter{max: max, perIP: perIP, byIP: make(map[string]int)}
}

// acquire takes a slot for remoteAddr ("host:port"). The returned release
// func is idempotent; ok=false means no slot was taken.
func (l *pendingLimiter) acquire(remoteAddr string) (release func(), ok bool) {
	key := peerKey(remoteAddr)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.max || l.byIP[key] >= l.perIP {
		return nil, false
	}
	l.total++
	l.byIP[key]++
	var once sync.Once
	return func() { once.Do(func() { l.release(key) }) }, true
}

func (l *pendingLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total--
	if l.byIP[key]--; l.byIP[key] <= 0 {
		delete(l.byIP, key)
	}
}

// count returns the number of slots in use.
func (l *pendingLimiter) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.total
}

// peerKey is the limiter bucket of a remote address.
func peerKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is6() {
		p, _ := addr.Prefix(ipv6Bucket)
		return p.String()
	}
	return addr.String()
}
