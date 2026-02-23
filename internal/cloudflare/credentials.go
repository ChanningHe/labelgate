package cloudflare

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/config"
)

// Credential represents a Cloudflare API credential.
type Credential struct {
	Name     string   // Credential name for reference
	APIToken string   // API token
	Zones    []string // Zones this credential applies to
	Default  bool     // Whether this is the default credential
}

// TunnelCredential represents credentials for a Cloudflare Tunnel.
type TunnelCredential struct {
	TunnelID   string // Tunnel ID
	TunnelName string // Tunnel name for reference
	AccountID  string // Account ID
	Credential string // Credential name to use for API calls
}

// HealthResult holds the result of a Cloudflare API health check.
type HealthResult struct {
	Reachable bool
	LastCheck time.Time
	Error     string
}

// CredentialManager manages multiple Cloudflare credentials.
type CredentialManager struct {
	credentials       []Credential
	tunnelCredentials []TunnelCredential
	defaultCredential *Credential
	clients           map[string]*Client // credential name -> client
	clientsMu         sync.RWMutex

	// Cached health check
	healthResult *HealthResult
	healthMu     sync.RWMutex
}

// NewCredentialManager creates a new credential manager from config.
func NewCredentialManager(cfg *config.Config) (*CredentialManager, error) {
	cm := &CredentialManager{
		credentials:       make([]Credential, 0),
		tunnelCredentials: make([]TunnelCredential, 0),
		clients:           make(map[string]*Client),
	}

	// Load default credential from root-level cloudflare config
	if cfg.Cloudflare.APIToken != "" {
		defaultCred := Credential{
			Name:     "default",
			APIToken: cfg.Cloudflare.APIToken,
			Default:  true,
		}
		cm.credentials = append(cm.credentials, defaultCred)
		cm.defaultCredential = &cm.credentials[0]
	}

	// Load additional named credentials from config
	for name, cfgCred := range cfg.Cloudflare.Credentials {
		cred := Credential{
			Name:     name,
			APIToken: cfgCred.APIToken,
			Zones:    cfgCred.Zones,
			Default:  false,
		}
		cm.credentials = append(cm.credentials, cred)
	}

	// If no default set, use the first one
	if cm.defaultCredential == nil && len(cm.credentials) > 0 {
		cm.credentials[0].Default = true
		cm.defaultCredential = &cm.credentials[0]
	}

	// Load default tunnel from root-level cloudflare config
	if cfg.Cloudflare.AccountID != "" && cfg.Cloudflare.TunnelID != "" {
		tunnelCred := TunnelCredential{
			TunnelID:   cfg.Cloudflare.TunnelID,
			TunnelName: "default",
			AccountID:  cfg.Cloudflare.AccountID,
		}
		cm.tunnelCredentials = append(cm.tunnelCredentials, tunnelCred)
	}

	// Load additional named tunnels from config
	for name, tunnel := range cfg.Cloudflare.Tunnels {
		tunnelCred := TunnelCredential{
			TunnelID:   tunnel.TunnelID,
			TunnelName: name,
			AccountID:  tunnel.AccountID,
			Credential: tunnel.Credential,
		}
		cm.tunnelCredentials = append(cm.tunnelCredentials, tunnelCred)
	}

	if len(cm.credentials) == 0 {
		return nil, fmt.Errorf("no Cloudflare credentials configured")
	}

	return cm, nil
}

// GetCredentialForZone returns the appropriate credential for a given hostname.
// Priority: explicit label > zone matching > default
func (cm *CredentialManager) GetCredentialForZone(hostname string, explicitCredName string) (*Credential, error) {
	// 1. Check explicit credential name
	if explicitCredName != "" {
		for i := range cm.credentials {
			if cm.credentials[i].Name == explicitCredName {
				return &cm.credentials[i], nil
			}
		}
		return nil, fmt.Errorf("credential not found: %s", explicitCredName)
	}

	// 2. Match by zone — check if hostname belongs to any credential's zones
	// using suffix matching against user-configured zone names.
	for i := range cm.credentials {
		if matchZone(&cm.credentials[i], hostname) {
			return &cm.credentials[i], nil
		}
	}

	// 3. Use default
	if cm.defaultCredential != nil {
		return cm.defaultCredential, nil
	}

	return nil, fmt.Errorf("no matching credential found for hostname: %s", hostname)
}

// GetTunnelCredential returns the tunnel credential for a given tunnel ID or name.
func (cm *CredentialManager) GetTunnelCredential(tunnelIDOrName string) (*TunnelCredential, error) {
	for i := range cm.tunnelCredentials {
		if cm.tunnelCredentials[i].TunnelID == tunnelIDOrName ||
			cm.tunnelCredentials[i].TunnelName == tunnelIDOrName {
			return &cm.tunnelCredentials[i], nil
		}
	}
	return nil, fmt.Errorf("tunnel credential not found: %s", tunnelIDOrName)
}

// GetClient returns a cached Cloudflare client for the given credential.
func (cm *CredentialManager) GetClient(cred *Credential) (*Client, error) {
	cm.clientsMu.RLock()
	if client, ok := cm.clients[cred.Name]; ok {
		cm.clientsMu.RUnlock()
		return client, nil
	}
	cm.clientsMu.RUnlock()

	cm.clientsMu.Lock()
	defer cm.clientsMu.Unlock()

	// Double-check after acquiring write lock
	if client, ok := cm.clients[cred.Name]; ok {
		return client, nil
	}

	if cred.APIToken == "" {
		return nil, fmt.Errorf("invalid credential %s: missing API token", cred.Name)
	}

	client := NewClient(cred.APIToken)
	cm.clients[cred.Name] = client
	log.Debug().
		Str("credential", cred.Name).
		Msg("Created new Cloudflare client")

	return client, nil
}

// GetClientForHostname returns a client suitable for operations on the given hostname.
func (cm *CredentialManager) GetClientForHostname(hostname string, explicitCredName string) (*Client, error) {
	cred, err := cm.GetCredentialForZone(hostname, explicitCredName)
	if err != nil {
		return nil, err
	}
	return cm.GetClient(cred)
}

// GetTunnelClient returns a client configured for tunnel operations.
func (cm *CredentialManager) GetTunnelClient(tunnelIDOrName string) (*Client, *TunnelCredential, error) {
	tunnelCred, err := cm.GetTunnelCredential(tunnelIDOrName)
	if err != nil {
		// Fall back to default credential
		if cm.defaultCredential != nil {
			client, clientErr := cm.GetClient(cm.defaultCredential)
			if clientErr != nil {
				return nil, nil, clientErr
			}
			// Try to set account ID from first tunnel config
			if len(cm.tunnelCredentials) > 0 {
				client.SetAccountID(cm.tunnelCredentials[0].AccountID)
				return client, &cm.tunnelCredentials[0], nil
			}
			return client, nil, nil
		}
		return nil, nil, err
	}

	// Use the tunnel's specified credential, or fall back to default
	var cred *Credential
	if tunnelCred.Credential != "" {
		cred, err = cm.GetCredentialForZone("", tunnelCred.Credential)
		if err != nil {
			cred = cm.defaultCredential
		}
	} else {
		cred = cm.defaultCredential
	}

	if cred == nil {
		return nil, nil, fmt.Errorf("no credential available for tunnel %s", tunnelIDOrName)
	}

	client, err := cm.GetClient(cred)
	if err != nil {
		return nil, nil, err
	}

	client.SetAccountID(tunnelCred.AccountID)
	return client, tunnelCred, nil
}

// ListTunnels returns all tunnel names.
func (cm *CredentialManager) ListTunnels() []string {
	names := make([]string, 0, len(cm.tunnelCredentials))
	for _, cred := range cm.tunnelCredentials {
		names = append(names, cred.TunnelName)
	}
	return names
}

// Validate validates all configured credentials.
func (cm *CredentialManager) Validate(ctx context.Context) error {
	for i := range cm.credentials {
		cred := &cm.credentials[i]
		client, err := cm.GetClient(cred)
		if err != nil {
			return fmt.Errorf("failed to create client for %s: %w", cred.Name, err)
		}

		if err := client.Validate(ctx); err != nil {
			return fmt.Errorf("validation failed for credential %s: %w", cred.Name, err)
		}

		log.Info().
			Str("credential", cred.Name).
			Bool("default", cred.Default).
			Msg("Credential validated successfully")
	}
	return nil
}

// GetDefaultClient returns the default Cloudflare client.
func (cm *CredentialManager) GetDefaultClient() (*Client, error) {
	if cm.defaultCredential == nil {
		return nil, fmt.Errorf("no default credential configured")
	}
	return cm.GetClient(cm.defaultCredential)
}

// HealthCheck returns a cached health check result for the Cloudflare API.
// Results are cached for 30 seconds to avoid excessive API calls.
func (cm *CredentialManager) HealthCheck(ctx context.Context) *HealthResult {
	const cacheTTL = 30 * time.Second

	cm.healthMu.RLock()
	if cm.healthResult != nil && time.Since(cm.healthResult.LastCheck) < cacheTTL {
		result := cm.healthResult
		cm.healthMu.RUnlock()
		return result
	}
	cm.healthMu.RUnlock()

	// Perform the check
	result := &HealthResult{
		LastCheck: time.Now(),
		Reachable: true,
	}

	if err := cm.Validate(ctx); err != nil {
		result.Reachable = false
		result.Error = err.Error()
	}

	cm.healthMu.Lock()
	cm.healthResult = result
	cm.healthMu.Unlock()

	return result
}

// matchZone checks if hostname belongs to any of the credential's configured zones.
// It performs suffix matching: hostname "api.sub.example.com" matches zone "example.com".
// Wildcard zones like "*.example.com" match any subdomain of example.com.
func matchZone(cred *Credential, hostname string) bool {
	hostname = strings.TrimSuffix(hostname, ".")
	for _, zone := range cred.Zones {
		zone = strings.TrimSuffix(zone, ".")
		// Exact match: hostname is the zone itself
		if hostname == zone {
			return true
		}
		// Suffix match: hostname ends with ".zone"
		if strings.HasSuffix(hostname, "."+zone) {
			return true
		}
		// Wildcard match: "*.example.com" matches any subdomain
		if strings.HasPrefix(zone, "*.") {
			baseDomain := zone[2:] // strip "*."
			if hostname == baseDomain || strings.HasSuffix(hostname, "."+baseDomain) {
				return true
			}
		}
	}
	return false
}
