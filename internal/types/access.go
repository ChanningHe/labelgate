package types

// AccessPolicyDef is a named, reusable access policy template parsed from labels.
// It has no hostname - hostname is inherited from the referencing tunnel/dns service.
// Multiple tunnel/dns services can reference the same policy definition.
type AccessPolicyDef struct {
	// Name is the policy template name (from label key, e.g., "internal", "bypass")
	Name string `json:"name"`

	// AppName is the CF "Application name" (optional, defaults to "labelgate-<name>")
	AppName string `json:"app_name,omitempty"`

	// SessionDuration is the CF "Session Duration" (default: "24h")
	SessionDuration string `json:"session_duration"`

	// Policies are the access policies to apply.
	// Phase 5.1: single policy (len=1). Phase 5.2: multiple numbered policies.
	Policies []AccessPolicy `json:"policies"`
}

// AccessPolicy corresponds to one CF Access Policy under an Application.
// Maps to CF Web UI: Access > Applications > [app] > Policies > [policy].
type AccessPolicy struct {
	// Decision is the CF Web UI "Action": allow, block, bypass, service_auth.
	// Mapped to CF API: allow -> "allow", block -> "deny", bypass -> "bypass",
	// service_auth -> "non_identity".
	Decision string `json:"decision"`

	// Name is the CF Web UI "Policy name".
	Name string `json:"name,omitempty"`

	// Include rules use OR logic (CF Web UI "Configure rules" > "Include").
	Include []AccessRule `json:"include,omitempty"`

	// Require rules use AND logic (CF Web UI "Configure rules" > "Require").
	Require []AccessRule `json:"require,omitempty"`

	// Exclude rules use NOT logic (CF Web UI "Configure rules" > "Exclude").
	Exclude []AccessRule `json:"exclude,omitempty"`
}

// AccessDecision constants matching Cloudflare Web UI action names.
const (
	AccessDecisionAllow       = "allow"
	AccessDecisionBlock       = "block"
	AccessDecisionBypass      = "bypass"
	AccessDecisionServiceAuth = "service_auth"
)

// MapDecisionToAPI maps label decision values to Cloudflare API decision values.
func MapDecisionToAPI(decision string) string {
	switch decision {
	case AccessDecisionBlock:
		return "deny"
	case AccessDecisionServiceAuth:
		return "non_identity"
	default:
		return decision // allow, bypass are the same
	}
}

// AccessRule is a single rule entry with a selector and values.
// Selector names are aligned with Cloudflare Web UI selector names.
type AccessRule struct {
	// Selector is the CF Web UI selector name.
	Selector string `json:"selector"`

	// Values are the selector values (comma-separated in labels, split into slice).
	Values []string `json:"values"`
}

// Access rule selector constants aligned with CF Web UI.
const (
	// SelectorEmails maps to CF Web UI "Emails".
	SelectorEmails = "emails"
	// SelectorEmailsEndingIn maps to CF Web UI "Emails ending in".
	SelectorEmailsEndingIn = "emails_ending_in"
	// SelectorIPRanges maps to CF Web UI "IP ranges".
	SelectorIPRanges = "ip_ranges"
	// SelectorCountry maps to CF Web UI "Country".
	SelectorCountry = "country"
	// SelectorEveryone maps to CF Web UI "Everyone".
	SelectorEveryone = "everyone"
	// SelectorServiceToken maps to CF Web UI "Service Token".
	SelectorServiceToken = "service_token"
	// SelectorAccessGroups maps to CF Web UI "Access groups".
	SelectorAccessGroups = "access_groups"
	// SelectorCertificate maps to CF Web UI "Valid Certificate".
	SelectorCertificate = "certificate"
	// SelectorLoginMethods maps to CF Web UI "Login Methods".
	SelectorLoginMethods = "login_methods"
)

// ValidAccessSelectors contains all valid access rule selectors.
var ValidAccessSelectors = map[string]bool{
	SelectorEmails:        true,
	SelectorEmailsEndingIn: true,
	SelectorIPRanges:       true,
	SelectorCountry:        true,
	SelectorEveryone:       true,
	SelectorServiceToken:   true,
	SelectorAccessGroups:   true,
	SelectorCertificate:    true,
	SelectorLoginMethods:   true,
}

// DefaultAccessPolicyDef returns an AccessPolicyDef with default values.
func DefaultAccessPolicyDef(name string) *AccessPolicyDef {
	return &AccessPolicyDef{
		Name:            name,
		SessionDuration: "24h",
		Policies: []AccessPolicy{
			{
				Decision: AccessDecisionAllow,
			},
		},
	}
}

// ResolvedAccessBinding represents a resolved access policy bound to a hostname.
// Created by the reconciler when resolving tunnel/dns access references.
type ResolvedAccessBinding struct {
	// Hostname from the referencing tunnel/dns service
	Hostname string `json:"hostname"`

	// PolicyDef is the resolved access policy definition
	PolicyDef *AccessPolicyDef `json:"policy_def"`

	// ContainerID of the referencing tunnel/dns service
	ContainerID string `json:"container_id,omitempty"`

	// ContainerName of the referencing tunnel/dns service
	ContainerName string `json:"container_name,omitempty"`

	// ServiceName of the referencing tunnel/dns service
	ServiceName string `json:"service_name"`

	// AgentID of the agent that reported this container
	AgentID string `json:"agent_id,omitempty"`

	// Cleanup follows the tunnel/dns service's cleanup setting
	Cleanup bool `json:"cleanup"`

	// Credential for CF API calls
	Credential string `json:"credential"`
}
