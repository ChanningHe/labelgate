package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/channinghe/labelgate/internal/model"
)

// accessPerPage is the page size for Access lists.
const accessPerPage = 100

// appTypeSelfHosted is the Access application type labelgate manages.
const appTypeSelfHosted = "self_hosted"

type tagWire struct {
	Name string `json:"name"`
}

// appWire mirrors zero_trust.AccessApplicationNewParamsBodySelfHostedApplication.
type appWire struct {
	ID              string     `json:"id,omitempty"`
	Name            string     `json:"name"`
	Domain          string     `json:"domain"`
	Type            string     `json:"type,omitempty"`
	SessionDuration string     `json:"session_duration,omitempty"`
	Tags            []string   `json:"tags"`
	Policies        []linkWire `json:"policies"`
}

// linkWire mirrors ...SelfHostedApplicationPoliciesAccessAppPolicyLink.
type linkWire struct {
	ID         string `json:"id"`
	Precedence int64  `json:"precedence"`
}

// policyWire mirrors zero_trust.AccessPolicyNewParams (reusable policies).
type policyWire struct {
	ID              string            `json:"id,omitempty"`
	Name            string            `json:"name"`
	Decision        string            `json:"decision"`
	Include         []json.RawMessage `json:"include"`
	Require         []json.RawMessage `json:"require"`
	Exclude         []json.RawMessage `json:"exclude"`
	SessionDuration string            `json:"session_duration,omitempty"`
}

func (c *Client) accountCred(acct Account) (*credEntry, error) {
	if acct.ID == "" {
		return nil, fmt.Errorf("access: empty account ID: %w", model.ErrInvalid)
	}
	return c.credByName(acct.Credential)
}

func accessPath(acct Account, kind string, id ...string) string {
	p := fmt.Sprintf("accounts/%s/access/%s", url.PathEscape(acct.ID), kind)
	for _, s := range id {
		p += "/" + url.PathEscape(s)
	}
	return p
}

// EnsureAccessTag creates the Access tag if it does not exist (idempotent).
func (c *Client) EnsureAccessTag(ctx context.Context, acct Account, tag string) error {
	cr, err := c.accountCred(acct)
	if err != nil {
		return err
	}
	tags, err := listAll[tagWire](ctx, c, cr, "access.tags.list", accessPath(acct, "tags"), accessPerPage)
	if err != nil {
		return err
	}
	for _, t := range tags {
		if t.Name == tag {
			return nil
		}
	}
	body := mustJSON(tagWire{Name: tag})
	var env envelope[tagWire]
	err = c.call(ctx, cr, request{op: "access.tags.create", method: http.MethodPost, path: accessPath(acct, "tags"), body: []byte(body), out: &env})
	var apiErr *APIError
	if errors.Is(err, ErrAlreadyExists) || (errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict) {
		return nil // created concurrently
	}
	return err
}

// ListAccessApps returns every Access application of the account.
func (c *Client) ListAccessApps(ctx context.Context, acct Account) ([]AccessApp, error) {
	cr, err := c.accountCred(acct)
	if err != nil {
		return nil, err
	}
	wires, err := listAll[appWire](ctx, c, cr, "access.apps.list", accessPath(acct, "apps"), accessPerPage)
	if err != nil {
		return nil, err
	}
	out := make([]AccessApp, 0, len(wires))
	for _, w := range wires {
		out = append(out, fromAppWire(w))
	}
	return out, nil
}

func fromAppWire(w appWire) AccessApp {
	links := append([]linkWire(nil), w.Policies...)
	sort.SliceStable(links, func(i, j int) bool { return links[i].Precedence < links[j].Precedence })
	var ids []string
	for _, l := range links {
		ids = append(ids, l.ID)
	}
	return AccessApp{
		ID:              w.ID,
		Name:            w.Name,
		Domain:          strings.ToLower(w.Domain),
		SessionDuration: w.SessionDuration,
		Tags:            append([]string(nil), w.Tags...), // nil when empty
		PolicyIDs:       ids,
	}
}

func toAppWire(app AccessApp) appWire {
	w := appWire{
		Name:            app.Name,
		Domain:          strings.ToLower(app.Domain),
		Type:            appTypeSelfHosted,
		SessionDuration: app.SessionDuration,
		Tags:            append([]string{}, app.Tags...),
		Policies:        make([]linkWire, 0, len(app.PolicyIDs)),
	}
	for i, id := range app.PolicyIDs {
		w.Policies = append(w.Policies, linkWire{ID: id, Precedence: int64(i + 1)})
	}
	return w
}

// CreateAccessApp creates a self-hosted application (ID ignored).
func (c *Client) CreateAccessApp(ctx context.Context, acct Account, app AccessApp) (AccessApp, error) {
	return c.writeApp(ctx, acct, "access.apps.create", http.MethodPost, accessPath(acct, "apps"), app)
}

// UpdateAccessApp overwrites application app.ID (full PUT: tags and the
// policy list are replaced; an empty PolicyIDs detaches all policies).
func (c *Client) UpdateAccessApp(ctx context.Context, acct Account, app AccessApp) (AccessApp, error) {
	if app.ID == "" {
		return AccessApp{}, fmt.Errorf("access.apps.update: empty app ID: %w", model.ErrInvalid)
	}
	return c.writeApp(ctx, acct, "access.apps.update", http.MethodPut, accessPath(acct, "apps", app.ID), app)
}

func (c *Client) writeApp(ctx context.Context, acct Account, op, method, path string, app AccessApp) (AccessApp, error) {
	cr, err := c.accountCred(acct)
	if err != nil {
		return AccessApp{}, err
	}
	body, err := json.Marshal(toAppWire(app))
	if err != nil {
		return AccessApp{}, err
	}
	var env envelope[appWire]
	if err := c.call(ctx, cr, request{op: op, method: method, path: path, body: body, out: &env}); err != nil {
		return AccessApp{}, err
	}
	return fromAppWire(env.Result), nil
}

// DeleteAccessApp deletes an application.
func (c *Client) DeleteAccessApp(ctx context.Context, acct Account, appID string) error {
	return c.deleteAccess(ctx, acct, "access.apps.delete", "apps", appID)
}

// ListAccessPolicies returns every reusable Access policy of the account.
func (c *Client) ListAccessPolicies(ctx context.Context, acct Account) ([]AccessPolicy, error) {
	cr, err := c.accountCred(acct)
	if err != nil {
		return nil, err
	}
	wires, err := listAll[policyWire](ctx, c, cr, "access.policies.list", accessPath(acct, "policies"), accessPerPage)
	if err != nil {
		return nil, err
	}
	out := make([]AccessPolicy, 0, len(wires))
	for _, w := range wires {
		out = append(out, fromPolicyWire(w))
	}
	return out, nil
}

func fromPolicyWire(w policyWire) AccessPolicy {
	return AccessPolicy{
		ID:              w.ID,
		Name:            w.Name,
		Decision:        w.Decision,
		Include:         rulesFromAPI(w.Include),
		Require:         rulesFromAPI(w.Require),
		Exclude:         rulesFromAPI(w.Exclude),
		SessionDuration: w.SessionDuration,
	}
}

func toPolicyWire(p AccessPolicy) (policyWire, error) {
	w := policyWire{Name: p.Name, Decision: p.Decision, SessionDuration: p.SessionDuration}
	var err error
	if w.Include, err = rulesToAPI(p.Include); err != nil {
		return policyWire{}, err
	}
	if w.Require, err = rulesToAPI(p.Require); err != nil {
		return policyWire{}, err
	}
	if w.Exclude, err = rulesToAPI(p.Exclude); err != nil {
		return policyWire{}, err
	}
	return w, nil
}

// CreateAccessPolicy creates a reusable policy (ID ignored).
func (c *Client) CreateAccessPolicy(ctx context.Context, acct Account, p AccessPolicy) (AccessPolicy, error) {
	return c.writePolicy(ctx, acct, "access.policies.create", http.MethodPost, accessPath(acct, "policies"), p)
}

// UpdateAccessPolicy overwrites reusable policy p.ID.
func (c *Client) UpdateAccessPolicy(ctx context.Context, acct Account, p AccessPolicy) (AccessPolicy, error) {
	if p.ID == "" {
		return AccessPolicy{}, fmt.Errorf("access.policies.update: empty policy ID: %w", model.ErrInvalid)
	}
	return c.writePolicy(ctx, acct, "access.policies.update", http.MethodPut, accessPath(acct, "policies", p.ID), p)
}

func (c *Client) writePolicy(ctx context.Context, acct Account, op, method, path string, p AccessPolicy) (AccessPolicy, error) {
	cr, err := c.accountCred(acct)
	if err != nil {
		return AccessPolicy{}, err
	}
	w, err := toPolicyWire(p)
	if err != nil {
		return AccessPolicy{}, fmt.Errorf("%s: %w", op, err)
	}
	body, err := json.Marshal(w)
	if err != nil {
		return AccessPolicy{}, err
	}
	var env envelope[policyWire]
	if err := c.call(ctx, cr, request{op: op, method: method, path: path, body: body, out: &env}); err != nil {
		return AccessPolicy{}, err
	}
	return fromPolicyWire(env.Result), nil
}

// DeleteAccessPolicy deletes a reusable policy.
func (c *Client) DeleteAccessPolicy(ctx context.Context, acct Account, policyID string) error {
	return c.deleteAccess(ctx, acct, "access.policies.delete", "policies", policyID)
}

func (c *Client) deleteAccess(ctx context.Context, acct Account, op, kind, id string) error {
	if id == "" {
		return fmt.Errorf("%s: empty ID: %w", op, model.ErrInvalid)
	}
	cr, err := c.accountCred(acct)
	if err != nil {
		return err
	}
	var env envelope[json.RawMessage]
	return c.call(ctx, cr, request{op: op, method: http.MethodDelete, path: accessPath(acct, kind, id), out: &env})
}
