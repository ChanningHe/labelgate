package publicip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock.
type fakeClock struct{ t atomic.Int64 }

func newClock() *fakeClock {
	c := &fakeClock{}
	c.t.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	return c
}
func (c *fakeClock) Now() time.Time          { return time.Unix(0, c.t.Load()).UTC() }
func (c *fakeClock) Advance(d time.Duration) { c.t.Add(int64(d)) }

// endpoint serves a fixed body and counts hits.
func endpoint(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestLookup_ParsesAndTrims(t *testing.T) {
	tests := []struct {
		name string
		v6   bool
		body string
		want string
	}{
		{"ipv4 with newline", false, "203.0.113.7\n", "203.0.113.7"},
		{"ipv4 padded", false, "  198.51.100.2  \r\n", "198.51.100.2"},
		{"ipv6", true, "2001:db8::1\n", "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := endpoint(t, 200, tt.body)
			r := New(srv.Client(), []string{srv.URL}, []string{srv.URL}, time.Minute, nil)
			got, err := r.Lookup(context.Background(), tt.v6)
			if err != nil || got.String() != tt.want {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestLookup_Rejects(t *testing.T) {
	tests := []struct {
		name   string
		v6     bool
		status int
		body   string
		errSub string
	}{
		{"v6 address for v4 lookup", false, 200, "2001:db8::1", "not of the requested family"},
		{"v4 address for v6 lookup", true, 200, "203.0.113.7", "not of the requested family"},
		{"v4-mapped v6", true, 200, "::ffff:203.0.113.7", "not of the requested family"},
		{"garbage", false, 200, "<html>nope</html>", "invalid address"},
		{"empty", false, 200, "", "invalid address"},
		{"oversized", false, 200, strings.Repeat("1", 65), "exceeds 64 bytes"},
		{"zone", true, 200, "fe80::1%eth0", "has a zone"},
		{"unspecified", false, 200, "0.0.0.0", "not a public address"},
		{"loopback", false, 200, "127.0.0.1", "not a public address"},
		{"link-local", true, 200, "fe80::1", "not a public address"},
		{"server error", false, 500, "oops", "unexpected status 500"},
		{"not found", false, 404, "203.0.113.7", "unexpected status 404"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := endpoint(t, tt.status, tt.body)
			r := New(srv.Client(), []string{srv.URL}, []string{srv.URL}, time.Minute, nil)
			got, err := r.Lookup(context.Background(), tt.v6)
			if err == nil || !strings.Contains(err.Error(), tt.errSub) {
				t.Fatalf("want error containing %q, got %v, %v", tt.errSub, got, err)
			}
			if got.IsValid() {
				t.Errorf("address must be zero on error, got %v", got)
			}
		})
	}
}

func TestLookup_BodyAtLimitIsAccepted(t *testing.T) {
	// 39 chars max for IPv6; pad with whitespace to exactly 64 bytes.
	body := "2001:db8::1" + strings.Repeat(" ", 64-len("2001:db8::1"))
	srv, _ := endpoint(t, 200, body)
	r := New(srv.Client(), nil, []string{srv.URL}, 0, nil)
	if _, err := r.Lookup(context.Background(), true); err != nil {
		t.Fatalf("64-byte body must be accepted: %v", err)
	}
}

func TestLookup_FallbackOrder(t *testing.T) {
	bad, badHits := endpoint(t, 500, "")
	mismatch, mismatchHits := endpoint(t, 200, "2001:db8::1")
	good, goodHits := endpoint(t, 200, "203.0.113.9")
	never, neverHits := endpoint(t, 200, "203.0.113.10")

	r := New(good.Client(), []string{bad.URL, mismatch.URL, good.URL, never.URL}, nil, time.Minute, nil)
	got, err := r.Lookup(context.Background(), false)
	if err != nil || got.String() != "203.0.113.9" {
		t.Fatalf("got %v, %v", got, err)
	}
	if badHits.Load() != 1 || mismatchHits.Load() != 1 || goodHits.Load() != 1 || neverHits.Load() != 0 {
		t.Errorf("hits bad/mismatch/good/never = %d/%d/%d/%d",
			badHits.Load(), mismatchHits.Load(), goodHits.Load(), neverHits.Load())
	}
}

func TestLookup_AllEndpointsFail(t *testing.T) {
	a, _ := endpoint(t, 500, "")
	b, _ := endpoint(t, 503, "")
	r := New(a.Client(), []string{a.URL, b.URL}, nil, time.Minute, nil)
	_, err := r.Lookup(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "all 2 IPv4 endpoints failed") ||
		!strings.Contains(err.Error(), "status 503") {
		t.Fatalf("want wrapped last failure, got %v", err)
	}
}

func TestLookup_NoEndpoints(t *testing.T) {
	r := New(nil, []string{}, []string{}, 0, nil)
	for _, v6 := range []bool{false, true} {
		if _, err := r.Lookup(context.Background(), v6); err == nil || !strings.Contains(err.Error(), "no") {
			t.Errorf("v6=%v: want no-endpoints error, got %v", v6, err)
		}
	}
}

func TestLookup_ConnectionError(t *testing.T) {
	srv, _ := endpoint(t, 200, "203.0.113.9")
	url := srv.URL
	srv.Close()
	r := New(nil, []string{url}, nil, 0, nil)
	if _, err := r.Lookup(context.Background(), false); err == nil {
		t.Fatal("want error for closed server")
	}
}

func TestLookup_InvalidEndpointURL(t *testing.T) {
	r := New(nil, []string{"http://bad host/\x7f"}, nil, 0, nil)
	if _, err := r.Lookup(context.Background(), false); err == nil {
		t.Fatal("want error for invalid URL")
	}
}

func TestLookup_ContextCancelled(t *testing.T) {
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hang:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(hang); srv.Close() })
	second, secondHits := endpoint(t, 200, "203.0.113.9")

	r := New(srv.Client(), []string{srv.URL, second.URL}, nil, 0, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := r.Lookup(ctx, false)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("want context error, got %v", err)
	}
	if secondHits.Load() != 0 {
		t.Error("must not try further endpoints after the context ended")
	}
}

func TestLookup_TTLCacheWithInjectedClock(t *testing.T) {
	srv, hits := endpoint(t, 200, "203.0.113.9")
	v6srv, v6hits := endpoint(t, 200, "2001:db8::9")
	clock := newClock()
	r := New(srv.Client(), []string{srv.URL}, []string{v6srv.URL}, time.Minute, clock.Now)
	ctx := context.Background()

	lookup := func(v6 bool) {
		t.Helper()
		if _, err := r.Lookup(ctx, v6); err != nil {
			t.Fatal(err)
		}
	}
	lookup(false)
	lookup(false)
	if hits.Load() != 1 {
		t.Fatalf("second lookup within TTL must be cached, hits=%d", hits.Load())
	}
	lookup(true)
	if v6hits.Load() != 1 || hits.Load() != 1 {
		t.Fatalf("families cache independently: v4=%d v6=%d", hits.Load(), v6hits.Load())
	}
	clock.Advance(59 * time.Second)
	lookup(false)
	if hits.Load() != 1 {
		t.Fatalf("still within TTL, hits=%d", hits.Load())
	}
	clock.Advance(time.Second)
	lookup(false)
	if hits.Load() != 2 {
		t.Fatalf("expired entry must be refetched, hits=%d", hits.Load())
	}
}

func TestLookup_FailureIsNotCached(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte("203.0.113.9"))
	}))
	t.Cleanup(srv.Close)
	r := New(srv.Client(), []string{srv.URL}, nil, time.Hour, nil)
	if _, err := r.Lookup(context.Background(), false); err == nil {
		t.Fatal("want error")
	}
	fail.Store(false)
	if got, err := r.Lookup(context.Background(), false); err != nil || got.String() != "203.0.113.9" {
		t.Fatalf("recovery lookup: %v, %v", got, err)
	}
}

func TestLookup_ConcurrentSafe(t *testing.T) {
	srv, _ := endpoint(t, 200, "203.0.113.9")
	r := New(srv.Client(), []string{srv.URL}, []string{srv.URL}, time.Minute, nil)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 20; j++ {
				_, _ = r.Lookup(context.Background(), j%2 == 0)
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}

func TestNew_Defaults(t *testing.T) {
	r := New(nil, nil, nil, 0, nil)
	if r.client == nil || r.client.Timeout != defaultTimeout {
		t.Errorf("default client: %+v", r.client)
	}
	if len(r.v4) != len(DefaultV4Endpoints) || len(r.v6) != len(DefaultV6Endpoints) {
		t.Errorf("default endpoints not applied")
	}
	if r.ttl != DefaultTTL || r.now == nil {
		t.Errorf("ttl/now defaults not applied")
	}
}
