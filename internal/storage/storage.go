// Package storage provides persistent storage for labelgate.
package storage

import (
	"context"
	"time"
)

// ResourceType represents the type of managed resource.
type ResourceType string

const (
	// ResourceTypeDNS represents a DNS record.
	ResourceTypeDNS ResourceType = "dns"
	// ResourceTypeTunnelIngress represents a Tunnel ingress rule.
	ResourceTypeTunnelIngress ResourceType = "tunnel_ingress"
	// ResourceTypeAccessApp represents a Zero Trust Access Application.
	ResourceTypeAccessApp ResourceType = "access_app"
)

// ResourceStatus represents the status of a managed resource.
type ResourceStatus string

const (
	// StatusActive means the resource is actively managed.
	StatusActive ResourceStatus = "active"
	// StatusOrphaned means the container stopped but cleanup is disabled.
	StatusOrphaned ResourceStatus = "orphaned"
	// StatusPendingCleanup means the resource is scheduled for cleanup.
	StatusPendingCleanup ResourceStatus = "pending_cleanup"
	// StatusDeleted means the resource has been deleted from Cloudflare.
	StatusDeleted ResourceStatus = "deleted"
	// StatusError means the resource failed to be created or updated on Cloudflare.
	StatusError ResourceStatus = "error"
)

// AgentStatus represents the status of an agent.
type AgentStatus string

const (
	// AgentStatusActive means the agent is active.
	AgentStatusActive AgentStatus = "active"
	// AgentStatusDisconnected means the agent is disconnected.
	AgentStatusDisconnected AgentStatus = "disconnected"
	// AgentStatusRemoved means the agent has been removed.
	AgentStatusRemoved AgentStatus = "removed"
)

// ManagedResource represents a resource managed by labelgate.
type ManagedResource struct {
	ID string `json:"id"`

	// Resource identification
	ResourceType ResourceType `json:"resource_type"`
	CFID         string       `json:"cf_id,omitempty"`

	// DNS record fields
	ZoneID     string `json:"zone_id,omitempty"`
	Hostname   string `json:"hostname"`
	RecordType string `json:"record_type,omitempty"`
	Content    string `json:"content,omitempty"`
	Proxied    bool   `json:"proxied,omitempty"`
	TTL        int    `json:"ttl,omitempty"`

	// Tunnel fields
	TunnelID string `json:"tunnel_id,omitempty"`
	Service  string `json:"service,omitempty"`
	Path     string `json:"path,omitempty"`

	// Access App fields
	AccessAppID string `json:"access_app_id,omitempty"` // Cloudflare Access Application ID
	AccountID   string `json:"account_id,omitempty"`    // Cloudflare Account ID

	// Source information
	ContainerID   string `json:"container_id,omitempty"`
	ContainerName string `json:"container_name,omitempty"`
	ServiceName   string `json:"service_name"`
	AgentID       string `json:"agent_id,omitempty"`

	// Status
	Status         ResourceStatus `json:"status"`
	CleanupEnabled bool           `json:"cleanup_enabled"`
	LastError      string         `json:"last_error,omitempty"`

	// Timestamps
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// Agent represents an agent connection.
type Agent struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`

	// Connection info
	Connected bool       `json:"connected"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
	PublicIP  string     `json:"public_ip,omitempty"`

	// Configuration
	DefaultTunnel string `json:"default_tunnel,omitempty"`

	// Status
	Status AgentStatus `json:"status"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ResourceFilter represents filter options for querying resources.
type ResourceFilter struct {
	ResourceType ResourceType
	Hostname     string
	ContainerID  string
	ServiceName  string
	AgentID      string
	Status       ResourceStatus   // single status filter (for backwards compatibility)
	Statuses     []ResourceStatus // multi-status filter (takes precedence over Status)
	Limit        int
	Offset       int
}

// Storage defines the interface for persistent storage.
type Storage interface {
	// Initialize initializes the storage (create tables, run migrations).
	Initialize(ctx context.Context) error

	// Close closes the storage connection.
	Close() error

	// Resource operations
	GetResource(ctx context.Context, id string) (*ManagedResource, error)
	GetResourceByHostname(ctx context.Context, hostname string, resourceType ResourceType) (*ManagedResource, error)
	GetResourceByContainerService(ctx context.Context, containerID, serviceName string) (*ManagedResource, error)
	ListResources(ctx context.Context, filter ResourceFilter) ([]*ManagedResource, error)
	SaveResource(ctx context.Context, resource *ManagedResource) error
	UpdateResourceStatus(ctx context.Context, id string, status ResourceStatus) error
	UpdateResourceError(ctx context.Context, id string, status ResourceStatus, lastError string) error
	DeleteResource(ctx context.Context, id string) error

	// Agent operations
	GetAgent(ctx context.Context, id string) (*Agent, error)
	ListAgents(ctx context.Context) ([]*Agent, error)
	SaveAgent(ctx context.Context, agent *Agent) error
	UpdateAgentStatus(ctx context.Context, id string, connected bool, status AgentStatus) error
	DeleteAgent(ctx context.Context, id string) error

	// Sync state operations
	GetSyncState(ctx context.Context, key string) (string, error)
	SetSyncState(ctx context.Context, key, value string) error

	// Maintenance operations
	CleanupDeletedResources(ctx context.Context, before time.Time) (int64, error)
	ListExpiredOrphans(ctx context.Context, olderThan time.Time) ([]*ManagedResource, error)
	ListOrphanedForCleanup(ctx context.Context, olderThan time.Time) ([]*ManagedResource, error)
	Vacuum(ctx context.Context) error
}

// Common errors
var (
	ErrNotFound = &StorageError{Code: "not_found", Message: "resource not found"}
	ErrConflict = &StorageError{Code: "conflict", Message: "resource already exists"}
)

// StorageError represents a storage error.
type StorageError struct {
	Code    string
	Message string
}

func (e *StorageError) Error() string {
	return e.Message
}

// IsNotFound checks if the error is a not found error.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if se, ok := err.(*StorageError); ok {
		return se.Code == "not_found"
	}
	return false
}

// IsConflict checks if the error is a conflict error.
func IsConflict(err error) bool {
	if err == nil {
		return false
	}
	if se, ok := err.(*StorageError); ok {
		return se.Code == "conflict"
	}
	return false
}
