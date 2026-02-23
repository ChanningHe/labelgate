package api

import (
	"net/http"
	"runtime"
)

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version":    s.config.Version,
		"go_version": runtime.Version(),
	})
}
