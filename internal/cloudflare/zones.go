package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
)

// zonesPerPage is the page size for the zones list (Cloudflare maximum: 50).
const zonesPerPage = 50

// zoneCache holds one credential's zone list.
//
// A fetch is attempted when the list was never loaded, is older than
// ZoneRefreshInterval, or a lookup missed; in every case at most once per
// ZoneMissMinInterval. A failed refresh keeps serving the previous list.
type zoneCache struct {
	mu          sync.Mutex
	zones       []Zone
	loaded      bool
	fetchedAt   time.Time
	lastAttempt time.Time
	lastErr     error
}

type zoneWire struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

// ResolveZone selects the credential and zone for hostname (see API).
func (c *Client) ResolveZone(ctx context.Context, hostname, credential string) (Zone, error) {
	host := model.NormalizeHostname(hostname)
	cands, err := c.zoneCandidates(credential)
	if err != nil {
		return Zone{}, err
	}
	explicit := credential != ""
	for _, refresh := range []bool{false, true} {
		z, ok, err := c.matchZone(ctx, host, cands, explicit, refresh)
		if err != nil {
			return Zone{}, err
		}
		if ok {
			return z, nil
		}
	}
	return Zone{}, NoZoneError(host, credential)
}

func (c *Client) zoneCandidates(credential string) ([]*credEntry, error) {
	if credential != "" {
		cr, err := c.credByName(credential)
		if err != nil {
			return nil, err
		}
		return []*credEntry{cr}, nil
	}
	out := make([]*credEntry, 0, len(c.credOrder))
	for _, name := range c.credOrder {
		out = append(out, c.creds[name])
	}
	return out, nil
}

// matchZone finds the best zone for host among the candidates' zone lists.
// Any credential whose list cannot be loaded fails the lookup (a longer match
// could hide there; there is no silent fallback).
func (c *Client) matchZone(ctx context.Context, host string, cands []*credEntry, explicit, refresh bool) (Zone, bool, error) {
	var best Zone
	found := false
	for _, cr := range cands {
		zones, err := c.zones(ctx, cr, refresh)
		if err != nil {
			return Zone{}, false, fmt.Errorf("list zones of credential %q: %w", cr.name, err)
		}
		for _, z := range zones {
			if !zoneContains(z.Name, host) || (!explicit && !cr.allowsZone(z.Name)) {
				continue
			}
			if !found || betterZone(z, best) {
				best, found = z, true
			}
		}
	}
	return best, found, nil
}

func zoneContains(zone, host string) bool {
	return host == zone || strings.HasSuffix(host, "."+zone)
}

// betterZone: longer name wins; ties prefer the root credential, then the smaller name.
func betterZone(z, cur Zone) bool {
	if len(z.Name) != len(cur.Name) {
		return len(z.Name) > len(cur.Name)
	}
	if (z.Credential == config.RootCredential) != (cur.Credential == config.RootCredential) {
		return z.Credential == config.RootCredential
	}
	return z.Credential < cur.Credential
}

// allowsZone applies the credential's zone patterns (automatic selection only).
func (cr *credEntry) allowsZone(zone string) bool {
	if len(cr.patterns) == 0 {
		return true
	}
	for _, p := range cr.patterns {
		if p == zone {
			return true
		}
		if ok, err := path.Match(p, zone); err == nil && ok {
			return true
		}
	}
	return false
}

// zones returns the credential's cached zone list, refreshing it per the cache policy.
func (c *Client) zones(ctx context.Context, cr *credEntry, miss bool) ([]Zone, error) {
	zc := &cr.cache
	zc.mu.Lock()
	defer zc.mu.Unlock()
	now := c.now()
	stale := !zc.loaded || now.Sub(zc.fetchedAt) >= ZoneRefreshInterval
	canAttempt := zc.lastAttempt.IsZero() || now.Sub(zc.lastAttempt) >= ZoneMissMinInterval
	if (stale || miss) && canAttempt {
		prev := zc.lastAttempt
		zc.lastAttempt = now
		zones, err := c.fetchZones(ctx, cr)
		switch {
		case err == nil:
			zc.zones, zc.loaded, zc.fetchedAt, zc.lastErr = zones, true, now, nil
		case ctx.Err() != nil:
			zc.lastAttempt = prev // a cancelled caller must not block the next one
			return nil, err
		default:
			zc.lastErr = err
			if zc.loaded {
				c.log.Warn("cloudflare zone list refresh failed; using the zones from the previous refresh",
					"credential", cr.name, "error", err.Error())
			}
		}
	}
	if !zc.loaded {
		if zc.lastErr == nil {
			return nil, errors.New("zone list not loaded")
		}
		return nil, zc.lastErr
	}
	return zc.zones, nil
}

func (c *Client) fetchZones(ctx context.Context, cr *credEntry) ([]Zone, error) {
	wires, err := listAll[zoneWire](ctx, c, cr, "zones.list", "zones", zonesPerPage)
	if err != nil {
		return nil, err
	}
	out := make([]Zone, 0, len(wires))
	for _, w := range wires {
		out = append(out, Zone{
			ID:         w.ID,
			Name:       model.NormalizeHostname(w.Name),
			AccountID:  w.Account.ID,
			Credential: cr.name,
		})
	}
	return out, nil
}
