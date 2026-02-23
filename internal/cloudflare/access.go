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
//  1. Create account-level reusable policies via ZeroTrust.Access.Policies.New
//  2. Create or update the Access Application, linking policies via the Policies field
func (a *AccessClient) EnsureAccessForHostname(ctx context.Context, hostname string, policyDef *types.AccessPolicyDef, existingAppID string) (string, error) {
	if a.accountID == "" {
		return "", fmt.Errorf("account ID is required for access operations")
	}

	// Step 1: Create reusable policies at the account level
	var policyLinks []policyLink
	for i := range policyDef.Policies {
		policy := &policyDef.Policies[i]
		policyID, err := a.createReusablePolicy(ctx, policy, i)
		if err != nil {
			return "", fmt.Errorf("failed to create reusable policy %d for %s: %w", i, hostname, err)
		}
		policyLinks = append(policyLinks, policyLink{
			ID:         policyID,
			Precedence: int64(i + 1),
		})
	}

	// Step 2: Create or update the application with policy links
	appName := policyDef.AppName
	if appName == "" {
		appName = fmt.Sprintf("labelgate-%s", policyDef.Name)
	}

	var appID string
	if existingAppID != "" {
		// Delete old policies linked to this app before updating
		a.cleanupOldAppPolicies(ctx, existingAppID)

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

	return appID, nil
}

// policyLink holds a reusable policy ID and its precedence for linking to an application.
type policyLink struct {
	ID         string
	Precedence int64
}

// createReusablePolicy creates an account-level reusable Access Policy.
func (a *AccessClient) createReusablePolicy(ctx context.Context, policy *types.AccessPolicy, index int) (string, error) {
	policyName := policy.Name
	if policyName == "" {
		policyName = fmt.Sprintf("labelgate-%s-%d", policy.Decision, index)
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

// DeleteAccessApplication deletes an Access Application.
// Associated reusable policies are NOT automatically deleted by Cloudflare,
// but they become unlinked and can be cleaned up separately if needed.
func (a *AccessClient) DeleteAccessApplication(ctx context.Context, appID string) error {
	if a.accountID == "" {
		return fmt.Errorf("account ID is required for access operations")
	}

	// First, get the app's linked policies so we can clean them up
	a.cleanupOldAppPolicies(ctx, appID)

	// Then delete the application itself
	_, err := a.client.API().ZeroTrust.Access.Applications.Delete(ctx, appID, zero_trust.AccessApplicationDeleteParams{
		AccountID: cf.F(a.accountID),
	})
	if err != nil {
		return fmt.Errorf("failed to delete access application %s: %w", appID, err)
	}

	log.Info().
		Str("app_id", appID).
		Msg("Deleted Access Application")

	return nil
}

// cleanupOldAppPolicies lists policies linked to an app and deletes the
// account-level reusable policies that were created by labelgate.
func (a *AccessClient) cleanupOldAppPolicies(ctx context.Context, appID string) {
	policies, err := a.client.API().ZeroTrust.Access.Applications.Policies.List(ctx, appID, zero_trust.AccessApplicationPolicyListParams{
		AccountID: cf.F(a.accountID),
	})
	if err != nil {
		log.Warn().Err(err).Str("app_id", appID).Msg("Failed to list app policies for cleanup")
		return
	}

	for _, p := range policies.Result {
		// Delete all linked reusable policies at the account level.
		// Labelgate fully owns policies on apps it manages, so we clean them all.
		_, err := a.client.API().ZeroTrust.Access.Policies.Delete(ctx, p.ID, zero_trust.AccessPolicyDeleteParams{
			AccountID: cf.F(a.accountID),
		})
		if err != nil {
			// May fail for inline (non-reusable) policies — that's fine, they go away with the app.
			log.Debug().Err(err).
				Str("policy_id", p.ID).
				Str("policy_name", p.Name).
				Msg("Could not delete linked policy (may be non-reusable)")
		} else {
			log.Debug().
				Str("policy_id", p.ID).
				Str("policy_name", p.Name).
				Msg("Cleaned up old reusable policy")
		}
	}
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
