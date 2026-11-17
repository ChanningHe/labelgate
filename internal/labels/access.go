package labels

import (
	"fmt"
	"strings"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

const (
	defaultSessionDuration = "24h"
	defaultAppNamePrefix   = "labelgate-"
)

// Rule sections of a policy.
const (
	sectionInclude = "include"
	sectionRequire = "require"
	sectionExclude = "exclude"
)

// parseAccess turns the access groups into definitions sorted by name. Rules
// within a section end up ordered by selector because properties are applied
// in sorted key order.
// The pseudo name "default" is skipped.
func (p *parser) parseAccess(services map[string]props) []model.AccessPolicyDef {
	var out []model.AccessPolicyDef
	for _, name := range sortedKeys(services) {
		if name == defaultsName {
			continue
		}
		stem := p.stem(typeAccess, name)
		def := newAccessDef(name)
		invalid := p.applyProps(stem, services[name], nil, func(prop, v string) (bool, error) {
			return applyAccessProp(&def, prop, v)
		})
		if invalid {
			continue
		}
		if err := checkAccessDef(def); err != nil {
			p.errs.add(stem, "%v", err)
			continue
		}
		out = append(out, def)
	}
	return out
}

func newAccessDef(name string) model.AccessPolicyDef {
	return model.AccessPolicyDef{
		Name:            name,
		AppName:         defaultAppNamePrefix + name,
		SessionDuration: defaultSessionDuration,
		Policies:        []model.AccessPolicy{{Decision: model.AccessDecisionAllow}},
	}
}

// applyAccessProp applies one property to def. known=false marks an unknown property.
func applyAccessProp(def *model.AccessPolicyDef, prop, v string) (known bool, err error) {
	policy := &def.Policies[0]
	switch prop {
	case "app_name":
		if v = strings.TrimSpace(v); v != "" {
			def.AppName = v
		}
		return true, nil
	case "session_duration":
		def.SessionDuration, err = parseSession(v)
		return true, err
	case "policy.decision":
		policy.Decision, err = parseDecision(v)
		return true, err
	case "policy.name":
		policy.Name = strings.TrimSpace(v)
		return true, nil
	}
	for _, section := range []string{sectionInclude, sectionRequire, sectionExclude} {
		if selector, ok := strings.CutPrefix(prop, "policy."+section+"."); ok {
			return true, addRule(policy, section, selector, v)
		}
	}
	return false, nil
}

func parseSession(v string) (string, error) {
	v = strings.TrimSpace(v)
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return "", fmt.Errorf("invalid session_duration %q (use e.g. 12h)", v)
	}
	return v, nil
}

func parseDecision(v string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(v))
	switch d {
	case model.AccessDecisionAllow, model.AccessDecisionBlock,
		model.AccessDecisionBypass, model.AccessDecisionServiceAuth:
		return d, nil
	}
	return "", fmt.Errorf("invalid decision %q (use allow, block, bypass or service_auth)", v)
}

// addRule appends a selector rule to the given section of policy.
func addRule(policy *model.AccessPolicy, section, selector, value string) error {
	if !model.ValidSelector(selector) {
		return fmt.Errorf("invalid %s selector %q", section, selector)
	}
	rule := model.AccessRule{Selector: selector, Values: []string{}}
	if !model.ValuelessSelector(selector) {
		rule.Values = splitList(value)
		if len(rule.Values) == 0 {
			return fmt.Errorf("%s.%s requires at least one value", section, selector)
		}
	}
	switch section {
	case sectionInclude:
		policy.Include = append(policy.Include, rule)
	case sectionRequire:
		policy.Require = append(policy.Require, rule)
	default:
		policy.Exclude = append(policy.Exclude, rule)
	}
	return nil
}

// checkAccessDef enforces cross-property rules.
func checkAccessDef(def model.AccessPolicyDef) error {
	policy := def.Policies[0]
	allowBlock := policy.Decision == model.AccessDecisionAllow || policy.Decision == model.AccessDecisionBlock
	if allowBlock && len(policy.Include) == 0 {
		return fmt.Errorf("%s decision requires at least one include rule", policy.Decision)
	}
	return nil
}
