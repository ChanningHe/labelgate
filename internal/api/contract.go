package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/reconcile"
	"github.com/channinghe/labelgate/internal/store"
)

// Store is the read-only persistence subset the API uses (satisfied by *store.SQLite).
type Store interface {
	ListResources(ctx context.Context, q store.ResourceQuery) (store.Page, error)
	CountResources(ctx context.Context) (store.Counts, error)
	CountResourcesByAgent(ctx context.Context) (map[string]int, error)
	ListAgents(ctx context.Context) ([]model.AgentRecord, error)
}

// SyncReporter exposes reconciler status (satisfied by *reconcile.Reconciler).
type SyncReporter interface {
	Status() reconcile.SyncStatus
}

// AgentPresence exposes live agent sessions (satisfied by *agent.Hub).
type AgentPresence interface {
	Connected(agentID string) bool
}

// CloudflareHealth exposes API reachability (satisfied by cloudflare.API).
type CloudflareHealth interface {
	Health() cloudflare.Health
}

// Deps are the API server's collaborators.
type Deps struct {
	Config config.APIConfig
	Store  Store
	Sync   SyncReporter
	// Agents may be nil (agent server disabled): every agent reports connected=false.
	Agents      AgentPresence
	Cloudflare  CloudflareHealth
	Version     string
	LabelPrefix string
	StartedAt   time.Time
	AgentGrace  time.Duration
	Now         func() time.Time
	Log         *slog.Logger
}
