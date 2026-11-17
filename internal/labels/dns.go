package labels

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/channinghe/labelgate/internal/model"
)

const (
	maxTTL      = 86400
	maxUint16   = 65535
	maxCAAFlags = 255
)

// CAA property tags.
var caaTags = []string{"issue", "issuewild", "iodef"}

// dnsBuilder accumulates one DNS service while properties are applied.
type dnsBuilder struct {
	spec      model.DNSSpec
	targetSet bool
}

// dnsDefaultProps are the properties allowed in <prefix>.dns.default.*.
var dnsDefaultProps = map[string]bool{
	"type": true, "target": true, "proxied": true, "ttl": true, "credential": true, "cleanup": true,
}

var dnsHandlers = map[string]func(*dnsBuilder, string) error{
	"hostname":   func(b *dnsBuilder, v string) error { b.spec.Hostname = model.NormalizeHostname(v); return nil },
	"type":       setDNSType,
	"target":     func(b *dnsBuilder, v string) error { b.spec.Target, b.targetSet = v, true; return nil },
	"proxied":    func(b *dnsBuilder, v string) (err error) { b.spec.Proxied, err = parseBool(v); return },
	"ttl":        func(b *dnsBuilder, v string) (err error) { b.spec.TTL, err = parseInt(v, 0, maxTTL); return },
	"credential": func(b *dnsBuilder, v string) error { b.spec.Credential = strings.TrimSpace(v); return nil },
	"cleanup":    func(b *dnsBuilder, v string) (err error) { b.spec.Cleanup, err = parseBool(v); return },
	"comment":    func(b *dnsBuilder, v string) error { b.spec.Comment = v; return nil },
	"access":     func(b *dnsBuilder, v string) (err error) { b.spec.Access, err = accessRef(v); return },
	"priority":   func(b *dnsBuilder, v string) (err error) { b.spec.Priority, err = parseInt(v, 0, maxUint16); return },
	"weight":     func(b *dnsBuilder, v string) (err error) { b.spec.Weight, err = parseInt(v, 0, maxUint16); return },
	"port":       func(b *dnsBuilder, v string) (err error) { b.spec.Port, err = parseInt(v, 0, maxUint16); return },
	"flags":      func(b *dnsBuilder, v string) (err error) { b.spec.Flags, err = parseInt(v, 0, maxCAAFlags); return },
	"tag":        setCAATag,
}

func setDNSType(b *dnsBuilder, v string) error {
	t := model.DNSType(strings.ToUpper(strings.TrimSpace(v)))
	if !slices.Contains(model.DNSTypes, t) {
		return fmt.Errorf("invalid record type %q", v)
	}
	b.spec.Type = t
	return nil
}

func setCAATag(b *dnsBuilder, v string) error {
	tag := strings.ToLower(strings.TrimSpace(v))
	if !slices.Contains(caaTags, tag) {
		return fmt.Errorf("invalid CAA tag %q (use issue, issuewild or iodef)", v)
	}
	b.spec.Tag = tag
	return nil
}

// parseDNS turns the dns service groups into specs sorted by service name.
func (p *parser) parseDNS(services map[string]props) []model.DNSSpec {
	base := dnsBuilder{spec: model.DNSSpec{
		Type:    model.DNSTypeA,
		Target:  model.TargetAuto,
		Proxied: true,
		Cleanup: true,
	}}
	defaultsStem := p.stem(typeDNS, defaultsName)
	defaultsOK := true
	if d, ok := services[defaultsName]; ok {
		defaultsOK = !p.applyDNS(&base, defaultsStem, d, dnsDefaultProps)
	}

	var out []model.DNSSpec
	for _, name := range sortedKeys(services) {
		if name == defaultsName {
			continue
		}
		stem := p.stem(typeDNS, name)
		if !defaultsOK {
			p.errs.add(stem, "service skipped: invalid %s settings", defaultsStem)
			continue
		}
		b := base
		b.spec.Service = name
		if p.applyDNS(&b, stem, services[name], nil) {
			continue
		}
		if spec, ok := p.finalizeDNS(b, stem); ok {
			out = append(out, spec)
		}
	}
	return out
}

// applyDNS applies props to b; it reports whether any value was invalid.
func (p *parser) applyDNS(b *dnsBuilder, stem string, in props, allowed map[string]bool) bool {
	var allow func(string) bool
	if allowed != nil {
		allow = func(n string) bool { return allowed[n] }
	}
	return p.applyProps(stem, in, allow, func(name, v string) (bool, error) {
		h, ok := dnsHandlers[name]
		if !ok {
			return false, nil
		}
		return true, h(b, v)
	})
}

// finalizeDNS validates the assembled service and normalizes type-specific fields.
func (p *parser) finalizeDNS(b dnsBuilder, stem string) (model.DNSSpec, bool) {
	s := b.spec
	if s.Hostname == "" {
		p.errs.add(stem, "hostname is required")
		return s, false
	}
	if !model.ValidHostname(s.Hostname) {
		p.errs.add(stem+".hostname", "invalid hostname %q", s.Hostname)
		return s, false
	}
	target, err := dnsTarget(s.Type, s.Target, b.targetSet)
	if err != nil {
		p.errs.add(stem+".target", "%v", err)
		return s, false
	}
	s.Target = target
	normalizeDNS(&s)
	return s, true
}

// normalizeDNS zeroes fields irrelevant to the record type and applies the
// proxied/TTL rules.
func normalizeDNS(s *model.DNSSpec) {
	switch s.Type {
	case model.DNSTypeA, model.DNSTypeAAAA, model.DNSTypeCNAME:
	default:
		s.Proxied = false
	}
	if s.Proxied || s.TTL == 1 {
		s.TTL = 0
	}
	if s.Type != model.DNSTypeMX && s.Type != model.DNSTypeSRV {
		s.Priority = 0
	}
	if s.Type != model.DNSTypeSRV {
		s.Weight, s.Port = 0, 0
	}
	switch {
	case s.Type != model.DNSTypeCAA:
		s.Flags, s.Tag = 0, ""
	case s.Tag == "":
		s.Tag = "issue"
	}
}

// dnsTarget validates and normalizes the target for the record type.
func dnsTarget(t model.DNSType, raw string, set bool) (string, error) {
	switch t {
	case model.DNSTypeA, model.DNSTypeAAAA:
		return ipTarget(t, raw)
	}
	if !set || isSpecialTarget(raw) {
		return "", fmt.Errorf("target is required for %s records", t)
	}
	switch t {
	case model.DNSTypeCNAME, model.DNSTypeMX, model.DNSTypeSRV:
		host := model.NormalizeHostname(raw)
		if !model.ValidHostname(host) {
			return "", fmt.Errorf("invalid target hostname %q", raw)
		}
		return host, nil
	}
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("target is required for %s records", t)
	}
	return raw, nil // TXT, CAA: value kept verbatim
}

func isSpecialTarget(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	return s == model.TargetAuto || s == model.TargetContainer
}

// ipTarget validates an A/AAAA target: auto, container or an address of the right family.
func ipTarget(t model.DNSType, raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == model.TargetAuto || s == model.TargetContainer {
		return s, nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil || addr.Zone() != "" {
		return "", fmt.Errorf("invalid %s target %q (use auto, container or an IP address)", t, raw)
	}
	if (t == model.DNSTypeA) != addr.Is4() {
		return "", fmt.Errorf("target %q is not valid for %s records", raw, t)
	}
	return addr.String(), nil
}
