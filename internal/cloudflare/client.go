// Package cloudflare provides Cloudflare API client for labelgate.
package cloudflare

import (
	"context"
	"fmt"
	"strings"
	"sync"

	cf "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/option"
	"github.com/cloudflare/cloudflare-go/v6/zones"
	"github.com/rs/zerolog/log"
)

// Client wraps the Cloudflare API client with caching and credential management.
type Client struct {
	api         *cf.Client
	accountID   string
	zoneCache   map[string]string // zone name -> zone ID (e.g. "example.com" -> "abc123")
	zonesLoaded bool              // whether zone cache has been populated
	zoneCacheMu sync.RWMutex
}

// NewClient creates a new Cloudflare client with API token authentication.
func NewClient(apiToken string) *Client {
	return &Client{
		api:       cf.NewClient(option.WithAPIToken(apiToken)),
		zoneCache: make(map[string]string),
	}
}

// SetAccountID sets the account ID for tunnel operations.
func (c *Client) SetAccountID(accountID string) {
	c.accountID = accountID
}

// AccountID returns the configured account ID.
func (c *Client) AccountID() string {
	return c.accountID
}

// API returns the underlying Cloudflare API client.
func (c *Client) API() *cf.Client {
	return c.api
}

// GetZoneID retrieves the zone ID for a given hostname.
// It loads and caches all available zones from the API, then performs
// longest-suffix matching to find the correct zone. This avoids the need
// to guess root domains from TLD structure.
func (c *Client) GetZoneID(ctx context.Context, hostname string) (string, error) {
	// Ensure zones are loaded
	if err := c.loadZones(ctx); err != nil {
		return "", err
	}

	// Match hostname against cached zones using longest-suffix match
	c.zoneCacheMu.RLock()
	defer c.zoneCacheMu.RUnlock()

	zoneName, zoneID := c.matchZoneForHostname(hostname)
	if zoneID == "" {
		return "", fmt.Errorf("no matching zone found for hostname: %s", hostname)
	}

	log.Debug().
		Str("hostname", hostname).
		Str("zone", zoneName).
		Str("zone_id", zoneID).
		Msg("Resolved zone ID")

	return zoneID, nil
}

// loadZones fetches all zones accessible by this API token and caches them.
// It is safe for concurrent use; only the first caller triggers the API call.
func (c *Client) loadZones(ctx context.Context) error {
	c.zoneCacheMu.RLock()
	if c.zonesLoaded {
		c.zoneCacheMu.RUnlock()
		return nil
	}
	c.zoneCacheMu.RUnlock()

	c.zoneCacheMu.Lock()
	defer c.zoneCacheMu.Unlock()

	// Double-check after acquiring write lock
	if c.zonesLoaded {
		return nil
	}

	zoneList, err := c.api.Zones.List(ctx, zones.ZoneListParams{})
	if err != nil {
		return fmt.Errorf("failed to list zones: %w", err)
	}

	for _, z := range zoneList.Result {
		c.zoneCache[z.Name] = z.ID
	}
	c.zonesLoaded = true

	log.Debug().
		Int("count", len(zoneList.Result)).
		Msg("Loaded zones into cache")

	return nil
}

// matchZoneForHostname finds the longest matching zone name for a hostname.
// For example, given hostname "api.sub.example.com" and zones
// ["example.com", "sub.example.com"], it returns "sub.example.com" as the
// longest (most specific) match.
// Must be called with zoneCacheMu held (at least RLock).
func (c *Client) matchZoneForHostname(hostname string) (string, string) {
	hostname = strings.TrimSuffix(hostname, ".")
	bestName := ""
	bestID := ""

	// Walk up the hostname labels to find the longest matching zone.
	// e.g. for "a.b.example.com": try "a.b.example.com", "b.example.com", "example.com", "com"
	// The first match is always the longest since we start from the full hostname.
	candidate := hostname
	for candidate != "" {
		if zoneID, ok := c.zoneCache[candidate]; ok {
			return candidate, zoneID
		}
		idx := strings.Index(candidate, ".")
		if idx < 0 {
			break
		}
		candidate = candidate[idx+1:]
	}

	return bestName, bestID
}

// Validate verifies the API token is valid.
// Uses /user/tokens/verify endpoint which only requires the token to be valid,
// without needing additional User:Read permissions.
func (c *Client) Validate(ctx context.Context) error {
	result, err := c.api.User.Tokens.Verify(ctx)
	if err != nil {
		// If token verification fails, try listing zones as fallback
		_, zoneErr := c.api.Zones.List(ctx, zones.ZoneListParams{})
		if zoneErr != nil {
			return fmt.Errorf("credential validation failed: %w", err)
		}
		return nil
	}

	// Check token status
	if result.Status != "active" {
		return fmt.Errorf("token is not active: status=%s", result.Status)
	}

	return nil
}

// InvalidateZoneCache clears the zone cache, forcing a reload on next GetZoneID call.
// Useful when zones may have been added or removed from the Cloudflare account.
func (c *Client) InvalidateZoneCache() {
	c.zoneCacheMu.Lock()
	defer c.zoneCacheMu.Unlock()
	c.zoneCache = make(map[string]string)
	c.zonesLoaded = false
}
