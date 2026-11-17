package cloudflare

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
)

var testZone = Zone{ID: "z1", Name: "example.com", AccountID: "acct1", Credential: "default"}

// recordSleeps replaces the client's sleep with a recorder that does not wait.
func recordSleeps(c *Client) *[]time.Duration {
	var mu sync.Mutex
	var got []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, d)
		return ctx.Err()
	}
	return &got
}

func TestRetryOn429HonoursRetryAfter(t *testing.T) {
	// Arrange
	f := newFakeCF(t)
	c := f.client(t, rootCfg(), func(o *Options) { o.Retry.MaxDelay = time.Minute })
	sleeps := recordSleeps(c)
	f.inject("GET", "/zones/z1/dns_records", 2, 429, cfErrorBody(10000, "rate limited"), map[string]string{"Retry-After": "7"})

	// Act
	_, err := c.ListDNSRecords(context.Background(), testZone)

	// Assert
	if err != nil {
		t.Fatalf("expected success after retries: %v", err)
	}
	if n := f.count("GET", "/zones/z1/dns_records"); n != 3 {
		t.Fatalf("requests = %d, want 3", n)
	}
	if !slices.Equal(*sleeps, []time.Duration{7 * time.Second, 7 * time.Second}) {
		t.Fatalf("sleeps = %v", *sleeps)
	}
}

func TestRetryOn5xxThenGiveUp(t *testing.T) {
	f := newFakeCF(t)
	c := f.client(t, rootCfg())
	sleeps := recordSleeps(c)
	f.inject("GET", "/zones/z1/dns_records", 5, 502, "<html>bad gateway</html>", nil)

	_, err := c.ListDNSRecords(context.Background(), testZone)

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 502 || apiErr.Op != "dns.list" {
		t.Fatalf("want APIError 502, got %#v", err)
	}
	if n := f.count("GET", "/zones/z1/dns_records"); n != 3 {
		t.Fatalf("requests = %d, want 3 (attempts)", n)
	}
	if len(*sleeps) != 2 {
		t.Fatalf("sleeps = %v", *sleeps)
	}
}

func TestNoRetryOn4xxAndPost5xx(t *testing.T) {
	tests := []struct {
		name, method, path string
		status             int
		call               func(c *Client) error
	}{
		{"get 400", "GET", "/zones/z1/dns_records", 400, func(c *Client) error {
			_, err := c.ListDNSRecords(context.Background(), testZone)
			return err
		}},
		{"post 500", "POST", "/zones/z1/dns_records", 500, func(c *Client) error {
			_, err := c.CreateDNSRecord(context.Background(), testZone, DNSRecord{Name: "a.example.com", Type: model.DNSTypeA, Content: "1.2.3.4"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCF(t)
			c := f.client(t, rootCfg())
			f.inject(tt.method, tt.path, 3, tt.status, cfErrorBody(1, "x"), nil)
			if err := tt.call(c); err == nil {
				t.Fatal("expected error")
			}
			if n := f.count(tt.method, tt.path); n != 1 {
				t.Fatalf("requests = %d, want 1", n)
			}
		})
	}
}

func TestPostRetriedOn429(t *testing.T) {
	f := newFakeCF(t)
	c := f.client(t, rootCfg())
	f.inject("POST", "/zones/z1/dns_records", 1, 429, cfErrorBody(971, "throttled"), nil)
	rec, err := c.CreateDNSRecord(context.Background(), testZone, DNSRecord{Name: "a.example.com", Type: model.DNSTypeA, Content: "1.2.3.4"})
	if err != nil || rec.ID == "" {
		t.Fatalf("got %+v %v", rec, err)
	}
}

func TestPerRequestTimeout(t *testing.T) {
	// Arrange: the server hangs on every attempt.
	f := newFakeCF(t)
	c := f.client(t, rootCfg(), func(o *Options) { o.Retry.Attempts = 2 })
	c.timeout = 50 * time.Millisecond
	f.hang("GET", "/zones/z1/dns_records", 2)

	// Act
	start := time.Now()
	_, err := c.ListDNSRecords(context.Background(), testZone)

	// Assert
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want timeout error, got %v", err)
	}
	if n := f.count("GET", "/zones/z1/dns_records"); n != 2 {
		t.Fatalf("timeouts must be retried: requests = %d", n)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout not enforced")
	}
	if h := c.Health(); h.Reachable {
		t.Fatalf("timeout must mark unreachable: %+v", h)
	}
}

func TestContextCancellationStopsRetries(t *testing.T) {
	f := newFakeCF(t)
	c := f.client(t, rootCfg(), func(o *Options) {
		o.Retry = config.RetryConfig{Attempts: 5, Delay: time.Hour, MaxDelay: time.Hour, Backoff: 1}
	})
	f.inject("GET", "/zones/z1/dns_records", 5, 503, cfErrorBody(1, "down"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.ListDNSRecords(ctx, testZone)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want ctx error, got %v", err)
	}
	if n := f.count("GET", "/zones/z1/dns_records"); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
		codes  []int
	}{
		{"not found", 404, cfErrorBody(81044, "Record does not exist."), ErrNotFound, []int{81044}},
		{"unauthorized", 401, cfErrorBody(10000, "Authentication error"), ErrUnauthorized, []int{10000}},
		{"forbidden", 403, cfErrorBody(9109, "Unauthorized to access requested resource"), ErrUnauthorized, []int{9109}},
		{"cname clash", 400, cfErrorBody(81053, "exists"), ErrAlreadyExists, []int{81053}},
		{"cname over existing record", 400, cfErrorBody(81054, "A CNAME record with that host already exists."), ErrAlreadyExists, []int{81054}},
		{"identical", 400, cfErrorBody(81057, "identical"), ErrAlreadyExists, []int{81057}},
		{"same settings", 400, cfErrorBody(81058, "same"), ErrAlreadyExists, []int{81058}},
		{"other 400", 400, cfErrorBody(9005, "content invalid"), nil, []int{9005}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCF(t)
			c := f.client(t, rootCfg())
			f.inject("PUT", "/zones/z1/dns_records/r1", 1, tt.status, tt.body, nil)

			_, err := c.UpdateDNSRecord(context.Background(), testZone, DNSRecord{ID: "r1", Name: "a.example.com", Type: model.DNSTypeA, Content: "1.2.3.4"})

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("want *APIError, got %T %v", err, err)
			}
			if apiErr.Status != tt.status || !slices.Equal(apiErr.Codes, tt.codes) || len(apiErr.Messages) != 1 {
				t.Fatalf("unexpected details %+v", apiErr)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
			if tt.want == nil && apiErr.Err != nil {
				t.Fatalf("unexpected sentinel %v", apiErr.Err)
			}
			if !strings.Contains(err.Error(), "dns.update") || !strings.Contains(err.Error(), apiErr.Messages[0]) {
				t.Fatalf("message lacks details: %v", err)
			}
		})
	}
}

func TestBackoffGrowsWithJitterAndCap(t *testing.T) {
	c := &Client{retry: config.RetryConfig{Attempts: 5, Delay: 100 * time.Millisecond, MaxDelay: 300 * time.Millisecond, Backoff: 2}}
	for attempt, want := range []time.Duration{100, 200, 300, 300} {
		want *= time.Millisecond
		for i := 0; i < 50; i++ {
			d := c.backoff(attempt, nil)
			if d < want/2 || d > want {
				t.Fatalf("attempt %d: delay %v outside [%v,%v]", attempt, d, want/2, want)
			}
		}
	}
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"120"}}}
	if d := c.backoff(0, resp); d != 300*time.Millisecond {
		t.Fatalf("Retry-After must be capped by MaxDelay, got %v", d)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	date := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	tests := []struct {
		value string
		ok    bool
	}{{"", false}, {"3", true}, {"1.5", true}, {date, true}, {"soon", false}, {"-1", false}}
	for _, tt := range tests {
		resp := &http.Response{Header: http.Header{}}
		if tt.value != "" {
			resp.Header.Set("Retry-After", tt.value)
		}
		if _, ok := retryAfter(resp); ok != tt.ok {
			t.Errorf("Retry-After %q: ok=%v", tt.value, ok)
		}
	}
	if _, ok := retryAfter(nil); ok {
		t.Error("nil response")
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if err := sleepCtx(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestTransportErrorIsRetriedAndWrapped(t *testing.T) {
	f := newFakeCF(t)
	c := f.client(t, rootCfg())
	f.srv.Close() // connection refused
	_, err := c.ListDNSRecords(context.Background(), testZone)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 0 || apiErr.Err == nil {
		t.Fatalf("want transport APIError, got %#v", err)
	}
	if h := c.Health(); h.Reachable {
		t.Fatalf("health %+v", h)
	}
	// A dial failure is safe to retry even for POST.
	if !retryable(http.MethodPost, apiErr) {
		t.Fatalf("dial error must be retryable for POST: %v", apiErr.Err)
	}
}
