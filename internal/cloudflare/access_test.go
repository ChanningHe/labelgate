package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/channinghe/labelgate/internal/model"
)

var testAcct = Account{ID: "acct1", Credential: "default"}

func TestEnsureAccessTag(t *testing.T) {
	// Arrange: an existing tag on page 2.
	f := newFakeCF(t)
	f.maxPerPage = 1
	f.seed("tags:acct1", map[string]any{"name": "other"})
	f.seed("tags:acct1", map[string]any{"name": "labelgate-abcd1234"})
	c := f.client(t, rootCfg())
	ctx := context.Background()

	// Act + Assert: present -> no create
	if err := c.EnsureAccessTag(ctx, testAcct, "labelgate-abcd1234"); err != nil {
		t.Fatal(err)
	}
	if n := f.count("POST", "/accounts/acct1/access/tags"); n != 0 {
		t.Fatalf("existing tag recreated (%d)", n)
	}
	// missing -> created once
	if err := c.EnsureAccessTag(ctx, testAcct, "labelgate-new1"); err != nil {
		t.Fatal(err)
	}
	if body := f.lastBody(t, "POST", "/accounts/acct1/access/tags"); body["name"] != "labelgate-new1" {
		t.Fatalf("body %v", body)
	}
	// concurrent creation (409) is success
	f.inject("POST", "/accounts/acct1/access/tags", 1, 409, cfErrorBody(12000, "tag exists"), nil)
	if err := c.EnsureAccessTag(ctx, testAcct, "labelgate-race"); err != nil {
		t.Fatalf("409 must be treated as existing: %v", err)
	}
	f.inject("POST", "/accounts/acct1/access/tags", 1, 400, cfErrorBody(12001, "invalid"), nil)
	if err := c.EnsureAccessTag(ctx, testAcct, "bad tag"); err == nil {
		t.Fatal("other errors must surface")
	}
}

func TestAccessAppsListAndWrite(t *testing.T) {
	// Arrange: 3 apps on 2 pages; policy links out of precedence order.
	f := newFakeCF(t)
	f.maxPerPage = 2
	f.seed("apps:acct1", map[string]any{"id": "app1", "name": "One", "domain": "One.Example.com", "type": "self_hosted", "session_duration": "24h",
		"tags": []any{"labelgate-abcd1234"}, "policies": []any{
			map[string]any{"id": "p2", "precedence": 2, "name": "b"}, map[string]any{"id": "p1", "precedence": 1, "name": "a"}}})
	f.seed("apps:acct1", map[string]any{"id": "app2", "name": "Two", "domain": "two.example.com", "type": "self_hosted"})
	f.seed("apps:acct1", map[string]any{"id": "app3", "name": "Saas", "type": "saas"})
	c := f.client(t, rootCfg())
	ctx := context.Background()

	// Act
	apps, err := c.ListAccessApps(ctx, testAcct)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []AccessApp{
		{ID: "app1", Name: "One", Domain: "one.example.com", SessionDuration: "24h", Tags: []string{"labelgate-abcd1234"}, PolicyIDs: []string{"p1", "p2"}},
		{ID: "app2", Name: "Two", Domain: "two.example.com"},
		{ID: "app3", Name: "Saas"},
	}
	if !reflect.DeepEqual(apps, want) {
		t.Fatalf("apps:\n got %+v\nwant %+v", apps, want)
	}
	if !apps[0].HasTag("labelgate-abcd1234") {
		t.Fatal("tag")
	}

	// Create: body carries type, tags and precedence-ordered links.
	created, err := c.CreateAccessApp(ctx, testAcct, AccessApp{Name: "New", Domain: "New.example.com", SessionDuration: "12h",
		Tags: []string{"labelgate-abcd1234"}, PolicyIDs: []string{"pa", "pb"}})
	if err != nil || created.ID == "" || !reflect.DeepEqual(created.PolicyIDs, []string{"pa", "pb"}) || created.Domain != "new.example.com" {
		t.Fatalf("create: %+v %v", created, err)
	}
	wantBody := map[string]any{"name": "New", "domain": "new.example.com", "type": "self_hosted", "session_duration": "12h",
		"tags": []any{"labelgate-abcd1234"}, "policies": []any{
			map[string]any{"id": "pa", "precedence": 1.0}, map[string]any{"id": "pb", "precedence": 2.0}}}
	if body := f.lastBody(t, "POST", "/accounts/acct1/access/apps"); !reflect.DeepEqual(body, wantBody) {
		t.Fatalf("create body:\n got %v\nwant %v", body, wantBody)
	}

	// Update with no policies detaches all (explicit empty list, never omitted).
	updated, err := c.UpdateAccessApp(ctx, testAcct, AccessApp{ID: "app1", Name: "One", Domain: "one.example.com"})
	if err != nil || len(updated.PolicyIDs) != 0 {
		t.Fatalf("update: %+v %v", updated, err)
	}
	body := f.lastBody(t, "PUT", "/accounts/acct1/access/apps/app1")
	if !reflect.DeepEqual(body["policies"], []any{}) || !reflect.DeepEqual(body["tags"], []any{}) {
		t.Fatalf("update body %v", body)
	}

	if err := c.DeleteAccessApp(ctx, testAcct, "app2"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteAccessApp(ctx, testAcct, "app2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	if _, err := c.UpdateAccessApp(ctx, testAcct, AccessApp{ID: "gone", Domain: "x.example.com"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
}

func TestAccessPoliciesCRUD(t *testing.T) {
	// Arrange
	f := newFakeCF(t)
	f.maxPerPage = 1
	f.seed("policies:acct1", map[string]any{"id": "foreign", "name": "Foreign", "decision": "allow", "reusable": true,
		"include": []any{map[string]any{"everyone": map[string]any{}}}})
	c := f.client(t, rootCfg())
	ctx := context.Background()
	p := AccessPolicy{
		Name:     "staff (app.example.com)",
		Decision: "allow",
		Include: []model.AccessRule{
			{Selector: model.SelectorEmails, Values: []string{"alice@x.com", "bob@x.com"}},
			{Selector: model.SelectorEmailsEndingIn, Values: []string{"x.com"}},
		},
		Require: []model.AccessRule{{Selector: model.SelectorCountry, Values: []string{"DE"}}},
	}

	// Act: create, then remove bob.
	created, err := c.CreateAccessPolicy(ctx, testAcct, p)
	if err != nil {
		t.Fatal(err)
	}
	body := f.lastBody(t, "POST", "/accounts/acct1/access/policies")
	p.ID = created.ID
	p.Include[0] = model.AccessRule{Selector: model.SelectorEmails, Values: []string{"alice@x.com"}}
	if _, err := c.UpdateAccessPolicy(ctx, testAcct, p); err != nil {
		t.Fatal(err)
	}
	list, err := c.ListAccessPolicies(ctx, testAcct)

	// Assert
	wantCreate := map[string]any{"name": "staff (app.example.com)", "decision": "allow",
		"include": []any{
			map[string]any{"email": map[string]any{"email": "alice@x.com"}},
			map[string]any{"email": map[string]any{"email": "bob@x.com"}},
			map[string]any{"email_domain": map[string]any{"domain": "x.com"}}},
		"require": []any{map[string]any{"geo": map[string]any{"country_code": "DE"}}},
		"exclude": []any{}}
	if !reflect.DeepEqual(body, wantCreate) {
		t.Fatalf("create body:\n got %v\nwant %v", body, wantCreate)
	}
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if list[0].ID != "foreign" || !reflect.DeepEqual(list[0].Include, []model.AccessRule{{Selector: model.SelectorEveryone}}) {
		t.Fatalf("foreign policy %+v", list[0])
	}
	got := list[1]
	if got.ID != created.ID || !reflect.DeepEqual(got.Include, p.Include) || !reflect.DeepEqual(got.Require, p.Require) || got.Exclude != nil {
		t.Fatalf("updated policy %+v", got)
	}
	if err := c.DeleteAccessPolicy(ctx, testAcct, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteAccessPolicy(ctx, testAcct, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestAccessInputValidation(t *testing.T) {
	f := newFakeCF(t)
	c := f.client(t, rootCfg())
	ctx := context.Background()
	errs := map[string]error{}
	_, errs["app update without id"] = c.UpdateAccessApp(ctx, testAcct, AccessApp{})
	_, errs["policy update without id"] = c.UpdateAccessPolicy(ctx, testAcct, AccessPolicy{})
	errs["policy delete without id"] = c.DeleteAccessPolicy(ctx, testAcct, "")
	_, errs["account without id"] = c.ListAccessApps(ctx, Account{Credential: "default"})
	_, errs["bad selector"] = c.CreateAccessPolicy(ctx, testAcct, AccessPolicy{Include: []model.AccessRule{{Selector: "bogus", Values: []string{"x"}}}})
	_, errs["bad raw"] = c.CreateAccessPolicy(ctx, testAcct, AccessPolicy{Require: []model.AccessRule{{Selector: "unsupported:x", Values: []string{"{"}}}})
	_, errs["bad exclude"] = c.CreateAccessPolicy(ctx, testAcct, AccessPolicy{Exclude: []model.AccessRule{{Selector: "bogus"}}})
	for name, err := range errs {
		if !errors.Is(err, model.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if err := c.EnsureAccessTag(ctx, Account{ID: "a", Credential: "ghost"}, "t"); !errors.Is(err, ErrUnknownCredential) {
		t.Errorf("unknown credential: %v", err)
	}
	if len(f.reqs()) != 0 {
		t.Fatalf("invalid input reached Cloudflare: %+v", f.reqs())
	}
}

func TestAccessRuleConversionRoundTrip(t *testing.T) {
	all := []model.AccessRule{
		{Selector: model.SelectorEmails, Values: []string{"a@x.com", "b@x.com"}},
		{Selector: model.SelectorEmailsEndingIn, Values: []string{"x.com"}},
		{Selector: model.SelectorIPRanges, Values: []string{"10.0.0.0/8", "::1/128"}},
		{Selector: model.SelectorCountry, Values: []string{"US"}},
		{Selector: model.SelectorEveryone},
		{Selector: model.SelectorServiceToken, Values: []string{"tok"}},
		{Selector: model.SelectorAccessGroups, Values: []string{"grp"}},
		{Selector: model.SelectorCertificate},
		{Selector: model.SelectorLoginMethods, Values: []string{"idp"}},
		{Selector: "unsupported:auth_method", Values: []string{`{"auth_method":{"auth_method":"mfa"}}`}},
	}
	raws, err := rulesToAPI(all)
	if err != nil {
		t.Fatal(err)
	}
	if len(raws) != 12 {
		t.Fatalf("expanded to %d objects", len(raws))
	}
	if back := rulesFromAPI(raws); !reflect.DeepEqual(back, all) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", back, all)
	}
}

func TestRulesFromAPIUnsupportedShapes(t *testing.T) {
	tests := []struct {
		raw, selector, value string
	}{
		{`{"auth_context":{"id":"x","ac_id":"c1"}}`, "unsupported:auth_context", `{"auth_context":{"id":"x","ac_id":"c1"}}`},
		{`{"email":{"email":"a@x.com"},"ip":{"ip":"1.1.1.1"}}`, "unsupported:email,ip", `{"email":{"email":"a@x.com"},"ip":{"ip":"1.1.1.1"}}`},
		{`{"email":{"address":"a@x.com"}}`, "unsupported:email", `{"email":{"address":"a@x.com"}}`},
		{`{"email":"a@x.com"}`, "unsupported:email", `{"email":"a@x.com"}`},
		{`{"everyone":null}`, "unsupported:everyone", `{"everyone":null}`},
		{`[1]`, "unsupported:", `[1]`},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := rulesFromAPI([]json.RawMessage{json.RawMessage(tt.raw)})
			want := []model.AccessRule{{Selector: tt.selector, Values: []string{tt.value}}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v want %+v", got, want)
			}
		})
	}
	// Extra inner fields are tolerated; non-consecutive selectors are not merged.
	got := rulesFromAPI([]json.RawMessage{
		json.RawMessage(`{"email":{"email":"a@x.com","extra":1}}`),
		json.RawMessage(`{"ip":{"ip":"1.1.1.1"}}`),
		json.RawMessage(`{"email":{"email":"b@x.com"}}`),
		json.RawMessage(`{"everyone":{}}`),
		json.RawMessage(`{"everyone":{}}`),
	})
	want := []model.AccessRule{
		{Selector: model.SelectorEmails, Values: []string{"a@x.com"}},
		{Selector: model.SelectorIPRanges, Values: []string{"1.1.1.1"}},
		{Selector: model.SelectorEmails, Values: []string{"b@x.com"}},
		{Selector: model.SelectorEveryone},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if rulesFromAPI(nil) != nil {
		t.Fatal("empty list must map to nil")
	}
}

func TestAccessListPagingAcrossManyPages(t *testing.T) {
	f := newFakeCF(t)
	f.maxPerPage = 3
	for i := 0; i < 10; i++ {
		f.seed("policies:acct1", map[string]any{"name": fmt.Sprintf("p%d", i), "decision": "allow", "include": []any{}})
	}
	c := f.client(t, rootCfg())
	list, err := c.ListAccessPolicies(context.Background(), testAcct)
	if err != nil || len(list) != 10 {
		t.Fatalf("got %d %v", len(list), err)
	}
	if n := f.count("GET", "/accounts/acct1/access/policies"); n != 4 {
		t.Fatalf("pages = %d, want 4", n)
	}
}

func TestNormalizeAccessRules(t *testing.T) {
	in := []model.AccessRule{
		{Selector: model.SelectorEmails, Values: []string{"a@x.com"}},
		{Selector: model.SelectorEmails, Values: []string{"b@x.com"}},
		{Selector: model.SelectorEveryone, Values: []string{"ignored"}},
	}
	got, err := NormalizeAccessRules(in)
	want := []model.AccessRule{
		{Selector: model.SelectorEmails, Values: []string{"a@x.com", "b@x.com"}},
		{Selector: model.SelectorEveryone},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v %v", got, err)
	}
	if got, err := NormalizeAccessRules([]model.AccessRule{}); err != nil || got != nil {
		t.Fatalf("empty: %+v %v", got, err)
	}
	if _, err := NormalizeAccessRules([]model.AccessRule{{Selector: "bogus"}}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
}
