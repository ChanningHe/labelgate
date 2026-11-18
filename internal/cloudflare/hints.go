package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

// hint says how to fix a failed call, or "" when the cause is not known.
func (e *APIError) hint() string {
	switch {
	case e.Err == context.Canceled || e.Err == context.DeadlineExceeded: //nolint:errorlint // only the caller's own ctx error, never a wrapped per-request timeout
		return "" // the caller stopped waiting (shutdown): nothing to fix
	case errors.Is(e.Err, ErrUnauthorized):
		return e.authHint()
	case e.Status == http.StatusTooManyRequests:
		return "Cloudflare is rate-limiting this API token; labelgate retries on the next sync"
	case e.Status == 0 && e.Err != nil:
		return "cannot reach the Cloudflare API: check DNS, outbound HTTPS (port 443) and proxy settings of the labelgate host"
	case e.Status >= http.StatusInternalServerError:
		return "Cloudflare had a server-side error; labelgate retries on the next sync"
	}
	return ""
}

// authHint explains a 401/403: an invalid token or a missing permission.
func (e *APIError) authHint() string {
	key := CredentialKey(e.Credential)
	perm := permissionFor(e.Op)
	if e.Op == "tokens.verify" || perm == "" {
		return "the API token in " + key + " is invalid, expired or revoked; create a new one under " + tokenPage
	}
	return "the API token in " + key + " was refused: give it the " + perm +
		" permission for this zone or account under " + tokenPage + " (or replace it if it is invalid)"
}
