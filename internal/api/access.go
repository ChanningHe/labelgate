package api

import (
	"net/http"

	"github.com/channinghe/labelgate/internal/storage"
)

func (s *Server) handleAccess(w http.ResponseWriter, r *http.Request) {
	filter := storage.ResourceFilter{
		ResourceType: storage.ResourceTypeAccessApp,
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
