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
func resolve(claims []claim, tracked []model.Resource, held holdSet) resolution {
	tracked, rekeyed := rekeyLegacy(tracked, claims)
	byKey := indexRows(tracked)
	kept, rejClass := resolveClasses(claims, tracked, held)
	winners, rejKey := pickWinners(kept, byKey, held)
	winners = append(winners, deriveCNAMEs(winners, byKey, held)...)
	access, rejAccess := accessClaims(winners, byKey, held)
	winners = append(winners, access...)
	rejected := append(append(rejClass, rejKey...), rejAccess...)
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
	src := "local"
	if o.AgentID != model.LocalAgentID {
		src = "agent " + o.AgentID
	}
	return fmt.Sprintf("%s container %s service %s", src, o.ContainerName, o.Service)
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

// isClaiming reports whether a row's owner still claims it (same key, owner
// and class) or the owner's source is held.
func isClaiming(r model.Resource, idx map[rowKey]map[string]string, held holdSet) bool {
	if held.has(r) {
		return true
	}
	cls, ok := idx[keyOf(r)][r.Owner.Key()]
	return ok && cls == rowClass(r)
}

type hostClass struct {
	class string
	owner model.Owner
}

// resolveClasses enforces one hostname class per hostname (§6.4 step 1).
func resolveClasses(claims []claim, tracked []model.Resource, held holdSet) ([]claim, []model.Resource) {
	idx := claimIndex(claims)
	rows := append([]model.Resource(nil), tracked...)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Owner.Key() != rows[j].Owner.Key() {
			return rows[i].Owner.Less(rows[j].Owner)
		}
		return keyOf(rows[i]).less(keyOf(rows[j]))
	})
	classes := map[string]hostClass{}
	for _, r := range rows {
		cls := rowClass(r)
		if _, seen := classes[r.Hostname]; cls == "" || seen || !isClaiming(r, idx, held) {
			continue
		}
		classes[r.Hostname] = hostClass{class: cls, owner: r.Owner}
	}
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
		reason := fmt.Sprintf("conflict: hostname %s is used by %s as %s", c.Res.Hostname, describeOwner(hc.owner), hc.class)
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

// winnerIndex picks the winning claim of one key: the existing row's owner
// if it still claims, else the first claimant. -1 = the key is held.
func winnerIndex(cs []claim, row model.Resource, hasRow bool, held holdSet) int {
	if !hasRow {
		return 0
	}
	if held.has(row) {
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
	return fmt.Sprintf("conflict: %s already claimed by %s", k.Key, describeOwner(o))
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
		return model.AccessPolicyDef{}, fmt.Sprintf("access policy definition %q not found", name)
	}
	for _, d := range found[1:] {
		if !reflect.DeepEqual(d, found[0]) {
			return model.AccessPolicyDef{}, fmt.Sprintf("ambiguous access policy definition %q", name)
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
