package cloudflare

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func assertContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%q\ndoes not contain %q", got, w)
		}
	}
}

func TestAPIError_ExplainsHowToFix(t *testing.T) {
	tests := []struct {
		name  string
		err   *APIError
		wants []string
	}{
		{"dns permission, root token",
			&APIError{Op: "dns.create", Status: 403, Codes: []int{10000}, Messages: []string{"Authentication error"}, Err: ErrUnauthorized, Credential: "default"},
			[]string{"cloudflare dns.create: HTTP 403 (codes [10000]): Authentication error", "Zone:DNS:Edit",
				"cloudflare.api_token (env LABELGATE_CLOUDFLARE_API_TOKEN)", "API Tokens"}},
		{"tunnel permission, named token",
			&APIError{Op: "tunnel.config.put", Status: 403, Err: ErrUnauthorized, Credential: "corp"},
			[]string{"cloudflare.credentials.corp.api_token", "Account:Cloudflare Tunnel:Edit"}},
		{"access permission",
			&APIError{Op: "access.apps.list", Status: 403, Err: ErrUnauthorized, Credential: "default"},
			[]string{"Access: Apps and Policies:Edit"}},
		{"zones permission",
			&APIError{Op: "zones.list", Status: 403, Err: ErrUnauthorized, Credential: "default"},
			[]string{"Zone:Zone:Read"}},
		{"invalid token",
			&APIError{Op: "tokens.verify", Status: 401, Messages: []string{"Invalid API Token"}, Err: ErrUnauthorized, Credential: "default"},
			[]string{"invalid, expired or revoked", "LABELGATE_CLOUDFLARE_API_TOKEN"}},
		{"unreachable",
			&APIError{Op: "dns.list", Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}},
			[]string{"cloudflare dns.list: dial: connection refused", "cannot reach the Cloudflare API"}},
		{"rate limited",
			&APIError{Op: "dns.list", Status: 429, Messages: []string{"rate limited"}},
			[]string{"HTTP 429", "rate-limiting"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertContains(t, tt.err.Error(), tt.wants...) })
	}
}

func TestAPIError_NoHintOnShutdown(t *testing.T) {
	e := &APIError{Op: "dns.list", Err: context.Canceled}
	if got := e.Error(); got != "cloudflare dns.list: context canceled" {
		t.Errorf("got %q", got)
	}
}

func TestAPIError_NamedCredentialHasNoEnv(t *testing.T) {
	e := &APIError{Op: "dns.create", Status: 403, Err: ErrUnauthorized, Credential: "corp"}
	if strings.Contains(e.Error(), "(env ") {
		t.Errorf("map-section credential must not name an env var: %q", e.Error())
	}
}

func TestResolutionErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		sentinel error
		wants    []string
	}{
		{"no zone, any credential", NoZoneError("app.example.org", ""), ErrNoZone,
			[]string{"app.example.org", "no configured API token can see a zone", "Zone Resources"}},
		{"no zone, explicit credential", NoZoneError("app.example.org", "corp"), ErrNoZone,
			[]string{`credential "corp"`, "cloudflare.credentials.corp.api_token"}},
		{"unknown credential", UnknownCredentialError("corp"), ErrUnknownCredential,
			[]string{"add cloudflare.credentials.corp"}},
		{"unknown tunnel", UnknownTunnelError("media"), ErrUnknownTunnel,
			[]string{`tunnel "media"`, "cloudflare.tunnels", "LABELGATE_CLOUDFLARE_TUNNEL_ID"}},
		{"no default tunnel", NoDefaultTunnelError(), ErrNoDefaultTunnel,
			[]string{"LABELGATE_CLOUDFLARE_TUNNEL_ID", "default_tunnel (env LABELGATE_DEFAULT_TUNNEL)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.sentinel) {
				t.Errorf("%v does not wrap %v", tt.err, tt.sentinel)
			}
			assertContains(t, tt.err.Error(), tt.wants...)
		})
	}
}

func TestTunnelVerifyHint(t *testing.T) {
	root := Tunnel{Ref: "default", ID: "t1", AccountID: "acct"}
	assertContains(t, tunnelVerifyHint(root, &APIError{Op: "tunnel.get", Status: 404, Err: ErrNotFound}),
		"no such tunnel in account acct", "cloudflare.tunnel_id (env LABELGATE_CLOUDFLARE_TUNNEL_ID)",
		"cloudflare.account_id (env LABELGATE_CLOUDFLARE_ACCOUNT_ID)")
	named := Tunnel{Ref: "media", ID: "t2", AccountID: "acct"}
	assertContains(t, tunnelVerifyHint(named, ErrNotFound), "cloudflare.tunnels.media.tunnel_id")
	if got := tunnelVerifyHint(root, ErrUnauthorized); got != "" {
		t.Errorf("non-404 got hint %q", got)
	}
}
