package reconcile

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

type zoneLive struct {
	Records []cloudflare.DNSRecord
	Err     error
}

type tunnelLive struct {
	Tunnel cloudflare.Tunnel
	Config cloudflare.TunnelConfig
	Err    error
}

type accountLive struct {
	Apps     []cloudflare.AccessApp
	Policies []cloudflare.AccessPolicy
	Err      error
	// TagErr is a failure to ensure the ownership tag; it blocks applying
	// desired apps but not deletions.
	TagErr error
}

// liveState is the Cloudflare state observed in one cycle (§6.5).
type liveState struct {
	Zones    map[string]zoneLive    // zone ID
	Tunnels  map[string]tunnelLive  // tunnel ID
	Accounts map[string]accountLive // accountKey
}

func accountKey(a cloudflare.Account) string { return a.Credential + "/" + a.ID }

func zoneOfRow(r model.Resource) cloudflare.Zone {
	return cloudflare.Zone{ID: r.ZoneID, AccountID: r.AccountID, Credential: r.Credential}
}

func accountOfRow(r model.Resource) cloudflare.Account {
	return cloudflare.Account{ID: r.AccountID, Credential: r.Credential}
}

// needsLive reports whether a tracked row may be mutated this cycle.
func needsLive(r model.Resource, held holdSet) bool {
	return !r.Rejected && !held.has(r)
}

// observer resolves winners and reads live state for one cycle.
type observer struct {
	r     *Reconciler
	zones map[string]zoneResult // host|credential
	ips   map[bool]ipResult     // v6 -> local public IP
}

type zoneResult struct {
	zone cloudflare.Zone
	err  error
}

type ipResult struct {
	addr netip.Addr
	err  error
}

// observed is the outcome of observe.
type observed struct {
	Winners []claim
	Tracked []model.Resource // zone bindings repaired by rebind
	Live    liveState
}

// observe resolves zones and dynamic DNS content of winners, repairs the zone
// binding of tracked rows, then reads the live state referenced by winners
// and mutable tracked rows.
func (r *Reconciler) observe(ctx context.Context, winners []claim, tracked []model.Resource, held holdSet) (observed, error) {
	o := &observer{r: r, zones: map[string]zoneResult{}, ips: map[bool]ipResult{}}
	resolved := make([]claim, len(winners))
	for i, w := range winners {
		resolved[i] = o.resolveWinner(ctx, w)
	}
	rows := make([]model.Resource, len(tracked))
	for i, t := range tracked {
		rows[i] = o.rebind(ctx, t, held)
	}
	live := liveState{
		Zones:    r.observeZones(ctx, resolved, rows, held),
		Tunnels:  r.observeTunnels(ctx, resolved, rows, held),
		Accounts: r.observeAccounts(ctx, resolved, rows, held),
	}
	if err := ctx.Err(); err != nil {
		return observed{}, err
	}
	return observed{Winners: resolved, Tracked: rows, Live: live}, nil
}

// rebind repairs the zone binding of a DNS or Access row Labelgate may still
// have to read or delete: a row whose credential no longer exists (e.g.
// "default" migrated from v0.2 while the v0.3 config has no root token) or
// that has no zone is re-resolved by automatic zone matching. A zone or
// account different from the recorded one is never adopted.
func (o *observer) rebind(ctx context.Context, r model.Resource, held holdSet) model.Resource {
	if (r.Kind != model.KindDNS && r.Kind != model.KindAccess) || !needsLive(r, held) {
		return r
	}
	z, err := o.zone(ctx, r.Hostname, r.Credential)
	switch {
	case errors.Is(err, cloudflare.ErrUnknownCredential):
		if z, err = o.zone(ctx, r.Hostname, ""); err != nil {
			return r
		}
	case err != nil, r.ZoneID != "" && r.AccountID != "":
		return r
	}
	if (r.ZoneID != "" && r.ZoneID != z.ID) || (r.AccountID != "" && r.AccountID != z.AccountID) {
		return r
	}
	r.ZoneID, r.AccountID, r.Credential = z.ID, z.AccountID, z.Credential
	return r
}

func (o *observer) resolveWinner(ctx context.Context, w claim) claim {
	if w.Res.Kind == model.KindTunnel {
		return w
	}
	z, err := o.zone(ctx, w.Res.Hostname, w.SpecCred)
	if err != nil {
		w.Err = fmt.Sprintf("zone for %s: %v", w.Res.Hostname, err)
		return w
	}
	w.Zone = z
	w.Res.ZoneID, w.Res.AccountID, w.Res.Credential = z.ID, z.AccountID, z.Credential
	if w.Res.Kind == model.KindDNS && isDynamicTarget(w.Res.RecordType, w.Target) {
		addr, err := o.target(ctx, w)
		if err != nil {
			w.Err = err.Error()
			return w
		}
		w.Res.Content = addr.String()
	}
	return w
}

func (o *observer) zone(ctx context.Context, host, cred string) (cloudflare.Zone, error) {
	k := host + "|" + cred
	if res, ok := o.zones[k]; ok {
		return res.zone, res.err
	}
	z, err := o.r.cf.ResolveZone(ctx, host, cred)
	o.zones[k] = zoneResult{zone: z, err: err}
	return z, err
}

func familyOK(a netip.Addr, v6 bool) bool {
	if v6 {
		return a.Is6() && !a.Is4In6()
	}
	return a.Is4()
}

// target resolves "auto" and "container" A/AAAA targets (§6.5).
func (o *observer) target(ctx context.Context, w claim) (netip.Addr, error) {
	v6 := w.Res.RecordType == model.DNSTypeAAAA
	family := "IPv4"
	if v6 {
		family = "IPv6"
	}
	if w.Target == model.TargetContainer {
		return containerIP(w.Ctr, v6, family)
	}
	if w.Src.isAgent() {
		raw := w.Src.Snapshot.PublicIP
		if v6 {
			raw = w.Src.Snapshot.PublicIPv6
		}
		a, err := netip.ParseAddr(raw)
		if err != nil || !familyOK(a, v6) {
			return netip.Addr{}, fmt.Errorf("agent %s reported no public %s address", w.Src.AgentID, family)
		}
		return a, nil
	}
	res, ok := o.ips[v6]
	if !ok {
		a, err := o.r.publicIP(ctx, v6)
		res = ipResult{addr: a, err: err}
		o.ips[v6] = res
	}
	if res.err != nil {
		return netip.Addr{}, fmt.Errorf("public %s address: %w", family, res.err)
	}
	if !familyOK(res.addr, v6) {
		return netip.Addr{}, fmt.Errorf("public %s address: got %s", family, res.addr)
	}
	return res.addr, nil
}

// containerIP returns the first address of the right family, networks sorted by name.
func containerIP(c *model.Container, v6 bool, family string) (netip.Addr, error) {
	for _, name := range sortedKeys(c.Networks) {
		if a, err := netip.ParseAddr(c.Networks[name]); err == nil && familyOK(a, v6) {
			return a, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("container %s has no %s address", c.Name, family)
}

func (r *Reconciler) observeZones(ctx context.Context, winners []claim, tracked []model.Resource, held holdSet) map[string]zoneLive {
	zones := map[string]cloudflare.Zone{}
	for _, w := range winners {
		if w.Res.Kind == model.KindDNS && w.Err == "" {
			zones[w.Zone.ID] = w.Zone
		}
	}
	for _, t := range tracked {
		if _, ok := zones[t.ZoneID]; !ok && t.Kind == model.KindDNS && t.ZoneID != "" && needsLive(t, held) {
			zones[t.ZoneID] = zoneOfRow(t)
		}
	}
	out := make(map[string]zoneLive, len(zones))
	for _, id := range sortedKeys(zones) {
		recs, err := r.cf.ListDNSRecords(ctx, zones[id])
		out[id] = zoneLive{Records: sortRecords(recs), Err: err}
	}
	return out
}

func sortRecords(in []cloudflare.DNSRecord) []cloudflare.DNSRecord {
	out := append([]cloudflare.DNSRecord(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Content != b.Content {
			return a.Content < b.Content
		}
		return a.ID < b.ID
	})
	return out
}

func (r *Reconciler) observeTunnels(ctx context.Context, winners []claim, tracked []model.Resource, held holdSet) map[string]tunnelLive {
	out := map[string]tunnelLive{}
	for _, w := range winners {
		if w.Res.Kind == model.KindTunnel {
			out[w.Tunnel.ID] = tunnelLive{Tunnel: w.Tunnel}
		}
	}
	for _, t := range tracked {
		if _, ok := out[t.TunnelID]; ok || t.Kind != model.KindTunnel || t.TunnelID == "" || !needsLive(t, held) {
			continue
		}
		tun, err := r.cf.ResolveTunnel(t.TunnelID)
		out[t.TunnelID] = tunnelLive{Tunnel: tun, Err: err}
	}
	for _, id := range sortedKeys(out) {
		tl := out[id]
		if tl.Err != nil {
			continue
		}
		tl.Config, tl.Err = r.cf.GetTunnelConfig(ctx, tl.Tunnel)
		out[id] = tl
	}
	return out
}

func (r *Reconciler) observeAccounts(ctx context.Context, winners []claim, tracked []model.Resource, held holdSet) map[string]accountLive {
	accts := map[string]cloudflare.Account{}
	desired := map[string]bool{}
	for _, w := range winners {
		if w.Res.Kind == model.KindAccess && w.Err == "" {
			a := w.Zone.Account()
			accts[accountKey(a)], desired[accountKey(a)] = a, true
		}
	}
	for _, t := range tracked {
		if t.Kind == model.KindAccess && (t.CFID != "" || len(t.PolicyIDs) > 0) && needsLive(t, held) {
			a := accountOfRow(t)
			accts[accountKey(a)] = a
		}
	}
	out := make(map[string]accountLive, len(accts))
	for _, k := range sortedKeys(accts) {
		out[k] = r.readAccount(ctx, accts[k], desired[k])
	}
	return out
}

func (r *Reconciler) readAccount(ctx context.Context, a cloudflare.Account, ensureTag bool) accountLive {
	apps, err := r.cf.ListAccessApps(ctx, a)
	if err != nil {
		return accountLive{Err: err}
	}
	pols, err := r.cf.ListAccessPolicies(ctx, a)
	if err != nil {
		return accountLive{Err: err}
	}
	al := accountLive{Apps: apps, Policies: pols}
	if ensureTag && !r.tagged[accountKey(a)] {
		if err := r.cf.EnsureAccessTag(ctx, a, model.AccessTag(r.settings.InstanceID)); err != nil {
			al.TagErr = err
		} else {
			r.tagged[accountKey(a)] = true
		}
	}
	return al
}
