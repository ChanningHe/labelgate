package reconcile

import (
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/cloudflare/cffake"
	"github.com/channinghe/labelgate/internal/model"
)

func TestAccessNormalizedRulesAndUserTagsAreStable(t *testing.T) {
	h := newHarness(t)
	def := model.AccessPolicyDef{Name: "team", AppName: "Team App", SessionDuration: "12h", Policies: []model.AccessPolicy{{
		Include: []model.AccessRule{
			{Selector: model.SelectorEmails, Values: []string{"a@example.com"}},
			{Selector: model.SelectorEmails, Values: []string{"b@example.com"}},
			{Selector: model.SelectorEveryone},
		},
		Exclude: []model.AccessRule{{Selector: model.SelectorCountry, Values: []string{"XX"}}},
	}}}
	h.src.set(accessCtr("web", def))
	h.cycle()
	app := h.onlyApp()
	app.Tags = append(app.Tags, "user-tag")
	h.cf.PutApp(testAccount, app)
	h.assertIdempotent()
	h.src.set(accessCtr("web", teamDef("c@example.com")))
	h.cycle()
	if got := h.onlyApp(); !got.HasTag("user-tag") || !got.HasTag(ownTag) || got.Name != "labelgate-team" {
		t.Fatalf("app %+v", got)
	}
}

func TestAccessAdoptsTaggedAppAfterLostCreate(t *testing.T) {
	h := newHarness(t)
	existing := h.cf.PutApp(testAccount, cloudflare.AccessApp{Name: "labelgate-team", Domain: appHost, SessionDuration: "24h", Tags: []string{ownTag}})
	h.src.set(accessCtr("web", teamDef("a@example.com")))
	h.cycle()
	if app := h.onlyApp(); app.ID != existing.ID || len(app.PolicyIDs) != 1 {
		t.Fatalf("app %+v", app)
	}
	if h.cf.CountCalls(cffake.OpCreateAccessApp) != 0 {
		t.Fatal("duplicate app created")
	}
}

func TestAccessAccountErrors(t *testing.T) {
	ops := []cffake.Op{cffake.OpListAccessApps, cffake.OpListAccessPolicies, cffake.OpEnsureAccessTag,
		cffake.OpCreateAccessPolicy, cffake.OpUpdateAccessPolicy, cffake.OpCreateAccessApp}
	for _, op := range ops {
		t.Run(string(op), func(t *testing.T) {
			h := newHarness(t)
			h.src.set(accessCtr("web", teamDef("a@example.com")))
			if op == cffake.OpUpdateAccessPolicy {
				h.cycle()
				h.src.set(accessCtr("web", teamDef("b@example.com")))
			}
			h.cf.FailNext(op, errBoom)
			h.cycle()
			if row := h.row(model.KindAccess, appHost); row.Status != model.StatusError || !strings.Contains(row.LastError, "boom") {
				t.Fatalf("row %+v", row)
			}
			if st := h.cycle(); st.State != SyncSuccess {
				t.Fatalf("retry %+v", st)
			}
			if len(h.cf.Apps(testAccount)) != 1 || len(h.cf.Policies(testAccount)) != 1 {
				t.Fatal("duplicates after retry")
			}
		})
	}
}

func TestAccessCleanupWithAccountListFailure(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.RemoveDelay = 0 })
	h.src.set(accessCtr("web", teamDef("a@example.com")))
	h.cycle()
	h.src.set()
	h.cf.FailNext(cffake.OpListAccessApps, errBoom)
	h.cycle()
	wantStatus(t, h.row(model.KindAccess, appHost), model.StatusError)
	if len(h.cf.Apps(testAccount)) != 1 {
		t.Fatal("deleted without observing")
	}
	h.cf.FailNext(cffake.OpDeleteAccessApp, errBoom)
	h.cycle()
	wantStatus(t, h.row(model.KindAccess, appHost), model.StatusError)
	h.cycle()
	h.noRow(model.KindAccess, appHost)
}

func TestAccessDetachFailureRetried(t *testing.T) {
	h := newHarness(t)
	two := teamDef("a@example.com")
	two.Policies = append(two.Policies, model.AccessPolicy{Decision: model.AccessDecisionBypass, Include: []model.AccessRule{{Selector: model.SelectorIPRanges, Values: []string{"10.0.0.0/8"}}}})
	h.src.set(accessCtr("web", two))
	h.cycle()
	h.src.set(accessCtr("web", teamDef("a@example.com")))
	h.cf.FailNext(cffake.OpDeleteAccessPolicy, errBoom)
	h.cycle()
	if row := h.row(model.KindAccess, appHost); row.Status != model.StatusError || len(row.PolicyIDs) != 2 {
		t.Fatalf("row %+v", row)
	}
	h.cycle()
	if row := h.row(model.KindAccess, appHost); row.Status != model.StatusActive || len(row.PolicyIDs) != 1 || len(h.cf.Policies(testAccount)) != 1 {
		t.Fatalf("row %+v policies %+v", row, h.cf.Policies(testAccount))
	}
}

func TestAccessInvalidRuleSelector(t *testing.T) {
	h := newHarness(t)
	def := teamDef("a@example.com")
	def.Policies[0].Include = []model.AccessRule{{Selector: "bogus", Values: []string{"x"}}}
	h.src.set(accessCtr("web", def))
	h.cycle()
	if row := h.row(model.KindAccess, appHost); !strings.Contains(row.LastError, "access policy definition team") {
		t.Fatalf("row %+v", row)
	}
}

func TestAccessLabelHoldOnDefinitionError(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.RemoveDelay = 0 })
	h.src.set(accessCtr("web", teamDef("a@example.com")))
	h.cycle()
	svc := dnsSpec("web", appHost)
	svc.Access = "team"
	h.src.set(ctr("web", model.ParsedLabels{DNS: []model.DNSSpec{svc},
		Errors: []model.LabelError{{Key: "labelgate.access.team.policy.decision", Message: "invalid decision"}}}))
	h.cycle()
	if row := h.row(model.KindAccess, appHost); row.Status != model.StatusError || !strings.Contains(row.LastError, "label error") {
		t.Fatalf("row %+v", row)
	}
	if len(h.cf.Apps(testAccount)) != 1 || len(h.cf.Policies(testAccount)) != 1 {
		t.Fatal("access removed because of a label error")
	}
}

func TestDNSMultiValuedIdentity(t *testing.T) {
	h := newHarness(t)
	h.cf.PutRecord(testZone, cloudflare.DNSRecord{Name: appHost, Type: model.DNSTypeTXT, Content: "other"})
	ours := model.DNSSpec{Service: "txt", Hostname: appHost, Type: model.DNSTypeTXT, Target: "mine", Cleanup: true}
	same := model.DNSSpec{Service: "dup", Hostname: appHost, Type: model.DNSTypeTXT, Target: "other", Cleanup: true}
	h.src.set(ctr("web", dnsOnly(ours, same)))
	h.cycle()
	wantStatus(t, h.row(model.KindDNS, model.DNSKey(appHost, model.DNSTypeTXT, "mine")), model.StatusActive)
	if row := h.row(model.KindDNS, model.DNSKey(appHost, model.DNSTypeTXT, "other")); row.LastError != model.ConflictNotOwned {
		t.Fatalf("row %+v", row)
	}
	if len(h.recordsNamed(appHost)) != 2 {
		t.Fatalf("records %+v", h.records())
	}
}
