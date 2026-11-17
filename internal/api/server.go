package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/channinghe/labelgate/dashboard"
)

// HTTP server timeouts.
const (
	ReadHeaderTimeout = 10 * time.Second
	ReadTimeout       = 30 * time.Second
	WriteTimeout      = 30 * time.Second
	IdleTimeout       = 120 * time.Second
	ShutdownTimeout   = 10 * time.Second

	maxHeaderBytes = 64 << 10
)

// dashboardPrefix is where the SPA is mounted (it is built with Vite base /dashboard/).
const dashboardPrefix = "/dashboard"

// ErrNoToken is returned by New when api.token is empty. The caller disables
// the API and dashboard (an open API is never started).
var ErrNoToken = errors.New("api: api.token is empty")

// Server is the HTTP API + dashboard server.
//
//   - Routes (GET/HEAD only, 405 otherwise): {base}/health (no auth),
//     {base}/overview, {base}/resources/{dns,tunnels,access}, {base}/agents,
//     {base}/version (Bearer auth, constant time); /dashboard/ SPA; "/" and
//     "/dashboard" redirect to "/dashboard/"; everything else 404 JSON.
//   - Storage errors -> 500 {"error":"internal error"}; details are logged only.
//   - The dashboard learns the API base path from a <meta> tag injected into
//     index.html, so api.base_path is honoured end to end.
type Server struct {
	deps      Deps
	tokenHash [sha256.Size]byte
	base      string
	routes    map[string]http.HandlerFunc
	dash      *dashboardHandler
	handler   http.Handler
}

// New validates deps and builds the handler tree. No I/O.
func New(d Deps) (*Server, error) {
	return newServer(d, dashboard.FS)
}

func newServer(d Deps, assets fs.FS) (*Server, error) {
	if d.Config.Token == "" {
		return nil, ErrNoToken
	}
	if err := validateBasePath(d.Config.BasePath); err != nil {
		return nil, err
	}
	if d.Store == nil || d.Sync == nil || d.Cloudflare == nil {
		return nil, errors.New("api: Store, Sync and Cloudflare are required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	s := &Server{
		deps:      d,
		tokenHash: sha256.Sum256([]byte(d.Config.Token)),
		base:      d.Config.BasePath,
	}
	s.routes = s.buildRoutes()
	s.dash = newDashboardHandler(assets, s.base, d.Log)
	s.handler = secureHeaders(http.HandlerFunc(s.route))
	return s, nil
}

// validateBasePath enforces the shape config.Validate already guarantees:
// absolute, no trailing slash, not colliding with the dashboard mount.
func validateBasePath(p string) error {
	switch {
	case !strings.HasPrefix(p, "/") || p == "/" || strings.HasSuffix(p, "/"):
		return fmt.Errorf("api: base_path %q must start with / and not end with /", p)
	case p == dashboardPrefix || strings.HasPrefix(p, dashboardPrefix+"/"):
		return fmt.Errorf("api: base_path %q collides with %s", p, dashboardPrefix)
	case strings.ContainsAny(p, "?#\\ \t\r\n"):
		return fmt.Errorf("api: base_path %q contains invalid characters", p)
	}
	return nil
}

// Handler returns the root handler (for tests).
func (s *Server) Handler() http.Handler { return s.handler }

// route dispatches between the API, the dashboard and the root redirects.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == s.base || strings.HasPrefix(p, s.base+"/"):
		s.serveAPI(w, r, strings.TrimPrefix(p, s.base))
	case p == "/" || p == dashboardPrefix:
		redirectToDashboard(w, r)
	case strings.HasPrefix(p, dashboardPrefix+"/"):
		s.dash.ServeHTTP(w, r)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func redirectToDashboard(w http.ResponseWriter, r *http.Request) {
	setDashboardHeaders(w.Header())
	http.Redirect(w, r, dashboardPrefix+"/", http.StatusFound)
}

// Run listens on Config.Address until ctx is cancelled, then shuts down
// gracefully within ShutdownTimeout. Returns nil on clean shutdown.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.deps.Config.Address)
	if err != nil {
		return fmt.Errorf("api: listen %s: %w", s.deps.Config.Address, err)
	}
	return s.Serve(ctx, ln)
}

// Serve serves on ln until ctx is cancelled (see Run).
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(s.deps.Log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	s.deps.Log.Info("API server listening", "address", ln.Addr().String(), "base_path", s.base)

	select {
	case err := <-errc:
		return fmt.Errorf("api: serve: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("api: shutdown: %w", err)
	}
	<-errc
	return nil
}
