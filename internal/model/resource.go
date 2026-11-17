// Package model holds the domain types shared by every labelgate package.
//
// This package is a frozen contract: it has no dependencies outside the
// standard library and contains no I/O. Types are plain values; callers copy
// and return new values instead of mutating shared instances.
package model

import (
	"strings"
	"time"
)

// Kind is the kind of a tracked Cloudflare resource.
// The string values are part of the HTTP API ("resource_type").
type Kind string

const (
	// KindDNS is a DNS record (including tunnel CNAMEs).
	KindDNS Kind = "dns"
	// KindTunnel is one ingress rule of a remotely-managed tunnel.
	KindTunnel Kind = "tunnel_ingress"
	// KindAccess is a Zero Trust Access self-hosted application (one per hostname).
	KindAccess Kind = "access_app"
)

// Kinds lists every Kind in a stable order.
var Kinds = []Kind{KindDNS, KindTunnel, KindAccess}

// Status is the user-visible state of a tracked resource.
// The string values are part of the HTTP API ("status").
type Status string

const (
	// StatusPending: claimed and persisted (write-ahead) but not yet applied.
	StatusPending Status = "pending"
	// StatusActive: desired and applied; live state matched on the last cycle.
	StatusActive Status = "active"
	// StatusError: the last apply failed, or the claim was rejected (see Resource.Rejected).
	StatusError Status = "error"
	// StatusOrphaned: no longer desired, cleanup disabled; left on Cloudflare.
	StatusOrphaned Status = "orphaned"
	// StatusPendingCleanup: no longer desired, cleanup enabled; deleted once
	// OrphanedAt + remove_delay has passed.
	StatusPendingCleanup Status = "pending_cleanup"
)

// Owner identifies who claims a resource. The identity deliberately excludes
// ContainerID so ownership survives container re-creation (compose up).
type Owner struct {
	// AgentID is the reporting agent; LocalAgentID ("") for the local Docker source.
	AgentID string
	// ContainerName is the Docker container name without the leading slash.
	ContainerName string
	// ContainerID is informational only (latest known container ID).
	ContainerID string
	// Service is the label service name (labelgate.<type>.<service>.*). For a
	// tunnel CNAME it is the tunnel service that produced it.
	Service string
}

// LocalAgentID is the AgentID of resources sourced from the local Docker daemon.
const LocalAgentID = ""

// Key returns the stable identity of the owner: agent/container/service.
func (o Owner) Key() string {
	return o.AgentID + "/" + o.ContainerName + "/" + o.Service
}

// Less orders owners deterministically: local before agents, then by agent
// ID, container name and service name. Used for first-claim resolution.
func (o Owner) Less(p Owner) bool {
	if o.AgentID != p.AgentID {
		return o.AgentID < p.AgentID // "" (local) sorts first
	}
	if o.ContainerName != p.ContainerName {
		return o.ContainerName < p.ContainerName
	}
	return o.Service < p.Service
}

// Resource is one tracked resource row: a claim plus the last applied state.
//
// Exactly one non-rejected row exists per (Kind, Key). Rejected rows record a
// claim that lost conflict resolution or failed validation; they never cause
// Cloudflare mutations and are unique per (Kind, Key, Owner).
type Resource struct {
	ID   int64
	Kind Kind
	// Key is the canonical identity within Kind; see DNSKey, TunnelKey, AccessKey.
	Key string
	// Rejected marks a claim that was refused (conflict / validation). Status is StatusError.
	Rejected bool
	Hostname string
	Owner    Owner

	// Cloudflare location.
	Credential string // credential name used ("default" = root credential)
	AccountID  string // account of the zone / tunnel
	ZoneID     string // DNS and Access: zone of Hostname
	CFID       string // DNS record ID or Access application ID; "" = not created yet

	// DNS fields (KindDNS).
	RecordType DNSType
	Content    string // applied content (resolved IP / target / text / CAA value)
	Proxied    bool
	TTL        int // 1 = automatic
	Priority   int // MX, SRV
	Weight     int // SRV
	Port       int // SRV
	Flags      int // CAA
	Tag        string
	Comment    string // user part of the comment (marker excluded)

	// Tunnel fields (KindTunnel). TunnelID is also set on a tunnel CNAME (KindDNS).
	TunnelID string
	Path     string
	Service  string // origin service URL
	Origin   OriginRequest

	// Access fields (KindAccess).
	AppName    string
	PolicyName string   // label policy definition name (labelgate.access.<name>)
	Decision   string   // label decision (allow, block, bypass, service_auth)
	PolicyIDs  []string // owned reusable policy IDs, in precedence order

	Status    Status
	Cleanup   bool
	LastError string
	// Legacy marks rows migrated from v0.2 that still need their ownership
	// marker (DNS comment / Access tag) stamped on the next successful update.
	Legacy bool

	CreatedAt time.Time
	UpdatedAt time.Time
	// OrphanedAt is when the resource stopped being desired; zero while desired.
	OrphanedAt time.Time
	// SyncedAt is the last time the resource was applied/verified on Cloudflare; zero = never.
	SyncedAt time.Time
}

// IsTunnelCNAME reports whether r is the DNS CNAME that routes a hostname to a tunnel.
func (r Resource) IsTunnelCNAME() bool {
	return r.Kind == KindDNS && r.TunnelID != ""
}

// DNSKey returns the identity key of a DNS record.
//
// A, AAAA and CNAME are single-valued per (hostname, type): content may change
// (e.g. public IP) without changing identity. TXT, MX, SRV and CAA are
// multi-valued, so the content key is part of the identity.
func DNSKey(hostname string, t DNSType, contentKey string) string {
	switch t {
	case DNSTypeA, DNSTypeAAAA, DNSTypeCNAME:
		return hostname + "|" + string(t)
	default:
		return hostname + "|" + string(t) + "|" + contentKey
	}
}

// TunnelKey returns the identity key of a tunnel ingress rule.
func TunnelKey(tunnelID, hostname, path string) string {
	return tunnelID + "|" + hostname + "|" + path
}

// AccessKey returns the identity key of an Access application.
func AccessKey(hostname string) string {
	return hostname
}

// TunnelTarget returns the CNAME target that routes traffic to a tunnel.
func TunnelTarget(tunnelID string) string {
	return strings.ToLower(tunnelID) + ".cfargotunnel.com"
}
