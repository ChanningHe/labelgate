package cloudflare

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	cf "github.com/cloudflare/cloudflare-go/v6"

	"github.com/channinghe/labelgate/internal/config"
)

// CallTimeout bounds every single Cloudflare HTTP request.
const CallTimeout = 30 * time.Second

// Zone cache policy.
const (
	ZoneRefreshInterval = time.Hour
	ZoneMissMinInterval = time.Minute
)

// Options configures the production client.
type Options struct {
	Cloudflare    config.CloudflareConfig
	DefaultTunnel string // config.Config.DefaultTunnel
	Retry         config.RetryConfig
	// HTTPClient is used for all requests (tests: httptest server client). nil = default.
	HTTPClient *http.Client
	// BaseURL overrides the API base URL (tests). "" = Cloudflare production.
	BaseURL string
	Log     *slog.Logger
	Now     func() time.Time
}

var errNotImplemented = errors.New("cloudflare: not implemented")

// Client is the production API implementation (cloudflare-go/v6 for typed
// endpoints, raw JSON via the SDK's generic Get/Put for tunnel configurations
// so unmodelled fields round-trip).
//
// Implementation notes (WP2):
//   - One SDK client per credential; no shared mutable fields without locks
//     (the v0.2 SetAccountID race must not return).
//   - Access rules: model.AccessRule{emails, [a,b]} <-> one CF rule object per
//     value ({"email":{"email":"a"}}); everyone/certificate -> {"everyone":{}}
//     / {"certificate":{}}. Live rules of other types map to
//     Selector "unsupported:<key>" with the raw JSON as the single value, so an
//     owned policy edited by hand is detected as different and rewritten.
//   - DNS SRV/CAA use the structured "data" object; MX uses priority.
//   - Health is updated after every request (transport error or 5xx = unreachable).
type Client struct {
	sdk map[string]*cf.Client // credential name -> SDK client
}

var _ API = (*Client)(nil)

// New builds a client from configuration. It performs no network I/O.
func New(opts Options) (*Client, error) { return nil, errNotImplemented }

func (c *Client) Verify(ctx context.Context) error { return errNotImplemented }
func (c *Client) Health() Health                   { return Health{} }
func (c *Client) ResolveZone(ctx context.Context, hostname, credential string) (Zone, error) {
	return Zone{}, errNotImplemented
}
func (c *Client) ResolveTunnel(ref string) (Tunnel, error) { return Tunnel{}, errNotImplemented }
func (c *Client) ListDNSRecords(ctx context.Context, zone Zone) ([]DNSRecord, error) {
	return nil, errNotImplemented
}
func (c *Client) CreateDNSRecord(ctx context.Context, zone Zone, rec DNSRecord) (DNSRecord, error) {
	return DNSRecord{}, errNotImplemented
}
func (c *Client) UpdateDNSRecord(ctx context.Context, zone Zone, rec DNSRecord) (DNSRecord, error) {
	return DNSRecord{}, errNotImplemented
}
func (c *Client) DeleteDNSRecord(ctx context.Context, zone Zone, recordID string) error {
	return errNotImplemented
}
func (c *Client) GetTunnelConfig(ctx context.Context, t Tunnel) (TunnelConfig, error) {
	return TunnelConfig{}, errNotImplemented
}
func (c *Client) PutTunnelConfig(ctx context.Context, t Tunnel, cfg TunnelConfig) error {
	return errNotImplemented
}
func (c *Client) EnsureAccessTag(ctx context.Context, acct Account, tag string) error {
	return errNotImplemented
}
func (c *Client) ListAccessApps(ctx context.Context, acct Account) ([]AccessApp, error) {
	return nil, errNotImplemented
}
func (c *Client) CreateAccessApp(ctx context.Context, acct Account, app AccessApp) (AccessApp, error) {
	return AccessApp{}, errNotImplemented
}
func (c *Client) UpdateAccessApp(ctx context.Context, acct Account, app AccessApp) (AccessApp, error) {
	return AccessApp{}, errNotImplemented
}
func (c *Client) DeleteAccessApp(ctx context.Context, acct Account, appID string) error {
	return errNotImplemented
}
func (c *Client) ListAccessPolicies(ctx context.Context, acct Account) ([]AccessPolicy, error) {
	return nil, errNotImplemented
}
func (c *Client) CreateAccessPolicy(ctx context.Context, acct Account, p AccessPolicy) (AccessPolicy, error) {
	return AccessPolicy{}, errNotImplemented
}
func (c *Client) UpdateAccessPolicy(ctx context.Context, acct Account, p AccessPolicy) (AccessPolicy, error) {
	return AccessPolicy{}, errNotImplemented
}
func (c *Client) DeleteAccessPolicy(ctx context.Context, acct Account, policyID string) error {
	return errNotImplemented
}
