// Package labels provides label parsing for labelgate.
package labels

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/channinghe/labelgate/internal/types"
)

const (
	// DefaultPrefix is the default label prefix.
	DefaultPrefix = "labelgate"

	// TypeDNS is the DNS label type.
	TypeDNS = "dns"
	// TypeTunnel is the Tunnel label type.
	TypeTunnel = "tunnel"
	// TypeAccess is the Access label type (Phase 4).
	TypeAccess = "access"
)

// Reserved property names that cannot be used as service names.
var reservedNames = map[string]bool{
	"hostname":   true,
	"service":    true,
	"type":       true,
	"target":     true,
	"proxied":    true,
	"ttl":        true,
	"cleanup":    true,
	"credential": true,
	"tunnel":     true,
	"path":       true,
	"origin":     true,
	"access":     true,
	"default":    true, // Used for global defaults
	"priority":   true,
	"weight":     true,
	"port":       true,
	"flags":      true,
	"tag":        true,
	"comment":    true,
}

// Service name validation pattern: lowercase alphanumeric with hyphens.
var serviceNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// Parser parses container labels into service configurations.
type Parser struct {
	prefix string
}

// NewParser creates a new label parser with the given prefix.
func NewParser(prefix string) *Parser {
	if prefix == "" {
		prefix = DefaultPrefix
	}
	return &Parser{prefix: prefix}
}

// ParseResult contains the parsed DNS, Tunnel, and Access services.
type ParseResult struct {
	DNSServices    []*types.DNSService
	TunnelServices []*types.TunnelService
	AccessPolicies map[string]*types.AccessPolicyDef // policy_name -> definition
	Errors         []error
}

// Parse parses container labels into service configurations.
func (p *Parser) Parse(labels map[string]string) *ParseResult {
	result := &ParseResult{AccessPolicies: make(map[string]*types.AccessPolicyDef)}

	// Group labels by type and service name
	dnsLabels := make(map[string]map[string]string)    // service_name -> property -> value
	tunnelLabels := make(map[string]map[string]string) // service_name -> property -> value
	accessLabels := make(map[string]map[string]string) // service_name -> property -> value

	for key, value := range labels {
		// Check prefix
		if !strings.HasPrefix(key, p.prefix+".") {
			continue
		}

		// Remove prefix: labelgate.dns.web.hostname -> dns.web.hostname
		rest := strings.TrimPrefix(key, p.prefix+".")
		parts := strings.SplitN(rest, ".", 3)
		if len(parts) < 3 {
			// Not enough parts, might be global config
			continue
		}

		labelType := parts[0]  // dns, tunnel, access
		serviceName := parts[1] // web, api, etc.
		property := parts[2]    // hostname, type, etc.

		// Validate service name
		if reservedNames[serviceName] && serviceName != "default" {
			result.Errors = append(result.Errors, fmt.Errorf("reserved service name: %s", serviceName))
			continue
		}

		if serviceName != "default" && !serviceNamePattern.MatchString(serviceName) {
			result.Errors = append(result.Errors, fmt.Errorf("invalid service name: %s (must be lowercase alphanumeric with hyphens)", serviceName))
			continue
		}

		switch labelType {
		case TypeDNS:
			if dnsLabels[serviceName] == nil {
				dnsLabels[serviceName] = make(map[string]string)
			}
			dnsLabels[serviceName][property] = value

		case TypeTunnel:
			if tunnelLabels[serviceName] == nil {
				tunnelLabels[serviceName] = make(map[string]string)
			}
			tunnelLabels[serviceName][property] = value

		case TypeAccess:
			if accessLabels[serviceName] == nil {
				accessLabels[serviceName] = make(map[string]string)
			}
			accessLabels[serviceName][property] = value
		}
	}

	// Parse Access policy definitions
	for policyName, props := range accessLabels {
		if policyName == "default" {
			continue
		}
		policyDef, err := p.parseAccessPolicyDef(policyName, props)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}
		result.AccessPolicies[policyName] = policyDef
	}

	// Parse DNS services
	dnsDefaults := dnsLabels["default"]
	delete(dnsLabels, "default")
	for serviceName, props := range dnsLabels {
		svc, err := p.parseDNSService(serviceName, props, dnsDefaults)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}
		result.DNSServices = append(result.DNSServices, svc)
	}

	// Parse Tunnel services
	tunnelDefaults := tunnelLabels["default"]
	delete(tunnelLabels, "default")
	for serviceName, props := range tunnelLabels {
		svc, err := p.parseTunnelService(serviceName, props, tunnelDefaults)
		if err != nil {
			result.Errors = append(result.Errors, err)
			continue
		}
		result.TunnelServices = append(result.TunnelServices, svc)
	}

	return result
}

// parseDNSService parses DNS service properties.
func (p *Parser) parseDNSService(serviceName string, props map[string]string, defaults map[string]string) (*types.DNSService, error) {
	svc := types.DefaultDNSService()
	svc.ServiceName = serviceName

	// Apply defaults first
	if defaults != nil {
		applyDNSDefaults(svc, defaults)
	}

	// Apply service-specific properties
	for key, value := range props {
		switch key {
		case "hostname":
			svc.Hostname = value
		case "type":
			svc.Type = types.DNSRecordType(strings.ToUpper(value))
		case "target":
			svc.Target = value
		case "proxied":
			svc.Proxied = parseBool(value, svc.Proxied)
		case "ttl":
			svc.TTL = parseInt(value, svc.TTL)
		case "credential":
			svc.Credential = value
		case "cleanup":
			svc.Cleanup = parseBool(value, svc.Cleanup)
		case "comment":
			svc.Comment = value
		case "access":
			svc.Access = value
		case "priority":
			svc.Priority = parseInt(value, svc.Priority)
		case "weight":
			svc.Weight = parseInt(value, svc.Weight)
		case "port":
			svc.Port = parseInt(value, svc.Port)
		case "flags":
			svc.Flags = parseInt(value, svc.Flags)
		case "tag":
			svc.Tag = value
		}
	}

	// Validate required fields
	if svc.Hostname == "" {
		return nil, fmt.Errorf("DNS service %s: hostname is required", serviceName)
	}

	// Validate record type
	switch svc.Type {
	case types.DNSTypeA, types.DNSTypeAAAA, types.DNSTypeCNAME, types.DNSTypeTXT,
		types.DNSTypeMX, types.DNSTypeSRV, types.DNSTypeCAA:
		// Valid types
	default:
		return nil, fmt.Errorf("DNS service %s: invalid record type: %s", serviceName, svc.Type)
	}

	return svc, nil
}

// parseTunnelService parses Tunnel service properties.
func (p *Parser) parseTunnelService(serviceName string, props map[string]string, defaults map[string]string) (*types.TunnelService, error) {
	svc := types.DefaultTunnelService()
	svc.ServiceName = serviceName

	// Apply defaults first
	if defaults != nil {
		applyTunnelDefaults(svc, defaults)
	}

	// Parse origin request settings
	var origin *types.OriginRequestConfig

	// Apply service-specific properties
	for key, value := range props {
		// Handle origin.* properties
		if strings.HasPrefix(key, "origin.") {
			if origin == nil {
				origin = &types.OriginRequestConfig{}
			}
			originKey := strings.TrimPrefix(key, "origin.")
			applyOriginProperty(origin, originKey, value)
			continue
		}

		switch key {
		case "hostname":
			svc.Hostname = value
		case "service":
			svc.Service = value
		case "tunnel":
			svc.Tunnel = value
		case "path":
			svc.Path = value
		case "credential":
			svc.Credential = value
		case "cleanup":
			svc.Cleanup = parseBool(value, svc.Cleanup)
		case "access":
			svc.Access = value
		}
	}

	if origin != nil {
		svc.OriginRequest = origin
	}

	// Validate required fields
	if svc.Hostname == "" {
		return nil, fmt.Errorf("Tunnel service %s: hostname is required", serviceName)
	}
	if svc.Service == "" {
		return nil, fmt.Errorf("Tunnel service %s: service is required", serviceName)
	}

	return svc, nil
}

// parseAccessPolicyDef parses an access policy definition from labels.
func (p *Parser) parseAccessPolicyDef(policyName string, props map[string]string) (*types.AccessPolicyDef, error) {
	def := types.DefaultAccessPolicyDef(policyName)
	policy := &def.Policies[0]

	for key, value := range props {
		switch {
		case key == "app_name":
			def.AppName = value
		case key == "session_duration":
			def.SessionDuration = value
		case key == "policy.decision":
			decision := strings.ToLower(strings.TrimSpace(value))
			switch decision {
			case types.AccessDecisionAllow, types.AccessDecisionBlock,
				types.AccessDecisionBypass, types.AccessDecisionServiceAuth:
				policy.Decision = decision
			default:
				return nil, fmt.Errorf("access policy %s: invalid decision: %s (must be allow, block, bypass, or service_auth)", policyName, value)
			}
		case key == "policy.name":
			policy.Name = value
		case strings.HasPrefix(key, "policy.include."):
			selector := strings.TrimPrefix(key, "policy.include.")
			rule, err := parseAccessRule(policyName, "include", selector, value)
			if err != nil {
				return nil, err
			}
			policy.Include = append(policy.Include, *rule)
		case strings.HasPrefix(key, "policy.require."):
			selector := strings.TrimPrefix(key, "policy.require.")
			rule, err := parseAccessRule(policyName, "require", selector, value)
			if err != nil {
				return nil, err
			}
			policy.Require = append(policy.Require, *rule)
		case strings.HasPrefix(key, "policy.exclude."):
			selector := strings.TrimPrefix(key, "policy.exclude.")
			rule, err := parseAccessRule(policyName, "exclude", selector, value)
			if err != nil {
				return nil, err
			}
			policy.Exclude = append(policy.Exclude, *rule)
		}
	}

	if policy.Decision == types.AccessDecisionAllow || policy.Decision == types.AccessDecisionBlock {
		if len(policy.Include) == 0 {
			return nil, fmt.Errorf("access policy %s: %s decision requires at least one include rule", policyName, policy.Decision)
		}
	}

	return def, nil
}

func parseAccessRule(policyName, ruleType, selector, value string) (*types.AccessRule, error) {
	if !types.ValidAccessSelectors[selector] {
		return nil, fmt.Errorf("access policy %s: invalid %s selector: %s", policyName, ruleType, selector)
	}

	// Valueless selectors: "everyone" and "certificate" don't require values
	if selector == types.SelectorEveryone || selector == types.SelectorCertificate {
		return &types.AccessRule{
			Selector: selector,
			Values:   []string{},
		}, nil
	}

	values := splitAndTrim(value)
	if len(values) == 0 {
		return nil, fmt.Errorf("access policy %s: %s.%s requires at least one value", policyName, ruleType, selector)
	}

	return &types.AccessRule{
		Selector: selector,
		Values:   values,
	}, nil
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// applyDNSDefaults applies default values to DNS service.
func applyDNSDefaults(svc *types.DNSService, defaults map[string]string) {
	if v, ok := defaults["type"]; ok {
		svc.Type = types.DNSRecordType(strings.ToUpper(v))
	}
	if v, ok := defaults["target"]; ok {
		svc.Target = v
	}
	if v, ok := defaults["proxied"]; ok {
		svc.Proxied = parseBool(v, svc.Proxied)
	}
	if v, ok := defaults["ttl"]; ok {
		svc.TTL = parseInt(v, svc.TTL)
	}
	if v, ok := defaults["credential"]; ok {
		svc.Credential = v
	}
	if v, ok := defaults["cleanup"]; ok {
		svc.Cleanup = parseBool(v, svc.Cleanup)
	}
}

// applyTunnelDefaults applies default values to Tunnel service.
func applyTunnelDefaults(svc *types.TunnelService, defaults map[string]string) {
	if v, ok := defaults["tunnel"]; ok {
		svc.Tunnel = v
	}
	if v, ok := defaults["credential"]; ok {
		svc.Credential = v
	}
	if v, ok := defaults["cleanup"]; ok {
		svc.Cleanup = parseBool(v, svc.Cleanup)
	}
}

// applyOriginProperty applies an origin request property.
func applyOriginProperty(origin *types.OriginRequestConfig, key, value string) {
	switch key {
	case "connect_timeout":
		origin.ConnectTimeout = value
	case "tls_timeout":
		origin.TLSTimeout = value
	case "tcp_keepalive":
		origin.TCPKeepAlive = value
	case "keep_alive_connections":
		origin.KeepAliveConnections = parseInt(value, 0)
	case "keep_alive_timeout":
		origin.KeepAliveTimeout = value
	case "no_tls_verify":
		origin.NoTLSVerify = parseBool(value, false)
	case "origin_server_name":
		origin.OriginServerName = value
	case "ca_pool":
		origin.CAPool = value
	case "http_host_header":
		origin.HTTPHostHeader = value
	case "no_happy_eyeballs":
		origin.NoHappyEyeballs = parseBool(value, false)
	case "disable_chunked_encoding":
		origin.DisableChunkedEncoding = parseBool(value, false)
	case "proxy_type":
		origin.ProxyType = value
	}
}

// Helper functions

func parseBool(s string, def bool) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return def
	}
}

func parseInt(s string, def int) int {
	if i, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return i
	}
	return def
}

// CheckHostnameConflict checks if the same hostname is configured for both DNS and Tunnel.
func (p *Parser) CheckHostnameConflict(result *ParseResult) error {
	dnsHostnames := make(map[string]string)
	tunnelHostnames := make(map[string]string)

	for _, svc := range result.DNSServices {
		dnsHostnames[svc.Hostname] = svc.ServiceName
	}

	for _, svc := range result.TunnelServices {
		tunnelHostnames[svc.Hostname] = svc.ServiceName
	}

	// Check for conflicts
	for hostname, dnsService := range dnsHostnames {
		if tunnelService, exists := tunnelHostnames[hostname]; exists {
			return &HostnameConflictError{
				Hostname:      hostname,
				DNSService:    dnsService,
				TunnelService: tunnelService,
			}
		}
	}

	return nil
}

// HostnameConflictError represents a hostname conflict between DNS and Tunnel.
type HostnameConflictError struct {
	Hostname      string
	DNSService    string
	TunnelService string
}

func (e *HostnameConflictError) Error() string {
	return fmt.Sprintf("hostname %s is configured for both DNS service '%s' and Tunnel service '%s'",
		e.Hostname, e.DNSService, e.TunnelService)
}
