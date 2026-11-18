package cloudflare

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	cf "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/option"

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

// Client is the production API implementation on top of cloudflare-go/v6.
//
// All requests go through the SDK's generic Execute (auth, base URL, request
// encoding, API error decoding) with SDK retries disabled; paging, per-request
// timeouts, retries and error mapping are done here so that every page honours
// ctx (the SDK auto-pager fetches follow-up pages with context.Background()).
// Wire types are small local structs mirroring the SDK's JSON field names, and
// tunnel configurations are passed through as raw JSON so unmodelled fields
// round-trip.
//
//   - One SDK client per credential; immutable after New except the per
//     credential zone cache and Health, each guarded by its own mutex.
//   - Access rules: model.AccessRule{emails, [a,b]} <-> one CF rule object per
//     value ({"email":{"email":"a"}}); everyone/certificate -> {"everyone":{}}
//     / {"certificate":{}}. Live rules of other shapes map to Selector
//     "unsupported:<key>" with the raw JSON as the single value.
//   - DNS SRV/CAA use the structured "data" object; MX uses priority.
//   - Health is updated after every request (transport error or 5xx = unreachable).
type Client struct {
	creds         map[string]*credEntry
	credOrder     []string // sorted credential names
	tunnels       []Tunnel // sorted: root first, then by Ref
	defaultTunnel string   // Ref of the default tunnel; "" = none
	retry         config.RetryConfig
	timeout       time.Duration
	log           *slog.Logger
	now           func() time.Time
	sleep         func(ctx context.Context, d time.Duration) error

	healthMu sync.Mutex
	health   Health
}

// credEntry is one API token with its SDK client and zone cache.
type credEntry struct {
	name     string
	sdk      *cf.Client
	patterns []string // lower-case zone patterns for automatic selection; empty = all
	accounts []string // account IDs known from config (token verify fallback)
	cache    zoneCache
}

var _ API = (*Client)(nil)

// New builds a client from configuration. It performs no network I/O.
func New(opts Options) (*Client, error) {
	c := &Client{
		creds:   map[string]*credEntry{},
		retry:   opts.Retry,
		timeout: CallTimeout,
		log:     opts.Log,
		now:     opts.Now,
		sleep:   sleepCtx,
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.retry.Attempts < 1 {
		c.retry.Attempts = 1
	}
	base := []option.RequestOption{option.WithEnvironmentProduction(), option.WithMaxRetries(0)}
	if opts.BaseURL != "" {
		base = append(base, option.WithBaseURL(opts.BaseURL))
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	base = append(base, option.WithHTTPClient(hc))
	if err := c.addCredentials(opts.Cloudflare, base); err != nil {
		return nil, err
	}
	if err := c.addTunnels(opts.Cloudflare); err != nil {
		return nil, err
	}
	if err := c.setDefaultTunnel(opts.DefaultTunnel); err != nil {
		return nil, err
	}
	return c, nil
}

// newSDK builds an SDK client that ignores CLOUDFLARE_* environment variables
// (cf.NewClient would pick them up, e.g. CLOUDFLARE_BASE_URL or
// CLOUDFLARE_API_KEY, and leak the token elsewhere or add auth headers).
func newSDK(base []option.RequestOption, token string) *cf.Client {
	opts := append(append([]option.RequestOption(nil), base...), option.WithAPIToken(token))
	return &cf.Client{Options: opts}
}

func (c *Client) addCredentials(cfg config.CloudflareConfig, base []option.RequestOption) error {
	if cfg.APIToken != "" {
		c.creds[config.RootCredential] = &credEntry{
			name: config.RootCredential,
			sdk:  newSDK(base, cfg.APIToken),
		}
		if cfg.AccountID != "" {
			c.creds[config.RootCredential].accounts = []string{cfg.AccountID}
		}
	}
	for name, cc := range cfg.Credentials {
		if name == config.RootCredential {
			return fmt.Errorf("cloudflare: credential name %q is reserved for cloudflare.api_token", name)
		}
		if cc.APIToken == "" {
			return fmt.Errorf("cloudflare: credential %q has no api_token", name)
		}
		patterns := make([]string, 0, len(cc.Zones))
		for _, p := range cc.Zones {
			patterns = append(patterns, strings.ToLower(strings.TrimSpace(p)))
		}
		c.creds[name] = &credEntry{name: name, sdk: newSDK(base, cc.APIToken), patterns: patterns}
	}
	for name := range c.creds {
		c.credOrder = append(c.credOrder, name)
	}
	sort.Strings(c.credOrder)
	return nil
}

func (c *Client) addTunnels(cfg config.CloudflareConfig) error {
	if cfg.TunnelID != "" {
		t := Tunnel{Ref: config.RootTunnel, ID: strings.ToLower(cfg.TunnelID), AccountID: cfg.AccountID, Credential: config.RootCredential}
		if err := c.addTunnel(t); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(cfg.Tunnels))
	for name := range cfg.Tunnels {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		tc := cfg.Tunnels[name]
		if name == config.RootTunnel {
			return fmt.Errorf("cloudflare: tunnel name %q is reserved for cloudflare.tunnel_id", name)
		}
		cred := tc.Credential
		if cred == "" {
			cred = config.RootCredential
		}
		t := Tunnel{Ref: name, ID: strings.ToLower(tc.TunnelID), AccountID: tc.AccountID, Credential: cred}
		if err := c.addTunnel(t); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) addTunnel(t Tunnel) error {
	if t.ID == "" || t.AccountID == "" {
		return fmt.Errorf("cloudflare: tunnel %q needs tunnel_id and account_id", t.Ref)
	}
	cr, ok := c.creds[t.Credential]
	if !ok {
		return fmt.Errorf("cloudflare: tunnel %q: credential %q: %w", t.Ref, t.Credential, ErrUnknownCredential)
	}
	if !slices.Contains(cr.accounts, t.AccountID) {
		cr.accounts = append(cr.accounts, t.AccountID)
	}
	c.tunnels = append(c.tunnels, t)
	return nil
}

func (c *Client) setDefaultTunnel(ref string) error {
	if ref == "" || ref == config.RootTunnel {
		if _, ok := c.tunnelByRef(config.RootTunnel); ok {
			c.defaultTunnel = config.RootTunnel
		}
		return nil
	}
	if _, ok := c.tunnelByRef(ref); !ok {
		return fmt.Errorf("cloudflare: default_tunnel %q: %w", ref, ErrUnknownTunnel)
	}
	c.defaultTunnel = ref
	return nil
}

func (c *Client) tunnelByRef(ref string) (Tunnel, bool) {
	for _, t := range c.tunnels {
		if t.Ref == ref {
			return t, true
		}
	}
	return Tunnel{}, false
}

// ResolveTunnel maps a tunnel reference (configured name or ID) to a tunnel.
func (c *Client) ResolveTunnel(ref string) (Tunnel, error) {
	if ref == "" {
		if c.defaultTunnel == "" {
			return Tunnel{}, ErrNoDefaultTunnel
		}
		ref = c.defaultTunnel
	}
	if t, ok := c.tunnelByRef(ref); ok {
		return t, nil
	}
	id := strings.ToLower(ref)
	for _, t := range c.tunnels {
		if t.ID == id {
			return t, nil
		}
	}
	return Tunnel{}, fmt.Errorf("%s: %w", ref, ErrUnknownTunnel)
}

// credByName returns the named credential or ErrUnknownCredential.
func (c *Client) credByName(name string) (*credEntry, error) {
	cr, ok := c.creds[name]
	if !ok {
		return nil, fmt.Errorf("credential %q: %w", name, ErrUnknownCredential)
	}
	return cr, nil
}

// Health reports the outcome of the most recent request.
func (c *Client) Health() Health {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	return c.health
}

func (c *Client) setHealth(h Health) {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	c.health = h
}
