package reconcile

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

// collected is the desired-state input of one cycle (§6.1, §6.2).
type collected struct {
	Sources  []*sourceInfo // local first, then agents by ID
	Claims   []claim
	Rejected []model.Resource
	Holds    []labelHold
	Issues   []string
	// Deadline is the earliest agent grace expiry (a cycle must run then).
	Deadline time.Time
}

// collect reads every source and expands present sources into claims.
// The returned error is a store failure (the cycle must abort).
func (r *Reconciler) collect(ctx context.Context, now time.Time) (collected, error) {
	var out collected
	local := r.collectLocal(ctx, &out)
	snaps, err := r.store.AgentSnapshots(ctx)
	if err != nil {
		return collected{}, fmt.Errorf("load agent snapshots: %w", err)
	}
	sources := []*sourceInfo{local}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].AgentID < snaps[j].AgentID })
	for _, s := range snaps {
		if src := r.classifyAgent(s, now, &out); src != nil {
			sources = append(sources, src)
		}
	}
	out.Sources = sources
	for _, src := range sources {
		if src.State == sourcePresent {
			r.expand(src, &out)
		}
	}
	return out, nil
}

func (r *Reconciler) collectLocal(ctx context.Context, out *collected) *sourceInfo {
	src := &sourceInfo{AgentID: model.LocalAgentID, State: sourcePresent, Snapshot: model.Snapshot{Complete: true}}
	if r.local == nil {
		return src
	}
	snap, err := r.local.Snapshot(ctx)
	switch {
	case err != nil:
		src.State = sourceHeld
		out.Issues = append(out.Issues, fmt.Sprintf("local source: %v", err))
	case !snap.Complete:
		src.State = sourceHeld
		out.Issues = append(out.Issues, "local source: incomplete snapshot")
	default:
		snap.AgentID = model.LocalAgentID
		src.Snapshot = snap
	}
	return src
}

// classifyAgent applies the agent policy and grace window (§6.1).
func (r *Reconciler) classifyAgent(s model.Snapshot, now time.Time, out *collected) *sourceInfo {
	if !model.ValidAgentID(s.AgentID) {
		return nil
	}
	policy := r.settings.Agents(s.AgentID)
	src := &sourceInfo{AgentID: s.AgentID, Snapshot: s, Policy: policy}
	expiry := s.TakenAt.Add(r.settings.AgentGrace)
	switch {
	case !policy.Allowed, s.TakenAt.IsZero(), now.After(expiry):
		src.State = sourceAbsent
		return src
	case !s.Complete:
		src.State = sourceHeld
	default:
		src.State = sourcePresent
	}
	if out.Deadline.IsZero() || expiry.Before(out.Deadline) {
		out.Deadline = expiry
	}
	return src
}

// expand parses a present source's containers (sorted by name) into claims.
func (r *Reconciler) expand(src *sourceInfo, out *collected) {
	ctrs := append([]model.Container(nil), src.Snapshot.Containers...)
	sort.SliceStable(ctrs, func(i, j int) bool { return ctrs[i].Name < ctrs[j].Name })
	src.Defs = map[string][]model.AccessPolicyDef{}
	parsed := make([]parsedContainer, 0, len(ctrs))
	for i := range ctrs {
		c := &ctrs[i]
		pl := r.parse(r.settings.LabelPrefix, c.Labels)
		src.Defs[c.Name] = pl.Access
		parsed = append(parsed, parsedContainer{Ctr: c, Labels: pl})
		for _, e := range pl.Errors {
			out.Issues = append(out.Issues, fmt.Sprintf("%s container %s: %v", sourceName(src), c.Name, e))
			if h, ok := holdFor(r.settings.LabelPrefix, src.AgentID, c.Name, pl, e); ok {
				out.Holds = append(out.Holds, h)
			}
		}
	}
	claims, rejected := expandSource(r.cf, src, parsed)
	out.Claims = append(out.Claims, claims...)
	out.Rejected = append(out.Rejected, rejected...)
}

func sourceName(src *sourceInfo) string {
	if src.isAgent() {
		return "agent " + src.AgentID
	}
	return "local"
}

// labelHold keeps rows whose service failed to parse: a label error must
// never look like a removed service (no orphaning, no cleanup countdown).
type labelHold struct {
	AgentID   string
	Container string
	// Type is "dns", "tunnel" or "access".
	Type string
	// Service is the dropped service ("" = every service of Type in the
	// container, after an invalid defaults block); for Type "access" it is
	// the definition name.
	Service string
	Reason  string
}

// holdFor derives the hold for one label error, if the error dropped a
// service, a defaults block or an access definition.
func holdFor(prefix, agentID, ctr string, pl model.ParsedLabels, e model.LabelError) (labelHold, bool) {
	rest, ok := strings.CutPrefix(e.Key, prefix+".")
	if !ok {
		return labelHold{}, false
	}
	parts := strings.SplitN(rest, ".", 3)
	if len(parts) < 2 {
		return labelHold{}, false
	}
	h := labelHold{AgentID: agentID, Container: ctr, Type: parts[0], Service: parts[1], Reason: "label error: " + e.Error()}
	switch {
	case h.Type != "access" && h.Service == "default":
		h.Service = ""
		return h, true
	case serviceParsed(pl, h.Type, h.Service):
		return labelHold{}, false // e.g. unknown property: the service was kept
	case h.Type == "dns" || h.Type == "tunnel" || h.Type == "access":
		return h, true
	}
	return labelHold{}, false
}

func serviceParsed(pl model.ParsedLabels, typ, name string) bool {
	switch typ {
	case "dns":
		for _, s := range pl.DNS {
			if s.Service == name {
				return true
			}
		}
	case "tunnel":
		for _, s := range pl.Tunnels {
			if s.Service == name {
				return true
			}
		}
	case "access":
		for _, d := range pl.Access {
			if d.Name == name {
				return true
			}
		}
	}
	return false
}

// matches reports whether the hold covers row r. Access rows of a held
// service are held too (the service's access claim disappeared with it).
func (h labelHold) matches(r model.Resource) bool {
	if r.Owner.AgentID != h.AgentID {
		return false
	}
	if h.Type == "access" {
		return r.Kind == model.KindAccess && r.PolicyName == h.Service
	}
	if r.Owner.ContainerName != h.Container || (h.Service != "" && r.Owner.Service != h.Service) {
		return false
	}
	switch r.Kind {
	case model.KindAccess:
		return true
	case model.KindTunnel:
		return h.Type == "tunnel"
	default:
		return (h.Type == "tunnel") == (r.TunnelID != "")
	}
}

// holdSet decides which tracked rows are held this cycle.
type holdSet struct {
	agents map[string]bool  // held sources (all their rows)
	rows   map[int64]string // label-held rows -> reason
}

func newHoldSet(sources []*sourceInfo, holds []labelHold, tracked []model.Resource, claims []claim) holdSet {
	h := holdSet{agents: map[string]bool{}, rows: map[int64]string{}}
	for _, s := range sources {
		if s.State == sourceHeld {
			h.agents[s.AgentID] = true
		}
	}
	for _, r := range tracked {
		if r.Rejected || h.agents[r.Owner.AgentID] {
			continue
		}
		claimed := stillClaimed(r, claims)
		for _, lh := range holds {
			// A broken access definition holds its rows even though the
			// referencing service still parses (its binding cannot be built).
			if lh.matches(r) && (!claimed || lh.Type == "access") {
				h.rows[r.ID] = lh.Reason
				break
			}
		}
	}
	return h
}

func (h holdSet) has(r model.Resource) bool {
	if h.agents[r.Owner.AgentID] {
		return true
	}
	_, ok := h.rows[r.ID]
	return ok && r.ID != 0
}

func (h holdSet) reason(r model.Resource) string {
	if h.agents[r.Owner.AgentID] || r.ID == 0 {
		return ""
	}
	return h.rows[r.ID]
}

// stillClaimed reports whether a parsed service of the row's owner still
// produces the row, so a label error elsewhere in the container must not hold it.
func stillClaimed(r model.Resource, claims []claim) bool {
	for _, c := range claims {
		if c.Res.Owner.Key() != r.Owner.Key() {
			continue
		}
		switch {
		case c.key() == keyOf(r):
			return true
		case r.Kind == model.KindAccess && c.AccessRef != "" && c.Res.Hostname == r.Hostname:
			return true
		case r.IsTunnelCNAME() && c.Res.Kind == model.KindTunnel && c.Res.Hostname == r.Hostname:
			return true
		}
	}
	return false
}
