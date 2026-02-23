package api

import (
	"context"
	"net/http"
	"time"

	"github.com/channinghe/labelgate/internal/storage"
)

type agentResponse struct {
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

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	agents, err := s.config.Storage.ListAgents(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	result := make([]agentResponse, 0, len(agents))
	for _, a := range agents {
		connected := a.Connected
		// Override with live connection status if agent server is available
		if s.config.AgentServer != nil {
			connected = s.config.AgentServer.IsAgentConnected(a.ID)
		}

		// Count resources managed by this agent
		resourceCount := s.countAgentResources(ctx, a.ID)

		result = append(result, agentResponse{
			ID:            a.ID,
			Name:          a.Name,
			Connected:     connected,
			LastSeen:      a.LastSeen,
			PublicIP:      a.PublicIP,
			DefaultTunnel: a.DefaultTunnel,
			Status:        string(a.Status),
			ResourceCount: resourceCount,
			CreatedAt:     a.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agents": result,
		"total":  len(result),
	})
}

// countAgentResources counts resources belonging to an agent.
func (s *Server) countAgentResources(ctx context.Context, agentID string) int {
	resources, err := s.config.Storage.ListResources(ctx, storage.ResourceFilter{
		AgentID: agentID,
	})
	if err != nil {
		return 0
	}
	return len(resources)
}
