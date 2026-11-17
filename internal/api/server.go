package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/channinghe/labelgate/dashboard"
)

var errNotImplemented = errors.New("api: not implemented")

// HTTP server timeouts.
const (
	ReadHeaderTimeout = 10 * time.Second
	ReadTimeout       = 30 * time.Second
	WriteTimeout      = 30 * time.Second
	IdleTimeout       = 120 * time.Second
	ShutdownTimeout   = 10 * time.Second
)

// Server is the HTTP API + dashboard server.
//
// Required behavior (WP6):
//   - Routes (GET only, 405 otherwise): {base}/health (no auth),
//     {base}/overview, {base}/resources/dns, {base}/resources/tunnels,
//     {base}/resources/access, {base}/agents, {base}/version (Bearer auth,
//     subtle.ConstantTimeCompare); /dashboard/* SPA; "/" and "/dashboard"
//     redirect to "/dashboard/"; everything else 404 JSON.
//   - New returns an error if Config.Token is empty (caller disables the API).
//   - List endpoints: query status, agent_id, hostname, limit (default
//     store.DefaultLimit, 1..store.MaxLimit), offset (>= 0); invalid values ->
//     400 {"error":"invalid <param>"}. total = Page.Total.
//   - Storage errors -> 500 {"error":"internal error"} (details logged), never zeros.
//   - API responses: Content-Type application/json, Cache-Control: no-store,
//     X-Content-Type-Options: nosniff.
//   - Dashboard: CSP (default-src 'self'; frame-ancestors 'none'; ...),
//     X-Frame-Options DENY, Referrer-Policy no-referrer, nosniff. Requests under
//     /dashboard/assets/ that do not exist -> 404; other unknown paths -> index.html.
//     index.html is served with <meta name="labelgate-api-base" content="{base}">
//     injected so the dashboard honours api.base_path.
type Server struct {
	deps Deps
	srv  *http.Server
}

// New validates deps and builds the handler tree. No I/O.
func New(d Deps) (*Server, error) {
	_ = dashboard.FS
	return nil, errNotImplemented
}

// Handler returns the root handler (for tests).
func (s *Server) Handler() http.Handler { return http.NotFoundHandler() }

// Run listens on Config.Address until ctx is cancelled, then shuts down
// gracefully within ShutdownTimeout. Returns nil on clean shutdown.
func (s *Server) Run(ctx context.Context) error { return errNotImplemented }
