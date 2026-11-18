package reconcile

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/cloudflare/cffake"
	"github.com/channinghe/labelgate/internal/model"
)

const policyName = "labelgate-team (app.example.com)"

func teamDef(emails ...string) model.AccessPolicyDef {
	return model.AccessPolicyDef{
		Name: "team",
		Policies: []model.AccessPolicy{{
			Decision: model.AccessDecisionAllow,
			Include:  []model.AccessRule{{Selector: model.SelectorEmails, Values: emails}},
		}},
	}
}

func accessCtr(name string, def model.AccessPolicyDef) model.Container {
	svc := dnsSpec("web", appHost)
	svc.Access = def.Name
	return ctr(name, model.ParsedLabels{DNS: []model.DNSSpec{svc}, Access: []model.AccessPolicyDef{def}})
}

func (h *harness) onlyApp() cloudflare.AccessApp {
	h.t.Helper()
	apps := h.cf.Apps(testAccount)
	if len(apps) != 1 {
		h.t.Fatalf("apps %+v", apps)
	}
	return apps[0]
}

func TestAccessCreateAndIdempotent(t *testing.T) {
	h := newHarness(t)
	h.src.set(accessCtr("web", teamDef("a@example.com", "b@example.com")))
	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("status %+v", st)
	}
	app := h.onlyApp()
	pols := h.cf.Policies(testAccount)
	if app.Name != "labelgate-team" || app.Domain != appHost || app.SessionDuration != "24h" ||
		!app.HasTag(ownTag) || len(pols) != 1 || !reflect.DeepEqual(app.PolicyIDs, []string{pols[0].ID}) {
		t.Fatalf("app %+v policies %+v", app, pols)
	}
	if pols[0].Name != policyName || pols[0].Decision != "allow" {
		t.Fatalf("policy %+v", pols[0])
	}
	if got := h.cf.Tags(testAccount); !reflect.DeepEqual(got, []string{ownTag}) {
		t.Fatalf("tags %v", got)
	}
	row := h.row(model.KindAccess, appHost)
	if row.CFID != app.ID || !reflect.DeepEqual(row.PolicyIDs, app.PolicyIDs) || row.Status != model.StatusActive {
		t.Fatalf("row %+v", row)
	}
	h.assertIdempotent()
	if h.cf.CountCalls(cffake.OpEnsureAccessTag) != 0 {
		t.Fatal("tag ensured more than once per process")
	}
}

func TestAccessRemovingEmailRevokesIt(t *testing.T) {
	h := newHarness(t)
	h.src.set(accessCtr("web", teamDef("a@example.com", "bob@example.com")))
	h.cycle()
	polID := h.cf.Policies(testAccount)[0].ID

	h.src.set(accessCtr("web", teamDef("a@example.com")))
	h.cf.ResetCalls()
	h.cycle()
	pols := h.cf.Policies(testAccount)
	if len(pols) != 1 || pols[0].ID != polID || !reflect.DeepEqual(pols[0].Include[0].Values, []string{"a@example.com"}) {
		t.Fatalf("policies %+v", pols)
	}
	if h.cf.CountCalls(cffake.OpUpdateAccessPolicy) != 1 || h.cf.CountCalls(cffake.OpCreateAccessPolicy) != 0 {
		t.Fatalf("calls %+v", h.cf.Calls())
	}
	h.assertIdempotent()
}

func TestAccessPolicyAddedAndRemoved(t *testing.T) {
	h := newHarness(t)
	two := teamDef("a@example.com")
	two.Policies = append(two.Policies, model.AccessPolicy{Decision: model.AccessDecisionBlock, Name: "deny", Include: []model.AccessRule{{Selector: model.SelectorCountry, Values: []string{"XX"}}}})
	h.src.set(accessCtr("web", teamDef("a@example.com")))
	h.cycle()
	h.src.set(accessCtr("web", two))
	h.cycle()
	app := h.onlyApp()
	if len(app.PolicyIDs) != 2 || len(h.cf.Policies(testAccount)) != 2 {
		t.Fatalf("app %+v", app)
	}
	h.assertIdempotent()

	h.src.set(accessCtr("web", teamDef("a@example.com")))
	h.cycle()
	app = h.onlyApp()
	if len(app.PolicyIDs) != 1 || len(h.cf.Policies(testAccount)) != 1 {
		t.Fatalf("extra policy not detached/deleted: %+v %+v", app, h.cf.Policies(testAccount))
	}
	if row := h.row(model.KindAccess, appHost); !reflect.DeepEqual(row.PolicyIDs, app.PolicyIDs) {
		t.Fatalf("row %+v", row)
	}
}

func TestAccessForeignPolicyUntouched(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.RemoveDelay = 0 })
	foreign := h.cf.PutPolicy(testAccount, cloudflare.AccessPolicy{Name: policyName, Decision: "allow",
		Include: []model.AccessRule{{Selector: model.SelectorEveryone}}})
	h.src.set(accessCtr("web", teamDef("a@example.com")))
	h.cycle()
	if app := h.onlyApp(); app.PolicyIDs[0] == foreign.ID {
		t.Fatal("foreign same-name policy adopted")
	}
	h.src.set()
	h.cycle()
	pols := h.cf.Policies(testAccount)
	if len(pols) != 1 || !reflect.DeepEqual(pols[0], foreign) || len(h.cf.Apps(testAccount)) != 0 {
		t.Fatalf("policies %+v apps %+v", pols, h.cf.Apps(testAccount))
	}
	h.noRow(model.KindAccess, appHost)
}

func TestAccessAppUpdateFailureNeverCreatesSecondApp(t *testing.T) {
	h := newHarness(t)
	h.src.set(accessCtr("web", teamDef("a@example.com")))
	h.cycle()
	def := teamDef("a@example.com")
	def.SessionDuration = "1h"
	h.src.set(accessCtr("web", def))
	h.cf.FailNext(cffake.OpUpdateAccessApp, errBoom)
	h.cycle()
	if row := h.row(model.KindAccess, appHost); row.Status != model.StatusError || !strings.Contains(row.LastError, "cannot update Access application") {
		t.Fatalf("row %+v", row)
	}
	if len(h.cf.Apps(testAccount)) != 1 || h.cf.CountCalls(cffake.OpCreateAccessApp) != 1 {
		t.Fatal("second app created")
	}
	h.cycle()
	if app := h.onlyApp(); app.SessionDuration != "1h" {
		t.Fatalf("not retried: %+v", app)
	}
}

func TestAccessUntaggedAppIsConflict(t *testing.T) {
	h := newHarness(t)
	h.cf.PutApp(testAccount, cloudflare.AccessApp{Name: "manual", Domain: appHost})
	h.src.set(accessCtr("web", teamDef("a@example.com")))
	h.cycle()
	if row := h.row(model.KindAccess, appHost); !strings.HasPrefix(row.LastError, model.ConflictNotOwned) || !strings.Contains(row.LastError, "Zero Trust > Access > Applications") {
		t.Fatalf("row %+v", row)
	}
	if len(h.cf.Policies(testAccount)) != 0 || h.onlyApp().Name != "manual" {
		t.Fatal("conflicting app or its policies touched")
	}
}

func TestAccessCreateFailureKeepsPolicyIDs(t *testing.T) {
	h := newHarness(t)
	h.src.set(accessCtr("web", teamDef("a@example.com")))
	h.cf.FailNext(cffake.OpCreateAccessApp, errBoom)
	h.cycle()
	row := h.row(model.KindAccess, appHost)
	if row.Status != model.StatusError || len(row.PolicyIDs) != 1 {
		t.Fatalf("row %+v", row)
	}
	h.cycle()
	if len(h.cf.Policies(testAccount)) != 1 || h.onlyApp().PolicyIDs[0] != row.PolicyIDs[0] {
		t.Fatal("policy leaked or recreated")
	}
}

func TestAccessCleanupAndTagRemoval(t *testing.T) {
	t.Run("owned app deleted with its policies", func(t *testing.T) {
		h := newHarness(t)
		h.src.set(accessCtr("web", teamDef("a@example.com")))
		h.cycle()
		h.src.set()
		h.cycle()
		h.clk.advance(31 * time.Minute)
		h.cycle()
		if len(h.cf.Apps(testAccount)) != 0 || len(h.cf.Policies(testAccount)) != 0 {
			t.Fatalf("leftovers %+v %+v", h.cf.Apps(testAccount), h.cf.Policies(testAccount))
		}
		h.noRow(model.KindAccess, appHost)
	})
	t.Run("untagged by a human is left alone", func(t *testing.T) {
		h := newHarness(t, func(s *Settings) { s.RemoveDelay = 0 })
		h.src.set(accessCtr("web", teamDef("a@example.com")))
		h.cycle()
		app := h.onlyApp()
		app.Tags = nil
		h.cf.PutApp(testAccount, app)
		if h.cycle(); !strings.HasPrefix(h.row(model.KindAccess, appHost).LastError, model.ConflictNotOwned) {
			t.Fatal("untagged app not reported")
		}
		h.src.set()
		h.cycle()
		h.noRow(model.KindAccess, appHost)
		if len(h.cf.Apps(testAccount)) != 1 || len(h.cf.Policies(testAccount)) != 1 {
			t.Fatal("untagged app or its policies deleted")
		}
	})
	t.Run("delete failure retried", func(t *testing.T) {
		h := newHarness(t, func(s *Settings) { s.RemoveDelay = 0 })
		h.src.set(accessCtr("web", teamDef("a@example.com")))
		h.cycle()
		h.src.set()
		h.cf.FailNext(cffake.OpDeleteAccessPolicy, errBoom)
		h.cycle()
		row := h.row(model.KindAccess, appHost)
		if row.Status != model.StatusError || row.CFID != "" || len(row.PolicyIDs) != 1 {
			t.Fatalf("row %+v", row)
		}
		h.cycle()
		h.noRow(model.KindAccess, appHost)
		if len(h.cf.Policies(testAccount)) != 0 {
			t.Fatal("policy not deleted on retry")
		}
	})
}

func TestAccessCoOwnersAndConflicts(t *testing.T) {
	h := newHarness(t)
	h.src.set(accessCtr("a", teamDef("x@example.com")), accessCtr("b", teamDef("x@example.com")))
	h.cycle()
	if rej := h.st.list(true); len(rej) != 1 || rej[0].Kind != model.KindDNS {
		t.Fatalf("identical definitions must co-own access: %+v", rej)
	}
	if row := h.row(model.KindAccess, appHost); row.Owner.ContainerName != "a" {
		t.Fatalf("row %+v", row)
	}
}

func TestAccessDifferentDefinitionsConflict(t *testing.T) {
	h := newHarness(t)
	t1 := tunSpec("api", appHost, "http://a")
	t1.Path, t1.Access = "/api", "team"
	t2 := tunSpec("web", appHost, "http://b")
	t2.Access = "team"
	h.src.set(
		ctr("a", model.ParsedLabels{Tunnels: []model.TunnelSpec{t1}, Access: []model.AccessPolicyDef{teamDef("x@example.com")}}),
		ctr("b", model.ParsedLabels{Tunnels: []model.TunnelSpec{t2}, Access: []model.AccessPolicyDef{teamDef("evil@example.com")}}),
	)
	h.cycle()
	rej := h.st.list(true)
	if len(rej) != 1 || rej[0].Kind != model.KindAccess || rej[0].Owner.ContainerName != "b" {
		t.Fatalf("rejected %+v", rej)
	}
	if v := h.cf.Policies(testAccount)[0].Include[0].Values; !reflect.DeepEqual(v, []string{"x@example.com"}) {
		t.Fatalf("policy %v", v)
	}
}

func TestAccessDefinitionLookup(t *testing.T) {
	def := teamDef("a@example.com")
	other := teamDef("b@example.com")
	src := &sourceInfo{Defs: map[string][]model.AccessPolicyDef{"own": {def}, "x": {other}, "y": {other}, "z": {def}}}
	tests := []struct {
		ctr, name, wantErr string
		want               *model.AccessPolicyDef
	}{
		{"own", "team", "", &def},
		{"none", "missing", `access policy "missing" is not defined`, nil},
		{"none", "team", "defined differently on several containers", nil},
	}
	for _, tc := range tests {
		got, msg := lookupDef(src, tc.ctr, tc.name)
		if tc.wantErr != "" {
			if !strings.Contains(msg, tc.wantErr) {
				t.Errorf("%s/%s: msg %q", tc.ctr, tc.name, msg)
			}
			continue
		}
		if msg != "" || !reflect.DeepEqual(got, *tc.want) {
			t.Errorf("%s/%s: got %+v %q", tc.ctr, tc.name, got, msg)
		}
	}
	single := &sourceInfo{Defs: map[string][]model.AccessPolicyDef{"x": {other}, "y": {other}}}
	if got, msg := lookupDef(single, "none", "team"); msg != "" || !reflect.DeepEqual(got, other) {
		t.Errorf("identical definitions in one source must resolve: %q", msg)
	}
}
