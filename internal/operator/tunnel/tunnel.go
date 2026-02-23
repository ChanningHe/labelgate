// Package tunnel provides Tunnel operator implementation.
package tunnel

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/operator"
	"github.com/channinghe/labelgate/internal/storage"
	"github.com/channinghe/labelgate/internal/types"
)

// ensure TunnelOperatorImpl implements TunnelOperator
var _ operator.TunnelOperator = (*TunnelOperatorImpl)(nil)

// TunnelOperatorImpl implements the Tunnel operator.
type TunnelOperatorImpl struct {
	credManager   *cloudflare.CredentialManager
	storage       storage.Storage
	autoCreateDNS bool // automatically create CNAME records for tunnel hostnames
}

// NewTunnelOperator creates a new Tunnel operator.
func NewTunnelOperator(credManager *cloudflare.CredentialManager, store storage.Storage) *TunnelOperatorImpl {
	return &TunnelOperatorImpl{
		credManager:   credManager,
		storage:       store,
		autoCreateDNS: true, // enabled by default
	}
}

// SetAutoCreateDNS enables/disables automatic DNS record creation.
func (o *TunnelOperatorImpl) SetAutoCreateDNS(enabled bool) {
	o.autoCreateDNS = enabled
}

// Name returns the operator name.
func (o *TunnelOperatorImpl) Name() string {
	return "tunnel"
}

// Reconcile ensures Tunnel ingress rules match the desired state.
func (o *TunnelOperatorImpl) Reconcile(ctx context.Context, desired []*types.ParsedContainer) error {
	// Group services by tunnel
	tunnelServices := make(map[string][]*desiredTunnel)

	for _, container := range desired {
		for _, svc := range container.TunnelServices {
			tunnelName := svc.Tunnel
			if tunnelName == "" {
				tunnelName = "default"
			}

			tunnelServices[tunnelName] = append(tunnelServices[tunnelName], &desiredTunnel{
				container: container,
				service:   svc,
			})
		}
	}

	// Get all active, error, and orphaned tunnel resources from storage.
	// Orphaned resources are included so they can be reactivated when containers restart.
	resources, err := o.storage.ListResources(ctx, storage.ResourceFilter{
		ResourceType: storage.ResourceTypeTunnelIngress,
		Statuses:     []storage.ResourceStatus{storage.StatusActive, storage.StatusError, storage.StatusOrphaned},
	})
	if err != nil {
		return fmt.Errorf("failed to list tunnel resources: %w", err)
	}

	// Build current state map by tunnel
	currentByTunnel := make(map[string]map[string]*storage.ManagedResource)
	for _, r := range resources {
		tunnelID := r.TunnelID
		if currentByTunnel[tunnelID] == nil {
			currentByTunnel[tunnelID] = make(map[string]*storage.ManagedResource)
		}
		key := r.Hostname + ":" + r.Path
		currentByTunnel[tunnelID][key] = r
	}

	// Reconcile each tunnel
	for tunnelName, services := range tunnelServices {
		if err := o.reconcileTunnel(ctx, tunnelName, services, currentByTunnel); err != nil {
			log.Error().Err(err).
				Str("tunnel", tunnelName).
				Msg("Failed to reconcile tunnel")
		}
	}

	// Handle orphaned resources in tunnels that have no desired services
	for _, tunnelResources := range currentByTunnel {
		for _, resource := range tunnelResources {
			if resource.Status != storage.StatusOrphaned {
				if err := o.storage.UpdateResourceStatus(ctx, resource.ID, storage.StatusOrphaned); err != nil {
					log.Error().Err(err).
						Str("hostname", resource.Hostname).
						Msg("Failed to mark resource as orphaned")
				} else {
					log.Info().
						Str("hostname", resource.Hostname).
						Str("service_name", resource.ServiceName).
						Str("container", resource.ContainerName).
						Bool("cleanup_enabled", resource.CleanupEnabled).
						Msg("Tunnel ingress orphaned, no longer referenced by running containers")
				}
			}
		}
	}

	return nil
}

// reconcileTunnel reconciles a single tunnel's ingress rules.
func (o *TunnelOperatorImpl) reconcileTunnel(ctx context.Context, tunnelName string, desired []*desiredTunnel, currentByTunnel map[string]map[string]*storage.ManagedResource) error {
	// Get tunnel client
	client, tunnelCred, err := o.credManager.GetTunnelClient(tunnelName)
	if err != nil {
		return err
	}

	if tunnelCred == nil {
		return fmt.Errorf("tunnel credential not found: %s", tunnelName)
	}

	tunnelID := tunnelCred.TunnelID
	current := currentByTunnel[tunnelID]
	if current == nil {
		current = make(map[string]*storage.ManagedResource)
	}

	// Build desired ingress rules
	var ingresses []*types.TunnelIngress
	desiredMap := make(map[string]*desiredTunnel)

	for _, d := range desired {
		key := d.service.Hostname + ":" + d.service.Path
		if _, exists := desiredMap[key]; exists {
			log.Warn().
				Str("hostname", d.service.Hostname).
				Str("container", d.container.Info.Name).
				Msg("Duplicate tunnel hostname, first container wins")
			continue
		}
		desiredMap[key] = d

		ingresses = append(ingresses, &types.TunnelIngress{
			Hostname:      d.service.Hostname,
			Path:          d.service.Path,
			Service:       d.service.Service,
			OriginRequest: d.service.OriginRequest,
		})

		log.Debug().
			Str("hostname", d.service.Hostname).
			Str("service", d.service.Service).
			Str("container", d.container.Info.Name).
			Str("tunnel", tunnelName).
			Msg("Adding tunnel ingress rule")
	}

	// Check if tunnel configuration actually changed before pushing
	tunnelClient := cloudflare.NewTunnelClient(client, tunnelCred.AccountID)
	configChanged := true
	currentConfig, getErr := tunnelClient.GetTunnelConfiguration(ctx, tunnelID)
	if getErr == nil {
		configChanged = !ingressConfigEqual(currentConfig, ingresses)
	}

	if !configChanged {
		log.Debug().
			Str("tunnel_id", tunnelID).
			Int("ingress_count", len(ingresses)).
			Msg("Tunnel configuration unchanged, skipping update")
	} else if err := tunnelClient.UpdateTunnelConfiguration(ctx, tunnelID, ingresses); err != nil {
		// Mark all desired services as error since tunnel config push failed
		for key, d := range desiredMap {
			existing, exists := current[key]
			if exists {
				// Update existing resource to error state
				if updateErr := o.storage.UpdateResourceError(ctx, existing.ID, storage.StatusError, err.Error()); updateErr != nil {
					log.Error().Err(updateErr).Str("hostname", d.service.Hostname).Msg("Failed to update resource error status")
				}
				delete(current, key)
			} else {
			// Save new resource in error state
			errResource := &storage.ManagedResource{
				ResourceType:   storage.ResourceTypeTunnelIngress,
				TunnelID:       tunnelID,
				Hostname:       d.service.Hostname,
				Service:        d.service.Service,
				Path:           d.service.Path,
				ContainerID:    d.container.Info.ID,
				ContainerName:  d.container.Info.Name,
				ServiceName:    d.service.ServiceName,
				AgentID:        d.container.AgentID,
				Status:         storage.StatusError,
				LastError:      err.Error(),
				CleanupEnabled: d.service.Cleanup,
			}
				if saveErr := o.storage.SaveResource(ctx, errResource); saveErr != nil {
					log.Error().Err(saveErr).Str("hostname", d.service.Hostname).Msg("Failed to save error resource")
				}
			}
		}
		return err
	}

	// Auto-create DNS CNAME records for tunnel hostnames
	// Cloudflare API does not auto-create DNS records (unlike Dashboard UI)
	if o.autoCreateDNS {
		o.ensureTunnelDNSRecords(ctx, tunnelID, desiredMap)
	}

	// Update storage for new/updated resources
	for key, d := range desiredMap {
		existing, exists := current[key]
		if exists {
			// Update existing — clear any previous error
			existing.Service = d.service.Service
			existing.CleanupEnabled = d.service.Cleanup
			existing.AgentID = d.container.AgentID
			existing.Status = storage.StatusActive
			existing.LastError = ""
			if err := o.storage.SaveResource(ctx, existing); err != nil {
				log.Error().Err(err).Str("hostname", d.service.Hostname).Msg("Failed to update resource")
			}
			delete(current, key)

			log.Debug().
				Str("hostname", d.service.Hostname).
				Str("container", d.container.Info.Name).
				Msg("Updated tunnel ingress")
		} else {
		// Create new resource record
		resource := &storage.ManagedResource{
				ResourceType:   storage.ResourceTypeTunnelIngress,
				TunnelID:       tunnelID,
				Hostname:       d.service.Hostname,
				Service:        d.service.Service,
				Path:           d.service.Path,
				ContainerID:    d.container.Info.ID,
				ContainerName:  d.container.Info.Name,
				ServiceName:    d.service.ServiceName,
				AgentID:        d.container.AgentID,
				Status:         storage.StatusActive,
				CleanupEnabled: d.service.Cleanup,
			}
			if err := o.storage.SaveResource(ctx, resource); err != nil {
				log.Error().Err(err).Str("hostname", d.service.Hostname).Msg("Failed to save resource")
			}

			log.Info().
				Str("hostname", d.service.Hostname).
				Str("service", d.service.Service).
				Str("container", d.container.Info.Name).
				Str("tunnel", tunnelName).
				Msg("Created tunnel ingress")
		}
	}

	// Mark remaining current resources as orphaned
	for _, resource := range current {
		if resource.Status != storage.StatusOrphaned {
			if err := o.storage.UpdateResourceStatus(ctx, resource.ID, storage.StatusOrphaned); err != nil {
				log.Error().Err(err).Str("hostname", resource.Hostname).Msg("Failed to mark as orphaned")
			} else {
				log.Info().
					Str("hostname", resource.Hostname).
					Str("service_name", resource.ServiceName).
					Str("container", resource.ContainerName).
					Bool("cleanup_enabled", resource.CleanupEnabled).
					Msg("Tunnel ingress orphaned, no longer referenced by running containers")
			}
		}
	}

	// Remove processed tunnel from map
	delete(currentByTunnel, tunnelID)

	return nil
}

// ensureTunnelDNSRecords creates CNAME records for tunnel hostnames.
// Cloudflare API does not auto-create DNS records when adding tunnel ingress rules
// (unlike the Dashboard UI), so we need to create them manually.
func (o *TunnelOperatorImpl) ensureTunnelDNSRecords(ctx context.Context, tunnelID string, desired map[string]*desiredTunnel) {
	// Target for tunnel CNAME: <tunnel_id>.cfargotunnel.com
	tunnelTarget := tunnelID + ".cfargotunnel.com"

	for _, d := range desired {
		hostname := d.service.Hostname
		if hostname == "" {
			continue
		}

		// Get DNS client for this hostname's zone
		dnsClient, err := o.getDNSClientForHostname(hostname)
		if err != nil {
			log.Warn().
				Err(err).
				Str("hostname", hostname).
				Msg("Cannot create DNS client for tunnel hostname")
			continue
		}

		// Check if CNAME record already exists
		existingRecord, err := dnsClient.GetRecordByName(ctx, hostname, types.DNSTypeCNAME)
		if err == nil && existingRecord != nil {
			// Record exists, check if it's already pointing to tunnel
			if existingRecord.Content == tunnelTarget {
				log.Debug().
					Str("hostname", hostname).
					Msg("DNS CNAME already exists for tunnel")
				continue
			}

			// Record exists but points elsewhere
			log.Warn().
				Str("hostname", hostname).
				Str("existing_type", string(existingRecord.Type)).
				Str("existing_content", existingRecord.Content).
				Str("expected_content", tunnelTarget).
				Msg("DNS record exists but doesn't point to tunnel, updating")

			// Update the record to point to tunnel
			existingRecord.Type = types.DNSTypeCNAME
			existingRecord.Content = tunnelTarget
			existingRecord.Proxied = true
			if _, err := dnsClient.UpdateRecord(ctx, existingRecord); err != nil {
				log.Error().
					Err(err).
					Str("hostname", hostname).
					Msg("Failed to update DNS record to point to tunnel")
			} else {
				log.Info().
					Str("hostname", hostname).
					Str("target", tunnelTarget).
					Msg("Updated DNS CNAME for tunnel")
			}
			continue
		}

		// Create new CNAME record
		record := &types.DNSRecord{
			Type:    types.DNSTypeCNAME,
			Name:    hostname,
			Content: tunnelTarget,
			Proxied: true,
			TTL:     1, // Auto TTL
		}

		createdRecord, err := dnsClient.CreateRecord(ctx, record)
		if err != nil {
			// Check if error is because record already exists
			if strings.Contains(err.Error(), "already exists") {
				log.Debug().
					Str("hostname", hostname).
					Msg("DNS record already exists")
				continue
			}
			log.Error().
				Err(err).
				Str("hostname", hostname).
				Msg("Failed to create DNS CNAME for tunnel")
			continue
		}

		log.Info().
			Str("hostname", hostname).
			Str("target", tunnelTarget).
			Str("record_id", createdRecord.ID).
			Msg("Created DNS CNAME for tunnel")
	}
}

// getDNSClientForHostname returns a DNS client for the hostname's zone.
func (o *TunnelOperatorImpl) getDNSClientForHostname(hostname string) (*cloudflare.DNSClient, error) {
	// Get client for the hostname (uses credential matching for the zone)
	client, err := o.credManager.GetClientForHostname(hostname, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get client for hostname: %w", err)
	}

	return cloudflare.NewDNSClient(client), nil
}

// Create creates a resource (generic interface).
func (o *TunnelOperatorImpl) Create(ctx context.Context, resource *storage.ManagedResource) error {
	svc := &types.TunnelService{
		ServiceName: resource.ServiceName,
		Hostname:    resource.Hostname,
		Service:     resource.Service,
		Path:        resource.Path,
	}
	_, err := o.AddIngressRule(ctx, &types.ContainerInfo{
		ID:   resource.ContainerID,
		Name: resource.ContainerName,
	}, svc)
	return err
}

// Update updates a resource (generic interface).
func (o *TunnelOperatorImpl) Update(ctx context.Context, resource *storage.ManagedResource) error {
	svc := &types.TunnelService{
		ServiceName: resource.ServiceName,
		Hostname:    resource.Hostname,
		Service:     resource.Service,
		Path:        resource.Path,
	}
	return o.UpdateIngressRule(ctx, resource, svc)
}

// Delete deletes a resource (generic interface).
func (o *TunnelOperatorImpl) Delete(ctx context.Context, resource *storage.ManagedResource) error {
	return o.RemoveIngressRule(ctx, resource)
}

// AddIngressRule adds an ingress rule to the tunnel.
func (o *TunnelOperatorImpl) AddIngressRule(ctx context.Context, container *types.ContainerInfo, service *types.TunnelService) (*storage.ManagedResource, error) {
	tunnelName := service.Tunnel
	if tunnelName == "" {
		tunnelName = "default"
	}

	client, tunnelCred, err := o.credManager.GetTunnelClient(tunnelName)
	if err != nil {
		return nil, err
	}

	if tunnelCred == nil {
		return nil, fmt.Errorf("tunnel credential not found: %s", tunnelName)
	}

	tunnelClient := cloudflare.NewTunnelClient(client, tunnelCred.AccountID)

	ingress := &types.TunnelIngress{
		Hostname:      service.Hostname,
		Path:          service.Path,
		Service:       service.Service,
		OriginRequest: service.OriginRequest,
	}

	if err := tunnelClient.AddIngressRule(ctx, tunnelCred.TunnelID, ingress); err != nil {
		return nil, err
	}

	// Save to storage
	resource := &storage.ManagedResource{
		ResourceType:   storage.ResourceTypeTunnelIngress,
		TunnelID:       tunnelCred.TunnelID,
		Hostname:       service.Hostname,
		Service:        service.Service,
		Path:           service.Path,
		ContainerID:    container.ID,
		ContainerName:  container.Name,
		ServiceName:    service.ServiceName,
		Status:         storage.StatusActive,
		CleanupEnabled: service.Cleanup,
	}

	if err := o.storage.SaveResource(ctx, resource); err != nil {
		// Try to rollback
		_ = tunnelClient.RemoveIngressRule(ctx, tunnelCred.TunnelID, service.Hostname)
		return nil, fmt.Errorf("failed to save resource: %w", err)
	}

	log.Info().
		Str("hostname", service.Hostname).
		Str("service", service.Service).
		Str("tunnel", tunnelName).
		Str("container", container.Name).
		Msg("Added tunnel ingress rule")

	return resource, nil
}

// UpdateIngressRule updates an ingress rule.
func (o *TunnelOperatorImpl) UpdateIngressRule(ctx context.Context, resource *storage.ManagedResource, service *types.TunnelService) error {
	tunnelName := service.Tunnel
	if tunnelName == "" {
		tunnelName = "default"
	}

	client, tunnelCred, err := o.credManager.GetTunnelClient(tunnelName)
	if err != nil {
		return err
	}

	if tunnelCred == nil {
		return fmt.Errorf("tunnel credential not found: %s", tunnelName)
	}

	tunnelClient := cloudflare.NewTunnelClient(client, tunnelCred.AccountID)

	ingress := &types.TunnelIngress{
		Hostname:      service.Hostname,
		Path:          service.Path,
		Service:       service.Service,
		OriginRequest: service.OriginRequest,
	}

	if err := tunnelClient.AddIngressRule(ctx, tunnelCred.TunnelID, ingress); err != nil {
		return err
	}

	// Update storage
	resource.Service = service.Service
	resource.CleanupEnabled = service.Cleanup

	return o.storage.SaveResource(ctx, resource)
}

// RemoveIngressRule removes an ingress rule.
func (o *TunnelOperatorImpl) RemoveIngressRule(ctx context.Context, resource *storage.ManagedResource) error {
	// Find the tunnel that owns this resource
	var tunnelName string
	for name, cred := range o.listTunnelCredentials() {
		if cred.TunnelID == resource.TunnelID {
			tunnelName = name
			break
		}
	}

	if tunnelName == "" {
		return fmt.Errorf("tunnel not found for resource: %s", resource.TunnelID)
	}

	client, tunnelCred, err := o.credManager.GetTunnelClient(tunnelName)
	if err != nil {
		return err
	}

	if tunnelCred == nil {
		return fmt.Errorf("tunnel credential not found: %s", tunnelName)
	}

	tunnelClient := cloudflare.NewTunnelClient(client, tunnelCred.AccountID)

	if err := tunnelClient.RemoveIngressRule(ctx, tunnelCred.TunnelID, resource.Hostname); err != nil {
		return err
	}

	// Hard-delete from storage (no more soft-delete)
	return o.storage.DeleteResource(ctx, resource.ID)
}

// listTunnelCredentials returns all tunnel credentials.
func (o *TunnelOperatorImpl) listTunnelCredentials() map[string]*cloudflare.TunnelCredential {
	result := make(map[string]*cloudflare.TunnelCredential)
	for _, name := range o.credManager.ListTunnels() {
		cred, err := o.credManager.GetTunnelCredential(name)
		if err == nil {
			result[name] = cred
		}
	}
	return result
}

// ingressConfigEqual compares the current tunnel configuration from Cloudflare
// with the desired ingress rules. Returns true if they are equivalent.
func ingressConfigEqual(current *types.TunnelConfiguration, desired []*types.TunnelIngress) bool {
	if current == nil {
		return len(desired) == 0
	}

	// Filter out catch-all rules from current config for comparison
	var currentRules []types.IngressRule
	for _, rule := range current.Ingress {
		if rule.Hostname == "" {
			continue // skip catch-all
		}
		currentRules = append(currentRules, rule)
	}

	if len(currentRules) != len(desired) {
		return false
	}

	// Build a map from current rules for order-independent comparison
	currentMap := make(map[string]types.IngressRule, len(currentRules))
	for _, rule := range currentRules {
		key := rule.Hostname + ":" + rule.Path
		currentMap[key] = rule
	}

	for _, d := range desired {
		key := d.Hostname + ":" + d.Path
		c, exists := currentMap[key]
		if !exists {
			return false
		}
		if c.Service != d.Service {
			return false
		}
		if !originRequestEqual(c.OriginRequest, d.OriginRequest) {
			return false
		}
	}

	return true
}

// originRequestEqual compares two OriginRequestConfig values.
func originRequestEqual(a, b *types.OriginRequestConfig) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.ConnectTimeout == b.ConnectTimeout &&
		a.TLSTimeout == b.TLSTimeout &&
		a.TCPKeepAlive == b.TCPKeepAlive &&
		a.KeepAliveConnections == b.KeepAliveConnections &&
		a.KeepAliveTimeout == b.KeepAliveTimeout &&
		a.NoTLSVerify == b.NoTLSVerify &&
		a.OriginServerName == b.OriginServerName &&
		a.CAPool == b.CAPool &&
		a.HTTPHostHeader == b.HTTPHostHeader &&
		a.NoHappyEyeballs == b.NoHappyEyeballs &&
		a.DisableChunkedEncoding == b.DisableChunkedEncoding &&
		a.ProxyType == b.ProxyType
}

// desiredTunnel holds desired tunnel state.
type desiredTunnel struct {
	container *types.ParsedContainer
	service   *types.TunnelService
}
