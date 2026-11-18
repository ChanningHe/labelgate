package reconcile

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/channinghe/labelgate/internal/model"
)

// resolution is the outcome of conflict resolution (§6.4).
type resolution struct {
	Winners  []claim          // sorted by key; at most one per (kind, key)
	Rejected []model.Resource // new rejected rows
	Tracked  []model.Resource // non-rejected rows after legacy re-keying
	Rekeyed  []model.Resource // rows whose key changed (persist first)
}

// resolve applies "existing owner wins" deterministically. It is pure.
// A hostname belongs to the source of its rows (any state); within that
// source, keys and classes are handed over immediately.
func resolve(claims []claim, tracked []model.Resource, held holdSet) resolution {
	tracked, rekeyed := rekeyLegacy(tracked, claims)
	byKey := indexRows(tracked)
	owners := hostSources(claims, tracked)
	sourced, rejSource := resolveSources(claims, owners)
	kept, rejClass := resolveClasses(sourced, tracked, owners, newLiveClaims(claims, held))
	winners, rejKey := pickWinners(kept, byKey, held)
	winners = append(winners, deriveCNAMEs(winners, byKey, held)...)
	access, rejAccess := accessClaims(winners, byKey, held)
	winners = append(winners, access...)
	rejected := concatRows(rejSource, rejClass, rejKey, rejAccess)
	return resolution{
		Winners:  sortClaimsByKey(winners),
		Rejected: rejected,
		Tracked:  tracked,
		Rekeyed:  rekeyed,
	}
}

func indexRows(rows []model.Resource) map[rowKey]model.Resource {
	out := make(map[rowKey]model.Resource, len(rows))
	for _, r := range rows {
		out[keyOf(r)] = r
	}
	return out
}

func describeOwner(o model.Owner) string {
	return fmt.Sprintf("container %s service %s on %s", o.ContainerName, o.Service, describeSource(o.AgentID))
}

// rekeyLegacy rewrites the key of a legacy row whose identity fields and
// owner match a claim with a different key (§6.4 step 0).
func rekeyLegacy(tracked []model.Resource, claims []claim) ([]model.Resource, []model.Resource) {
	keys := make(map[rowKey]bool, len(tracked))
	for _, r := range tracked {
		keys[keyOf(r)] = true
	}
	sorted := sortClaims(claims)
	out := make([]model.Resource, 0, len(tracked))
	var changed []model.Resource
	for _, r := range tracked {
		if c, ok := legacyMatch(r, sorted, keys); ok {
			delete(keys, keyOf(r))
			keys[c.key()] = true
			r.Key = c.Res.Key
			changed = append(changed, r)
		}
		out = append(out, r)
	}
	return out, changed
}

func legacyMatch(r model.Resource, claims []claim, keys map[rowKey]bool) (claim, bool) {
	if !r.Legacy || (r.Kind != model.KindDNS && r.Kind != model.KindTunnel) {
		return claim{}, false
	}
	for _, c := range claims {
		x := c.Res
		if x.Kind != r.Kind || x.Hostname != r.Hostname || x.Owner.Key() != r.Owner.Key() ||
			c.key() == keyOf(r) || keys[c.key()] {
			continue
		}
		if r.Kind == model.KindDNS && x.RecordType == r.RecordType {
			return c, true
		}
		if r.Kind == model.KindTunnel && x.TunnelID == r.TunnelID && x.Path == r.Path {
			return c, true
		}
	}
	return claim{}, false
}

// claimIndex maps key -> owner key -> class of the claims.
func claimIndex(claims []claim) map[rowKey]map[string]string {
	out := map[rowKey]map[string]string{}
	for _, c := range claims {
		m := out[c.key()]
		if m == nil {
			m = map[string]string{}
			out[c.key()] = m
		}
		m[c.Res.Owner.Key()] = c.Class
	}
	return out
}

// liveClaims tells whether a tracked row is still claimed by its owner
// (same key, owner and class) or held this cycle.
type liveClaims struct {
	idx  map[rowKey]map[string]string
	held holdSet
}

func newLiveClaims(claims []claim, held holdSet) liveClaims {
	return liveClaims{idx: claimIndex(claims), held: held}
}

// claiming reports whether a row's owner still claims it (same key, owner
// and class) or the row is held.
func (rs liveClaims) claiming(r model.Resource) bool {
	if rs.held.has(r) {
		return true
	}
	cls, ok := rs.idx[keyOf(r)][r.Owner.Key()]
	return ok && cls == rowClass(r)
}

// sourceOf names the source of an owner: "" (local) or the agent ID.
func sourceOf(o model.Owner) string { return o.AgentID }

// describeSource names a source for users: "the main instance" or "agent <id>".
// It is display text only; identity keys keep using the raw source string.
func describeSource(src string) string {
	if src == model.LocalAgentID {
		return "the main instance"
	}
	return "agent " + src
}

// rowOlder orders rows by creation (oldest first), then ID, then owner.
func rowOlder(a, b model.Resource) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Owner.Less(b.Owner)
}

// hostSources maps every hostname to the one source owning it: the source of
// the oldest tracked row in any state (active, held, error, orphaned within
// or past remove_delay), so a hostname stays with its source until its last
// row is deleted or forgotten; else the source of the first claim
// (claimLess: local first, then agents by ID).
func hostSources(claims []claim, rows []model.Resource) map[string]string {
	sorted := append([]model.Resource(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return rowOlder(sorted[i], sorted[j]) })
	out := map[string]string{}
	for _, r := range sorted {
		if _, seen := out[r.Hostname]; !seen {
			out[r.Hostname] = sourceOf(r.Owner)
		}
	}
	for _, c := range sortClaims(claims) {
		if _, seen := out[c.Res.Hostname]; !seen {
			out[c.Res.Hostname] = sourceOf(c.Res.Owner)
		}
	}
	return out
}

// resolveSources enforces per-source hostname ownership (§6.4 step 1a): a
// claim on a hostname owned by another source is rejected, whatever its
// class, kind, path or record type.
func resolveSources(claims []claim, owners map[string]string) ([]claim, []model.Resource) {
	var kept []claim
	var rejected []model.Resource
	for _, c := range sortClaims(claims) {
		src := owners[c.Res.Hostname]
		if src == sourceOf(c.Res.Owner) {
			kept = append(kept, c)
			continue
		}
		reason := fmt.Sprintf("conflict: hostname %s is already managed by %s, and a hostname can belong to only one instance (main or one agent); "+
			"remove this label or the one on %s", c.Res.Hostname, describeSource(src), describeSource(src))
		rejected = append(rejected, rejectRow(c.Res, reason))
	}
	return kept, rejected
}

type hostClass struct {
	class string
	owner model.Owner
}

// incumbentClasses maps each hostname to the class of its oldest row of the
// owning source that its owner still claims (or that is held). Rows nobody
// claims any more never block a class change within their source.
func incumbentClasses(tracked []model.Resource, owners map[string]string, rs liveClaims) map[string]hostClass {
	rows := append([]model.Resource(nil), tracked...)
	sort.SliceStable(rows, func(i, j int) bool { return rowOlder(rows[i], rows[j]) })
	out := map[string]hostClass{}
	for _, r := range rows {
		cls := rowClass(r)
		if _, seen := out[r.Hostname]; seen || cls == "" || sourceOf(r.Owner) != owners[r.Hostname] || !rs.claiming(r) {
			continue
		}
		out[r.Hostname] = hostClass{class: cls, owner: r.Owner}
	}
	return out
}

// resolveClasses enforces one hostname class per hostname (§6.4 step 1):
// the incumbent class while its claimant still claims, else the class of the
// first claim.
func resolveClasses(claims []claim, tracked []model.Resource, owners map[string]string, rs liveClaims) ([]claim, []model.Resource) {
	classes := incumbentClasses(tracked, owners, rs)
	var kept []claim
	var rejected []model.Resource
	for _, c := range sortClaims(claims) {
		hc, ok := classes[c.Res.Hostname]
		if !ok {
			classes[c.Res.Hostname] = hostClass{class: c.Class, owner: c.Res.Owner}
			kept = append(kept, c)
			continue
		}
		if hc.class == c.Class {
			kept = append(kept, c)
			continue
		}
		reason := fmt.Sprintf("conflict: hostname %s already has %s from %s; a hostname can have DNS records, one CNAME record or "+
			"one tunnel route, not a mix: remove one of the labels", c.Res.Hostname, describeClass(hc.class), describeOwner(hc.owner))
		rejected = append(rejected, rejectRow(c.Res, reason))
	}
	return kept, rejected
}

// groupClaims groups sorted claims by key; keys are returned sorted.
func groupClaims(claims []claim) ([]rowKey, map[rowKey][]claim) {
	groups := map[rowKey][]claim{}
	var keys []rowKey
	for _, c := range sortClaims(claims) {
		if _, ok := groups[c.key()]; !ok {
			keys = append(keys, c.key())
		}
		groups[c.key()] = append(groups[c.key()], c)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
	return keys, groups
}

// winnerIndex picks the winning claim of one key (all claims come from the
// hostname's source): the existing row's owner if it still claims, else the
// first claimant, so same-source handover is immediate. A held row, or a row
// of another source (mixed legacy data), is never taken over: -1.
func winnerIndex(cs []claim, row model.Resource, hasRow bool, held holdSet) int {
	if !hasRow {
		return 0
	}
	if held.has(row) || sourceOf(row.Owner) != sourceOf(cs[0].Res.Owner) {
		return -1
	}
	for i, c := range cs {
		if c.Res.Owner.Key() == row.Owner.Key() && c.Class == rowClass(row) {
			return i
		}
	}
	return 0
}

func conflictReason(k rowKey, o model.Owner) string {
	return fmt.Sprintf("conflict: %s is already managed by %s; remove the duplicate label from one of the containers", describeKey(k), describeOwner(o))
}

// pickWinners resolves competing claims per (kind, key) (§6.4 step 2).
func pickWinners(claims []claim, rows map[rowKey]model.Resource, held holdSet) ([]claim, []model.Resource) {
	keys, groups := groupClaims(claims)
	var winners []claim
	var rejected []model.Resource
	for _, k := range keys {
		cs := groups[k]
		row, hasRow := rows[k]
		w := winnerIndex(cs, row, hasRow, held)
		owner := row.Owner
		if w >= 0 {
			winners = append(winners, cs[w])
			owner = cs[w].Res.Owner
		}
		for i, c := range cs {
			if i != w {
				rejected = append(rejected, rejectRow(c.Res, conflictReason(k, owner)))
			}
		}
	}
	return winners, rejected
}

// deriveCNAMEs adds one tunnel CNAME claim per hostname with winning tunnel
// rules (§6.2). The owner of record is the existing row's owner if it still
// has a winning rule on the hostname, else the smallest rule owner.
func deriveCNAMEs(winners []claim, rows map[rowKey]model.Resource, held holdSet) []claim {
	byHost := map[string][]claim{}
	for _, w := range winners {
		if w.Res.Kind == model.KindTunnel {
			byHost[w.Res.Hostname] = append(byHost[w.Res.Hostname], w)
		}
	}
	var out []claim
	for _, host := range sortedKeys(byHost) {
		rules := sortClaims(byHost[host])
		key := rowKey{Kind: model.KindDNS, Key: model.DNSKey(host, model.DNSTypeCNAME, "")}
		row, hasRow := rows[key]
		if hasRow && held.has(row) {
			continue
		}
		p := rules[0]
		for _, r := range rules {
			if hasRow && r.Res.Owner.Key() == row.Owner.Key() && rowClass(row) == r.Class {
				p = r
				break
			}
		}
		out = append(out, cnameClaim(p))
	}
	return out
}

func cnameClaim(rule claim) claim {
	tid := rule.Res.TunnelID
	res := model.Resource{
		Kind: model.KindDNS, Key: model.DNSKey(rule.Res.Hostname, model.DNSTypeCNAME, ""),
		Hostname: rule.Res.Hostname, Owner: rule.Res.Owner, Credential: rule.SpecCred,
		RecordType: model.DNSTypeCNAME, Content: model.TunnelTarget(tid), Proxied: true, TTL: 1,
		TunnelID: tid, Cleanup: rule.Res.Cleanup,
	}
	return claim{
		Res: res, Class: rule.Class, Target: res.Content, SpecCred: rule.SpecCred,
		Src: rule.Src, Ctr: rule.Ctr, Tunnel: rule.Tunnel,
	}
}

// lookupDef finds an access definition scoped to the claimant's source:
// its own container first, else the source's other containers (§6.2).
func lookupDef(src *sourceInfo, ctr, name string) (model.AccessPolicyDef, string) {
	for _, d := range src.Defs[ctr] {
		if d.Name == name {
			return d, ""
		}
	}
	var found []model.AccessPolicyDef
	for _, cn := range sortedKeys(src.Defs) {
		for _, d := range src.Defs[cn] {
			if cn != ctr && d.Name == name {
				found = append(found, d)
			}
		}
	}
	if len(found) == 0 {
		return model.AccessPolicyDef{}, fmt.Sprintf("access policy %q is not defined: add %s.access.%s.* labels to this container "+
			"or another container of the same host", name, src.LabelPrefix, name)
	}
	for _, d := range found[1:] {
		if !reflect.DeepEqual(d, found[0]) {
			return model.AccessPolicyDef{}, fmt.Sprintf("access policy %q is defined differently on several containers of this host: "+
				"define it on this container, or make the definitions identical", name)
		}
	}
	return found[0], ""
}

func appName(def model.AccessPolicyDef) string {
	if def.AppName != "" {
		return def.AppName
	}
	return "labelgate-" + def.Name
}

func accessCandidate(w claim) (claim, *model.Resource) {
	res := model.Resource{
		Kind: model.KindAccess, Key: model.AccessKey(w.Res.Hostname), Hostname: w.Res.Hostname,
		Owner: w.Res.Owner, Credential: w.SpecCred, PolicyName: w.AccessRef, Cleanup: w.Res.Cleanup,
	}
	def, reason := lookupDef(w.Src, w.Ctr.Name, w.AccessRef)
	if reason != "" {
		rej := rejectRow(res, reason)
		return claim{}, &rej
	}
	res.AppName = appName(def)
	if len(def.Policies) > 0 {
		res.Decision = def.Policies[0].Decision
	}
	return claim{Res: res, SpecCred: w.SpecCred, Def: &def, Src: w.Src, Ctr: w.Ctr}, nil
}

// accessClaims binds access definitions of winning services and resolves
// competing claims; identical definitions are co-owners (§6.4 step 2).
func accessClaims(winners []claim, rows map[rowKey]model.Resource, held holdSet) ([]claim, []model.Resource) {
	var cands []claim
	var rejected []model.Resource
	for _, w := range winners {
		if w.AccessRef == "" {
			continue
		}
		c, rej := accessCandidate(w)
		if rej != nil {
			rejected = append(rejected, *rej)
			continue
		}
		cands = append(cands, c)
	}
	keys, groups := groupClaims(cands)
	var out []claim
	for _, k := range keys {
		cs := groups[k]
		row, hasRow := rows[k]
		w := winnerIndex(cs, row, hasRow, held)
		owner := row.Owner
		if w >= 0 {
			out = append(out, cs[w])
			owner = cs[w].Res.Owner
		}
		for i, c := range cs {
			if i == w || (w >= 0 && reflect.DeepEqual(*c.Def, *cs[w].Def)) {
				continue
			}
			rejected = append(rejected, rejectRow(c.Res, conflictReason(k, owner)))
		}
	}
	return out, rejected
}

// dedupeRejected keeps the first rejected row per (kind, key, owner).
func dedupeRejected(rows []model.Resource) []model.Resource {
	seen := map[string]bool{}
	var out []model.Resource
	for _, r := range sortRows(rows) {
		id := string(r.Kind) + "\x00" + r.Key + "\x00" + r.Owner.Key()
		if !seen[id] {
			seen[id] = true
			out = append(out, r)
		}
	}
	return out
}
