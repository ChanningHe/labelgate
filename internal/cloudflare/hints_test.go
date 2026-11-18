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

func TestAPIError_InvalidTokenLeadsWithReplacingIt(t *testing.T) {
	tests := []struct {
		name string
		err  *APIError
	}{
		{"403 invalid access token, dns op",
			&APIError{Op: "dns.list", Status: 403, Codes: []int{9109}, Messages: []string{"Invalid access token"}, Err: ErrUnauthorized, Credential: "default"}},
		{"403 invalid access token, code 10000",
			&APIError{Op: "tunnel.config.get", Status: 403, Codes: []int{10000}, Messages: []string{"Invalid access token"}, Err: ErrUnauthorized, Credential: "default"}},
		{"401 invalid API token by code",
			&APIError{Op: "zones.list", Status: 401, Codes: []int{1000}, Messages: []string{"anything"}, Err: ErrUnauthorized, Credential: "default"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			assertContains(t, got, "replace the API token in cloudflare.api_token (env LABELGATE_CLOUDFLARE_API_TOKEN)", "invalid, expired or revoked", tokenPage)
			if strings.Contains(got, "give it the") {
				t.Errorf("an invalid token must not lead with a permission hint: %q", got)
			}
		})
	}
}

func TestAPIError_AuthLockoutExplainsTheTokenIsInvalid(t *testing.T) {
	e := &APIError{Op: "dns.list", Status: 429, Codes: []int{10502}, Messages: []string{"Too many authentication failures"}, Err: nil, Credential: "corp"}

	got := e.Error()

	assertContains(t, got, "HTTP 429 (codes [10502])", "cloudflare.credentials.corp.api_token", "is invalid",
		"temporarily blocks it", "replace the token", "wait for the lockout to expire")
	for _, bad := range []string{"rate-limiting", "retries on the next sync", "restart"} {
		if strings.Contains(got, bad) {
			t.Errorf("runtime lockout message must not contain %q: %q", bad, got)
		}
	}
}

func TestAPIError_StartupDoesNotPromiseARetry(t *testing.T) {
	tests := []struct {
		name  string
		err   *APIError
		wants []string
	}{
		{"lockout mentions the restart loop",
			&APIError{Op: "tokens.verify", Status: 429, Codes: []int{10502}, Messages: []string{"Too many authentication failures"}, Credential: "default", startup: true},
			[]string{"replace the token", "wait for the lockout to expire", "restart: unless-stopped", "keeps the lockout active"}},
		{"plain rate limit",
			&APIError{Op: "tokens.verify", Status: 429, Messages: []string{"slow down"}, Credential: "default", startup: true},
			[]string{"rate-limiting", "start labelgate again"}},
		{"server error",
			&APIError{Op: "tokens.verify", Status: 502, Messages: []string{"bad gateway"}, Credential: "default", startup: true},
			[]string{"server-side error", "start labelgate again"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			assertContains(t, got, tt.wants...)
			if strings.Contains(got, "next sync") {
				t.Errorf("startup exits, so no retry may be promised: %q", got)
			}
		})
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

func TestUnknownTunnelErrorHasNoNestedBrackets(t *testing.T) {
	got := UnknownTunnelError("media").Error()
	want := `use "default" (the tunnel in cloudflare.tunnel_id, env LABELGATE_CLOUDFLARE_TUNNEL_ID) or a name or tunnel ID from cloudflare.tunnels`
	assertContains(t, got, want)
	for _, err := range []error{UnknownTunnelError("media"), NoZoneError("app.example.com", "default"), NoZoneError("app.example.com", "")} {
		if nestedBrackets(err.Error()) {
			t.Errorf("nested brackets in %q", err)
		}
	}
}

// nestedBrackets reports whether s opens a "(" while another is still open.
func nestedBrackets(s string) bool {
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
			if depth > 1 {
				return true
			}
		case ')':
			depth--
		}
	}
	return false
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
