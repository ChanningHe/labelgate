package reconcile

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

// ingressID is the identity of an ingress rule within one tunnel.
type ingressID struct {
	Host string
	Path string
}

// keepRule is one owned rule that must be present after the merge.
type keepRule struct {
	Key     rowKey
	Host    string
	Path    string
	Service string
	Origin  model.OriginRequest
}

func keepRuleOf(r model.Resource) keepRule {
	return keepRule{Key: keyOf(r), Host: r.Hostname, Path: r.Path, Service: r.Service, Origin: r.Origin}
}

// mergeResult is the merged configuration plus the keep rules that could not
// be placed because an unmanaged rule uses the same hostname.
type mergeResult struct {
	Config    cloudflare.TunnelConfig
	Conflicts []rowKey
	Changed   bool
}

// catchAll404 is the catch-all used when the live config has none.
const catchAll404 = "http_status:404"

// mergeIngress is the pure GET-merge-PUT core (§6.7). owned holds the
// identities of every tracked rule of this tunnel (including rules being
// deleted); keep holds the owned rules that must remain.
func mergeIngress(live cloudflare.TunnelConfig, owned map[ingressID]bool, keep []keepRule) mergeResult {
	liveOwned, unmanaged := splitIngress(live.Ingress, owned)
	catchAll := cloudflare.NewIngressRule("", "", catchAll404)
	if n := len(unmanaged); n > 0 && unmanaged[n-1].IsCatchAll() {
		catchAll, unmanaged = unmanaged[n-1], unmanaged[:n-1]
	}
	unmanagedHosts := map[string]bool{}
	for _, r := range unmanaged {
		unmanagedHosts[r.Hostname()] = true
	}
	placed, conflicts := placeRules(liveOwned, unmanagedHosts, keep)
	if len(live.Ingress) == 0 && len(placed) == 0 {
		return mergeResult{Config: live, Conflicts: conflicts}
	}
	var exact, wild []cloudflare.IngressRule
	for _, p := range placed {
		if isWildcard(p.keep.Host) {
			wild = append(wild, p.rule)
		} else {
			exact = append(exact, p.rule)
		}
	}
	ingress := make([]cloudflare.IngressRule, 0, len(exact)+len(unmanaged)+len(wild)+1)
	ingress = append(append(append(append(ingress, exact...), unmanaged...), wild...), catchAll)
	cfg := cloudflare.TunnelConfig{Ingress: ingress, Extra: copyExtra(live.Extra)}
	return mergeResult{Config: cfg, Conflicts: conflicts, Changed: canonicalJSON(cfg) != canonicalJSON(live)}
}

// placedRule is one owned rule of the merged configuration.
type placedRule struct {
	keep keepRule
	rule cloudflare.IngressRule
}

// placeRules builds the owned rules in order. A keep rule whose hostname is
// also used by an unmanaged rule is a conflict: it is reported, and its live
// rule (if any) is kept verbatim so a conflict never drops traffic.
func placeRules(liveOwned map[ingressID]cloudflare.IngressRule, unmanagedHosts map[string]bool, keep []keepRule) ([]placedRule, []rowKey) {
	var placed []placedRule
	var conflicts []rowKey
	for _, k := range keep {
		live, isLive := liveOwned[ingressID{Host: k.Host, Path: k.Path}]
		switch {
		case !unmanagedHosts[k.Host]:
			placed = append(placed, placedRule{keep: k, rule: buildRule(live, k)})
		case isLive:
			conflicts = append(conflicts, k.Key)
			placed = append(placed, placedRule{keep: k, rule: live})
		default:
			conflicts = append(conflicts, k.Key)
		}
	}
	sort.SliceStable(placed, func(i, j int) bool { return ruleLess(placed[i].keep, placed[j].keep) })
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].less(conflicts[j]) })
	return placed, conflicts
}

// splitIngress separates owned rules (first occurrence per identity; later
// duplicates are dropped) from unmanaged rules (order kept).
func splitIngress(rules []cloudflare.IngressRule, owned map[ingressID]bool) (map[ingressID]cloudflare.IngressRule, []cloudflare.IngressRule) {
	liveOwned := map[ingressID]cloudflare.IngressRule{}
	var unmanaged []cloudflare.IngressRule
	for _, r := range rules {
		id := ingressID{Host: r.Hostname(), Path: r.Path()}
		if id.Host == "" || !owned[id] {
			unmanaged = append(unmanaged, r)
			continue
		}
		if _, dup := liveOwned[id]; !dup {
			liveOwned[id] = r
		}
	}
	return liveOwned, unmanaged
}

func isWildcard(host string) bool { return strings.HasPrefix(host, "*.") }

// ruleLess orders owned rules: exact before wildcard, hostname ascending,
// then paths: non-empty before empty, longer first, then lexicographic.
func ruleLess(a, b keepRule) bool {
	if aw, bw := isWildcard(a.Host), isWildcard(b.Host); aw != bw {
		return !aw
	}
	if a.Host != b.Host {
		return a.Host < b.Host
	}
	if (a.Path != "") != (b.Path != "") {
		return a.Path != ""
	}
	if len(a.Path) != len(b.Path) {
		return len(a.Path) > len(b.Path)
	}
	return a.Path < b.Path
}

// buildRule starts from the live rule (preserving keys labelgate does not
// model) and sets hostname, path, service and the modelled origin settings.
func buildRule(base cloudflare.IngressRule, k keepRule) cloudflare.IngressRule {
	r := base
	if r.Fields == nil {
		r = cloudflare.IngressRule{Fields: map[string]json.RawMessage{}}
	}
	r = r.WithString(cloudflare.RuleHostname, k.Host).WithString(cloudflare.RuleService, k.Service)
	if k.Path != "" {
		r = r.WithString(cloudflare.RulePath, k.Path)
	} else {
		r = r.Without(cloudflare.RulePath)
	}
	if origin, ok := mergeOrigin(r.Fields[cloudflare.RuleOriginRequest], k.Origin); ok {
		return r.With(cloudflare.RuleOriginRequest, origin)
	}
	return r.Without(cloudflare.RuleOriginRequest)
}

// originField is one modelled originRequest key: its JSON value and whether it is set.
type originField struct {
	key   string
	value any
	set   bool
}

func seconds(d time.Duration) int64 { return int64((d + time.Second - 1) / time.Second) }

func originFields(o model.OriginRequest) []originField {
	return []originField{
		{"connectTimeout", seconds(o.ConnectTimeout), o.ConnectTimeout > 0},
		{"tlsTimeout", seconds(o.TLSTimeout), o.TLSTimeout > 0},
		{"tcpKeepAlive", seconds(o.TCPKeepAlive), o.TCPKeepAlive > 0},
		{"keepAliveTimeout", seconds(o.KeepAliveTimeout), o.KeepAliveTimeout > 0},
		{"keepAliveConnections", o.KeepAliveConnections, o.KeepAliveConnections > 0},
		{"noTLSVerify", true, o.NoTLSVerify},
		{"originServerName", o.OriginServerName, o.OriginServerName != ""},
		{"caPool", o.CAPool, o.CAPool != ""},
		{"httpHostHeader", o.HTTPHostHeader, o.HTTPHostHeader != ""},
		{"noHappyEyeballs", true, o.NoHappyEyeballs},
		{"disableChunkedEncoding", true, o.DisableChunkedEncoding},
		{"proxyType", o.ProxyType, o.ProxyType != ""},
	}
}

// mergeOrigin sets/deletes the modelled keys on the live originRequest object
// and keeps every other key. ok=false means the result is empty.
func mergeOrigin(raw json.RawMessage, o model.OriginRequest) (json.RawMessage, bool) {
	fields := map[string]json.RawMessage{}
	if len(raw) > 0 {
		var live map[string]json.RawMessage
		if err := json.Unmarshal(raw, &live); err == nil {
			for k, v := range live {
				fields[k] = v
			}
		}
	}
	for _, f := range originFields(o) {
		if !f.set {
			delete(fields, f.key)
			continue
		}
		b, _ := json.Marshal(f.value) // ints, bools and strings always marshal
		fields[f.key] = b
	}
	if len(fields) == 0 {
		return nil, false
	}
	out, _ := json.Marshal(fields) // map of valid raw messages
	return out, true
}

func copyExtra(m map[string]json.RawMessage) map[string]json.RawMessage {
	if m == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// canonicalJSON renders v with sorted keys at every level, so semantically
// equal configurations compare equal regardless of key order or spacing.
func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "invalid:" + err.Error()
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return "invalid:" + err.Error()
	}
	out, err := json.Marshal(x)
	if err != nil {
		return "invalid:" + err.Error()
	}
	return string(out)
}
