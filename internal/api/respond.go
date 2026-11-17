package api

import (
	"encoding/json"
	"net/http"
)

const (
	contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'"
	errInternal           = "internal error"
)

// secureHeaders sets the headers every response carries.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// setDashboardHeaders adds the browser-facing policy header for HTML/asset responses.
func setDashboardHeaders(h http.Header) {
	h.Set("Content-Security-Policy", contentSecurityPolicy)
}

// writeJSON writes v as a JSON response. Encoding happens before any byte is
// sent so a failure can still become a clean 500.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":"` + errInternal + `"}`)
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorDTO{Error: msg})
}
