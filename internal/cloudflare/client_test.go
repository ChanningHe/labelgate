package cloudflare

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
)

func multiCfg() config.CloudflareConfig {
	return config.CloudflareConfig{
		APIToken:  testToken,
		AccountID: "acct1",
		TunnelID:  "TUN-ROOT",
		Credentials: map[string]config.CredentialConfig{
			"team": {APIToken: "tok-team", Zones: []string{"*.example.org"}},
		},
		Tunnels: map[string]config.TunnelConfig{
			"edge": {AccountID: "acct2", TunnelID: "Tun-Edge", Credential: "team"},
			"core": {AccountID: "acct1", TunnelID: "tun-core"},
		},
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.CloudflareConfig
		def  string
	}{
		{"reserved credential name", config.CloudflareConfig{Credentials: map[string]config.CredentialConfig{"default": {APIToken: "x"}}}, ""},
		{"credential without token", config.CloudflareConfig{Credentials: map[string]config.CredentialConfig{"a": {}}}, ""},
		{"reserved tunnel name", config.CloudflareConfig{APIToken: "x", Tunnels: map[string]config.TunnelConfig{"default": {AccountID: "a", TunnelID: "t"}}}, ""},
		{"tunnel without account", config.CloudflareConfig{APIToken: "x", TunnelID: "t"}, ""},
		{"tunnel with unknown credential", config.CloudflareConfig{Tunnels: map[string]config.TunnelConfig{"a": {AccountID: "a", TunnelID: "t", Credential: "nope"}}}, ""},
		{"root tunnel without root credential", config.CloudflareConfig{AccountID: "a", TunnelID: "t"}, ""},
		{"unknown default tunnel", rootCfg(), "missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			_, err := New(Options{Cloudflare: tt.cfg, DefaultTunnel: tt.def})
			// Assert
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestNewAcceptsEmptyConfig(t *testing.T) {
	c, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.ResolveTunnel(""); !errors.Is(err, ErrNoDefaultTunnel) {
		t.Fatalf("want ErrNoDefaultTunnel, got %v", err)
	}
	if _, err := c.ResolveZone(context.Background(), "a.example.com", ""); !errors.Is(err, ErrNoZone) {
		t.Fatalf("want ErrNoZone, got %v", err)
	}
	if err := c.Verify(context.Background()); err != nil {
		t.Fatalf("Verify with nothing configured: %v", err)
	}
}

func TestResolveTunnel(t *testing.T) {
	c, err := New(Options{Cloudflare: multiCfg()})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		ref, wantRef, wantID, wantCred, wantAcct string
		wantErr                                  error
	}{
		{"", "default", "tun-root", "default", "acct1", nil},
		{"default", "default", "tun-root", "default", "acct1", nil},
		{"edge", "edge", "tun-edge", "team", "acct2", nil},
		{"core", "core", "tun-core", "default", "acct1", nil},
		{"TUN-EDGE", "edge", "tun-edge", "team", "acct2", nil},
		{"tun-core", "core", "tun-core", "default", "acct1", nil},
		{"unknown", "", "", "", "", ErrUnknownTunnel},
		{"Edge", "", "", "", "", ErrUnknownTunnel},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got, err := c.ResolveTunnel(tt.ref)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want %v, got %v (%+v)", tt.wantErr, err, got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := Tunnel{Ref: tt.wantRef, ID: tt.wantID, AccountID: tt.wantAcct, Credential: tt.wantCred}
			if got != want {
				t.Fatalf("got %+v want %+v", got, want)
			}
		})
	}
}

func TestResolveTunnelConfiguredDefault(t *testing.T) {
	c, err := New(Options{Cloudflare: multiCfg(), DefaultTunnel: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.ResolveTunnel("")
	if err != nil || got.Ref != "edge" {
		t.Fatalf("got %+v %v", got, err)
	}
	cfg := multiCfg()
	cfg.TunnelID = ""
	c, err = New(Options{Cloudflare: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolveTunnel(""); !errors.Is(err, ErrNoDefaultTunnel) {
		t.Fatalf("no root tunnel and no default_tunnel: got %v", err)
	}
}

func TestHealthTransitions(t *testing.T) {
	// Arrange
	f := newFakeCF(t)
	f.addZone(testToken, "z1", "example.com", "acct1")
	now := time.Unix(1000, 0)
	c := f.client(t, rootCfg(), func(o *Options) {
		o.Now = func() time.Time { return now }
		o.Retry.Attempts = 1
	})
	zone := Zone{ID: "z1", Name: "example.com", Credential: "default"}
	if h := c.Health(); h.Reachable || !h.LastCheck.IsZero() {
		t.Fatalf("initial health %+v", h)
	}

	// Act + Assert: success
	if _, err := c.ListDNSRecords(context.Background(), zone); err != nil {
		t.Fatal(err)
	}
	if h := c.Health(); !h.Reachable || !h.LastCheck.Equal(now) || h.Error != "" {
		t.Fatalf("after success %+v", h)
	}
	// 5xx -> unreachable
	f.inject("GET", "/zones/z1", 1, 503, cfErrorBody(10013, "service unavailable"), nil)
	if _, err := c.ListDNSRecords(context.Background(), zone); err == nil {
		t.Fatal("expected error")
	}
	if h := c.Health(); h.Reachable || h.Error == "" {
		t.Fatalf("after 503 %+v", h)
	}
	// 4xx -> reachable again
	f.inject("GET", "/zones/z1", 1, 400, cfErrorBody(9999, "bad"), nil)
	if _, err := c.ListDNSRecords(context.Background(), zone); err == nil {
		t.Fatal("expected error")
	}
	if h := c.Health(); !h.Reachable || h.Error != "" {
		t.Fatalf("after 400 %+v", h)
	}
}

func TestConcurrentCallsAreRaceFree(t *testing.T) {
	f := newFakeCF(t)
	f.addZone(testToken, "z1", "example.com", "acct1")
	f.addZone("tok-team", "z2", "sub.example.org", "acct2")
	c := f.client(t, multiCfg())
	zone := Zone{ID: "z1", Name: "example.com", Credential: "default"}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.ResolveZone(context.Background(), "a.sub.example.org", ""); err != nil {
				errs <- err
			}
			if _, err := c.ListDNSRecords(context.Background(), zone); err != nil {
				errs <- err
			}
			_ = c.Health()
			if _, err := c.ResolveTunnel("edge"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
