// Package access provides Zero Trust Access operator implementation.
package access

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/operator"
	"github.com/channinghe/labelgate/internal/storage"
	"github.com/channinghe/labelgate/internal/types"
)

// ensure AccessOperatorImpl implements AccessOperator
var _ operator.AccessOperator = (*AccessOperatorImpl)(nil)

// AccessOperatorImpl implements the Access operator.
type AccessOperatorImpl struct {
	credManager *cloudflare.CredentialManager
	storage     storage.Storage
}

// NewAccessOperator creates a new Access operator.
func NewAccessOperator(credManager *cloudflare.CredentialManager, store storage.Storage) *AccessOperatorImpl {
	return &AccessOperatorImpl{
		credManager: credManager,
		storage:     store,
	}
}

// Name returns the operator name.
func (o *AccessOperatorImpl) Name() string {
	return "access"
}

// CheckPermissions probes the Cloudflare Access API to verify the token
// has sufficient permissions. Returns nil if valid, error otherwise.
func (o *AccessOperatorImpl) CheckPermissions(ctx context.Context) error {
	client, tunnelCred, err := o.credManager.GetTunnelClient("default")
	if err != nil {
		return fmt.Errorf("failed to get client for access permission check: %w", err)
	}

	accountID := ""
	if tunnelCred != nil {
		accountID = tunnelCred.AccountID
	}
	if accountID == "" {
		accountID = client.AccountID()
	}
	if accountID == "" {
		return fmt.Errorf("account ID is required for access permission check")
	}

	accessClient := cloudflare.NewAccessClient(client, accountID)
	return accessClient.CheckAccessPermissions(ctx)
}

// Reconcile ensures Access Applications match the desired state.
// This method is called by the reconciler with the list of all desired containers.
// The reconciler is responsible for resolving access references and creating
// ResolvedAccessBindings. This operator simply acts on the resolved bindings.
func (o *AccessOperatorImpl) Reconcile(ctx context.Context, desired []*types.ParsedContainer) error {
	// Access reconciliation is handled through ResolveAndReconcileAccess
	// which is called by the reconciler directly.
	// The standard Reconcile interface is a no-op for access.
	return nil
}

// ReconcileBindings reconciles access bindings (called by the reconciler).
func (o *AccessOperatorImpl) ReconcileBindings(ctx context.Context, bindings []*types.ResolvedAccessBinding) error {
	// Build desired state map: hostname -> binding
	desiredMap := make(map[string]*types.ResolvedAccessBinding)
	for _, binding := range bindings {
		desiredMap[binding.Hostname] = binding
	}

	// Get all active, error, and orphaned access app resources from storage.
	// Orphaned resources are included so they can be reactivated when containers restart.
	resources, err := o.storage.ListResources(ctx, storage.ResourceFilter{
		ResourceType: storage.ResourceTypeAccessApp,
		Statuses:     []storage.ResourceStatus{storage.StatusActive, storage.StatusError, storage.StatusOrphaned},
	})
	if err != nil {
		return fmt.Errorf("failed to list access resources: %w", err)
	}

	// Build current state map
	currentMap := make(map[string]*storage.ManagedResource)
	for _, r := range resources {
		currentMap[r.Hostname] = r
	}

	// Create or update
	for hostname, binding := range desiredMap {
		existing, hasExisting := currentMap[hostname]

		if hasExisting {
			// Update existing (also retry errors, reactivate orphaned)
			if err := o.updateAccess(ctx, existing, binding); err != nil {
				log.Error().Err(err).
					Str("hostname", hostname).
					Msg("Failed to update Access Application")
				// Mark resource as error
				if updateErr := o.storage.UpdateResourceError(ctx, existing.ID, storage.StatusError, err.Error()); updateErr != nil {
					log.Error().Err(updateErr).Str("hostname", hostname).Msg("Failed to update resource error status")
				}
			} else {
				// Success: clear any previous error, reactivate if orphaned
				if existing.LastError != "" || existing.Status != storage.StatusActive {
					_ = o.storage.UpdateResourceError(ctx, existing.ID, storage.StatusActive, "")
				}
			}
			delete(currentMap, hostname)
		} else {
			// Create new
			resource, err := o.EnsureAccess(ctx, binding)
			if err != nil {
				log.Error().Err(err).
					Str("hostname", hostname).
					Msg("Failed to create Access Application")
			// Save resource in error state so Dashboard can see the failure
			errResource := &storage.ManagedResource{
				ResourceType:   storage.ResourceTypeAccessApp,
				Hostname:       hostname,
				ContainerID:    binding.ContainerID,
				ContainerName:  binding.ContainerName,
				ServiceName:    binding.ServiceName,
				AgentID:        binding.AgentID,
				Status:         storage.StatusError,
				LastError:      err.Error(),
				CleanupEnabled: binding.Cleanup,
			}
				if saveErr := o.storage.SaveResource(ctx, errResource); saveErr != nil {
					log.Error().Err(saveErr).Str("hostname", hostname).Msg("Failed to save error resource")
				}
				continue
			}
			log.Info().
				Str("hostname", hostname).
				Str("resource_id", resource.ID).
				Msg("Created Access Application resource")
		}
	}

	// Handle orphaned resources (not in desired state)
	for hostname, resource := range currentMap {
		if resource.Status != storage.StatusOrphaned {
			if err := o.storage.UpdateResourceStatus(ctx, resource.ID, storage.StatusOrphaned); err != nil {
				log.Error().Err(err).
					Str("hostname", hostname).
					Msg("Failed to mark Access Application as orphaned")
			} else {
				log.Info().
					Str("hostname", hostname).
					Str("service_name", resource.ServiceName).
					Str("container", resource.ContainerName).
					Bool("cleanup_enabled", resource.CleanupEnabled).
					Msg("Access Application orphaned, no longer referenced by running containers")
			}
		}
	}

	return nil
}

// EnsureAccess creates or updates an Access Application for a resolved binding.
func (o *AccessOperatorImpl) EnsureAccess(ctx context.Context, binding *types.ResolvedAccessBinding) (*storage.ManagedResource, error) {
	// GetTunnelClient returns the API client + credential with AccountID.
	// Access operations need the same client/account as tunnel operations.
	client, tunnelCred, err := o.credManager.GetTunnelClient("default")
	if err != nil {
		return nil, fmt.Errorf("failed to get client for access: %w", err)
	}

	accountID := ""
	if tunnelCred != nil {
		accountID = tunnelCred.AccountID
	}
	if accountID == "" {
		accountID = client.AccountID()
	}
	if accountID == "" {
		return nil, fmt.Errorf("account ID is required for access operations")
	}

	accessClient := cloudflare.NewAccessClient(client, accountID)

	// Check if an Access Application already exists for this hostname (not managed by labelgate)
	existingAppID, existingAppName, err := accessClient.FindExistingAccessApp(ctx, binding.Hostname)
	if err != nil {
		log.Warn().Err(err).Str("hostname", binding.Hostname).Msg("Failed to check for existing Access Application")
		// Non-fatal: proceed with creation, let CF API reject if conflicting
	} else if existingAppID != "" {
		return nil, fmt.Errorf("access application %q (ID: %s) already exists for hostname %s, not managed by labelgate — refusing to overwrite",
			existingAppName, existingAppID, binding.Hostname)
	}

	// Create Access Application + Policy
	appID, err := accessClient.EnsureAccessForHostname(ctx, binding.Hostname, binding.PolicyDef, "")
	if err != nil {
		return nil, err
	}

	// Save to storage
	resource := &storage.ManagedResource{
		ResourceType:   storage.ResourceTypeAccessApp,
		Hostname:       binding.Hostname,
		CFID:           appID,
		AccessAppID:    appID,
		AccountID:      accountID,
		ContainerID:    binding.ContainerID,
		ContainerName:  binding.ContainerName,
		ServiceName:    binding.ServiceName,
		AgentID:        binding.AgentID,
		Status:         storage.StatusActive,
		CleanupEnabled: binding.Cleanup,
	}

	if err := o.storage.SaveResource(ctx, resource); err != nil {
		return nil, fmt.Errorf("failed to save access resource: %w", err)
	}

	return resource, nil
}

// updateAccess updates an existing Access Application.
func (o *AccessOperatorImpl) updateAccess(ctx context.Context, existing *storage.ManagedResource, binding *types.ResolvedAccessBinding) error {
	client, tunnelCred, err := o.credManager.GetTunnelClient("default")
	if err != nil {
		return fmt.Errorf("failed to get client for access: %w", err)
	}

	accountID := existing.AccountID
	if accountID == "" && tunnelCred != nil {
		accountID = tunnelCred.AccountID
	}
	if accountID == "" {
		accountID = client.AccountID()
	}

	accessClient := cloudflare.NewAccessClient(client, accountID)

	_, err = accessClient.EnsureAccessForHostname(ctx, binding.Hostname, binding.PolicyDef, existing.AccessAppID)
	if err != nil {
		return err
	}

	// Update storage
	existing.ContainerID = binding.ContainerID
	existing.ContainerName = binding.ContainerName
	existing.ServiceName = binding.ServiceName
	existing.AgentID = binding.AgentID
	existing.CleanupEnabled = binding.Cleanup
	return o.storage.SaveResource(ctx, existing)
}

// RemoveAccess removes an Access Application.
func (o *AccessOperatorImpl) RemoveAccess(ctx context.Context, resource *storage.ManagedResource) error {
	if resource.AccessAppID == "" {
		log.Warn().
			Str("hostname", resource.Hostname).
			Msg("No Access App ID found, hard-deleting from storage")
		return o.storage.DeleteResource(ctx, resource.ID)
	}

	client, tunnelCred, err := o.credManager.GetTunnelClient("default")
	if err != nil {
		return fmt.Errorf("failed to get client for access: %w", err)
	}

	accountID := resource.AccountID
	if accountID == "" && tunnelCred != nil {
		accountID = tunnelCred.AccountID
	}
	if accountID == "" {
		accountID = client.AccountID()
	}

	accessClient := cloudflare.NewAccessClient(client, accountID)

	if err := accessClient.DeleteAccessApplication(ctx, resource.AccessAppID); err != nil {
		return fmt.Errorf("failed to delete access app: %w", err)
	}

	// Hard-delete from storage (no more soft-delete)
	return o.storage.DeleteResource(ctx, resource.ID)
}

// Create creates a resource (part of Operator interface).
func (o *AccessOperatorImpl) Create(ctx context.Context, resource *storage.ManagedResource) error {
	return nil // Access creation is handled through EnsureAccess
}

// Update updates a resource (part of Operator interface).
func (o *AccessOperatorImpl) Update(ctx context.Context, resource *storage.ManagedResource) error {
	return nil // Access updates are handled through updateAccess
}

// Delete deletes a resource (part of Operator interface).
func (o *AccessOperatorImpl) Delete(ctx context.Context, resource *storage.ManagedResource) error {
	return o.RemoveAccess(ctx, resource)
}
