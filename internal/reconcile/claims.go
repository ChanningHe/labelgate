package reconcile

import (
	"fmt"
	"strings"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

// contentKey is the identity part of multi-valued record content (§6.2).
func contentKey(t model.DNSType, content string, prio, weight, port, flags int, tag string) string {
	switch t {
	case model.DNSTypeMX:
		return fmt.Sprintf("%d %s", prio, content)
	case model.DNSTypeSRV:
		return fmt.Sprintf("%d %d %d %s", prio, weight, port, content)
	case model.DNSTypeCAA:
		return fmt.Sprintf("%d %s %s", flags, tag, content)
	default:
		return content
	}
}

func recordContentKey(r cloudflare.DNSRecord) string {
	return contentKey(r.Type, r.Content, r.Priority, r.Weight, r.Port, r.Flags, r.Tag)
}

// isDynamicTarget reports whether an A/AAAA target is resolved per cycle.
func isDynamicTarget(t model.DNSType, target string) bool {
	return (t == model.DNSTypeA || t == model.DNSTypeAAAA) &&
		(target == model.TargetAuto || target == model.TargetContainer)
}

func normTTL(ttl int, proxied bool) int {
	if proxied || ttl <= 1 {
		return 1
	}
	return ttl
}

// normRef lower-cases credential and tunnel references: configuration map
// keys (credential/tunnel names) are lower-cased by the config loader.
func normRef(ref string) string { return strings.ToLower(strings.TrimSpace(ref)) }

func ownerOf(src *sourceInfo, c *model.Container, service string) model.Owner {
	return model.Owner{AgentID: src.AgentID, ContainerName: c.Name, ContainerID: c.ID, Service: service}
}

// dnsClaim builds the claim for one DNS spec.
func dnsClaim(src *sourceInfo, c *model.Container, s model.DNSSpec) claim {
	host := model.NormalizeHostname(s.Hostname)
	target := strings.TrimSpace(s.Target)
	if s.Type == model.DNSTypeCNAME || s.Type == model.DNSTypeMX || s.Type == model.DNSTypeSRV {
		target = model.NormalizeHostname(target)
	}
	if s.Type == model.DNSTypeTXT {
		target = cloudflare.NormalizeTXT(target) // identity matches live records
	}
	res := model.Resource{
		Kind: model.KindDNS, Hostname: host, Owner: ownerOf(src, c, s.Service),
		Credential: normRef(s.Credential), RecordType: s.Type, Proxied: s.Proxied,
		TTL: normTTL(s.TTL, s.Proxied), Comment: s.Comment, Cleanup: s.Cleanup,
	}
	switch s.Type {
	case model.DNSTypeMX:
		res.Priority = s.Priority
	case model.DNSTypeSRV:
		res.Priority, res.Weight, res.Port = s.Priority, s.Weight, s.Port
	case model.DNSTypeCAA:
		res.Flags, res.Tag = s.Flags, s.Tag
	}
	if !isDynamicTarget(s.Type, target) {
		res.Content = target
	}
	res.Key = model.DNSKey(host, s.Type, contentKey(s.Type, target, res.Priority, res.Weight, res.Port, res.Flags, res.Tag))
	class := classRecords
	if s.Type == model.DNSTypeCNAME {
		class = classCNAME
	}
	return claim{
		Res: res, Class: class, Target: target, SpecCred: normRef(s.Credential),
		AccessRef: s.Access, Src: src, Ctr: c,
	}
}

// tunnelClaim builds the claim for one tunnel spec. An unresolvable tunnel
// reference yields a rejected row instead.
func tunnelClaim(api tunnelResolver, src *sourceInfo, c *model.Container, s model.TunnelSpec) (claim, *model.Resource) {
	host := model.NormalizeHostname(s.Hostname)
	ref := normRef(s.Tunnel)
	if ref == "" && src.isAgent() {
		ref = normRef(src.Policy.DefaultTunnel)
	}
	res := model.Resource{
		Kind: model.KindTunnel, Hostname: host, Owner: ownerOf(src, c, s.Service),
		Path: s.Path, Service: s.URL, Origin: s.Origin, Cleanup: s.Cleanup,
	}
	t, err := api.ResolveTunnel(ref)
	if err != nil {
		res.Key = model.TunnelKey(ref, host, s.Path)
		rej := rejectRow(res, err.Error())
		return claim{}, &rej
	}
	res.TunnelID, res.AccountID, res.Credential = t.ID, t.AccountID, t.Credential
	res.Key = model.TunnelKey(t.ID, host, s.Path)
	return claim{
		Res: res, Class: classTunnelPrefix + t.ID, SpecCred: normRef(s.Credential),
		AccessRef: s.Access, Src: src, Ctr: c, Tunnel: t,
	}, nil
}

// tunnelResolver is the synchronous part of cloudflare.API used for claims.
type tunnelResolver interface {
	ResolveTunnel(ref string) (cloudflare.Tunnel, error)
}

// allowedTunnelIDs resolves an agent's tunnel allow-list to tunnel IDs.
// Unresolvable references allow nothing.
func allowedTunnelIDs(api tunnelResolver, refs []string) map[string]bool {
	out := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if t, err := api.ResolveTunnel(normRef(ref)); err == nil {
			out[t.ID] = true
		}
	}
	return out
}

// expandSource turns one present source's parsed containers into claims and
// rejected rows (agent policy violations, unknown tunnels).
func expandSource(api tunnelResolver, src *sourceInfo, parsed []parsedContainer) ([]claim, []model.Resource) {
	var claims []claim
	var rejected []model.Resource
	var allowed map[string]bool
	if src.isAgent() {
		allowed = allowedTunnelIDs(api, src.Policy.Tunnels)
	}
	add := func(c claim) {
		if src.isAgent() {
			if reason := agentReject(c, allowed); reason != "" {
				rejected = append(rejected, rejectRow(c.Res, reason))
				return
			}
		}
		claims = append(claims, c)
	}
	for _, pc := range parsed {
		for _, s := range pc.Labels.DNS {
			add(dnsClaim(src, pc.Ctr, s))
		}
		for _, s := range pc.Labels.Tunnels {
			c, rej := tunnelClaim(api, src, pc.Ctr, s)
			if rej != nil {
				rejected = append(rejected, *rej)
				continue
			}
			add(c)
		}
	}
	return claims, rejected
}

// parsedContainer is one container with its parsed labels.
type parsedContainer struct {
	Ctr    *model.Container
	Labels model.ParsedLabels
}
