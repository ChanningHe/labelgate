package api

import (
	"net/http"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

const (
	agentActive       = "active"
	agentDisconnected = "disconnected"
	agentRemoved      = "removed"
)

// handleAgents lists agents with their resource counts: one ListAgents and one
// CountResourcesByAgent call regardless of how many agents exist.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.deps.Store.ListAgents(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	counts, err := s.deps.Store.CountResourcesByAgent(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	now := s.deps.Now()
	out := AgentListDTO{Agents: make([]AgentDTO, 0, len(agents)), Total: len(agents)}
	for _, a := range agents {
		out.Agents = append(out.Agents, s.toAgentDTO(a, counts[a.ID], now))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) toAgentDTO(a model.AgentRecord, resources int, now time.Time) AgentDTO {
	connected := s.connected(a.ID)
	d := AgentDTO{
		ID:            a.ID,
		Name:          a.Name,
		Connected:     connected,
		PublicIP:      firstNonEmpty(a.PublicIP, a.PublicIPv6),
		DefaultTunnel: a.DefaultTunnel,
		Status:        s.agentStatus(a, connected, now),
		ResourceCount: resources,
		CreatedAt:     a.CreatedAt,
	}
	if !a.LastSeen.IsZero() {
		seen := a.LastSeen
		d.LastSeen = &seen
	}
	return d
}

// agentStatus: active while connected, disconnected while its last report is
// within agent_grace (resources still held), removed afterwards.
func (s *Server) agentStatus(a model.AgentRecord, connected bool, now time.Time) string {
	switch {
	case connected:
		return agentActive
	case !a.LastSeen.IsZero() && now.Sub(a.LastSeen) <= s.deps.AgentGrace:
		return agentDisconnected
	default:
		return agentRemoved
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
