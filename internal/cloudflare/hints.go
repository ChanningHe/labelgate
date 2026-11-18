package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/channinghe/labelgate/internal/config"
)

// tokenPage is where Cloudflare API tokens are created and edited.
const tokenPage = "My Profile > API Tokens in the Cloudflare dashboard"

// opPermissions maps an operation prefix to the token permission it needs
// (the names used by the Cloudflare dashboard's token editor).
var opPermissions = []struct{ prefix, perm string }{
	{"zones.", "Zone:Zone:Read"},
	{"dns.", "Zone:DNS:Edit"},
	{"tunnel.", "Account:Cloudflare Tunnel:Edit"},
	{"access.", "Access: Apps and Policies:Edit"},
}

// permissionFor returns the token permission op needs, or "".
func permissionFor(op string) string {
	for _, p := range opPermissions {
		if strings.HasPrefix(op, p.prefix) {
			return p.perm
		}
	}
	return ""
}

// CredentialKey names the config key holding a credential's API token, with
// its environment variable when it has one.
func CredentialKey(name string) string {
	if name == "" || name == config.RootCredential {
		return config.KeyWithEnv("cloudflare.api_token")
	}
	return "cloudflare.credentials." + name + ".api_token"
}

// NoZoneError reports that no zone visible to credential ("" = any
// credential) contains host. It wraps ErrNoZone.
func NoZoneError(host, credential string) error {
	if credential != "" {
		return fmt.Errorf("%s: %w: the API token of credential %q, set by %s, cannot see a zone containing this hostname; "+
			"add the zone to the token's Zone Resources under %s, or fix the hostname",
			host, ErrNoZone, credential, CredentialKey(credential), tokenPage)
	}
	return fmt.Errorf("%s: %w: no configured API token can see a zone containing this hostname "+
		"(cloudflare.credentials.<name>.zones may also exclude it); add the zone to a token's Zone Resources under %s, or fix the hostname",
		host, ErrNoZone, tokenPage)
}

// UnknownCredentialError reports a credential name that is not configured.
// It wraps ErrUnknownCredential.
func UnknownCredentialError(name string) error {
	return fmt.Errorf("credential %q: %w: add cloudflare.credentials.%s to the main instance config, "+
		"or use default (cloudflare.api_token) or another configured credential", name, ErrUnknownCredential, name)
}

// UnknownTunnelError reports a tunnel reference that is not configured. It
// wraps ErrUnknownTunnel.
func UnknownTunnelError(ref string) error {
	return fmt.Errorf("tunnel %q: %w: use \"default\" (the tunnel in %s) or a name or tunnel ID from cloudflare.tunnels in the main instance config",
		ref, ErrUnknownTunnel, config.KeyAndEnv("cloudflare.tunnel_id"))
}

// NoDefaultTunnelError reports a tunnel rule without a tunnel when no default
// is configured. It wraps ErrNoDefaultTunnel.
func NoDefaultTunnelError() error {
	return fmt.Errorf("%w: name the tunnel in the label, or set %s or %s in the main instance config",
		ErrNoDefaultTunnel, config.KeyWithEnv("cloudflare.tunnel_id"), config.KeyWithEnv("default_tunnel"))
}

// TunnelKeys names the config keys holding a tunnel's ID and account ID.
func TunnelKeys(ref string) (tunnelID, accountID string) {
	if ref == config.RootTunnel {
		return config.KeyWithEnv("cloudflare.tunnel_id"), config.KeyWithEnv("cloudflare.account_id")
	}
	return "cloudflare.tunnels." + ref + ".tunnel_id", "cloudflare.tunnels." + ref + ".account_id"
}

// Cloudflare error codes that say the token itself is bad.
const (
	// codeInvalidAPIToken is "Invalid API Token" (token verification).
	codeInvalidAPIToken = 1000
	// codeBadAuthHeader is "Invalid format for Authorization header".
	codeBadAuthHeader = 6111
	// codeAuthLockout is HTTP 429 "Too many authentication failures": after
	// repeated requests with an invalid token Cloudflare blocks it for a while.
	codeAuthLockout = 10502
)

// hint says how to fix a failed call, or "" when the cause is not known.
func (e *APIError) hint() string {
	switch {
	case e.Err == context.Canceled || e.Err == context.DeadlineExceeded: //nolint:errorlint // only the caller's own ctx error, never a wrapped per-request timeout
		return "" // the caller stopped waiting (shutdown): nothing to fix
	case errors.Is(e.Err, ErrUnauthorized):
		return e.authHint()
	case e.Status == http.StatusTooManyRequests && slices.Contains(e.Codes, codeAuthLockout):
		return e.lockoutHint()
	case e.Status == http.StatusTooManyRequests:
		return "Cloudflare is rate-limiting this API token; " + e.retryAdvice()
	case e.Status == 0 && e.Err != nil:
		return "cannot reach the Cloudflare API: check DNS, outbound HTTPS (port 443) and proxy settings of the labelgate host"
	case e.Status >= http.StatusInternalServerError:
		return "Cloudflare had a server-side error; " + e.retryAdvice()
	}
	return ""
}

// retryAdvice says what happens next: a sync retries, but startup validation
// exits the process, so there the operator has to start it again.
func (e *APIError) retryAdvice() string {
	if e.startup {
		return "wait a moment, then start labelgate again"
	}
	return "labelgate retries on the next sync"
}

// lockoutHint explains HTTP 429 code 10502: the token is invalid and
// Cloudflare temporarily blocks it after repeated failures.
func (e *APIError) lockoutHint() string {
	h := "the API token in " + CredentialKey(e.Credential) +
		" is invalid and Cloudflare temporarily blocks it after repeated authentication failures; " +
		"replace the token, then wait for the lockout to expire"
	if e.startup {
		h += " (a restart loop such as restart: unless-stopped keeps the lockout active)"
	}
	return h
}

// tokenRejected reports whether Cloudflare says the token itself is invalid,
// as opposed to lacking a permission.
func (e *APIError) tokenRejected() bool {
	if slices.Contains(e.Codes, codeInvalidAPIToken) || slices.Contains(e.Codes, codeBadAuthHeader) {
		return true
	}
	for _, m := range e.Messages {
		m = strings.ToLower(m)
		if strings.Contains(m, "invalid") && strings.Contains(m, "token") {
			return true
		}
	}
	return false
}

// authHint explains a 401/403: an invalid token or a missing permission.
func (e *APIError) authHint() string {
	key := CredentialKey(e.Credential)
	perm := permissionFor(e.Op)
	if e.Op == "tokens.verify" || perm == "" || e.tokenRejected() {
		return "replace the API token in " + key + ": Cloudflare reports it as invalid, expired or revoked; create a new one under " + tokenPage
	}
	return "the API token in " + key + " was refused: give it the " + perm +
		" permission for this zone or account under " + tokenPage + " (or replace it if it is invalid)"
}
