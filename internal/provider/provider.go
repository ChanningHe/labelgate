// Package provider defines the provider interface for container data sources.
package provider

import (
	"context"

	"github.com/channinghe/labelgate/internal/types"
)

// Provider defines the interface for container data sources.
type Provider interface {
	// Name returns the provider name.
	Name() string

	// Connect establishes connection to the container runtime.
	Connect(ctx context.Context) error

	// Close closes the provider connection.
	Close() error

	// ListContainers returns all running containers.
	ListContainers(ctx context.Context) ([]*types.ContainerInfo, error)

	// GetContainer returns a specific container by ID.
	GetContainer(ctx context.Context, id string) (*types.ContainerInfo, error)

	// Watch starts watching for container events and sends them to the channel.
	// It returns when the context is cancelled.
	Watch(ctx context.Context, events chan<- *types.ContainerEvent) error
}
