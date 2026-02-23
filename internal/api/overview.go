package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/channinghe/labelgate/internal/storage"
)

type overviewResponse struct {
	Resources  resourceOverview  `json:"resources"`
	Agents     agentOverview     `json:"agents"`
	Sync       syncOverview      `json:"sync"`
	Cloudflare cloudflareStatus  `json:"cloudflare"`
	Version    string            `json:"version"`
	Uptime     string            `json:"uptime"`
	StartedAt  time.Time         `json:"started_at"`
}

type resourceCounts struct {
	Total    int `json:"total"`
	Active   int `json:"active"`
	Orphaned int `json:"orphaned"`
	Error    int `json:"error"`
}

type resourceOverview struct {
	DNS           resourceCounts `json:"dns"`
	TunnelIngress resourceCounts `json:"tunnel_ingress"`
	AccessApp     resourceCounts `json:"access_app"`
}

type agentOverview struct {
	Total        int `json:"total"`
	Connected    int `json:"connected"`
	Disconnected int `json:"disconnected"`
}

type syncOverview struct {
	LastSync time.Time `json:"last_sync"`
	Status   string    `json:"status"`
	Error    string    `json:"error"`
}

type cloudflareStatus struct {
	Reachable bool      `json:"reachable"`
	LastCheck time.Time `json:"last_check"`
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Count resources by type and status
	dnsCounts := s.countResources(ctx, storage.ResourceTypeDNS)
	tunnelCounts := s.countResources(ctx, storage.ResourceTypeTunnelIngress)
	accessCounts := s.countResources(ctx, storage.ResourceTypeAccessApp)

	// Agent counts
	agentCounts := s.countAgents(ctx)

	// Sync status
	syncStatus := syncOverview{Status: "success"}
	if s.config.Reconciler != nil {
		syncStatus.LastSync = s.config.Reconciler.LastSyncTime()
		if err := s.config.Reconciler.LastSyncError(); err != nil {
			syncStatus.Status = "error"
			syncStatus.Error = err.Error()
		}
	}

	// Cloudflare health
	cfStatus := cloudflareStatus{Reachable: true}
	if s.config.CredManager != nil {
		result := s.config.CredManager.HealthCheck(ctx)
		cfStatus.Reachable = result.Reachable
		cfStatus.LastCheck = result.LastCheck
	}

	// Uptime
	var uptime string
	var startedAt time.Time
	if s.config.Reconciler != nil {
		startedAt = s.config.Reconciler.StartedAt()
		uptime = formatDuration(time.Since(startedAt))
	}

	writeJSON(w, http.StatusOK, overviewResponse{
		Resources: resourceOverview{
			DNS:           dnsCounts,
			TunnelIngress: tunnelCounts,
			AccessApp:     accessCounts,
		},
		Agents:     agentCounts,
		Sync:       syncStatus,
		Cloudflare: cfStatus,
		Version:    s.config.Version,
		Uptime:     uptime,
		StartedAt:  startedAt,
	})
}

// countResources counts resources by type and status.
func (s *Server) countResources(ctx context.Context, resourceType storage.ResourceType) resourceCounts {
	all, err := s.config.Storage.ListResources(ctx, storage.ResourceFilter{ResourceType: resourceType})
	if err != nil {
		return resourceCounts{}
	}

	counts := resourceCounts{Total: len(all)}
	for _, r := range all {
		switch r.Status {
		case storage.StatusActive:
			counts.Active++
		case storage.StatusOrphaned:
			counts.Orphaned++
		case storage.StatusError:
			counts.Error++
		}
	}
	return counts
}

// countAgents returns agent counts.
func (s *Server) countAgents(ctx context.Context) agentOverview {
	agents, err := s.config.Storage.ListAgents(ctx)
	if err != nil {
		return agentOverview{}
	}

	overview := agentOverview{Total: len(agents)}
	for _, a := range agents {
		connected := a.Connected
		// Override with live connection status if agent server is available
		if s.config.AgentServer != nil {
			connected = s.config.AgentServer.IsAgentConnected(a.ID)
		}
		if connected {
			overview.Connected++
		} else {
			overview.Disconnected++
		}
	}
	return overview
}

// formatDuration formats a duration to a human-readable string like "3d 5h 20m".
func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
