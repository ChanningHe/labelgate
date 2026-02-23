package cloudflare

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	cf "github.com/cloudflare/cloudflare-go/v6"
	"github.com/cloudflare/cloudflare-go/v6/zero_trust"
	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/types"
)

// TunnelClient provides Cloudflare Tunnel management operations.
type TunnelClient struct {
	client    *Client
	accountID string
}

// NewTunnelClient creates a new Tunnel client wrapper.
func NewTunnelClient(client *Client, accountID string) *TunnelClient {
	if accountID == "" {
		accountID = client.AccountID()
	}
	return &TunnelClient{
		client:    client,
		accountID: accountID,
	}
}

// GetTunnel retrieves a tunnel by ID.
func (t *TunnelClient) GetTunnel(ctx context.Context, tunnelID string) (*types.Tunnel, error) {
	if t.accountID == "" {
		return nil, fmt.Errorf("account ID is required for tunnel operations")
	}

	result, err := t.client.API().ZeroTrust.Tunnels.Cloudflared.Get(ctx, tunnelID, zero_trust.TunnelCloudflaredGetParams{
		AccountID: cf.F(t.accountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get tunnel: %w", err)
	}

	return &types.Tunnel{
		ID:        result.ID,
		AccountID: t.accountID,
		Name:      result.Name,
		Status:    string(result.Status),
	}, nil
}

// GetTunnelConfiguration retrieves the ingress configuration for a tunnel.
func (t *TunnelClient) GetTunnelConfiguration(ctx context.Context, tunnelID string) (*types.TunnelConfiguration, error) {
	if t.accountID == "" {
		return nil, fmt.Errorf("account ID is required for tunnel operations")
	}

	result, err := t.client.API().ZeroTrust.Tunnels.Cloudflared.Configurations.Get(ctx, tunnelID, zero_trust.TunnelCloudflaredConfigurationGetParams{
		AccountID: cf.F(t.accountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get tunnel configuration: %w", err)
	}

	config := &types.TunnelConfiguration{
		TunnelID: tunnelID,
		Ingress:  make([]types.IngressRule, 0),
	}

	if result.Config.Ingress != nil {
		for _, ing := range result.Config.Ingress {
			rule := types.IngressRule{
				Hostname: ing.Hostname,
				Service:  ing.Service,
				Path:     ing.Path,
			}
			// Convert origin request from API response to internal type
			or := convertGetResponseOriginRequest(&ing.OriginRequest)
			if or != nil {
				rule.OriginRequest = or
			}
			config.Ingress = append(config.Ingress, rule)
		}
	}

	return config, nil
}

// UpdateTunnelConfiguration updates the entire ingress configuration for a tunnel.
func (t *TunnelClient) UpdateTunnelConfiguration(ctx context.Context, tunnelID string, ingresses []*types.TunnelIngress) error {
	if t.accountID == "" {
		return fmt.Errorf("account ID is required for tunnel operations")
	}

	ingressParams := make([]zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfigIngress, 0, len(ingresses))
	for _, rule := range ingresses {
		ing := zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfigIngress{
			Service: cf.F(rule.Service),
		}
		if rule.Hostname != "" {
			ing.Hostname = cf.F(rule.Hostname)
		}
		if rule.Path != "" {
			ing.Path = cf.F(rule.Path)
		}
		if rule.OriginRequest != nil {
			ing.OriginRequest = cf.F(convertOriginRequestToParams(rule.OriginRequest))
		}
		ingressParams = append(ingressParams, ing)
	}

	// Add catch-all rule if not present
	if len(ingressParams) == 0 || ingresses[len(ingresses)-1].Hostname != "" {
		ingressParams = append(ingressParams, zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfigIngress{
			Service: cf.F("http_status:404"),
		})
	}

	_, err := t.client.API().ZeroTrust.Tunnels.Cloudflared.Configurations.Update(ctx, tunnelID, zero_trust.TunnelCloudflaredConfigurationUpdateParams{
		AccountID: cf.F(t.accountID),
		Config: cf.F(zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfig{
			Ingress: cf.F(ingressParams),
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to update tunnel configuration: %w", err)
	}

	log.Info().
		Str("tunnel_id", tunnelID).
		Int("ingress_count", len(ingresses)).
		Msg("Updated tunnel configuration")

	return nil
}

// AddIngressRule adds an ingress rule to the tunnel configuration.
func (t *TunnelClient) AddIngressRule(ctx context.Context, tunnelID string, rule *types.TunnelIngress) error {
	// Get current configuration
	config, err := t.GetTunnelConfiguration(ctx, tunnelID)
	if err != nil {
		return fmt.Errorf("failed to get current configuration: %w", err)
	}

	// Convert to TunnelIngress slice
	ingresses := make([]*types.TunnelIngress, 0, len(config.Ingress)+1)
	found := false

	for _, existing := range config.Ingress {
		// Skip catch-all rule
		if existing.Hostname == "" {
			continue
		}
		if existing.Hostname == rule.Hostname && existing.Path == rule.Path {
			// Update existing rule
			ingresses = append(ingresses, rule)
			found = true
		} else {
			ingresses = append(ingresses, &types.TunnelIngress{
				Hostname:      existing.Hostname,
				Path:          existing.Path,
				Service:       existing.Service,
				OriginRequest: existing.OriginRequest,
			})
		}
	}

	if !found {
		ingresses = append(ingresses, rule)
	}

	return t.UpdateTunnelConfiguration(ctx, tunnelID, ingresses)
}

// RemoveIngressRule removes an ingress rule from the tunnel configuration.
func (t *TunnelClient) RemoveIngressRule(ctx context.Context, tunnelID string, hostname string) error {
	// Get current configuration
	config, err := t.GetTunnelConfiguration(ctx, tunnelID)
	if err != nil {
		return fmt.Errorf("failed to get current configuration: %w", err)
	}

	// Filter out the rule to remove
	ingresses := make([]*types.TunnelIngress, 0, len(config.Ingress))
	found := false

	for _, rule := range config.Ingress {
		// Skip catch-all rule
		if rule.Hostname == "" {
			continue
		}
		if rule.Hostname == hostname {
			found = true
			continue
		}
		ingresses = append(ingresses, &types.TunnelIngress{
			Hostname:      rule.Hostname,
			Path:          rule.Path,
			Service:       rule.Service,
			OriginRequest: rule.OriginRequest,
		})
	}

	if !found {
		log.Warn().
			Str("hostname", hostname).
			Str("tunnel_id", tunnelID).
			Msg("Ingress rule not found, nothing to remove")
		return nil
	}

	return t.UpdateTunnelConfiguration(ctx, tunnelID, ingresses)
}

// convertOriginRequestToParams converts internal OriginRequestConfig to CF API params.
func convertOriginRequestToParams(or *types.OriginRequestConfig) zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfigIngressOriginRequest {
	params := zero_trust.TunnelCloudflaredConfigurationUpdateParamsConfigIngressOriginRequest{}

	if or.ConnectTimeout != "" {
		if secs := parseDurationToSeconds(or.ConnectTimeout); secs > 0 {
			params.ConnectTimeout = cf.F(secs)
		}
	}
	if or.TLSTimeout != "" {
		if secs := parseDurationToSeconds(or.TLSTimeout); secs > 0 {
			params.TLSTimeout = cf.F(secs)
		}
	}
	if or.TCPKeepAlive != "" {
		if secs := parseDurationToSeconds(or.TCPKeepAlive); secs > 0 {
			params.TCPKeepAlive = cf.F(secs)
		}
	}
	if or.KeepAliveConnections > 0 {
		params.KeepAliveConnections = cf.F(int64(or.KeepAliveConnections))
	}
	if or.KeepAliveTimeout != "" {
		if secs := parseDurationToSeconds(or.KeepAliveTimeout); secs > 0 {
			params.KeepAliveTimeout = cf.F(secs)
		}
	}
	if or.NoTLSVerify {
		params.NoTLSVerify = cf.F(true)
	}
	if or.OriginServerName != "" {
		params.OriginServerName = cf.F(or.OriginServerName)
	}
	if or.CAPool != "" {
		params.CAPool = cf.F(or.CAPool)
	}
	if or.HTTPHostHeader != "" {
		params.HTTPHostHeader = cf.F(or.HTTPHostHeader)
	}
	if or.NoHappyEyeballs {
		params.NoHappyEyeballs = cf.F(true)
	}
	if or.DisableChunkedEncoding {
		params.DisableChunkedEncoding = cf.F(true)
	}
	if or.ProxyType != "" {
		params.ProxyType = cf.F(or.ProxyType)
	}

	return params
}

// convertGetResponseOriginRequest converts CF API response origin request to internal type.
// Returns nil if all fields are zero/empty.
func convertGetResponseOriginRequest(or *zero_trust.TunnelCloudflaredConfigurationGetResponseConfigIngressOriginRequest) *types.OriginRequestConfig {
	if or == nil {
		return nil
	}

	cfg := &types.OriginRequestConfig{
		NoTLSVerify:            or.NoTLSVerify,
		OriginServerName:       or.OriginServerName,
		CAPool:                 or.CAPool,
		HTTPHostHeader:         or.HTTPHostHeader,
		NoHappyEyeballs:        or.NoHappyEyeballs,
		DisableChunkedEncoding: or.DisableChunkedEncoding,
		ProxyType:              or.ProxyType,
	}

	if or.ConnectTimeout > 0 {
		cfg.ConnectTimeout = fmt.Sprintf("%ds", or.ConnectTimeout)
	}
	if or.TLSTimeout > 0 {
		cfg.TLSTimeout = fmt.Sprintf("%ds", or.TLSTimeout)
	}
	if or.TCPKeepAlive > 0 {
		cfg.TCPKeepAlive = fmt.Sprintf("%ds", or.TCPKeepAlive)
	}
	if or.KeepAliveConnections > 0 {
		cfg.KeepAliveConnections = int(or.KeepAliveConnections)
	}
	if or.KeepAliveTimeout > 0 {
		cfg.KeepAliveTimeout = fmt.Sprintf("%ds", or.KeepAliveTimeout)
	}

	// Return nil if all fields are zero-value (nothing meaningful)
	if cfg.ConnectTimeout == "" && cfg.TLSTimeout == "" && cfg.TCPKeepAlive == "" &&
		cfg.KeepAliveConnections == 0 && cfg.KeepAliveTimeout == "" &&
		!cfg.NoTLSVerify && cfg.OriginServerName == "" && cfg.CAPool == "" &&
		cfg.HTTPHostHeader == "" && !cfg.NoHappyEyeballs && !cfg.DisableChunkedEncoding &&
		cfg.ProxyType == "" {
		return nil
	}

	return cfg
}

// parseDurationToSeconds parses a duration string (e.g. "30s", "5m", "30")
// and returns the value in seconds. Bare integers are treated as seconds.
func parseDurationToSeconds(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// Try as Go duration first ("30s", "5m")
	if d, err := time.ParseDuration(s); err == nil {
		return int64(d.Seconds())
	}
	// Try as bare integer (treat as seconds)
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v
	}
	return 0
}

// ListTunnels lists all tunnels for the account.
func (t *TunnelClient) ListTunnels(ctx context.Context) ([]*types.Tunnel, error) {
	if t.accountID == "" {
		return nil, fmt.Errorf("account ID is required for tunnel operations")
	}

	result, err := t.client.API().ZeroTrust.Tunnels.Cloudflared.List(ctx, zero_trust.TunnelCloudflaredListParams{
		AccountID: cf.F(t.accountID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list tunnels: %w", err)
	}

	tunnels := make([]*types.Tunnel, 0, len(result.Result))
	for _, tunnel := range result.Result {
		tunnels = append(tunnels, &types.Tunnel{
			ID:        tunnel.ID,
			AccountID: t.accountID,
			Name:      tunnel.Name,
			Status:    string(tunnel.Status),
		})
	}

	return tunnels, nil
}
