package api

import (
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/reconcile"
)

const waitingForFirstSync = "waiting for first sync"

// serveAPI handles every request below the base path. sub starts with "/" or is "".
// Only /health is open; everything else (including unknown paths) needs the token.
func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request, sub string) {
	w.Header().Set("Cache-Control", "no-store")
	if sub != "/health" && !s.authorized(r) {
		writeUnauthorized(w)
		return
	}
	h, ok := s.routes[sub]
	switch {
	case !ok:
		writeError(w, http.StatusNotFound, "not found")
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	default:
		h(w, r)
	}
}

func (s *Server) buildRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/health":            s.handleHealth,
		"/overview":          s.handleOverview,
		"/resources/dns":     s.resourceHandler(model.KindDNS),
		"/resources/tunnels": s.resourceHandler(model.KindTunnel),
		"/resources/access":  s.resourceHandler(model.KindAccess),
		"/agents":            s.handleAgents,
		"/version":           s.handleVersion,
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, HealthDTO{Status: "ok"})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, VersionDTO{Version: s.deps.Version, GoVersion: runtime.Version()})
}

// internalError logs the real cause and answers with a generic body.
func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.deps.Log.Error("API request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, errInternal)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts, err := s.deps.Store.CountResources(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	agents, err := s.deps.Store.ListAgents(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	now := s.deps.Now()
	cf := s.deps.Cloudflare.Health()
	writeJSON(w, http.StatusOK, OverviewDTO{
		Resources: OverviewResourcesDTO{
			DNS:           countsDTO(counts[model.KindDNS]),
			TunnelIngress: countsDTO(counts[model.KindTunnel]),
			AccessApp:     countsDTO(counts[model.KindAccess]),
		},
		Agents:      s.agentOverview(agents),
		Sync:        syncDTO(s.deps.Sync.Status()),
		Cloudflare:  OverviewCloudflareDTO{Reachable: cf.Reachable, LastCheck: cf.LastCheck},
		Version:     s.deps.Version,
		Uptime:      formatUptime(now.Sub(s.deps.StartedAt)),
		StartedAt:   s.deps.StartedAt,
		LabelPrefix: s.deps.LabelPrefix,
	})
}

func (s *Server) agentOverview(agents []model.AgentRecord) OverviewAgentsDTO {
	out := OverviewAgentsDTO{Total: len(agents)}
	for _, a := range agents {
		if s.connected(a.ID) {
			out.Connected++
		}
	}
	out.Disconnected = out.Total - out.Connected
	return out
}

func (s *Server) connected(agentID string) bool {
	return s.deps.Agents != nil && s.deps.Agents.Connected(agentID)
}

// countsDTO folds per-status counts into the dashboard's four numbers.
// Total counts every status (incl. pending); pending_cleanup counts as orphaned.
func countsDTO(byStatus map[model.Status]int) ResourceCountsDTO {
	var c ResourceCountsDTO
	for status, n := range byStatus {
		c.Total += n
		switch status {
		case model.StatusActive:
			c.Active += n
		case model.StatusOrphaned, model.StatusPendingCleanup:
			c.Orphaned += n
		case model.StatusError:
			c.Error += n
		}
	}
	return c
}

func syncDTO(st reconcile.SyncStatus) OverviewSyncDTO {
	out := OverviewSyncDTO{LastSync: st.LastSync, Status: string(st.State), Error: st.Error}
	switch st.State {
	case reconcile.SyncPending:
		out.Error = waitingForFirstSync
	case reconcile.SyncSuccess:
		out.Error = ""
	}
	return out
}

// formatUptime renders "3d 5h 20m", "5h 20m" or "20m".
func formatUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d / (24 * time.Hour))
	hours := int(d/time.Hour) % 24
	minutes := int(d/time.Minute) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}
