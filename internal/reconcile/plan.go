package reconcile

import (
	"reflect"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

// planInput is everything plan needs; plan performs no I/O.
type planInput struct {
	Now         time.Time
	InstanceID  string
	RemoveDelay time.Duration
	OrphanTTL   time.Duration
	Winners     []claim          // sorted by key, resolved by observe
	Tracked     []model.Resource // non-rejected rows (after legacy re-keying)
	Held        holdSet
	Live        liveState
}

type rowFail struct {
	Key rowKey
	Msg string
}

// plan is the ordered set of operations of one cycle (§6.6, §6.10).
type plan struct {
	Upserts       []model.Resource // write-ahead rows of winners
	Transitions   []model.Resource // orphan transitions and label-hold errors (DB only)
	Superseded    []dnsOp          // DNS deletes before tunnel PUTs
	Tunnels       []tunnelOp
	DNS           []dnsOp
	Access        []accessOp
	CleanupDNS    []dnsOp
	CleanupAccess []accessDelete
	Purge         []rowKey
	NextDeadline  time.Time
}

// lifecycle is the DB-level outcome before any Cloudflare operation.
type lifecycle struct {
	rows        map[rowKey]model.Resource // every non-rejected row after write-ahead/transitions
	winners     map[rowKey]claim
	upserts     []model.Resource
	transitions []model.Resource
	due         map[rowKey]bool // delete now (cleanup due or superseded)
	superseded  map[rowKey]bool
	purge       []rowKey
	deadline    time.Time
}

// buildPlan is the pure planner.
func buildPlan(in planInput) plan {
	lc := planLifecycle(in)
	sup := planDNSDeletes(in, lc, lc.superseded)
	deleting := map[string]bool{}
	for _, op := range sup {
		if op.Action == dnsDelete {
			deleting[op.Record.ID] = true
		}
	}
	tunnels := planTunnels(in, lc)
	cleanup := map[rowKey]bool{}
	for k := range lc.due {
		if !lc.superseded[k] {
			cleanup[k] = true
		}
	}
	return plan{
		Upserts:       lc.upserts,
		Transitions:   lc.transitions,
		Superseded:    sup,
		Tunnels:       tunnels,
		DNS:           planDNSOps(in, lc, deleting, tunnels),
		Access:        planAccessOps(in, lc),
		CleanupDNS:    planDNSDeletes(in, lc, cleanup),
		CleanupAccess: planAccessDeletes(in, lc),
		Purge:         lc.purge,
		NextDeadline:  lc.deadline,
	}
}

func planLifecycle(in planInput) lifecycle {
	lc := lifecycle{
		rows: indexRows(in.Tracked), winners: map[rowKey]claim{},
		due: map[rowKey]bool{}, superseded: map[rowKey]bool{},
	}
	for _, w := range in.Winners {
		k := w.key()
		lc.winners[k] = w
		old, ok := lc.rows[k]
		n := desiredRow(old, ok, w)
		if !ok || !sameRow(old, n) {
			lc.upserts = append(lc.upserts, n)
		}
		lc.rows[k] = n
	}
	classes := winningClasses(in)
	for _, t := range sortRows(in.Tracked) {
		k := keyOf(t)
		if _, won := lc.winners[k]; won {
			continue
		}
		if in.Held.has(t) {
			lc.holdError(t, in.Held.reason(t))
			continue
		}
		lc.orphan(t, in, classes)
	}
	return lc
}

// holdError surfaces a label error on a held row without touching Cloudflare.
func (lc *lifecycle) holdError(t model.Resource, reason string) {
	if reason == "" || (t.Status == model.StatusError && t.LastError == reason) {
		return
	}
	n := t
	n.Status, n.LastError = model.StatusError, reason
	lc.transitions = append(lc.transitions, n)
	lc.rows[keyOf(t)] = n
}

// orphan applies the orphan transition and schedules deletion/purge.
func (lc *lifecycle) orphan(t model.Resource, in planInput, classes map[string]string) {
	k := keyOf(t)
	n := t
	if n.OrphanedAt.IsZero() {
		n.OrphanedAt = in.Now
		n.Status, n.LastError = model.StatusOrphaned, ""
		if n.Cleanup {
			n.Status = model.StatusPendingCleanup
		}
		lc.transitions = append(lc.transitions, n)
	}
	lc.rows[k] = n
	if cls, ok := classes[n.Hostname]; ok && rowClass(n) != "" && cls != rowClass(n) {
		lc.superseded[k], lc.due[k] = true, true
		return
	}
	if n.Cleanup {
		at := n.OrphanedAt.Add(in.RemoveDelay)
		if !at.After(in.Now) {
			lc.due[k] = true
			return
		}
		lc.later(at)
		return
	}
	if in.OrphanTTL > 0 {
		at := n.OrphanedAt.Add(in.OrphanTTL)
		if !at.After(in.Now) {
			lc.purge = append(lc.purge, k)
			return
		}
		lc.later(at)
	}
}

func (lc *lifecycle) later(at time.Time) {
	if lc.deadline.IsZero() || at.Before(lc.deadline) {
		lc.deadline = at
	}
}

// winningClasses maps hostname -> class of winners and held rows.
func winningClasses(in planInput) map[string]string {
	out := map[string]string{}
	for _, w := range in.Winners {
		if w.Class != "" {
			out[w.Res.Hostname] = w.Class
		}
	}
	for _, t := range sortRows(in.Tracked) {
		if _, ok := out[t.Hostname]; !ok && rowClass(t) != "" && in.Held.has(t) {
			out[t.Hostname] = rowClass(t)
		}
	}
	return out
}

// desiredRow merges a winner's spec onto its existing row (write-ahead row).
func desiredRow(old model.Resource, hasOld bool, w claim) model.Resource {
	n := w.Res
	n.Rejected = false
	if !hasOld {
		n.Status = model.StatusPending
		return n
	}
	n.ID, n.CFID, n.PolicyIDs, n.Legacy = old.ID, old.CFID, old.PolicyIDs, old.Legacy
	n.CreatedAt, n.UpdatedAt, n.SyncedAt = old.CreatedAt, old.UpdatedAt, old.SyncedAt
	n.Status, n.LastError = old.Status, old.LastError
	if w.Err != "" && n.Kind != model.KindTunnel {
		n.ZoneID, n.AccountID, n.Credential = old.ZoneID, old.AccountID, old.Credential
		if n.Content == "" {
			n.Content = old.Content
		}
	}
	if specChanged(old, n) || old.Owner.Key() != n.Owner.Key() || !old.OrphanedAt.IsZero() {
		n.Status = model.StatusPending
	}
	n.OrphanedAt = time.Time{}
	return n
}

// specOf strips bookkeeping fields so only the desired spec is compared.
func specOf(r model.Resource) model.Resource {
	s := r
	s.ID, s.Rejected, s.Status, s.LastError, s.CFID, s.PolicyIDs, s.Legacy = 0, false, "", "", "", nil, false
	s.CreatedAt, s.UpdatedAt, s.OrphanedAt, s.SyncedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	s.Owner = model.Owner{}
	return s
}

func specChanged(a, b model.Resource) bool { return !reflect.DeepEqual(specOf(a), specOf(b)) }

func normRow(r model.Resource) model.Resource {
	if len(r.PolicyIDs) == 0 {
		r.PolicyIDs = nil
	}
	return r
}

// sameRow reports whether two rows would persist identically.
func sameRow(a, b model.Resource) bool {
	a, b = normRow(a), normRow(b)
	if !a.CreatedAt.Equal(b.CreatedAt) || !a.UpdatedAt.Equal(b.UpdatedAt) ||
		!a.OrphanedAt.Equal(b.OrphanedAt) || !a.SyncedAt.Equal(b.SyncedAt) {
		return false
	}
	a.CreatedAt, a.UpdatedAt, a.OrphanedAt, a.SyncedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	b.CreatedAt, b.UpdatedAt, b.OrphanedAt, b.SyncedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

// tunnelOp is the merged configuration of one tunnel.
type tunnelOp struct {
	Tunnel  cloudflare.Tunnel
	Put     bool
	Config  cloudflare.TunnelConfig
	Applied []rowKey  // winner rows that become active on success
	Failed  []rowFail // winner (and delete) rows that error regardless
	Deletes []rowKey  // rows dropped from the DB on success
	// Blocked: the live config could not be read; nothing was merged.
	Blocked bool
	// HostOK lists hostnames with at least one applied winner rule.
	HostOK map[string]bool
}

func planTunnels(in planInput, lc lifecycle) []tunnelOp {
	ids := map[string]bool{}
	for _, w := range in.Winners {
		if w.Res.Kind == model.KindTunnel {
			ids[w.Res.TunnelID] = true
		}
	}
	for _, t := range in.Tracked {
		if t.Kind == model.KindTunnel && !in.Held.has(t) {
			ids[t.TunnelID] = true
		}
	}
	var out []tunnelOp
	for _, id := range sortedKeys(ids) {
		out = append(out, planTunnel(in, lc, id))
	}
	return out
}

func planTunnel(in planInput, lc lifecycle, id string) tunnelOp {
	owned := map[ingressID]bool{}
	var keep []keepRule
	var winners, deletes []rowKey
	for _, t := range sortRows(in.Tracked) {
		if t.Kind != model.KindTunnel || t.TunnelID != id {
			continue
		}
		k := keyOf(t)
		owned[ingressID{Host: t.Hostname, Path: t.Path}] = true
		if _, won := lc.winners[k]; !won && lc.due[k] {
			deletes = append(deletes, k)
		} else if !won {
			keep = append(keep, keepRuleOf(lc.rows[k]))
		}
	}
	for _, w := range in.Winners {
		if w.Res.Kind == model.KindTunnel && w.Res.TunnelID == id {
			winners = append(winners, w.key())
			keep = append(keep, keepRuleOf(lc.rows[w.key()]))
		}
	}
	tl, ok := in.Live.Tunnels[id]
	op := tunnelOp{Tunnel: tl.Tunnel, Deletes: deletes, HostOK: map[string]bool{}}
	if !ok || tl.Err != nil {
		msg := "tunnel " + id + " configuration unavailable"
		if ok {
			msg = "get tunnel configuration: " + tl.Err.Error()
		}
		op.Blocked, op.Deletes = true, nil
		for _, k := range append(append([]rowKey(nil), winners...), deletes...) {
			op.Failed = append(op.Failed, rowFail{Key: k, Msg: msg})
		}
		return op
	}
	m := mergeIngress(tl.Config, owned, keep)
	op.Put, op.Config = m.Changed, m.Config
	conflicted := map[rowKey]bool{}
	for _, k := range m.Conflicts {
		conflicted[k] = true
	}
	for _, k := range winners {
		if conflicted[k] {
			op.Failed = append(op.Failed, rowFail{Key: k, Msg: model.ConflictNotOwned + " (unmanaged ingress rule uses this hostname)"})
			continue
		}
		op.Applied = append(op.Applied, k)
		op.HostOK[lc.rows[k].Hostname] = true
	}
	return op
}
