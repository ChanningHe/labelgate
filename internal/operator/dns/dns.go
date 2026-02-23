// Package dns provides DNS operator implementation.
package dns

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/operator"
	"github.com/channinghe/labelgate/internal/storage"
	"github.com/channinghe/labelgate/internal/types"
)

// ensure DNSOperatorImpl implements DNSOperator
var _ operator.DNSOperator = (*DNSOperatorImpl)(nil)

// DNSOperatorImpl implements the DNS operator.
type DNSOperatorImpl struct {
	credManager *cloudflare.CredentialManager
	storage     storage.Storage
}

// NewDNSOperator creates a new DNS operator.
func NewDNSOperator(credManager *cloudflare.CredentialManager, store storage.Storage) *DNSOperatorImpl {
	return &DNSOperatorImpl{
		credManager: credManager,
		storage:     store,
	}
}

// Name returns the operator name.
func (o *DNSOperatorImpl) Name() string {
	return "dns"
}

// Reconcile ensures DNS records match the desired state.
func (o *DNSOperatorImpl) Reconcile(ctx context.Context, desired []*types.ParsedContainer) error {
	// Build desired state map: hostname -> service config
	desiredMap := make(map[string]*desiredDNS)
	for _, container := range desired {
		for _, svc := range container.DNSServices {
			key := svc.Hostname + ":" + string(svc.Type)
			if existing, ok := desiredMap[key]; ok {
				// Conflict - first container wins
				log.Warn().
					Str("hostname", svc.Hostname).
					Str("existing_container", existing.container.Info.Name).
					Str("new_container", container.Info.Name).
					Msg("Hostname conflict, first container wins")
				continue
			}
			desiredMap[key] = &desiredDNS{
				container: container,
				service:   svc,
			}
		}
	}

	// Get all active, error, and orphaned DNS resources from storage.
	// Orphaned resources are included so they can be reactivated when containers restart.
	resources, err := o.storage.ListResources(ctx, storage.ResourceFilter{
		ResourceType: storage.ResourceTypeDNS,
		Statuses:     []storage.ResourceStatus{storage.StatusActive, storage.StatusError, storage.StatusOrphaned},
	})
	if err != nil {
		return fmt.Errorf("failed to list DNS resources: %w", err)
	}

	// Build current state map
	currentMap := make(map[string]*storage.ManagedResource)
	for _, r := range resources {
		key := r.Hostname + ":" + r.RecordType
		currentMap[key] = r
	}

	// Reconcile: create, update, or delete
	for key, desired := range desiredMap {
		current, exists := currentMap[key]
		if !exists {
		// Create new record
		if resource, err := o.CreateDNSRecord(ctx, desired.container.Info, desired.service); err != nil {
				log.Error().Err(err).
					Str("hostname", desired.service.Hostname).
					Msg("Failed to create DNS record")
			// Save resource in error state so Dashboard can see the failure
			errResource := &storage.ManagedResource{
				ResourceType:   storage.ResourceTypeDNS,
				Hostname:       desired.service.Hostname,
				RecordType:     string(desired.service.Type),
				Content:        desired.service.Target,
				Proxied:        desired.service.Proxied,
				TTL:            desired.service.TTL,
				ContainerID:    desired.container.Info.ID,
				ContainerName:  desired.container.Info.Name,
				ServiceName:    desired.service.ServiceName,
				AgentID:        desired.container.AgentID,
				Status:         storage.StatusError,
				LastError:      err.Error(),
				CleanupEnabled: desired.service.Cleanup,
			}
				if saveErr := o.storage.SaveResource(ctx, errResource); saveErr != nil {
					log.Error().Err(saveErr).Str("hostname", desired.service.Hostname).Msg("Failed to save error resource")
				}
			} else if desired.container.AgentID != "" {
				// CreateDNSRecord doesn't know about AgentID; patch it after creation
				resource.AgentID = desired.container.AgentID
				_ = o.storage.SaveResource(ctx, resource)
			}
		} else {
			// Update AgentID if it changed (e.g. resource was local, now from agent)
			if current.AgentID != desired.container.AgentID {
				current.AgentID = desired.container.AgentID
				_ = o.storage.SaveResource(ctx, current)
			}
			// Check if update needed (also retry errors, reactivate orphaned)
			if current.Status == storage.StatusError || current.Status == storage.StatusOrphaned || needsUpdate(current, desired.service) {
				if err := o.UpdateDNSRecord(ctx, current, desired.service); err != nil {
					log.Error().Err(err).
						Str("hostname", desired.service.Hostname).
						Msg("Failed to update DNS record")
					// Mark resource as error
					if updateErr := o.storage.UpdateResourceError(ctx, current.ID, storage.StatusError, err.Error()); updateErr != nil {
						log.Error().Err(updateErr).Str("hostname", desired.service.Hostname).Msg("Failed to update resource error status")
					}
				} else {
					// Success: clear any previous error, reactivate if orphaned
					if current.LastError != "" || current.Status != storage.StatusActive {
						_ = o.storage.UpdateResourceError(ctx, current.ID, storage.StatusActive, "")
					}
				}
			}
			delete(currentMap, key)
		}
	}

	// Handle orphaned resources (in currentMap but not in desiredMap).
	// All orphans get the same status; cleanup_enabled determines whether
	// the reconciler will eventually delete the CF resource or just the DB record.
	for _, resource := range currentMap {
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
					Msg("DNS record orphaned, no longer referenced by running containers")
			}
		}
	}

	return nil
}

// Create creates a resource (generic interface).
func (o *DNSOperatorImpl) Create(ctx context.Context, resource *storage.ManagedResource) error {
	// This is called from generic reconciler, convert to DNS-specific
	svc := &types.DNSService{
		ServiceName: resource.ServiceName,
		Hostname:    resource.Hostname,
		Type:        types.DNSRecordType(resource.RecordType),
		Target:      resource.Content,
	}
	_, err := o.CreateDNSRecord(ctx, &types.ContainerInfo{
		ID:   resource.ContainerID,
		Name: resource.ContainerName,
	}, svc)
	return err
}

// Update updates a resource (generic interface).
func (o *DNSOperatorImpl) Update(ctx context.Context, resource *storage.ManagedResource) error {
	svc := &types.DNSService{
		ServiceName: resource.ServiceName,
		Hostname:    resource.Hostname,
		Type:        types.DNSRecordType(resource.RecordType),
		Target:      resource.Content,
	}
	return o.UpdateDNSRecord(ctx, resource, svc)
}

// Delete deletes a resource (generic interface).
func (o *DNSOperatorImpl) Delete(ctx context.Context, resource *storage.ManagedResource) error {
	return o.DeleteDNSRecord(ctx, resource)
}

// CreateDNSRecord creates a DNS record from service configuration.
func (o *DNSOperatorImpl) CreateDNSRecord(ctx context.Context, container *types.ContainerInfo, service *types.DNSService) (*storage.ManagedResource, error) {
	// Get appropriate client
	client, err := o.credManager.GetClientForHostname(service.Hostname, service.Credential)
	if err != nil {
		return nil, fmt.Errorf("failed to get client: %w", err)
	}

	dnsClient := cloudflare.NewDNSClient(client)

	// Resolve target IP if needed
	target, err := o.resolveTarget(service.Target, container)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve target: %w", err)
	}

	// Get zone ID
	zoneID, err := client.GetZoneID(ctx, service.Hostname)
	if err != nil {
		return nil, err
	}

	// Create DNS record
	record := &types.DNSRecord{
		ZoneID:   zoneID,
		Name:     service.Hostname,
		Type:     service.Type,
		Content:  target,
		Proxied:  service.Proxied,
		TTL:      service.TTL,
		Priority: service.Priority,
		Comment:  fmt.Sprintf("Managed by labelgate [container:%s service:%s]", container.Name, service.ServiceName),
	}

	created, err := dnsClient.CreateRecord(ctx, record)
	if err != nil {
		// If record already exists in Cloudflare (e.g. manually created, or DB lost
		// the CFID), adopt the existing record instead of failing permanently.
		if isAlreadyExistsError(err) {
			existing, lookupErr := dnsClient.GetRecordByName(ctx, service.Hostname, service.Type)
			if lookupErr == nil && existing != nil {
				log.Info().
					Str("hostname", service.Hostname).
					Str("cf_id", existing.ID).
					Msg("DNS record already exists in Cloudflare, adopting it")
				created = existing
				err = nil
			}
		}
		if err != nil {
			return nil, err
		}
	}

	// Save to storage
	resource := &storage.ManagedResource{
		ResourceType:   storage.ResourceTypeDNS,
		CFID:           created.ID,
		ZoneID:         zoneID,
		Hostname:       service.Hostname,
		RecordType:     string(service.Type),
		Content:        target,
		Proxied:        service.Proxied,
		TTL:            service.TTL,
		ContainerID:    container.ID,
		ContainerName:  container.Name,
		ServiceName:    service.ServiceName,
		Status:         storage.StatusActive,
		CleanupEnabled: service.Cleanup,
	}

	if err := o.storage.SaveResource(ctx, resource); err != nil {
		// Try to rollback CF record
		_ = dnsClient.DeleteRecord(ctx, zoneID, created.ID)
		return nil, fmt.Errorf("failed to save resource: %w", err)
	}

	log.Info().
		Str("hostname", service.Hostname).
		Str("type", string(service.Type)).
		Str("content", target).
		Str("container", container.Name).
		Msg("Created DNS record")

	return resource, nil
}

// UpdateDNSRecord updates a DNS record.
func (o *DNSOperatorImpl) UpdateDNSRecord(ctx context.Context, resource *storage.ManagedResource, service *types.DNSService) error {
	client, err := o.credManager.GetClientForHostname(service.Hostname, service.Credential)
	if err != nil {
		return err
	}

	dnsClient := cloudflare.NewDNSClient(client)

	// Recover missing CFID/ZoneID by looking up the record in Cloudflare.
	// This breaks the "already exists -> record ID required" dead loop.
	if resource.CFID == "" || resource.ZoneID == "" {
		existing, lookupErr := dnsClient.GetRecordByName(ctx, service.Hostname, service.Type)
		if lookupErr != nil || existing == nil {
			return fmt.Errorf("cannot update: record not found in Cloudflare and no CFID in storage for %s", service.Hostname)
		}
		resource.CFID = existing.ID
		resource.ZoneID = existing.ZoneID
		log.Info().
			Str("hostname", service.Hostname).
			Str("cf_id", existing.ID).
			Msg("Recovered missing CFID from Cloudflare")
	}

	// Resolve new target if needed
	target := service.Target
	if target == "auto" || target == "" {
		target, err = getPublicIP()
		if err != nil {
			return err
		}
	}

	record := &types.DNSRecord{
		ID:       resource.CFID,
		ZoneID:   resource.ZoneID,
		Name:     service.Hostname,
		Type:     service.Type,
		Content:  target,
		Proxied:  service.Proxied,
		TTL:      service.TTL,
		Priority: service.Priority,
	}

	if _, err := dnsClient.UpdateRecord(ctx, record); err != nil {
		return err
	}

	// Update storage (including service_name which may have changed)
	resource.Content = target
	resource.Proxied = service.Proxied
	resource.TTL = service.TTL
	resource.ServiceName = service.ServiceName
	resource.CleanupEnabled = service.Cleanup

	return o.storage.SaveResource(ctx, resource)
}

// DeleteDNSRecord deletes a DNS record.
func (o *DNSOperatorImpl) DeleteDNSRecord(ctx context.Context, resource *storage.ManagedResource) error {
	client, err := o.credManager.GetClientForHostname(resource.Hostname, "")
	if err != nil {
		return err
	}

	dnsClient := cloudflare.NewDNSClient(client)

	if err := dnsClient.DeleteRecord(ctx, resource.ZoneID, resource.CFID); err != nil {
		// If the record no longer exists on Cloudflare (404), treat as success
		// and proceed with DB cleanup.
		if !isNotFoundError(err) {
			return err
		}
		log.Warn().
			Str("hostname", resource.Hostname).
			Str("cf_id", resource.CFID).
			Msg("DNS record already gone from Cloudflare, cleaning up DB record")
	}

	// Hard-delete from storage (no more soft-delete)
	return o.storage.DeleteResource(ctx, resource.ID)
}

// resolveTarget resolves the target IP address.
func (o *DNSOperatorImpl) resolveTarget(target string, container *types.ContainerInfo) (string, error) {
	switch target {
	case "auto", "":
		return getPublicIP()
	case "container":
		// Get first network IP
		for _, ip := range container.Networks {
			return ip, nil
		}
		return "", fmt.Errorf("container has no network IP")
	default:
		// Validate IP address
		if net.ParseIP(target) != nil {
			return target, nil
		}
		// Might be a hostname for CNAME
		return target, nil
	}
}

// getPublicIP retrieves the public IP address.
func getPublicIP() (string, error) {
	services := []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for _, svc := range services {
		resp, err := client.Get(svc)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			buf := make([]byte, 64)
			n, _ := resp.Body.Read(buf)
			ip := string(buf[:n])
			ip = ip[:len(ip)-1] // Remove trailing newline if any
			if net.ParseIP(ip) != nil {
				return ip, nil
			}
		}
	}

	return "", fmt.Errorf("failed to get public IP")
}

// needsUpdate checks if a DNS record needs updating.
func needsUpdate(current *storage.ManagedResource, desired *types.DNSService) bool {
	// Check if target changed
	if desired.Target != "auto" && desired.Target != current.Content {
		return true
	}

	// Check if proxied changed
	if desired.Proxied != current.Proxied {
		return true
	}

	// Check if TTL changed
	if desired.TTL != current.TTL && desired.TTL != 0 {
		return true
	}

	// Check if service_name changed (need to update storage metadata)
	if desired.ServiceName != current.ServiceName {
		return true
	}

	return false
}

// isAlreadyExistsError checks whether a Cloudflare API error indicates the record
// already exists (error code 81058).
func isAlreadyExistsError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}

// isNotFoundError checks whether a Cloudflare API error indicates the record
// does not exist (404 / code 81044).
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "Record does not exist") || strings.Contains(s, "404")
}

// desiredDNS holds desired DNS state.
type desiredDNS struct {
	container *types.ParsedContainer
	service   *types.DNSService
}
