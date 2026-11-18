// Package labels parses labelgate container labels into model specs.
//
// The label grammar is backward compatible with v0.2 (see
// docsite/content/docs/en/labels):
//
//	<prefix>.dns.<service>.<property>=<value>
//	<prefix>.tunnel.<service>.<property>=<value>
//	<prefix>.tunnel.<service>.origin.<setting>=<value>
//	<prefix>.access.<policy>.<property>=<value>
//	<prefix>.dns.default.<property> / <prefix>.tunnel.default.<property>  (per-container defaults)
//
// Parsing is pure and deterministic: output slices are sorted and identical
// input always yields identical output.
package labels

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/channinghe/labelgate/internal/model"
)

// DefaultPrefix is the default label prefix.
const DefaultPrefix = "labelgate"

// Label types (second key segment).
const (
	typeDNS    = "dns"
	typeTunnel = "tunnel"
	typeAccess = "access"
)

// defaultsName is the pseudo service holding per-container defaults.
const defaultsName = "default"

// reservedNames cannot be used as service names because they collide with properties.
var reservedNames = map[string]bool{
	"hostname": true, "service": true, "type": true, "target": true,
	"proxied": true, "ttl": true, "cleanup": true, "credential": true,
	"tunnel": true, "path": true, "origin": true, "access": true,
	"priority": true, "weight": true, "port": true, "flags": true,
	"tag": true, "comment": true,
}

var serviceNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// props maps a property name (possibly dotted) to its raw value.
type props map[string]string

// parser carries the per-call state.
type parser struct {
	prefix string
	errs   collector
}

// Parse parses one container's labels into specs.
//
//   - Keys without the "<prefix>." prefix are ignored. Keys with the prefix but
//     an unknown type segment or fewer than 3 dot-separated parts after it
//     produce a LabelError keyed by the full label key (never a service stem).
//   - Invalid or reserved service names produce a LabelError and skip the service.
//   - Hostnames are normalized (lower-case, trimmed, no trailing dot) and validated.
//   - Invalid values drop the service; unknown properties are reported but keep it.
//
// See the per-type files for the property rules.
func Parse(prefix string, labels map[string]string) model.ParsedLabels {
	if prefix == "" {
		prefix = DefaultPrefix
	}
	p := &parser{prefix: prefix}
	groups := p.group(labels)

	out := model.ParsedLabels{
		DNS:     p.parseDNS(groups[typeDNS]),
		Tunnels: p.parseTunnels(groups[typeTunnel]),
		Access:  p.parseAccess(groups[typeAccess]),
	}
	out.Errors = p.errs.sorted()
	return out
}

// group splits labels into type -> service -> property -> value, dropping
// services whose name is not acceptable.
func (p *parser) group(labels map[string]string) map[string]map[string]props {
	groups := map[string]map[string]props{}
	for _, key := range sortedKeys(labels) {
		rest, ok := strings.CutPrefix(key, p.prefix+".")
		if !ok {
			continue
		}
		parts := strings.SplitN(rest, ".", 3)
		if !knownType(parts[0]) {
			p.errs.add(key, "unknown label type %q: use %s.dns.*, %s.tunnel.* or %s.access.*", parts[0], p.prefix, p.prefix, p.prefix)
			continue
		}
		if len(parts) < 3 {
			p.errs.add(key, "malformed label key (expected %s.%s.<service>.<property>)", p.prefix, parts[0])
			continue
		}
		typ, service, prop := parts[0], parts[1], parts[2]
		if groups[typ] == nil {
			groups[typ] = map[string]props{}
		}
		if groups[typ][service] == nil {
			groups[typ][service] = props{}
		}
		groups[typ][service][prop] = labels[key]
	}
	for typ, services := range groups {
		for name := range services {
			if msg := serviceNameProblem(name); msg != "" {
				p.errs.add(p.stem(typ, name), "%s", msg)
				delete(services, name)
			}
		}
	}
	return groups
}

func knownType(t string) bool {
	return t == typeDNS || t == typeTunnel || t == typeAccess
}

// serviceNameProblem returns why name is unusable, or "".
func serviceNameProblem(name string) string {
	if name == defaultsName {
		return ""
	}
	if reservedNames[name] {
		return "service name " + name + " is reserved (it is a property name); rename the service in this label key"
	}
	if !serviceNamePattern.MatchString(name) {
		return "invalid service name " + name + ": use lower-case letters, digits and inner hyphens"
	}
	return ""
}

// stem returns the "<prefix>.<type>.<service>" key stem.
func (p *parser) stem(typ, service string) string {
	return p.prefix + "." + typ + "." + service
}

// applyProps feeds each property to apply in sorted order. Properties for
// which apply reports known=false (or that allowed rejects) are reported as
// unknown but do not fail; the return value reports whether any value was invalid.
func (p *parser) applyProps(stem string, in props, allowed func(string) bool, apply func(name, value string) (known bool, err error)) (valueErr bool) {
	for _, name := range sortedKeys(in) {
		known := allowed == nil || allowed(name)
		var err error
		if known {
			known, err = apply(name, in[name])
		}
		switch {
		case !known:
			p.errs.addUnknown(stem+"."+name, name)
		case err != nil:
			p.errs.add(stem+"."+name, "%v", err)
			valueErr = true
		}
	}
	return valueErr
}

// accessRef validates an "access" property value (a policy definition name).
func accessRef(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if !serviceNamePattern.MatchString(v) {
		return "", fmt.Errorf("invalid access policy name %q: use lower-case letters, digits and inner hyphens", v)
	}
	return v, nil
}
