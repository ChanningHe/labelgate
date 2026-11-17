package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
)

// clock is a manually advanced time source.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Unix(1_700_000_000, 0)} }

func TestZonesAutoPaging(t *testing.T) {
	// Arrange: 5 zones, server caps pages at 2 -> 3 pages.
	f := newFakeCF(t)
	f.maxPerPage = 2
	for i := 0; i < 5; i++ {
		f.addZone(testToken, fmt.Sprintf("z%d", i), fmt.Sprintf("zone%d.com", i), "acct1")
	}
	c := f.client(t, rootCfg())

	// Act
	z, err := c.ResolveZone(context.Background(), "www.zone4.com", "")

	// Assert
	if err != nil || z.ID != "z4" || z.AccountID != "acct1" || z.Credential != "default" {
		t.Fatalf("got %+v %v", z, err)
	}
	if n := f.count("GET", "/zones"); n != 3 {
		t.Fatalf("zone list requests = %d, want 3 pages", n)
	}
}

func TestPagingWithoutTotals(t *testing.T) {
	f := newFakeCF(t)
	f.maxPerPage = 2
	f.noTotals = true
	for i := 0; i < 4; i++ {
		f.addZone(testToken, fmt.Sprintf("z%d", i), fmt.Sprintf("zone%d.com", i), "acct1")
	}
	c := f.client(t, rootCfg())
	if _, err := c.ResolveZone(context.Background(), "zone3.com", ""); err != nil {
		t.Fatal(err)
	}
	// Pages 1, 2 full; page 3 empty terminates.
	if n := f.count("GET", "/zones"); n != 3 {
		t.Fatalf("zone list requests = %d, want 3", n)
	}
}

func TestZoneRefreshOnMissIsRateLimited(t *testing.T) {
	// Arrange
	f := newFakeCF(t)
	f.addZone(testToken, "z1", "example.com", "acct1")
	clk := newClock()
	c := f.client(t, rootCfg(), func(o *Options) { o.Now = clk.now })
	ctx := context.Background()
	if _, err := c.ResolveZone(ctx, "example.com", ""); err != nil {
		t.Fatal(err)
	}

	// Act: a zone added later is not found within the miss interval...
	f.addZone(testToken, "z2", "example.net", "acct1")
	clk.advance(10 * time.Second)
	_, err := c.ResolveZone(ctx, "a.example.net", "")

	// Assert
	if !errors.Is(err, ErrNoZone) {
		t.Fatalf("want ErrNoZone within miss interval, got %v", err)
	}
	if n := f.count("GET", "/zones"); n != 1 {
		t.Fatalf("zone list requests = %d, want 1 (rate limited)", n)
	}

	// ...but after ZoneMissMinInterval a miss triggers exactly one refresh.
	clk.advance(ZoneMissMinInterval)
	z, err := c.ResolveZone(ctx, "a.example.net", "")
	if err != nil || z.ID != "z2" {
		t.Fatalf("got %+v %v", z, err)
	}
	if _, err := c.ResolveZone(ctx, "nothing.invalid", ""); !errors.Is(err, ErrNoZone) {
		t.Fatalf("got %v", err)
	}
	if n := f.count("GET", "/zones"); n != 2 {
		t.Fatalf("zone list requests = %d, want 2", n)
	}
}

func TestZoneCachePeriodicRefresh(t *testing.T) {
	f := newFakeCF(t)
	f.addZone(testToken, "z1", "example.com", "acct1")
	clk := newClock()
	c := f.client(t, rootCfg(), func(o *Options) { o.Now = clk.now })
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := c.ResolveZone(ctx, "example.com", ""); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count("GET", "/zones"); n != 1 {
		t.Fatalf("hits must be served from cache: %d requests", n)
	}
	clk.advance(ZoneRefreshInterval)
	if _, err := c.ResolveZone(ctx, "example.com", ""); err != nil {
		t.Fatal(err)
	}
	if n := f.count("GET", "/zones"); n != 2 {
		t.Fatalf("stale cache must refresh: %d requests", n)
	}
}

func TestZoneRefreshFailureServesStaleList(t *testing.T) {
	f := newFakeCF(t)
	f.addZone(testToken, "z1", "example.com", "acct1")
	clk := newClock()
	c := f.client(t, rootCfg(), func(o *Options) { o.Now = clk.now; o.Retry.Attempts = 1 })
	ctx := context.Background()
	if _, err := c.ResolveZone(ctx, "example.com", ""); err != nil {
		t.Fatal(err)
	}
	clk.advance(ZoneRefreshInterval)
	f.inject("GET", "/zones", 1, 500, cfErrorBody(1, "down"), nil)
	z, err := c.ResolveZone(ctx, "example.com", "")
	if err != nil || z.ID != "z1" {
		t.Fatalf("stale list must be used: %+v %v", z, err)
	}
}

func TestZoneLoadFailureIsReportedAndRateLimited(t *testing.T) {
	f := newFakeCF(t)
	f.addZone(testToken, "z1", "example.com", "acct1")
	clk := newClock()
	c := f.client(t, rootCfg(), func(o *Options) { o.Now = clk.now; o.Retry.Attempts = 1 })
	f.inject("GET", "/zones", 1, 500, cfErrorBody(1, "down"), nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := c.ResolveZone(ctx, "example.com", "")
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 500 {
			t.Fatalf("want load error, got %v", err)
		}
	}
	if n := f.count("GET", "/zones"); n != 1 {
		t.Fatalf("failed loads must be rate limited: %d", n)
	}
	clk.advance(ZoneMissMinInterval)
	if _, err := c.ResolveZone(ctx, "example.com", ""); err != nil {
		t.Fatalf("retry after interval: %v", err)
	}
}

func TestZoneLoadCancelledDoesNotBlockNextCaller(t *testing.T) {
	f := newFakeCF(t)
	f.addZone(testToken, "z1", "example.com", "acct1")
	c := f.client(t, rootCfg())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ResolveZone(ctx, "example.com", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if _, err := c.ResolveZone(context.Background(), "example.com", ""); err != nil {
		t.Fatalf("next caller blocked: %v", err)
	}
}

func TestResolveZoneSelection(t *testing.T) {
	// Arrange
	f := newFakeCF(t)
	f.addZone(testToken, "root-com", "example.com", "acct1")
	f.addZone(testToken, "root-org", "example.org", "acct1")
	f.addZone(testToken, "root-tie", "tie.io", "acct1")
	f.addZone("tok-team", "team-sub", "Sub.Example.com", "acct2")
	f.addZone("tok-team", "team-org", "a.example.org", "acct2")
	f.addZone("tok-team", "team-tie", "tie.io", "acct2")
	f.addZone("tok-zeta", "zeta-net", "shared.net", "acct3")
	f.addZone("tok-alpha", "alpha-net", "shared.net", "acct4")
	f.addZone("tok-filtered", "filt", "x.example.com", "acct5")
	cfg := config.CloudflareConfig{
		APIToken: testToken,
		Credentials: map[string]config.CredentialConfig{
			"team":     {APIToken: "tok-team"},
			"zeta":     {APIToken: "tok-zeta"},
			"alpha":    {APIToken: "tok-alpha"},
			"filtered": {APIToken: "tok-filtered", Zones: []string{"*.example.net", "other.com"}},
		},
	}
	c := f.client(t, cfg)
	tests := []struct {
		host, cred, wantID string
		wantErr            error
	}{
		{"WWW.SUB.example.com.", "", "team-sub", nil},       // longest suffix, case-insensitive, trailing dot
		{"www.example.com", "", "root-com", nil},            // shorter only
		{"b.a.example.org", "", "team-org", nil},            //
		{"x.tie.io", "", "root-tie", nil},                   // tie -> root credential
		{"x.shared.net", "", "alpha-net", nil},              // tie -> smallest name
		{"y.x.example.com", "", "root-com", nil},            // filtered credential excluded from auto
		{"y.x.example.com", "filtered", "filt", nil},        // explicit ignores patterns
		{"www.sub.example.com", "default", "root-com", nil}, // explicit restricts candidates
		{"badexample.com", "", "", ErrNoZone},               // label boundary
		{"a.example.com", "nope", "", ErrUnknownCredential},
		{"a.unknown.dev", "team", "", ErrNoZone},
	}
	for _, tt := range tests {
		t.Run(tt.host+"/"+tt.cred, func(t *testing.T) {
			// Act
			z, err := c.ResolveZone(context.Background(), tt.host, tt.cred)
			// Assert
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want %v, got %+v %v", tt.wantErr, z, err)
				}
				return
			}
			if err != nil || z.ID != tt.wantID {
				t.Fatalf("got %+v %v, want %s", z, err, tt.wantID)
			}
		})
	}
	z, _ := c.ResolveZone(context.Background(), "q.sub.example.com", "")
	if z.Name != "sub.example.com" || z.Credential != "team" || z.Account() != (Account{ID: "acct2", Credential: "team"}) {
		t.Fatalf("zone fields %+v", z)
	}
}

func TestResolveZoneFailsWhenAnyCredentialFails(t *testing.T) {
	f := newFakeCF(t)
	f.addZone(testToken, "z1", "example.com", "acct1")
	cfg := config.CloudflareConfig{APIToken: testToken, Credentials: map[string]config.CredentialConfig{"bad": {APIToken: "tok-revoked"}}}
	c := f.client(t, cfg)
	_, err := c.ResolveZone(context.Background(), "a.example.com", "")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a failing credential must fail automatic selection, got %v", err)
	}
	if z, err := c.ResolveZone(context.Background(), "a.example.com", "default"); err != nil || z.ID != "z1" {
		t.Fatalf("explicit healthy credential: %+v %v", z, err)
	}
}

func TestAllowsZonePatterns(t *testing.T) {
	cr := &credEntry{patterns: []string{"example.com", "*.example.org", "[invalid"}}
	tests := map[string]bool{"example.com": true, "a.example.org": true, "a.b.example.org": true, "example.org": false, "other.com": false}
	for zone, want := range tests {
		if got := cr.allowsZone(zone); got != want {
			t.Errorf("%s: got %v", zone, got)
		}
	}
}
