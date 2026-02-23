package api

import (
	"net/http"
	"strconv"

	"github.com/channinghe/labelgate/internal/storage"
)

func (s *Server) handleDNS(w http.ResponseWriter, r *http.Request) {
	filter := storage.ResourceFilter{
		ResourceType: storage.ResourceTypeDNS,
	}
	applyQueryFilters(r, &filter)

	resources, err := s.config.Storage.ListResources(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"resources": resources,
		"total":     len(resources),
	})
}

// applyQueryFilters populates a ResourceFilter from query parameters.
func applyQueryFilters(r *http.Request, filter *storage.ResourceFilter) {
	if status := r.URL.Query().Get("status"); status != "" {
		filter.Status = storage.ResourceStatus(status)
	}
	if agentID := r.URL.Query().Get("agent_id"); agentID != "" {
		filter.AgentID = agentID
	}
	if hostname := r.URL.Query().Get("hostname"); hostname != "" {
		filter.Hostname = hostname
	}
	if limit := r.URL.Query().Get("limit"); limit != "" {
		if n, err := strconv.Atoi(limit); err == nil {
			filter.Limit = n
		}
	}
	if offset := r.URL.Query().Get("offset"); offset != "" {
		if n, err := strconv.Atoi(offset); err == nil {
			filter.Offset = n
		}
	}
}
