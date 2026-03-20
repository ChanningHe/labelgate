package cloudflare

import (
	"context"
	"fmt"

	cf "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/zero_trust"
	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/types"
)

// AccessClient provides Cloudflare Zero Trust Access management operations.
type AccessClient struct {
	client    *Client
	accountID string
}

// NewAccessClient creates a new Access client wrapper.
func NewAccessClient(client *Client, accountID string) *AccessClient {
	if accountID == "" {
		accountID = client.AccountID()
	}
	return &AccessClient{
		client:    client,
		accountID: accountID,
	}
}

// CheckAccessPermissions probes the Cloudflare Access API to verify the token
// has sufficient permissions. It does a lightweight List Applications call.
// Returns nil if permissions are valid, error with details otherwise.
func (a *AccessClient) CheckAccessPermissions(ctx context.Context) error {
	if a.accountID == "" {
		return fmt.Errorf("account ID is required for access operations")
	}

	// Probe with a List Applications call (read-only, minimal impact)
	_, err := a.client.API().ZeroTrust.Access.Applications.List(ctx, zero_trust.AccessApplicationListParams{
		AccountID: cf.F(a.accountID),
	})
	if err != nil {
		return fmt.Errorf("access API permission check failed: %w", err)
	}
	return nil
}

// FindExistingAccessApp searches for an existing Access Application by hostname.
// Returns the app ID if found, empty string if not found.
// This is used to detect pre-existing apps not managed by labelgate.
func (a *AccessClient) FindExistingAccessApp(ctx context.Context, hostname string) (string, string, error) {
	if a.accountID == "" {
		return "", "", fmt.Errorf("account ID is required for access operations")
	}

	apps, err := a.client.API().ZeroTrust.Access.Applications.List(ctx, zero_trust.AccessApplicationListParams{
		AccountID: cf.F(a.accountID),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to list access applications: %w", err)
	}

	for _, app := range apps.Result {
		if app.Domain == hostname {
			return app.ID, app.Name, nil
		}
	}
	return "", "", nil
}

// EnsureAccessForHostname creates or updates an Access Application + Policies for a hostname.
// This is the main entry point used by the Access Operator.
//
// Flow:
//  1. Find or create account-level reusable policies
//  2. Create or update the Access Application, linking policies via the Policies field
//  3. Clean up old unreferenced policies from the previous app state
func (a *AccessClient) EnsureAccessForHostname(ctx context.Context, hostname string, policyDef *types.AccessPolicyDef, existingAppID string) (string, error) {
	if a.accountID == "" {
		return "", fmt.Errorf("account ID is required for access operations")
	}

	// Step 1: Collect old policy IDs before changes (for cleanup later)
	var oldPolicyIDs []string
	if existingAppID != "" {
		oldPolicyIDs = a.listAppPolicyIDs(ctx, existingAppID)
	}

	// Step 2: Find or create reusable policies at the account level
	var policyLinks []policyLink
	for i := range policyDef.Policies {
		policy := &policyDef.Policies[i]
		policyID, err := a.ensureReusablePolicy(ctx, policyDef.Name, policy, i)
		if err != nil {
			return "", fmt.Errorf("failed to ensure reusable policy %d for %s: %w", i, hostname, err)
		}
		policyLinks = append(policyLinks, policyLink{
			ID:         policyID,
			Precedence: int64(i + 1),
		})
	}

	// Step 3: Create or update the application with policy links
	appName := policyDef.AppName
	if appName == "" {
		appName = fmt.Sprintf("labelgate:%s", hostname)
	}

	var appID string
	if existingAppID != "" {
		err := a.updateApplication(ctx, existingAppID, hostname, appName, policyDef.SessionDuration, policyLinks)
		if err != nil {
			return "", err
		}
		appID = existingAppID
	} else {
		var err error
		appID, err = a.createApplication(ctx, hostname, appName, policyDef.SessionDuration, policyLinks)
		if err != nil {
			return "", err
		}
	}

	// Step 4: Clean up old policies that are no longer referenced by this app
	newPolicyIDs := make(map[string]bool)
	for _, link := range policyLinks {
		newPolicyIDs[link.ID] = true
	}
	for _, oldID := range oldPolicyIDs {
		if !newPolicyIDs[oldID] {
			a.tryDeleteReusablePolicy(ctx, oldID)
		}
	}

	return appID, nil
}

// policyLink holds a reusable policy ID and its precedence for linking to an application.
type policyLink struct {
	ID         string
	Precedence int64
}

// ensureReusablePolicy finds an existing reusable policy by name or creates a new one.
func (a *AccessClient) ensureReusablePolicy(ctx context.Context, defName string, policy *types.AccessPolicy, index int) (string, error) {
	policyName := policy.Name
	if policyName == "" {
		policyName = fmt.Sprintf("labelgate:%s:%s", defName, policy.Decision)
		if index > 0 {
			policyName = fmt.Sprintf("%s:%d", policyName, index)
		}
	}

	// Try to find an existing reusable policy with the same name
	existingID, err := a.findReusablePolicyByName(ctx, policyName)
	if err != nil {
		log.Warn().Err(err).Str("policy_name", policyName).Msg("Failed to search for existing policy, will create new")
	} else if existingID != "" {
		log.Debug().
			Str("policy_id", existingID).
			Str("policy_name", policyName).
			Msg("Reusing existing Access Policy")
		return existingID, nil
	}

	includeRules := buildAccessRules(policy.Include)
	requireRules := buildAccessRules(policy.Require)
	excludeRules := buildAccessRules(policy.Exclude)

	params := zero_trust.AccessPolicyNewParams{
		AccountID: cf.F(a.accountID),
		Decision:  cf.F(zero_trust.Decision(types.MapDecisionToAPI(policy.Decision))),
		Include:   cf.F(includeRules),
		Name:      cf.F(policyName),
	}
	if len(requireRules) > 0 {
		params.Require = cf.F(requireRules)
	}
	if len(excludeRules) > 0 {
		params.Exclude = cf.F(excludeRules)
	}

	result, err := a.client.API().ZeroTrust.Access.Policies.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("failed to create reusable policy %q: %w", policyName, err)
	}

	log.Info().
		Str("policy_id", result.ID).
		Str("policy_name", policyName).
		Str("decision", policy.Decision).
		Msg("Created reusable Access Policy")

	return result.ID, nil
}

// findReusablePolicyByName searches account-level reusable policies for a matching name.
func (a *AccessClient) findReusablePolicyByName(ctx context.Context, name string) (string, error) {
	policies, err := a.client.API().ZeroTrust.Access.Policies.List(ctx, zero_trust.AccessPolicyListParams{
		AccountID: cf.F(a.accountID),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list reusable policies: %w", err)
	}

	for _, p := range policies.Result {
		if p.Name == name {
			return p.ID, nil
		}
	}
	return "", nil
}

// listAppPolicyIDs returns the IDs of policies currently linked to an application.
func (a *AccessClient) listAppPolicyIDs(ctx context.Context, appID string) []string {
	policies, err := a.client.API().ZeroTrust.Access.Applications.Policies.List(ctx, appID, zero_trust.AccessApplicationPolicyListParams{
		AccountID: cf.F(a.accountID),
	})
	if err != nil {
		log.Warn().Err(err).Str("app_id", appID).Msg("Failed to list app policies")
		return nil
	}
	ids := make([]string, 0, len(policies.Result))
	for _, p := range policies.Result {
		ids = append(ids, p.ID)
	}
	return ids
}

// tryDeleteReusablePolicy attempts to delete a reusable policy.
// Silently ignores 409 (still in use) and 404 (already gone) errors.
func (a *AccessClient) tryDeleteReusablePolicy(ctx context.Context, policyID string) {
	_, err := a.client.API().ZeroTrust.Access.Policies.Delete(ctx, policyID, zero_trust.AccessPolicyDeleteParams{
		AccountID: cf.F(a.accountID),
	})
	if err != nil {
		log.Debug().Err(err).
			Str("policy_id", policyID).
			Msg("Could not delete old reusable policy (may still be in use)")
	} else {
		log.Debug().
			Str("policy_id", policyID).
			Msg("Cleaned up old reusable policy")
	}
}

// createApplication creates a new Access Application with linked policies.
func (a *AccessClient) createApplication(ctx context.Context, hostname, appName, sessionDuration string, links []policyLink) (string, error) {
	// Build policy link params for application creation
	var policies []zero_trust.AccessApplicationNewParamsBodySelfHostedApplicationPolicyUnion
	for _, link := range links {
		policies = append(policies, zero_trust.AccessApplicationNewParamsBodySelfHostedApplicationPoliciesAccessAppPolicyLink{
			ID:         cf.F(link.ID),
			Precedence: cf.F(link.Precedence),
		})
	}

	app, err := a.client.API().ZeroTrust.Access.Applications.New(ctx, zero_trust.AccessApplicationNewParams{
		AccountID: cf.F(a.accountID),
		Body: zero_trust.AccessApplicationNewParamsBodySelfHostedApplication{
			Domain:          cf.F(hostname),
			Type:            cf.F(zero_trust.ApplicationTypeSelfHosted),
			Name:            cf.F(appName),
			SessionDuration: cf.F(sessionDuration),
			Policies:        cf.F(policies),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create access application for %s: %w", hostname, err)
	}

	log.Info().
		Str("hostname", hostname).
		Str("app_id", app.ID).
		Str("app_name", appName).
		Int("policies", len(links)).
		Msg("Created Access Application")

	return app.ID, nil
}

// updateApplication updates an existing Access Application with new policy links.
func (a *AccessClient) updateApplication(ctx context.Context, appID, hostname, appName, sessionDuration string, links []policyLink) error {
	// Build policy link params for application update
	var policies []zero_trust.AccessApplicationUpdateParamsBodySelfHostedApplicationPolicyUnion
	for _, link := range links {
		policies = append(policies, zero_trust.AccessApplicationUpdateParamsBodySelfHostedApplicationPoliciesAccessAppPolicyLink{
			ID:         cf.F(link.ID),
			Precedence: cf.F(link.Precedence),
		})
	}

	_, err := a.client.API().ZeroTrust.Access.Applications.Update(ctx, appID, zero_trust.AccessApplicationUpdateParams{
		AccountID: cf.F(a.accountID),
		Body: zero_trust.AccessApplicationUpdateParamsBodySelfHostedApplication{
			Domain:          cf.F(hostname),
			Type:            cf.F(zero_trust.ApplicationTypeSelfHosted),
			Name:            cf.F(appName),
			SessionDuration: cf.F(sessionDuration),
			Policies:        cf.F(policies),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update access application %s: %w", appID, err)
	}

	log.Info().
		Str("app_id", appID).
		Str("hostname", hostname).
		Int("policies", len(links)).
		Msg("Updated Access Application")

	return nil
}

// DeleteAccessApplication deletes an Access Application and its linked policies.
func (a *AccessClient) DeleteAccessApplication(ctx context.Context, appID string) error {
	if a.accountID == "" {
		return fmt.Errorf("account ID is required for access operations")
	}

	// Collect linked policy IDs before deleting the app
	policyIDs := a.listAppPolicyIDs(ctx, appID)

	// Delete the application first
	_, err := a.client.API().ZeroTrust.Access.Applications.Delete(ctx, appID, zero_trust.AccessApplicationDeleteParams{
		AccountID: cf.F(a.accountID),
	})
	if err != nil {
		return fmt.Errorf("failed to delete access application %s: %w", appID, err)
	}

	log.Info().
		Str("app_id", appID).
		Msg("Deleted Access Application")

	// Then try to clean up the now-unlinked reusable policies
	for _, pID := range policyIDs {
		a.tryDeleteReusablePolicy(ctx, pID)
	}

	return nil
}

// buildAccessRules converts internal AccessRule types to Cloudflare API rule params.
func buildAccessRules(rules []types.AccessRule) []zero_trust.AccessRuleUnionParam {
	var result []zero_trust.AccessRuleUnionParam
	for _, rule := range rules {
		cfRules := convertAccessRule(rule)
		result = append(result, cfRules...)
	}
	return result
}

// convertAccessRule converts a single internal AccessRule to CF API rule param(s).
// Some selectors (emails, ip_ranges, country) produce one rule per value.
func convertAccessRule(rule types.AccessRule) []zero_trust.AccessRuleUnionParam {
	var result []zero_trust.AccessRuleUnionParam

	switch rule.Selector {
	case types.SelectorEmails:
		for _, email := range rule.Values {
			result = append(result, zero_trust.EmailRuleParam{
				Email: cf.F(zero_trust.EmailRuleEmailParam{
					Email: cf.F(email),
				}),
			})
		}

	case types.SelectorEmailsEndingIn:
		for _, domain := range rule.Values {
			result = append(result, zero_trust.DomainRuleParam{
				EmailDomain: cf.F(zero_trust.DomainRuleEmailDomainParam{
					Domain: cf.F(domain),
				}),
			})
		}

	case types.SelectorIPRanges:
		for _, ip := range rule.Values {
			result = append(result, zero_trust.IPRuleParam{
				IP: cf.F(zero_trust.IPRuleIPParam{
					IP: cf.F(ip),
				}),
			})
		}

	case types.SelectorCountry:
		for _, cc := range rule.Values {
			result = append(result, zero_trust.CountryRuleParam{
				Geo: cf.F(zero_trust.CountryRuleGeoParam{
					CountryCode: cf.F(cc),
				}),
			})
		}

	case types.SelectorEveryone:
		result = append(result, zero_trust.EveryoneRuleParam{
			Everyone: cf.F(zero_trust.EveryoneRuleEveryoneParam{}),
		})

	case types.SelectorServiceToken:
		for _, tokenID := range rule.Values {
			result = append(result, zero_trust.ServiceTokenRuleParam{
				ServiceToken: cf.F(zero_trust.ServiceTokenRuleServiceTokenParam{
					TokenID: cf.F(tokenID),
				}),
			})
		}

	case types.SelectorAccessGroups:
		for _, groupID := range rule.Values {
			result = append(result, zero_trust.GroupRuleParam{
				Group: cf.F(zero_trust.GroupRuleGroupParam{
					ID: cf.F(groupID),
				}),
			})
		}

	case types.SelectorCertificate:
		result = append(result, zero_trust.CertificateRuleParam{
			Certificate: cf.F(zero_trust.CertificateRuleCertificateParam{}),
		})

	case types.SelectorLoginMethods:
		for _, methodID := range rule.Values {
			result = append(result, zero_trust.AccessRuleAccessLoginMethodRuleParam{
				LoginMethod: cf.F(zero_trust.AccessRuleAccessLoginMethodRuleLoginMethodParam{
					ID: cf.F(methodID),
				}),
			})
		}
	}

	return result
}
