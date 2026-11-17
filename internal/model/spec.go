package model

import (
	"fmt"
	"time"
)

// DNSType is a DNS record type supported by labelgate.
type DNSType string

const (
	DNSTypeA     DNSType = "A"
	DNSTypeAAAA  DNSType = "AAAA"
	DNSTypeCNAME DNSType = "CNAME"
	DNSTypeTXT   DNSType = "TXT"
	DNSTypeMX    DNSType = "MX"
	DNSTypeSRV   DNSType = "SRV"
	DNSTypeCAA   DNSType = "CAA"
)

// DNSTypes lists the supported record types.
var DNSTypes = []DNSType{DNSTypeA, DNSTypeAAAA, DNSTypeCNAME, DNSTypeTXT, DNSTypeMX, DNSTypeSRV, DNSTypeCAA}

// Special DNS target values.
const (
	// TargetAuto resolves to the public IP of the host running the container.
	TargetAuto = "auto"
	// TargetContainer resolves to the container's IP (first network by name).
	TargetContainer = "container"
)

// DNSSpec is one labelgate.dns.<service>.* service after parsing and defaults.
// Hostname is normalized (see NormalizeHostname). Target is NOT resolved yet.
type DNSSpec struct {
	Service    string
	Hostname   string
	Type       DNSType // default A
	Target     string  // default "auto"; literal IP, hostname, text, "auto" or "container"
	Proxied    bool    // default true; forced false for types other than A/AAAA/CNAME
	TTL        int     // seconds; 0 or 1 = automatic (default)
	Credential string  // "" = automatic selection
	Cleanup    bool    // default true
	Comment    string  // appended after the ownership marker
	Access     string  // name of an access policy definition; "" = none
	Priority   int     // MX, SRV
	Weight     int     // SRV
	Port       int     // SRV
	Flags      int     // CAA (0-255)
	Tag        string  // CAA: issue (default), issuewild, iodef
}

// TunnelSpec is one labelgate.tunnel.<service>.* service after parsing and defaults.
type TunnelSpec struct {
	Service    string
	Hostname   string
	URL        string // origin service, label "service" (e.g. http://app:80, http_status:404)
	Tunnel     string // tunnel reference (config name or tunnel ID); "" = default tunnel
	Path       string // optional path regex; "" = all paths
	Credential string // used for the CNAME zone and Access; "" = automatic selection
	Cleanup    bool   // default true
	Access     string
	Origin     OriginRequest
}

// OriginRequest holds origin settings from labelgate.tunnel.<svc>.origin.*.
// Zero values mean "not set": the key is removed from the rule's originRequest.
// Keys labelgate does not model are preserved from the live rule.
type OriginRequest struct {
	ConnectTimeout         time.Duration // connectTimeout (seconds on the wire)
	TLSTimeout             time.Duration // tlsTimeout
	TCPKeepAlive           time.Duration // tcpKeepAlive
	KeepAliveTimeout       time.Duration // keepAliveTimeout
	KeepAliveConnections   int           // keepAliveConnections
	NoTLSVerify            bool          // noTLSVerify
	OriginServerName       string        // originServerName
	CAPool                 string        // caPool
	HTTPHostHeader         string        // httpHostHeader
	NoHappyEyeballs        bool          // noHappyEyeballs
	DisableChunkedEncoding bool          // disableChunkedEncoding
	ProxyType              string        // proxyType ("" or "socks")
}

// IsZero reports whether no origin setting is set.
func (o OriginRequest) IsZero() bool { return o == OriginRequest{} }

// Access decisions as written in labels (Cloudflare dashboard names).
const (
	AccessDecisionAllow       = "allow"
	AccessDecisionBlock       = "block"
	AccessDecisionBypass      = "bypass"
	AccessDecisionServiceAuth = "service_auth"
)

// DecisionToAPI maps a label decision to the Cloudflare API value.
func DecisionToAPI(decision string) string {
	switch decision {
	case AccessDecisionBlock:
		return "deny"
	case AccessDecisionServiceAuth:
		return "non_identity"
	default:
		return decision
	}
}

// Access rule selectors (label names, aligned with the Cloudflare dashboard).
const (
	SelectorEmails         = "emails"
	SelectorEmailsEndingIn = "emails_ending_in"
	SelectorIPRanges       = "ip_ranges"
	SelectorCountry        = "country"
	SelectorEveryone       = "everyone"
	SelectorServiceToken   = "service_token"
	SelectorAccessGroups   = "access_groups"
	SelectorCertificate    = "certificate"
	SelectorLoginMethods   = "login_methods"
)

// ValidSelector reports whether s is a supported selector.
func ValidSelector(s string) bool {
	switch s {
	case SelectorEmails, SelectorEmailsEndingIn, SelectorIPRanges, SelectorCountry,
		SelectorEveryone, SelectorServiceToken, SelectorAccessGroups,
		SelectorCertificate, SelectorLoginMethods:
		return true
	}
	return false
}

// ValuelessSelector reports whether s takes no values (everyone, certificate).
func ValuelessSelector(s string) bool {
	return s == SelectorEveryone || s == SelectorCertificate
}

// AccessRule is one selector with its values (comma-split, trimmed, in label order).
type AccessRule struct {
	Selector string
	Values   []string
}

// AccessPolicy is one Access policy of a definition.
type AccessPolicy struct {
	Decision string // label decision; default allow
	Name     string // optional display name (label policy.name)
	Include  []AccessRule
	Require  []AccessRule
	Exclude  []AccessRule
}

// AccessPolicyDef is a labelgate.access.<name>.* definition. It has no
// hostname: services reference it by Name via their "access" property.
type AccessPolicyDef struct {
	Name            string
	AppName         string // default "labelgate-<Name>"
	SessionDuration string // default "24h"
	Policies        []AccessPolicy
}

// ParsedLabels is the output of parsing one container's labels.
// All slices are sorted (DNS/Tunnels by Service, Access by Name, Errors by Key)
// so the result is deterministic for identical input.
type ParsedLabels struct {
	DNS     []DNSSpec
	Tunnels []TunnelSpec
	Access  []AccessPolicyDef
	Errors  []LabelError
}

// LabelError describes an invalid label. Key is the full label key, or a
// "<prefix>.<type>.<service>" stem when the error concerns a whole service.
type LabelError struct {
	Key     string
	Message string
	// Unknown marks an unknown property: reported, but nothing was dropped.
	Unknown bool `json:",omitempty"`
}

func (e LabelError) Error() string {
	return fmt.Sprintf("label %s: %s", e.Key, e.Message)
}
