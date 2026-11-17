package model

import (
	"context"
	"net/netip"
	"time"
)

// Container is a running container as seen by a source (local Docker or agent).
// It is also the wire format of agent reports (JSON tags are protocol).
type Container struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"` // without leading slash
	Labels map[string]string `json:"labels"`
	// Networks maps network name -> IPv4 address (A "container" targets).
	Networks map[string]string `json:"networks,omitempty"`
	// Networks6 maps network name -> global IPv6 address, also on dual-stack
	// networks (AAAA "container" targets).
	Networks6 map[string]string `json:"networks6,omitempty"`
	State     string            `json:"state"`
}

// Snapshot is the complete container list of one source at one point in time.
type Snapshot struct {
	// AgentID is LocalAgentID for the local Docker source.
	AgentID string
	// Complete is false when the source has never delivered a full container
	// list (e.g. migrated agent without a stored report). Resources owned by an
	// incomplete source are held: neither updated nor orphaned.
	Complete   bool
	Containers []Container
	// PublicIP / PublicIPv6 are the host's public addresses as reported by an
	// agent ("" if unknown). Empty for the local source (resolved by PublicIPFunc).
	PublicIP   string
	PublicIPv6 string
	// TakenAt is the server-clock time the snapshot was received (agents: last_seen).
	TakenAt time.Time
}

// AgentRecord is the persisted state of an agent known to the main instance.
type AgentRecord struct {
	ID            string
	Name          string // optional display name
	DefaultTunnel string // tunnel reference from config, informational
	PublicIP      string
	PublicIPv6    string
	// LastSeen is the server-clock time of the last accepted report; zero = never.
	LastSeen  time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PublicIPFunc returns the public IP of this host (v6=false: IPv4, true: IPv6).
// Implementations cache results; callers may call it every cycle.
type PublicIPFunc func(ctx context.Context, v6 bool) (netip.Addr, error)
