package types

import "time"

// EventType represents the type of container event.
type EventType string

const (
	// EventStart is emitted when a container starts.
	EventStart EventType = "start"
	// EventStop is emitted when a container stops.
	EventStop EventType = "stop"
	// EventDie is emitted when a container dies.
	EventDie EventType = "die"
	// EventDestroy is emitted when a container is destroyed.
	EventDestroy EventType = "destroy"
	// EventUpdate is emitted when container labels are updated.
	EventUpdate EventType = "update"
)

// ContainerEvent represents a container lifecycle event.
type ContainerEvent struct {
	// Type is the event type
	Type EventType `json:"type"`

	// ContainerID is the container ID
	ContainerID string `json:"container_id"`

	// ContainerName is the container name
	ContainerName string `json:"container_name"`

	// Labels are the container labels
	Labels map[string]string `json:"labels,omitempty"`

	// Timestamp is when the event occurred
	Timestamp time.Time `json:"timestamp"`

	// AgentID is the agent that reported the event (empty for local)
	AgentID string `json:"agent_id,omitempty"`
}

// ContainerInfo represents information about a running container.
type ContainerInfo struct {
	// ID is the container ID
	ID string `json:"id"`

	// Name is the container name
	Name string `json:"name"`

	// Image is the container image
	Image string `json:"image"`

	// Labels are the container labels
	Labels map[string]string `json:"labels"`

	// Networks maps network name to IP address
	Networks map[string]string `json:"networks,omitempty"`

	// State is the container state (running, exited, etc.)
	State string `json:"state"`

	// Created is when the container was created
	Created time.Time `json:"created"`

	// Started is when the container was started
	Started time.Time `json:"started,omitempty"`
}

// ParsedContainer represents a container with parsed label configurations.
type ParsedContainer struct {
	// Container info
	Info *ContainerInfo `json:"info"`

	// DNS services parsed from labels
	DNSServices []*DNSService `json:"dns_services,omitempty"`

	// Tunnel services parsed from labels
	TunnelServices []*TunnelService `json:"tunnel_services,omitempty"`

	// AccessPolicies are named access policy definitions from this container.
	// Key is the policy name (e.g., "internal", "bypass").
	AccessPolicies map[string]*AccessPolicyDef `json:"access_policies,omitempty"`

	// AgentID is the agent that reported this container
	AgentID string `json:"agent_id,omitempty"`
}
