package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// tokenStatusActive is the status of a usable API token.
const tokenStatusActive = "active"

type tokenWire struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Verify checks every credential (token verify) and every tunnel (GET with its
// credential). All failures are reported, joined.
func (c *Client) Verify(ctx context.Context) error {
	var errs []error
	for _, name := range c.credOrder {
		if err := c.verifyToken(ctx, c.creds[name]); err != nil {
			errs = append(errs, fmt.Errorf("credential %q: %w", name, forStartup(err)))
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	for _, t := range c.tunnels {
		if err := c.verifyTunnel(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("tunnel %q (%s): %w%s", t.Ref, t.ID, forStartup(err), tunnelVerifyHint(t, err)))
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return errors.Join(errs...)
}

// forStartup returns err with its *APIError (if any) marked as a startup
// failure, so its hints do not promise a retry the exiting process cannot make.
// The original error is left untouched.
func forStartup(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	marked := *apiErr
	marked.startup = true
	return &marked
}

// verifyToken tries the user-token endpoint, then (account-owned tokens) the
// account-token endpoint of each account known for the credential.
func (c *Client) verifyToken(ctx context.Context, cr *credEntry) error {
	err := c.verifyTokenAt(ctx, cr, "user/tokens/verify")
	for _, acct := range cr.accounts {
		if err == nil || !errors.Is(err, ErrUnauthorized) {
			break
		}
		err = c.verifyTokenAt(ctx, cr, fmt.Sprintf("accounts/%s/tokens/verify", url.PathEscape(acct)))
	}
	return err
}

func (c *Client) verifyTokenAt(ctx context.Context, cr *credEntry, path string) error {
	var env envelope[tokenWire]
	if err := c.call(ctx, cr, request{op: "tokens.verify", method: http.MethodGet, path: path, out: &env}); err != nil {
		return err
	}
	if env.Result.Status != tokenStatusActive {
		return fmt.Errorf("token status is %q, not active: %w; the API token in %s is disabled or expired: create a new one under %s",
			env.Result.Status, ErrUnauthorized, CredentialKey(cr.name), tokenPage)
	}
	return nil
}

// tunnelVerifyHint explains a tunnel that does not exist where configured.
func tunnelVerifyHint(t Tunnel, err error) string {
	if !errors.Is(err, ErrNotFound) {
		return ""
	}
	idKey, acctKey := TunnelKeys(t.Ref)
	return "; no such tunnel in account " + t.AccountID + ": check " + idKey + " and " + acctKey +
		" against Zero Trust > Networks > Tunnels in the Cloudflare dashboard"
}

func (c *Client) verifyTunnel(ctx context.Context, t Tunnel) error {
	cr, err := c.tunnelCred(t)
	if err != nil {
		return err
	}
	var env envelope[json.RawMessage]
	path := fmt.Sprintf("accounts/%s/cfd_tunnel/%s", url.PathEscape(t.AccountID), url.PathEscape(t.ID))
	return c.call(ctx, cr, request{op: "tunnel.get", method: http.MethodGet, path: path, out: &env})
}
