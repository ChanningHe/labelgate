// Package api provides the HTTP API server and dashboard for labelgate.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/agent"
	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/reconciler"
	"github.com/channinghe/labelgate/internal/storage"
)

// Config holds configuration for the API server.
type Config struct {
	Address     string
	BasePath    string
	Token       string
	Storage     storage.Storage
	Reconciler  *reconciler.Reconciler
	AgentServer *agent.Server
	CredManager *cloudflare.CredentialManager
	Version     string
}

// Server is the HTTP API server.
type Server struct {
	config *Config
	server *http.Server
}

// NewServer creates a new API server.
func NewServer(cfg *Config) *Server {
	mux := http.NewServeMux()
	s := &Server{config: cfg}

	// Health endpoint is registered outside auth middleware so Docker
	// health checks work even when api.token is configured.
	mux.HandleFunc("GET "+cfg.BasePath+"/health", s.handleHealth)

	// Apply auth middleware to all other API routes
	apiHandler := tokenAuth(cfg.Token, s.apiMux(cfg.BasePath))

	// Dashboard static files (no auth required for SPA assets)
	dashboardHandler := newDashboardHandler()

	// Root mux: route /api/* to API, /dashboard/* to SPA, / redirects to dashboard
	mux.Handle(cfg.BasePath+"/", apiHandler)
	mux.Handle("/dashboard/", dashboardHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/dashboard" {
			http.Redirect(w, r, "/dashboard/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	s.server = &http.Server{
		Addr:    cfg.Address,
		Handler: mux,
	}

	return s
}

// apiMux creates the API route multiplexer.
func (s *Server) apiMux(basePath string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET "+basePath+"/overview", s.handleOverview)
	mux.HandleFunc("GET "+basePath+"/resources/dns", s.handleDNS)
	mux.HandleFunc("GET "+basePath+"/resources/tunnels", s.handleTunnels)
	mux.HandleFunc("GET "+basePath+"/resources/access", s.handleAccess)
	mux.HandleFunc("GET "+basePath+"/agents", s.handleAgents)
	mux.HandleFunc("GET "+basePath+"/version", s.handleVersion)
	// Note: /health is registered outside apiMux (no auth required)

	return mux
}

// Start starts the API server and blocks until the context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	go func() {
		log.Info().Str("address", s.config.Address).Msg("Starting API server")
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("API server error")
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return s.server.Shutdown(shutdownCtx)
}
