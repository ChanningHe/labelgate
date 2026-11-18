// Package api serves the read-only HTTP API and the embedded dashboard.
//
// dto.go and contract.go are frozen contracts. The JSON shapes below are
// consumed by dashboard/src/api/client.ts and must not change.
package api

import "time"

// ResourceCountsDTO mirrors ResourceCounts.
// Orphaned counts orphaned + pending_cleanup; Total counts every row incl. pending and rejected.
type ResourceCountsDTO struct {
	Total    int `json:"total"`
	Active   int `json:"active"`
	Orphaned int `json:"orphaned"`
	Error    int `json:"error"`
}

// OverviewResourcesDTO groups counts by kind.
type OverviewResourcesDTO struct {
	DNS           ResourceCountsDTO `json:"dns"`
	TunnelIngress ResourceCountsDTO `json:"tunnel_ingress"`
	AccessApp     ResourceCountsDTO `json:"access_app"`
}

// OverviewAgentsDTO counts agents. Connected = live session.
type OverviewAgentsDTO struct {
	Total        int `json:"total"`
	Connected    int `json:"connected"`
	Disconnected int `json:"disconnected"`
}

// OverviewSyncDTO mirrors OverviewData.sync. Status is "pending", "success"
// or "error" (client.ts union gains 'pending'); Error is "" on success and
// "waiting for first sync" while pending.
// Problems lists the latest cycle's problems (at most
// reconcile.MaxStatusProblems, [] unless Status is "error"); ProblemsTotal
// counts all of them, so the client can show "+N more".
type OverviewSyncDTO struct {
	LastSync      time.Time        `json:"last_sync"`
	Status        string           `json:"status"`
	Error         string           `json:"error"`
	Problems      []SyncProblemDTO `json:"problems"`
	ProblemsTotal int              `json:"problems_total"`
}

// SyncProblemDTO mirrors SyncProblem: what the problem concerns and its full text.
type SyncProblemDTO struct {
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

// OverviewCloudflareDTO mirrors OverviewData.cloudflare.
type OverviewCloudflareDTO struct {
	Reachable bool      `json:"reachable"`
	LastCheck time.Time `json:"last_check"`
}

// OverviewDTO mirrors OverviewData (GET {base}/overview).
type OverviewDTO struct {
	Resources   OverviewResourcesDTO  `json:"resources"`
	Agents      OverviewAgentsDTO     `json:"agents"`
	Sync        OverviewSyncDTO       `json:"sync"`
	Cloudflare  OverviewCloudflareDTO `json:"cloudflare"`
	Version     string                `json:"version"`
	Uptime      string                `json:"uptime"` // "3d 5h 20m" / "5h 20m" / "20m"
	StartedAt   time.Time             `json:"started_at"`
	LabelPrefix string                `json:"label_prefix"`
}

// ResourceDTO mirrors ManagedResource. Mapping from model.Resource:
// id = decimal ID; cf_id = CFID (DNS record / Access app ID); access_app_id =
// CFID for access_app rows; access_app_name = AppName; access_policy_name =
// PolicyName; access_decision = Decision; cleanup_enabled = Cleanup;
// deleted_at is never set (rows are removed after deletion).
type ResourceDTO struct {
	ID               string     `json:"id"`
	ResourceType     string     `json:"resource_type"`
	CFID             string     `json:"cf_id,omitempty"`
	ZoneID           string     `json:"zone_id,omitempty"`
	Hostname         string     `json:"hostname"`
	RecordType       string     `json:"record_type,omitempty"`
	Content          string     `json:"content,omitempty"`
	Proxied          bool       `json:"proxied,omitempty"`
	TTL              int        `json:"ttl,omitempty"`
	TunnelID         string     `json:"tunnel_id,omitempty"`
	Service          string     `json:"service,omitempty"`
	Path             string     `json:"path,omitempty"`
	AccessAppID      string     `json:"access_app_id,omitempty"`
	AccountID        string     `json:"account_id,omitempty"`
	AccessAppName    string     `json:"access_app_name,omitempty"`
	AccessPolicyName string     `json:"access_policy_name,omitempty"`
	AccessDecision   string     `json:"access_decision,omitempty"`
	ContainerID      string     `json:"container_id,omitempty"`
	ContainerName    string     `json:"container_name,omitempty"`
	ServiceName      string     `json:"service_name"`
	AgentID          string     `json:"agent_id,omitempty"`
	Credential       string     `json:"credential,omitempty"`
	Status           string     `json:"status"`
	CleanupEnabled   bool       `json:"cleanup_enabled"`
	LastError        string     `json:"last_error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
}

// ResourceListDTO mirrors ResourceListResponse. Total is the true filtered
// count, independent of limit/offset.
type ResourceListDTO struct {
	Resources []ResourceDTO `json:"resources"` // never null
	Total     int           `json:"total"`
}

// AgentDTO mirrors AgentInfo. Status: "active" if connected; "disconnected"
// if last_seen within agent_grace; "removed" otherwise (resources orphaned).
type AgentDTO struct {
	ID            string     `json:"id"`
	Name          string     `json:"name,omitempty"`
	Connected     bool       `json:"connected"`
	LastSeen      *time.Time `json:"last_seen,omitempty"`
	PublicIP      string     `json:"public_ip,omitempty"`
	DefaultTunnel string     `json:"default_tunnel,omitempty"`
	Status        string     `json:"status"`
	ResourceCount int        `json:"resource_count"`
	CreatedAt     time.Time  `json:"created_at"`
}

// AgentListDTO mirrors AgentListResponse.
type AgentListDTO struct {
	Agents []AgentDTO `json:"agents"` // never null
	Total  int        `json:"total"`
}

// VersionDTO is GET {base}/version.
type VersionDTO struct {
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
}

// HealthDTO is GET {base}/health (unauthenticated): always {"status":"ok"}.
type HealthDTO struct {
	Status string `json:"status"`
}

// ErrorDTO is every error body. Messages are generic for 5xx (details logged only).
type ErrorDTO struct {
	Error string `json:"error"`
}
