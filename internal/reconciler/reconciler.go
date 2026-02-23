// Package reconciler provides state reconciliation for labelgate.
package reconciler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/operator"
	"github.com/channinghe/labelgate/internal/provider"
	"github.com/channinghe/labelgate/internal/storage"
	"github.com/channinghe/labelgate/internal/types"
	"github.com/channinghe/labelgate/pkg/labels"
)

// Reconciler manages the reconciliation of desired vs actual state.
type Reconciler struct {
	provider   provider.Provider
	storage    storage.Storage
	parser     *labels.Parser
	dnsOp       operator.DNSOperator
	tunnelOp    operator.TunnelOperator
	accessOp    operator.AccessOperator
	interval    time.Duration
	orphanTTL   time.Duration // 0 = never auto-clean orphans from DB
	removeDelay time.Duration // delay before cleaning up orphaned CF resources
	mu          sync.RWMutex
	containers  map[string]*types.ParsedContainer   // containerID -> parsed container
	agentData   map[string][]*types.ParsedContainer // agentID -> containers

	// Channel to trigger reconciliation when agent data changes
	agentTrigger chan struct{}

	// Sync state exposed for the API layer
	startedAt     time.Time
	lastSyncTime  time.Time
	lastSyncError error
	syncMu        sync.RWMutex
}

// Config holds reconciler configuration.
type Config struct {
	Provider     provider.Provider
	Storage      storage.Storage
	LabelPrefix  string
	DNSOperator  operator.DNSOperator
	TunnelOp     operator.TunnelOperator
	AccessOp     operator.AccessOperator
	PollInterval time.Duration
	OrphanTTL    time.Duration // 0 = never auto-clean orphans from DB
	RemoveDelay  time.Duration // delay before cleaning up orphaned CF resources
}

// NewReconciler creates a new reconciler.
func NewReconciler(cfg *Config) *Reconciler {
	return &Reconciler{
		provider:     cfg.Provider,
		storage:      cfg.Storage,
		parser:       labels.NewParser(cfg.LabelPrefix),
		dnsOp:        cfg.DNSOperator,
		tunnelOp:     cfg.TunnelOp,
		accessOp:     cfg.AccessOp,
		interval:     cfg.PollInterval,
		orphanTTL:    cfg.OrphanTTL,
		removeDelay:  cfg.RemoveDelay,
		containers:   make(map[string]*types.ParsedContainer),
		agentData:    make(map[string][]*types.ParsedContainer),
		agentTrigger: make(chan struct{}, 1),
		startedAt:    time.Now(),
	}
}

// Run starts the reconciliation loop.
func (r *Reconciler) Run(ctx context.Context) error {
	// Initial sync
	if err := r.syncContainers(ctx); err != nil {
		log.Error().Err(err).Msg("Initial container sync failed")
	}

	if err := r.reconcile(ctx); err != nil {
		log.Error().Err(err).Msg("Initial reconciliation failed")
	}

	// Start watching for events
	eventsChan := make(chan *types.ContainerEvent, 100)
	go func() {
		if err := r.provider.Watch(ctx, eventsChan); err != nil {
			if ctx.Err() == nil {
				log.Error().Err(err).Msg("Event watcher error")
			}
		}
	}()

	// Start periodic sync
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	log.Info().
		Dur("interval", r.interval).
		Msg("Started reconciliation loop (event-driven + periodic)")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event := <-eventsChan:
			r.handleEvent(ctx, event)

		case <-r.agentTrigger:
			log.Debug().Msg("Agent data updated, triggering reconciliation")
			if err := r.reconcile(ctx); err != nil {
				log.Error().Err(err).Msg("Reconciliation after agent update failed")
			}

		case <-ticker.C:
			log.Debug().Msg("Periodic sync triggered")
			if err := r.syncContainers(ctx); err != nil {
				log.Error().Err(err).Msg("Periodic container sync failed")
				continue
			}
			if err := r.reconcile(ctx); err != nil {
				log.Error().Err(err).Msg("Periodic reconciliation failed")
			}
		}
	}
}

// handleEvent handles a container event.
func (r *Reconciler) handleEvent(ctx context.Context, event *types.ContainerEvent) {
	log.Debug().
		Str("type", string(event.Type)).
		Str("container", event.ContainerName).
		Str("id", event.ContainerID).
		Msg("Handling container event")

	switch event.Type {
	case types.EventStart:
		// Get full container info
		container, err := r.provider.GetContainer(ctx, event.ContainerID)
		if err != nil {
			log.Error().Err(err).Str("id", event.ContainerID).Msg("Failed to get container info")
			return
		}

		// Parse labels
		parsed := r.parseContainer(container, event.AgentID)
		if parsed == nil {
			return
		}

		r.mu.Lock()
		r.containers[event.ContainerID] = parsed
		r.mu.Unlock()

		// Trigger reconciliation
		if err := r.reconcile(ctx); err != nil {
			log.Error().Err(err).Msg("Reconciliation after container start failed")
		}

	case types.EventStop, types.EventDie:
		r.mu.Lock()
		delete(r.containers, event.ContainerID)
		r.mu.Unlock()

		// Trigger reconciliation
		if err := r.reconcile(ctx); err != nil {
			log.Error().Err(err).Msg("Reconciliation after container stop failed")
		}

	case types.EventDestroy:
		r.mu.Lock()
		delete(r.containers, event.ContainerID)
		r.mu.Unlock()
	}
}

// syncContainers syncs the current container state from the provider.
func (r *Reconciler) syncContainers(ctx context.Context) error {
	containers, err := r.provider.ListContainers(ctx)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Clear and rebuild
	r.containers = make(map[string]*types.ParsedContainer)

	for _, container := range containers {
		parsed := r.parseContainer(container, "")
		if parsed != nil {
			r.containers[container.ID] = parsed
		}
	}

	// Collect container names for logging
	containerNames := make([]string, 0, len(r.containers))
	for _, c := range r.containers {
		containerNames = append(containerNames, c.Info.Name)
	}

	// INFO level: one-line summary of detected containers
	if len(r.containers) > 0 {
		log.Info().
			Msgf("Detected labeled containers: %s", strings.Join(containerNames, ", "))
	}

	// DEBUG level: detailed info for each container
	for _, c := range r.containers {
		dnsHostnames := make([]string, 0, len(c.DNSServices))
		for _, svc := range c.DNSServices {
			dnsHostnames = append(dnsHostnames, svc.Hostname)
		}
		tunnelHostnames := make([]string, 0, len(c.TunnelServices))
		for _, svc := range c.TunnelServices {
			tunnelHostnames = append(tunnelHostnames, svc.Hostname)
		}

		log.Debug().
			Str("container", c.Info.Name).
			Str("id", c.Info.ID[:12]).
			Strs("dns_hostnames", dnsHostnames).
			Strs("tunnel_hostnames", tunnelHostnames).
			Int("dns_services", len(c.DNSServices)).
			Int("tunnel_services", len(c.TunnelServices)).
			Msg("Container details")
	}

	return nil
}

// parseContainer parses container labels into configurations.
func (r *Reconciler) parseContainer(container *types.ContainerInfo, agentID string) *types.ParsedContainer {
	result := r.parser.Parse(container.Labels)

	// Log any parse errors
	for _, err := range result.Errors {
		log.Warn().
			Err(err).
			Str("container", container.Name).
			Msg("Label parsing error")
	}

	// Check hostname conflicts
	if err := r.parser.CheckHostnameConflict(result); err != nil {
		log.Error().
			Err(err).
			Str("container", container.Name).
			Msg("Hostname conflict detected")
		// Still return the parsed result, but one of the services will be ignored
	}

	if len(result.DNSServices) == 0 && len(result.TunnelServices) == 0 && len(result.AccessPolicies) == 0 {
		return nil
	}

	return &types.ParsedContainer{
		Info:           container,
		DNSServices:    result.DNSServices,
		TunnelServices: result.TunnelServices,
		AccessPolicies: result.AccessPolicies,
		AgentID:        agentID,
	}
}

// reconcile performs the actual reconciliation.
func (r *Reconciler) reconcile(ctx context.Context) error {
	r.mu.RLock()
	desired := r.getDesiredState()
	r.mu.RUnlock()

	// Filter out hostname conflicts across all containers
	desired = r.filterHostnameConflicts(desired)

	var errs []error

	// Reconcile DNS
	if r.dnsOp != nil {
		if err := r.dnsOp.Reconcile(ctx, desired); err != nil {
			log.Error().Err(err).Msg("DNS reconciliation failed")
			errs = append(errs, fmt.Errorf("dns: %w", err))
		}
	}

	// Reconcile Tunnel
	if r.tunnelOp != nil {
		if err := r.tunnelOp.Reconcile(ctx, desired); err != nil {
			log.Error().Err(err).Msg("Tunnel reconciliation failed")
			errs = append(errs, fmt.Errorf("tunnel: %w", err))
		}
	}

	// Reconcile Access (after DNS and Tunnel, since access depends on hostnames)
	// Always call ReconcileBindings even with empty bindings so orphan cleanup runs.
	if r.accessOp != nil {
		bindings := r.resolveAccessReferences(desired)
		if err := r.accessOp.ReconcileBindings(ctx, bindings); err != nil {
			log.Error().Err(err).Msg("Access reconciliation failed")
			errs = append(errs, fmt.Errorf("access: %w", err))
		}
	}

	// Process orphaned resources with cleanup_enabled=true whose remove_delay has expired
	r.processOrphanedCleanups(ctx)

	// Clean up orphaned DB records (cleanup_enabled=false) that exceeded the TTL
	if r.orphanTTL > 0 {
		r.cleanupExpiredOrphans(ctx)
	}

	// Update sync state for API layer — errors.Join returns nil when errs is empty
	r.syncMu.Lock()
	r.lastSyncTime = time.Now()
	r.lastSyncError = errors.Join(errs...)
	r.syncMu.Unlock()

	return nil
}

// processOrphanedCleanups deletes CF resources for orphaned entries with
// cleanup_enabled=true whose remove_delay has expired, then hard-deletes from storage.
func (r *Reconciler) processOrphanedCleanups(ctx context.Context) {
	cutoff := time.Now().Add(-r.removeDelay)
	resources, err := r.storage.ListOrphanedForCleanup(ctx, cutoff)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list orphaned resources for cleanup")
		return
	}

	if len(resources) == 0 {
		return
	}

	log.Info().
		Int("count", len(resources)).
		Dur("remove_delay", r.removeDelay).
		Msg("Processing orphaned resources scheduled for cleanup")

	for _, resource := range resources {
		// Delete from Cloudflare first, then hard-delete from DB.
		// operator.Delete() already hard-deletes the DB record on success.
		var deleteErr error
		switch resource.ResourceType {
		case storage.ResourceTypeDNS:
			if r.dnsOp == nil {
				log.Warn().Str("hostname", resource.Hostname).Msg("DNS operator not configured, skipping orphan cleanup")
				continue
			}
			deleteErr = r.dnsOp.Delete(ctx, resource)
		case storage.ResourceTypeTunnelIngress:
			if r.tunnelOp == nil {
				log.Warn().Str("hostname", resource.Hostname).Msg("Tunnel operator not configured, skipping orphan cleanup")
				continue
			}
			deleteErr = r.tunnelOp.Delete(ctx, resource)
		case storage.ResourceTypeAccessApp:
			if r.accessOp == nil {
				log.Warn().Str("hostname", resource.Hostname).Msg("Access operator not configured, skipping orphan cleanup")
				continue
			}
			deleteErr = r.accessOp.Delete(ctx, resource)
		default:
			_ = r.storage.DeleteResource(ctx, resource.ID)
			continue
		}

		if deleteErr != nil {
			log.Error().Err(deleteErr).
				Str("hostname", resource.Hostname).
				Str("resource_type", string(resource.ResourceType)).
				Str("service_name", resource.ServiceName).
				Msg("Failed to clean up orphaned resource from Cloudflare")
		} else {
			log.Info().
				Str("hostname", resource.Hostname).
				Str("resource_type", string(resource.ResourceType)).
				Str("service_name", resource.ServiceName).
				Msg("Cleaned up orphaned resource from Cloudflare and storage")
		}
	}
}

// cleanupExpiredOrphans hard-deletes orphaned DB records that have been orphaned
// longer than the configured orphan TTL. This only touches the database, NOT Cloudflare.
// (Cloudflare resources are preserved for cleanup_enabled=false orphans.)
func (r *Reconciler) cleanupExpiredOrphans(ctx context.Context) {
	cutoff := time.Now().Add(-r.orphanTTL)
	expired, err := r.storage.ListExpiredOrphans(ctx, cutoff)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list expired orphaned resources")
		return
	}

	if len(expired) == 0 {
		return
	}

	log.Info().
		Int("count", len(expired)).
		Dur("orphan_ttl", r.orphanTTL).
		Msg("Cleaning up expired orphaned resources from database")

	for _, resource := range expired {
		// DB-only hard-delete: do NOT touch Cloudflare resources.
		// These are cleanup_enabled=false orphans; the user wants CF resources to persist.
		if err := r.storage.DeleteResource(ctx, resource.ID); err != nil {
			log.Error().Err(err).
				Str("hostname", resource.Hostname).
				Str("resource_type", string(resource.ResourceType)).
				Str("service_name", resource.ServiceName).
				Msg("Failed to hard-delete expired orphaned resource from DB")
		} else {
			log.Info().
				Str("hostname", resource.Hostname).
				Str("resource_type", string(resource.ResourceType)).
				Str("service_name", resource.ServiceName).
				Msg("Hard-deleted expired orphaned resource from DB (CF resource preserved)")
		}
	}
}

// getDesiredState returns the desired state from all sources.
func (r *Reconciler) getDesiredState() []*types.ParsedContainer {
	var desired []*types.ParsedContainer

	// Add local containers
	for _, c := range r.containers {
		desired = append(desired, c)
	}

	// Add agent containers
	for _, containers := range r.agentData {
		desired = append(desired, containers...)
	}

	return desired
}

// filterHostnameConflicts checks for hostname conflicts across all containers
// and returns a filtered desired state with conflicting services removed.
//
// Rules:
//   - DNS+Tunnel same hostname: remove the tunnel service (CF auto-creates
//     CNAME for tunnels which would conflict with explicit DNS records)
//   - DNS duplicate hostname across containers: first container wins (logged)
//   - Tunnel duplicate hostname across containers: first container wins (logged)
func (r *Reconciler) filterHostnameConflicts(containers []*types.ParsedContainer) []*types.ParsedContainer {
	// First pass: collect all DNS hostnames (first-wins)
	dnsHostnames := make(map[string]string)    // hostname -> container name
	tunnelHostnames := make(map[string]string) // hostname -> container name
	conflictedHostnames := make(map[string]bool)

	for _, c := range containers {
		for _, svc := range c.DNSServices {
			if existing, ok := dnsHostnames[svc.Hostname]; ok {
				log.Warn().
					Str("hostname", svc.Hostname).
					Str("existing", existing).
					Str("new", c.Info.Name).
					Msg("DNS hostname conflict across containers, first wins")
			} else {
				dnsHostnames[svc.Hostname] = c.Info.Name
			}
		}
		for _, svc := range c.TunnelServices {
			if existing, ok := tunnelHostnames[svc.Hostname]; ok {
				log.Warn().
					Str("hostname", svc.Hostname).
					Str("existing", existing).
					Str("new", c.Info.Name).
					Msg("Tunnel hostname conflict across containers, first wins")
			} else {
				tunnelHostnames[svc.Hostname] = c.Info.Name
			}
		}
	}

	// Detect DNS vs Tunnel conflicts
	for hostname, dnsContainer := range dnsHostnames {
		if tunnelContainer, ok := tunnelHostnames[hostname]; ok {
			conflictedHostnames[hostname] = true
			log.Error().
				Str("hostname", hostname).
				Str("dns_container", dnsContainer).
				Str("tunnel_container", tunnelContainer).
				Msg("Hostname cannot be used for both DNS and Tunnel, removing tunnel service")
		}
	}

	if len(conflictedHostnames) == 0 {
		return containers
	}

	// Second pass: rebuild containers with conflicted tunnel services removed
	result := make([]*types.ParsedContainer, 0, len(containers))
	for _, c := range containers {
		// Filter tunnel services to remove conflicted hostnames
		hasConflict := false
		for _, svc := range c.TunnelServices {
			if conflictedHostnames[svc.Hostname] {
				hasConflict = true
				break
			}
		}

		if !hasConflict {
			result = append(result, c)
			continue
		}

		// Create a copy with filtered tunnel services
		filtered := make([]*types.TunnelService, 0, len(c.TunnelServices))
		for _, svc := range c.TunnelServices {
			if !conflictedHostnames[svc.Hostname] {
				filtered = append(filtered, svc)
			}
		}

		result = append(result, &types.ParsedContainer{
			Info:           c.Info,
			DNSServices:    c.DNSServices,
			TunnelServices: filtered,
			AccessPolicies: c.AccessPolicies,
			AgentID:        c.AgentID,
		})
	}

	return result
}

// resolveAccessReferences resolves access policy references from DNS and Tunnel services.
func (r *Reconciler) resolveAccessReferences(containers []*types.ParsedContainer) []*types.ResolvedAccessBinding {
	globalPolicies := make(map[string]*types.AccessPolicyDef)
	for _, c := range containers {
		for name, policyDef := range c.AccessPolicies {
			if existing, ok := globalPolicies[name]; ok {
				log.Warn().
					Str("policy_name", name).
					Str("existing_def", existing.Name).
					Msg("Duplicate access policy definition across containers, first wins")
				continue
			}
			globalPolicies[name] = policyDef
		}
	}

	var bindings []*types.ResolvedAccessBinding
	seenHostnames := make(map[string]bool)

	for _, c := range containers {
		for _, svc := range c.TunnelServices {
			if svc.Access == "" {
				continue
			}
			policyDef, ok := globalPolicies[svc.Access]
			if !ok {
				log.Warn().Str("container", c.Info.Name).Str("service", svc.ServiceName).Str("access_ref", svc.Access).Msg("Access policy reference not found")
				continue
			}
			if seenHostnames[svc.Hostname] {
				log.Warn().Str("hostname", svc.Hostname).Msg("Duplicate access binding for hostname, first wins")
				continue
			}
			seenHostnames[svc.Hostname] = true
			bindings = append(bindings, &types.ResolvedAccessBinding{
				Hostname: svc.Hostname, PolicyDef: policyDef,
				ContainerID: c.Info.ID, ContainerName: c.Info.Name,
				ServiceName: svc.ServiceName, AgentID: c.AgentID, Cleanup: svc.Cleanup, Credential: svc.Credential,
			})
			log.Debug().Str("hostname", svc.Hostname).Str("access_policy", svc.Access).Str("container", c.Info.Name).Msg("Resolved access reference for tunnel service")
		}

		for _, svc := range c.DNSServices {
			if svc.Access == "" {
				continue
			}
			policyDef, ok := globalPolicies[svc.Access]
			if !ok {
				log.Warn().Str("container", c.Info.Name).Str("service", svc.ServiceName).Str("access_ref", svc.Access).Msg("Access policy reference not found")
				continue
			}
			if seenHostnames[svc.Hostname] {
				log.Warn().Str("hostname", svc.Hostname).Msg("Duplicate access binding for hostname, first wins")
				continue
			}
			seenHostnames[svc.Hostname] = true
			bindings = append(bindings, &types.ResolvedAccessBinding{
				Hostname: svc.Hostname, PolicyDef: policyDef,
				ContainerID: c.Info.ID, ContainerName: c.Info.Name,
				ServiceName: svc.ServiceName, AgentID: c.AgentID, Cleanup: svc.Cleanup, Credential: svc.Credential,
			})
			log.Debug().Str("hostname", svc.Hostname).Str("access_policy", svc.Access).Str("container", c.Info.Name).Msg("Resolved access reference for DNS service")
		}
	}

	if len(bindings) > 0 {
		log.Info().Int("count", len(bindings)).Int("policies", len(globalPolicies)).Msg("Resolved access bindings")
	}

	return bindings
}

// UpdateAgentData updates container data from an agent.
func (r *Reconciler) UpdateAgentData(agentID string, containers []*types.ParsedContainer) {
	r.mu.Lock()
	r.agentData[agentID] = containers
	r.mu.Unlock()

	log.Debug().
		Str("agent", agentID).
		Int("containers", len(containers)).
		Msg("Updated agent data")

	// Non-blocking send to trigger immediate reconciliation
	select {
	case r.agentTrigger <- struct{}{}:
	default:
		// Reconciliation already pending, skip duplicate trigger
	}
}

// RemoveAgentData removes container data for an agent.
func (r *Reconciler) RemoveAgentData(agentID string) {
	r.mu.Lock()
	delete(r.agentData, agentID)
	r.mu.Unlock()

	log.Debug().
		Str("agent", agentID).
		Msg("Removed agent data")
}

// TriggerReconcile triggers an immediate reconciliation.
func (r *Reconciler) TriggerReconcile(ctx context.Context) error {
	return r.reconcile(ctx)
}

// GetContainers returns the current container state.
func (r *Reconciler) GetContainers() []*types.ParsedContainer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.getDesiredState()
}

// StartedAt returns when the reconciler started.
func (r *Reconciler) StartedAt() time.Time {
	return r.startedAt
}

// LastSyncTime returns the time of the last reconciliation.
func (r *Reconciler) LastSyncTime() time.Time {
	r.syncMu.RLock()
	defer r.syncMu.RUnlock()
	return r.lastSyncTime
}

// LastSyncError returns the error from the last reconciliation (nil if successful).
func (r *Reconciler) LastSyncError() error {
	r.syncMu.RLock()
	defer r.syncMu.RUnlock()
	return r.lastSyncError
}

// Storage returns the reconciler's storage for API access.
func (r *Reconciler) Storage() storage.Storage {
	return r.storage
}
