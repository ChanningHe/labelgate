package reconcile

import (
	"fmt"
	"slices"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

// defaultSessionDuration is Cloudflare's application default.
const defaultSessionDuration = "24h"

type appMode int

const (
	appNoop appMode = iota
	appCreate
	appUpdate
)

// policyStep is one desired reusable policy at index Index.
type policyStep struct {
	Index  int
	Policy cloudflare.AccessPolicy // ID "" = create
	Write  bool                    // create or update needed
}

// accessOp applies one Access application (§6.9).
type accessOp struct {
	Key     rowKey
	Account cloudflare.Account
	Fail    string
	Steps   []policyStep
	// App is the desired application; PolicyIDs has "" for policies created by Steps.
	App  cloudflare.AccessApp
	Mode appMode
	// Detach lists extra owned policies to delete after the app no longer references them.
	Detach []string
}

// accessDelete removes one Access row (§6.9 Delete).
type accessDelete struct {
	Key      rowKey
	Account  cloudflare.Account
	AppID    string   // owned app to delete ("" = none)
	Policies []string // owned policies to delete (ErrNotFound ignored)
	Fail     string
	Warn     string
}

func decisionOrDefault(d string) string {
	if d == "" {
		return model.AccessDecisionAllow
	}
	return d
}

// desiredPolicies builds one reusable policy per (hostname, index), with
// rules normalized the way Cloudflare returns them.
func desiredPolicies(def model.AccessPolicyDef, app, host string) ([]cloudflare.AccessPolicy, error) {
	out := make([]cloudflare.AccessPolicy, 0, len(def.Policies))
	for _, p := range def.Policies {
		name := p.Name
		if name == "" {
			name = app
		}
		pol := cloudflare.AccessPolicy{
			Name:     fmt.Sprintf("%s (%s)", name, host),
			Decision: model.DecisionToAPI(decisionOrDefault(p.Decision)),
		}
		var err error
		if pol.Include, err = cloudflare.NormalizeAccessRules(p.Include); err != nil {
			return nil, err
		}
		if pol.Require, err = cloudflare.NormalizeAccessRules(p.Require); err != nil {
			return nil, err
		}
		if pol.Exclude, err = cloudflare.NormalizeAccessRules(p.Exclude); err != nil {
			return nil, err
		}
		out = append(out, pol)
	}
	return out, nil
}

func rulesEqual(a, b []model.AccessRule) bool {
	return slices.EqualFunc(a, b, func(x, y model.AccessRule) bool {
		return x.Selector == y.Selector && slices.Equal(x.Values, y.Values)
	})
}

func policyDiffers(live, want cloudflare.AccessPolicy) bool {
	return live.Name != want.Name || live.Decision != want.Decision ||
		!rulesEqual(live.Include, want.Include) || !rulesEqual(live.Require, want.Require) ||
		!rulesEqual(live.Exclude, want.Exclude)
}

func appDiffers(live, want cloudflare.AccessApp, tag string) bool {
	return live.Name != want.Name || live.Domain != want.Domain ||
		live.SessionDuration != want.SessionDuration || !live.HasTag(tag) ||
		!slices.Equal(live.PolicyIDs, want.PolicyIDs)
}

func findApp(apps []cloudflare.AccessApp, id string) (cloudflare.AccessApp, bool) {
	for _, a := range apps {
		if a.ID == id {
			return a, true
		}
	}
	return cloudflare.AccessApp{}, false
}

// ownedApp finds our live application for a row: by CFID (tag or legacy),
// else a tagged app on the same domain (adopt). An untagged app on the
// domain is a conflict. nil + "" means create.
func ownedApp(row model.Resource, apps []cloudflare.AccessApp, tag string) (*cloudflare.AccessApp, string) {
	if row.CFID != "" {
		if a, ok := findApp(apps, row.CFID); ok {
			if a.HasTag(tag) || row.Legacy {
				return &a, ""
			}
			return nil, appTagRemoved(a, tag)
		}
	}
	for _, a := range apps {
		if a.Domain == row.Hostname && a.HasTag(tag) {
			return &a, ""
		}
	}
	for _, a := range apps {
		if a.Domain == row.Hostname {
			return nil, notOwnedApp(a)
		}
	}
	return nil, ""
}

func withTag(tags []string, tag string) []string {
	out := append([]string(nil), tags...)
	if !slices.Contains(out, tag) {
		out = append(out, tag)
	}
	return out
}

// planAccess decides the operations for one Access winner.
func planAccess(row model.Resource, w claim, al accountLive, observed bool, instanceID string) accessOp {
	op := accessOp{Key: keyOf(row), Account: w.Zone.Account()}
	switch {
	case w.Err != "":
		op.Fail = w.Err
	case !observed:
		op.Fail = accountNotRead(op.Account.ID)
	case al.Err != nil:
		op.Fail = "cannot list Access applications and policies: " + al.Err.Error()
	case al.TagErr != nil:
		op.Fail = "cannot create the labelgate Access tag: " + al.TagErr.Error()
	}
	if op.Fail != "" {
		return op
	}
	tag := model.AccessTag(instanceID)
	live, msg := ownedApp(row, al.Apps, tag)
	if msg != "" {
		op.Fail = msg
		return op
	}
	want, err := desiredPolicies(*w.Def, row.AppName, row.Hostname)
	if err != nil {
		op.Fail = "invalid access policy " + w.Def.Name + ": " + err.Error()
		return op
	}
	op.Steps, op.Detach = policySteps(row, want, al.Policies)
	op.App = desiredApp(row, *w.Def, op.Steps, live, tag)
	op.Mode = appModeFor(op, live, tag)
	return op
}

// policySteps pairs desired policies with owned live ones by index.
func policySteps(row model.Resource, want, live []cloudflare.AccessPolicy) ([]policyStep, []string) {
	byID := map[string]cloudflare.AccessPolicy{}
	for _, p := range live {
		byID[p.ID] = p
	}
	steps := make([]policyStep, 0, len(want))
	for i, p := range want {
		if i < len(row.PolicyIDs) {
			if lp, ok := byID[row.PolicyIDs[i]]; ok {
				p.ID = lp.ID
				steps = append(steps, policyStep{Index: i, Policy: p, Write: policyDiffers(lp, p)})
				continue
			}
		}
		steps = append(steps, policyStep{Index: i, Policy: p, Write: true})
	}
	var detach []string
	for i := len(want); i < len(row.PolicyIDs); i++ {
		if _, ok := byID[row.PolicyIDs[i]]; ok {
			detach = append(detach, row.PolicyIDs[i])
		}
	}
	return steps, detach
}

func desiredApp(row model.Resource, def model.AccessPolicyDef, steps []policyStep, live *cloudflare.AccessApp, tag string) cloudflare.AccessApp {
	sd := def.SessionDuration
	if sd == "" {
		sd = defaultSessionDuration
	}
	ids := make([]string, len(steps))
	for i, s := range steps {
		ids[i] = s.Policy.ID
	}
	app := cloudflare.AccessApp{Name: row.AppName, Domain: row.Hostname, SessionDuration: sd, PolicyIDs: ids, Tags: []string{tag}}
	if live != nil {
		app.ID, app.Tags = live.ID, withTag(live.Tags, tag)
	}
	return app
}

func appModeFor(op accessOp, live *cloudflare.AccessApp, tag string) appMode {
	if live == nil {
		return appCreate
	}
	for _, s := range op.Steps {
		if s.Policy.ID == "" {
			return appUpdate
		}
	}
	if len(op.Detach) > 0 || appDiffers(*live, op.App, tag) {
		return appUpdate
	}
	return appNoop
}

func planAccessOps(in planInput, lc lifecycle) []accessOp {
	var out []accessOp
	for _, w := range in.Winners {
		if w.Res.Kind != model.KindAccess {
			continue
		}
		al, ok := in.Live.Accounts[accountKey(w.Zone.Account())]
		out = append(out, planAccess(lc.rows[w.key()], w, al, ok, in.InstanceID))
	}
	return out
}

// planAccessDelete decides how to remove one Access row.
func planAccessDelete(row model.Resource, al accountLive, observed bool, instanceID string) accessDelete {
	d := accessDelete{Key: keyOf(row), Account: accountOfRow(row)}
	if row.CFID == "" && len(row.PolicyIDs) == 0 {
		return d
	}
	if !observed || al.Err != nil {
		d.Fail = accountNotRead(d.Account.ID)
		if observed {
			d.Fail = "cannot list Access applications and policies: " + al.Err.Error()
		}
		return d
	}
	if row.CFID != "" {
		if a, ok := findApp(al.Apps, row.CFID); ok {
			if !a.HasTag(model.AccessTag(instanceID)) && !row.Legacy {
				d.Warn = "the labelgate tag was removed from the Access application for " + a.Domain + " (id " + a.ID +
					") in Cloudflare, so labelgate stops managing it and leaves it in place"
				return d
			}
			d.AppID = a.ID
		}
	}
	d.Policies = append([]string(nil), row.PolicyIDs...)
	return d
}

func planAccessDeletes(in planInput, lc lifecycle) []accessDelete {
	var out []accessDelete
	for _, t := range sortRows(in.Tracked) {
		k := keyOf(t)
		if t.Kind != model.KindAccess || !lc.due[k] {
			continue
		}
		row := lc.rows[k]
		al, ok := in.Live.Accounts[accountKey(accountOfRow(row))]
		out = append(out, planAccessDelete(row, al, ok, in.InstanceID))
	}
	return out
}
