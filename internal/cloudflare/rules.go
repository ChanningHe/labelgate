package cloudflare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/channinghe/labelgate/internal/model"
)

// UnsupportedSelectorPrefix marks a live Access rule labelgate does not model.
// Such a rule is returned as AccessRule{Selector: "unsupported:<key>",
// Values: [<raw rule JSON>]} and written back verbatim.
const UnsupportedSelectorPrefix = "unsupported:"

// ruleShape is the Cloudflare JSON shape of one selector: {"<key>":{"<field>":v}}.
// field "" = valueless ({"<key>":{}}).
type ruleShape struct{ key, field string }

// ruleShapes maps label selectors to Cloudflare rule objects (cloudflare-go/v6
// zero_trust.*RuleParam JSON names).
var ruleShapes = map[string]ruleShape{
	model.SelectorEmails:         {"email", "email"},
	model.SelectorEmailsEndingIn: {"email_domain", "domain"},
	model.SelectorIPRanges:       {"ip", "ip"},
	model.SelectorCountry:        {"geo", "country_code"},
	model.SelectorEveryone:       {"everyone", ""},
	model.SelectorServiceToken:   {"service_token", "token_id"},
	model.SelectorAccessGroups:   {"group", "id"},
	model.SelectorCertificate:    {"certificate", ""},
	model.SelectorLoginMethods:   {"login_method", "id"},
}

// selectorByKey is the inverse of ruleShapes.
var selectorByKey = func() map[string]string {
	m := make(map[string]string, len(ruleShapes))
	for sel, s := range ruleShapes {
		m[s.key] = sel
	}
	return m
}()

// rulesToAPI expands model rules into Cloudflare rule objects, one per value.
// The result is never nil (Cloudflare PUT semantics: [] clears a list).
func rulesToAPI(rules []model.AccessRule) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	for _, r := range rules {
		if strings.HasPrefix(r.Selector, UnsupportedSelectorPrefix) {
			for _, v := range r.Values {
				if !json.Valid([]byte(v)) {
					return nil, fmt.Errorf("access rule %s: invalid raw JSON: %w", r.Selector, model.ErrInvalid)
				}
				out = append(out, json.RawMessage(v))
			}
			continue
		}
		shape, ok := ruleShapes[r.Selector]
		if !ok {
			return nil, fmt.Errorf("access rule selector %q: %w", r.Selector, model.ErrInvalid)
		}
		if shape.field == "" {
			out = append(out, mustJSON(map[string]map[string]string{shape.key: {}}))
			continue
		}
		for _, v := range r.Values {
			out = append(out, mustJSON(map[string]map[string]string{shape.key: {shape.field: v}}))
		}
	}
	return out, nil
}

// rulesFromAPI groups consecutive Cloudflare rule objects of the same
// selector into one model rule. nil for an empty list.
func rulesFromAPI(raws []json.RawMessage) []model.AccessRule {
	var out []model.AccessRule
	for _, raw := range raws {
		sel, val, ok := parseRule(raw)
		if !ok {
			out = append(out, model.AccessRule{Selector: sel, Values: []string{val}})
			continue
		}
		valueless := ruleShapes[sel].field == ""
		if n := len(out); n > 0 && out[n-1].Selector == sel {
			if !valueless {
				out[n-1].Values = append(out[n-1].Values, val)
			}
			continue
		}
		r := model.AccessRule{Selector: sel}
		if !valueless {
			r.Values = []string{val}
		}
		out = append(out, r)
	}
	return out
}

// parseRule decodes one rule object. ok=false returns the unsupported
// selector and the compact raw JSON as the value.
func parseRule(raw json.RawMessage) (selector, value string, ok bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) != 1 {
		return unsupportedRule(raw, obj)
	}
	for key, inner := range obj {
		sel, known := selectorByKey[key]
		if !known {
			break
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(inner, &fields); err != nil || fields == nil {
			break
		}
		shape := ruleShapes[sel]
		if shape.field == "" {
			return sel, "", true
		}
		var v string
		if err := json.Unmarshal(fields[shape.field], &v); err != nil || v == "" {
			break
		}
		return sel, v, true
	}
	return unsupportedRule(raw, obj)
}

func unsupportedRule(raw json.RawMessage, obj map[string]json.RawMessage) (string, string, bool) {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return UnsupportedSelectorPrefix + strings.Join(keys, ","), string(raw), false
	}
	return UnsupportedSelectorPrefix + strings.Join(keys, ","), buf.String(), false
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("cloudflare: marshal %T: %v", v, err)) // maps of strings always marshal
	}
	return b
}
