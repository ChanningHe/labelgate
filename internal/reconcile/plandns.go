package reconcile

import (
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

type dnsAction int

const (
	dnsNoop   dnsAction = iota // record matches; ensure CFID/status
	dnsCreate                  // create Record
	dnsUpdate                  // update Record (Record.ID set)
	dnsDelete                  // delete Record.ID, then drop the row
	dnsDrop                    // drop the row only (Msg = optional warning)
	dnsFail                    // record Msg on the row, no mutation
)

// dnsOp is one planned DNS operation for one row (§6.8).
type dnsOp struct {
	Key    rowKey
	Action dnsAction
	Zone   cloudflare.Zone
	Record cloudflare.DNSRecord
	Msg    string
	// Blockers are record IDs that must be deleted (superseded) before create.
	Blockers []string
	// WaitTunnel: apply only if this tunnel's PUT succeeded (tunnel CNAMEs).
	WaitTunnel string
}

// maxCommentLen is Cloudflare's DNS comment limit (free plans).
const maxCommentLen = 100

func dnsComment(instanceID, user string) string {
	c := model.DNSComment(instanceID, user)
	if utf8.RuneCountInString(c) <= maxCommentLen {
		return c
	}
	return strings.TrimRight(string([]rune(c)[:maxCommentLen]), " ")
}

// desiredRecord is the record a row should have on Cloudflare, normalized
// the way Cloudflare stores it so comparisons with live records never flap.
func desiredRecord(r model.Resource, instanceID string) cloudflare.DNSRecord {
	return cloudflare.NormalizeDNSRecord(cloudflare.DNSRecord{
		Name: r.Hostname, Type: r.RecordType, Content: r.Content, Proxied: r.Proxied,
		TTL: r.TTL, Priority: r.Priority, Weight: r.Weight, Port: r.Port,
		Flags: r.Flags, Tag: r.Tag, Comment: dnsComment(instanceID, r.Comment),
	})
}

func isHostTarget(t model.DNSType) bool {
	return t == model.DNSTypeCNAME || t == model.DNSTypeMX || t == model.DNSTypeSRV
}

func sameContent(t model.DNSType, a, b string) bool {
	if t == model.DNSTypeA || t == model.DNSTypeAAAA {
		x, errA := netip.ParseAddr(a)
		y, errB := netip.ParseAddr(b)
		if errA == nil && errB == nil {
			return x == y
		}
	}
	if isHostTarget(t) {
		return model.NormalizeHostname(a) == model.NormalizeHostname(b)
	}
	return a == b
}

// recordDiffers compares the fields labelgate manages for the record type.
func recordDiffers(live, want cloudflare.DNSRecord) bool {
	live = cloudflare.NormalizeDNSRecord(live)
	if !sameContent(want.Type, live.Content, want.Content) || live.Proxied != want.Proxied ||
		normTTL(live.TTL, live.Proxied) != want.TTL || live.Comment != want.Comment {
		return true
	}
	switch want.Type {
	case model.DNSTypeMX:
		return live.Priority != want.Priority
	case model.DNSTypeSRV:
		return live.Priority != want.Priority || live.Weight != want.Weight || live.Port != want.Port
	case model.DNSTypeCAA:
		return live.Flags != want.Flags || live.Tag != want.Tag
	}
	return false
}

func singleValued(t model.DNSType) bool {
	return t == model.DNSTypeA || t == model.DNSTypeAAAA || t == model.DNSTypeCNAME
}

func sameIdentity(rec, want cloudflare.DNSRecord) bool {
	if rec.Name != want.Name || rec.Type != want.Type {
		return false
	}
	return singleValued(want.Type) || recordContentKey(rec) == recordContentKey(want)
}

func cnameClash(rec, want cloudflare.DNSRecord) bool {
	return rec.Name == want.Name && (rec.Type == model.DNSTypeCNAME || want.Type == model.DNSTypeCNAME) &&
		!sameIdentity(rec, want)
}

func findRecord(recs []cloudflare.DNSRecord, id string) (cloudflare.DNSRecord, bool) {
	for _, r := range recs {
		if r.ID == id {
			return r, true
		}
	}
	return cloudflare.DNSRecord{}, false
}

func failDNS(k rowKey, msg string) dnsOp { return dnsOp{Key: k, Action: dnsFail, Msg: msg} }

// planDNS decides the operation for one DNS winner.
func planDNS(row model.Resource, w claim, zl zoneLive, observed bool, instanceID string, deleting map[string]bool) dnsOp {
	k := keyOf(row)
	switch {
	case w.Err != "":
		return failDNS(k, w.Err)
	case !observed:
		return failDNS(k, "zone "+w.Zone.ID+" not observed")
	case zl.Err != nil:
		return failDNS(k, "list dns records: "+zl.Err.Error())
	}
	want := desiredRecord(row, instanceID)
	op := dnsOp{Key: k, Zone: w.Zone}
	if row.CFID != "" {
		if rec, ok := findRecord(zl.Records, row.CFID); ok {
			if !model.IsOwnedComment(instanceID, rec.Comment) && !row.Legacy {
				return failDNS(k, model.ConflictNotOwned+" (ownership marker removed)")
			}
			return existingOp(op, row, rec, want)
		}
	}
	return createOp(op, row, want, zl.Records, instanceID, deleting)
}

// existingOp updates an owned record when it differs or must be stamped.
func existingOp(op dnsOp, row model.Resource, rec, want cloudflare.DNSRecord) dnsOp {
	want.ID = rec.ID
	op.Record = want
	op.Action = dnsNoop
	if row.Legacy || recordDiffers(rec, want) {
		op.Action = dnsUpdate
	}
	return op
}

// createOp handles the create path: adopt our own record (crash recovery or
// legacy tunnel CNAME), refuse foreign records, wait for superseded ones.
func createOp(op dnsOp, row model.Resource, want cloudflare.DNSRecord, recs []cloudflare.DNSRecord, id string, deleting map[string]bool) dnsOp {
	for _, rec := range recs {
		if sameIdentity(rec, want) && (model.IsOwnedComment(id, rec.Comment) || legacyCNAME(row, rec, want)) {
			return existingOp(op, row, rec, want)
		}
	}
	for _, rec := range recs {
		if sameIdentity(rec, want) {
			return failDNS(op.Key, model.ConflictNotOwned)
		}
	}
	for _, rec := range recs {
		if !cnameClash(rec, want) {
			continue
		}
		if !model.IsOwnedComment(id, rec.Comment) {
			return failDNS(op.Key, model.ConflictNotOwned)
		}
		if !deleting[rec.ID] {
			return failDNS(op.Key, "waiting for superseded "+string(rec.Type)+" record "+rec.ID)
		}
		op.Blockers = append(op.Blockers, rec.ID)
	}
	op.Action, op.Record = dnsCreate, want
	return op
}

// legacyCNAME: a synthesized v0.2 tunnel CNAME pointing at our tunnel is adopted.
func legacyCNAME(row model.Resource, rec, want cloudflare.DNSRecord) bool {
	return row.Legacy && row.TunnelID != "" && rec.Type == model.DNSTypeCNAME &&
		sameContent(model.DNSTypeCNAME, rec.Content, want.Content)
}

// planDNSDelete decides how to remove one DNS row.
func planDNSDelete(row model.Resource, zl zoneLive, observed bool, instanceID string) dnsOp {
	k := keyOf(row)
	op := dnsOp{Key: k, Zone: zoneOfRow(row)}
	switch {
	case row.CFID == "":
		op.Action = dnsDrop
		return op
	case !observed:
		return failDNS(k, "zone "+row.ZoneID+" not observed")
	case zl.Err != nil:
		return failDNS(k, "list dns records: "+zl.Err.Error())
	}
	rec, ok := findRecord(zl.Records, row.CFID)
	switch {
	case !ok:
		op.Action = dnsDrop
	case model.IsOwnedComment(instanceID, rec.Comment) || row.Legacy:
		op.Action, op.Record = dnsDelete, rec
	default:
		op.Action, op.Msg = dnsDrop, "ownership marker removed from record "+rec.ID+"; left in place"
	}
	return op
}

// planDNSOps plans every DNS winner; tunnel CNAMEs depend on their tunnel.
func planDNSOps(in planInput, lc lifecycle, deleting map[string]bool, tunnels []tunnelOp) []dnsOp {
	byTunnel := map[string]tunnelOp{}
	for _, t := range tunnels {
		byTunnel[t.Tunnel.ID] = t
	}
	var out []dnsOp
	for _, w := range in.Winners {
		if w.Res.Kind != model.KindDNS {
			continue
		}
		row := lc.rows[w.key()]
		zl, ok := in.Live.Zones[w.Zone.ID]
		op := planDNS(row, w, zl, ok, in.InstanceID, deleting)
		if tid := w.Res.TunnelID; tid != "" && op.Action != dnsFail {
			op = gateOnTunnel(op, byTunnel[tid], tid, row.Hostname)
		}
		out = append(out, op)
	}
	return out
}

func gateOnTunnel(op dnsOp, t tunnelOp, tid, host string) dnsOp {
	switch {
	case t.Tunnel.ID == "" || t.Blocked:
		return failDNS(op.Key, "waiting for tunnel "+tid+" configuration")
	case !t.HostOK[host]:
		return failDNS(op.Key, "no ingress rule applied for "+host)
	}
	op.WaitTunnel = tid
	return op
}

// planDNSDeletes plans deletion of the DNS rows in set (sorted).
func planDNSDeletes(in planInput, lc lifecycle, set map[rowKey]bool) []dnsOp {
	var out []dnsOp
	for _, t := range sortRows(in.Tracked) {
		k := keyOf(t)
		if t.Kind != model.KindDNS || !set[k] {
			continue
		}
		zl, ok := in.Live.Zones[t.ZoneID]
		out = append(out, planDNSDelete(lc.rows[k], zl, ok, in.InstanceID))
	}
	return out
}
