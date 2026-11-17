package cloudflare

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/config"
)

func TestVerifySuccess(t *testing.T) {
	f := newFakeCF(t)
	f.addTunnel("tun-root")
	f.addTunnel("tun-edge")
	f.addToken("tok-team")
	c := f.client(t, multiCfg())
	f.addTunnel("tun-core")
	if err := c.Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// Each tunnel is checked with its own credential.
	for _, r := range f.reqs() {
		if strings.Contains(r.Path, "tun-edge") && r.Token != "tok-team" {
			t.Fatalf("tunnel edge checked with token %q", r.Token)
		}
	}
}

func TestVerifyReportsEveryFailure(t *testing.T) {
	// Arrange: team token revoked, root tunnel missing.
	f := newFakeCF(t)
	f.addTunnel("tun-core")
	c := f.client(t, multiCfg())

	// Act
	err := c.Verify(context.Background())

	// Assert
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{`credential "team"`, `tunnel "default"`, `tunnel "edge"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %s: %v", want, err)
		}
	}
	if !errors.Is(err, ErrUnauthorized) || !errors.Is(err, ErrNotFound) {
		t.Fatalf("sentinels not joined: %v", err)
	}
}

func TestVerifyAccountOwnedToken(t *testing.T) {
	f := newFakeCF(t)
	f.userVerify4 = true
	c := f.client(t, config.CloudflareConfig{APIToken: testToken, AccountID: "acct1"})
	if err := c.Verify(context.Background()); err != nil {
		t.Fatalf("account token fallback: %v", err)
	}
	if f.count("GET", "/accounts/acct1/tokens/verify") != 1 {
		t.Fatal("account endpoint not used")
	}
}

func TestVerifyInactiveToken(t *testing.T) {
	f := newFakeCF(t)
	f.inject("GET", "/user/tokens/verify", 1, 200, `{"success":true,"errors":[],"result":{"id":"x","status":"disabled"}}`, nil)
	c := f.client(t, config.CloudflareConfig{APIToken: testToken})
	if err := c.Verify(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v", err)
	}
}

func TestVerifyCancelled(t *testing.T) {
	f := newFakeCF(t)
	c := f.client(t, multiCfg())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Verify(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
