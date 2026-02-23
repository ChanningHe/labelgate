// Package operator defines the operator interface for managing Cloudflare resources.
package operator

import (
	"context"

	"github.com/channinghe/labelgate/internal/storage"
	"github.com/channinghe/labelgate/internal/types"
)

// Operator defines the interface for resource operators.
type Operator interface {
	// Name returns the operator name.
	Name() string

	// Reconcile ensures the desired state matches actual state.
	Reconcile(ctx context.Context, desired []*types.ParsedContainer) error

	// Create creates a resource.
	Create(ctx context.Context, resource *storage.ManagedResource) error

	// Update updates a resource.
	Update(ctx context.Context, resource *storage.ManagedResource) error

	// Delete deletes a resource.
	Delete(ctx context.Context, resource *storage.ManagedResource) error
}

// DNSOperator interface for DNS-specific operations.
type DNSOperator interface {
	Operator

	// CreateDNSRecord creates a DNS record from service configuration.
	CreateDNSRecord(ctx context.Context, container *types.ContainerInfo, service *types.DNSService) (*storage.ManagedResource, error)

	// UpdateDNSRecord updates a DNS record.
	UpdateDNSRecord(ctx context.Context, resource *storage.ManagedResource, service *types.DNSService) error

	// DeleteDNSRecord deletes a DNS record.
	DeleteDNSRecord(ctx context.Context, resource *storage.ManagedResource) error
}

// TunnelOperator interface for Tunnel-specific operations.
type TunnelOperator interface {
	Operator

	// AddIngressRule adds an ingress rule to the tunnel.
	AddIngressRule(ctx context.Context, container *types.ContainerInfo, service *types.TunnelService) (*storage.ManagedResource, error)

	// UpdateIngressRule updates an ingress rule.
	UpdateIngressRule(ctx context.Context, resource *storage.ManagedResource, service *types.TunnelService) error

	// RemoveIngressRule removes an ingress rule.
	RemoveIngressRule(ctx context.Context, resource *storage.ManagedResource) error
}

// AccessOperator interface for Zero Trust Access-specific operations.
type AccessOperator interface {
	Operator

	// ReconcileBindings reconciles resolved access bindings (desired vs actual state).
	// Called by the reconciler after resolving cross-container access references.
	ReconcileBindings(ctx context.Context, bindings []*types.ResolvedAccessBinding) error

	// EnsureAccess creates or updates an Access Application for a resolved binding.
	EnsureAccess(ctx context.Context, binding *types.ResolvedAccessBinding) (*storage.ManagedResource, error)

	// RemoveAccess removes an Access Application.
	RemoveAccess(ctx context.Context, resource *storage.ManagedResource) error

	// CheckPermissions probes the Cloudflare API to verify Access permissions.
	// Returns nil if permissions are valid, error otherwise.
	CheckPermissions(ctx context.Context) error
}
