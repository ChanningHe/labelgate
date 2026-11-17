package agent

import (
	"fmt"
	"testing"
	"time"
)

func TestPendingLimiterPerIPAndGlobal(t *testing.T) {
	l := newPendingLimiter(6, 2)
	var releases []func()
	take := func(addr string, want bool) {
		t.Helper()
		rel, ok := l.acquire(addr)
		if ok != want {
			t.Fatalf("%s: acquire = %v, want %v (count %d)", addr, ok, want, l.count())
		}
		if ok {
			releases = append(releases, rel)
		}
	}
	take("192.0.2.1:1000", true)
	take("192.0.2.1:1001", true)
	take("192.0.2.1:1002", false) // per-IP cap
	take("[2001:db8:1:2::1]:1", true)
	take("[2001:db8:1:2::ffff]:2", true)
	take("[2001:db8:1:2:aaaa::3]:3", false) // same /64
	take("[::ffff:192.0.2.1]:4", false)     // mapped form of the same IPv4
	take("198.51.100.1:1", true)
	take("198.51.100.2:1", true)
	take("198.51.100.3:1", false) // global cap
	releases[0]()
	releases[0]() // idempotent
	if l.count() != 5 {
		t.Fatalf("count %d after release", l.count())
	}
	take("192.0.2.1:1003", true)
	take("garbage", false) // global cap again
	if peerKey("garbage") != "garbage" || peerKey("[fe80::1%eth0]:1") != "fe80::/64" {
		t.Fatalf("peer keys %q %q", peerKey("garbage"), peerKey("[fe80::1%eth0]:1"))
	}
}

func TestPendingLimiterReleaseAll(t *testing.T) {
	l := newPendingLimiter(MaxPendingHandshakes, MaxPendingPerIP)
	var rels []func()
	for i := range MaxPendingHandshakes {
		rel, ok := l.acquire(fmt.Sprintf("10.0.%d.%d:1", i/200, i%200))
		if !ok {
			t.Fatalf("slot %d refused", i)
		}
		rels = append(rels, rel)
	}
	if _, ok := l.acquire("10.9.9.9:1"); ok {
		t.Fatal("global cap not enforced")
	}
	for _, r := range rels {
		r()
	}
	if l.count() != 0 || len(l.byIP) != 0 {
		t.Fatalf("leak: count %d buckets %d", l.count(), len(l.byIP))
	}
}

func TestPreAuthDeadlines(t *testing.T) {
	const limit = 5 * time.Second
	if HandshakeTimeout > limit || readHeaderTimeout > limit {
		t.Fatalf("pre-auth deadlines too long: handshake %s, header %s", HandshakeTimeout, readHeaderTimeout)
	}
	if MaxPendingPerIP > 4 || MaxPendingPerIP < 1 {
		t.Fatalf("MaxPendingPerIP %d", MaxPendingPerIP)
	}
}
