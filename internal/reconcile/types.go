package reconcile

import (
	"sort"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

// rowKey is the identity of a non-rejected resource row.
type rowKey struct {
	Kind model.Kind
	Key  string
}

func keyOf(r model.Resource) rowKey { return rowKey{Kind: r.Kind, Key: r.Key} }

func (k rowKey) less(o rowKey) bool {
	if k.Kind != o.Kind {
		return k.Kind < o.Kind
	}
	return k.Key < o.Key
}

func (k rowKey) String() string { return string(k.Kind) + " " + k.Key }

// Hostname classes (§6.4): every hostname is used either for plain records,
// for a user CNAME, or for exactly one tunnel.
const (
	classRecords      = "records"
	classCNAME        = "cname"
	classTunnelPrefix = "tunnel:"
)

// rowClass returns the hostname class of a row ("" for Access rows).
func rowClass(r model.Resource) string {
	switch r.Kind {
	case model.KindTunnel:
		return classTunnelPrefix + r.TunnelID
	case model.KindDNS:
		if r.TunnelID != "" {
			return classTunnelPrefix + r.TunnelID
		}
		if r.RecordType == model.DNSTypeCNAME {
			return classCNAME
		}
		return classRecords
	}
	return ""
}

// sourceState is how a source participates in a cycle (§6.1).
type sourceState int

const (
	// sourcePresent: its snapshot is authoritative; claims are processed.
	sourcePresent sourceState = iota
	// sourceHeld: unknown state; its rows are neither updated nor orphaned.
	sourceHeld
	// sourceAbsent: gone (not allowed / beyond grace); its rows orphan.
	sourceAbsent
)

// sourceInfo is one collected source.
type sourceInfo struct {
	AgentID  string
	State    sourceState
	Snapshot model.Snapshot
	Policy   AgentPolicy
	// Defs holds access policy definitions per container name.
	Defs map[string][]model.AccessPolicyDef
	// LabelPrefix is the label prefix the containers were parsed with (for messages).
	LabelPrefix string
}

func (s *sourceInfo) isAgent() bool { return s.AgentID != model.LocalAgentID }

// claim is one desired resource from one source.
type claim struct {
	// Res is the desired row: identity, owner and spec fields.
	Res   model.Resource
	Class string
	// Target is the raw DNS label target ("auto", "container" or a literal).
	Target string
	// SpecCred is the label credential used for zone selection ("" = automatic).
	SpecCred string
	// AccessRef names the access definition referenced by the service.
	AccessRef string
	// Def is the bound access definition (access claims only).
	Def *model.AccessPolicyDef
	Src *sourceInfo
	Ctr *model.Container
	// Tunnel is the resolved tunnel (tunnel rules and tunnel CNAMEs).
	Tunnel cloudflare.Tunnel
	// Zone is the resolved zone (DNS and access; set by observe).
	Zone cloudflare.Zone
	// Err is a resolution failure found while observing (zone, IP).
	Err string
}

func (c claim) key() rowKey { return keyOf(c.Res) }

// claimLess orders claims by owner, then kind, then key (§6.4).
func claimLess(a, b claim) bool {
	if a.Res.Owner.Key() != b.Res.Owner.Key() {
		return a.Res.Owner.Less(b.Res.Owner)
	}
	return a.key().less(b.key())
}

func sortClaims(cs []claim) []claim {
	out := append([]claim(nil), cs...)
	sort.SliceStable(out, func(i, j int) bool { return claimLess(out[i], out[j]) })
	return out
}

func sortClaimsByKey(cs []claim) []claim {
	out := append([]claim(nil), cs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].key().less(out[j].key()) })
	return out
}

func sortRows(rs []model.Resource) []model.Resource {
	out := append([]model.Resource(nil), rs...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := keyOf(out[i]), keyOf(out[j])
		if a != b {
			return a.less(b)
		}
		return out[i].Owner.Less(out[j].Owner)
	})
	return out
}

// rejectRow turns a desired row into a rejected row with reason.
func rejectRow(r model.Resource, reason string) model.Resource {
	out := r
	out.ID = 0
	out.Rejected = true
	out.Status = model.StatusError
	out.LastError = reason
	out.CFID = ""
	out.PolicyIDs = nil
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
