package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/channinghe/labelgate/dashboard"
)

// newDashboardHandler returns an HTTP handler that serves the embedded
// dashboard SPA. For SPA routing, non-file paths fall back to index.html.
func newDashboardHandler() http.Handler {
	fileServer := http.FileServerFS(dashboard.FS)

	return http.StripPrefix("/dashboard/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the requested file
		path := r.URL.Path
		if path == "" || path == "/" {
			path = "index.html"
		}

		// Check if the file exists in the embedded FS
		if _, err := fs.Stat(dashboard.FS, strings.TrimPrefix(path, "/")); err != nil {
			// File not found — serve index.html for SPA client-side routing
			r.URL.Path = "/"
		}

		fileServer.ServeHTTP(w, r)
	}))
}
