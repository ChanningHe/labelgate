// Package reconcile turns desired state (container labels from all sources)
// into Cloudflare mutations. It is the only package that mutates Cloudflare.
//
// contract.go is a frozen contract: dependencies, settings, status and the
// exported engine surface. Everything else in this directory belongs to the
// reconcile work package.
package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/source"
)

// Store is the persistence subset the reconciler uses (satisfied by *store.SQLite).
type Store interface {
	AllResources(ctx context.Context) ([]model.Resource, error)
	UpsertResource(ctx context.Context, r model.Resource) (model.Resource, error)
	UpdateResource(ctx context.Context, r model.Resource) error
	DeleteResource(ctx context.Context, id int64) error
	ReplaceRejected(ctx context.Context, rows []model.Resource) error
	AgentSnapshots(ctx context.Context) ([]model.Snapshot, error)
}

// AgentPolicy is what one agent may claim. Built from config by PoliciesFromConfig.
type AgentPolicy struct {
	// Allowed is false for agents that are neither configured nor admissible
	// via accept_token: their snapshots are ignored (resources orphan normally).
	Allowed bool
	// Hostnames is a glob allow-list (path.Match on normalized hostnames). Empty = unrestricted.
	// Wildcard hostnames ("*.x") are allowed only by an entry equal to them.
	Hostnames []string
	// Tunnels lists allowed tunnel references (resolved via cloudflare.API.ResolveTunnel).
	Tunnels []string
	// DefaultTunnel is the reference used when a label omits `tunnel`; "" = global default.
	DefaultTunnel string
	// Services allow-lists tunnel origins beyond public/private IP literals:
	// hostname globs, IP addresses and CIDRs. Empty = IP literals only.
	Services []string
}

// AgentPolicies returns the policy for an agent ID.
type AgentPolicies func(agentID string) AgentPolicy

// Settings are the reconciler's static parameters.
type Settings struct {
	InstanceID  string
	LabelPrefix string
	// Interval is the periodic full-cycle interval (config sync.interval).
	Interval    time.Duration
	RemoveDelay time.Duration
	OrphanTTL   time.Duration // 0 = keep orphaned rows forever
	AgentGrace  time.Duration
	// Debounce coalesces triggers: a cycle starts Debounce after the last
	// trigger, but no later than MaxDebounce after the first. Zero = 2s / 10s.
	Debounce    time.Duration
	MaxDebounce time.Duration
	Agents      AgentPolicies
}

// Deps are the reconciler's collaborators.
type Deps struct {
	Settings   Settings
	Cloudflare cloudflare.API
	Store      Store
	// Local is the local Docker source; nil disables local containers (tests).
	Local source.Source
	// PublicIP resolves "auto" targets of local containers.
	PublicIP model.PublicIPFunc
	// Now defaults to time.Now.
	Now func() time.Time
	Log *slog.Logger
}

// SyncState is the overall outcome of the latest cycle.
type SyncState string

const (
	// SyncPending: no cycle has completed since process start.
	SyncPending SyncState = "pending"
	// SyncSuccess: the latest cycle applied everything without error.
	SyncSuccess SyncState = "success"
	// SyncError: the latest cycle had at least one source, store, label,
	// validation, conflict or Cloudflare error.
	SyncError SyncState = "error"
)

// SyncStatus is a snapshot of the reconciler state for the API.
type SyncStatus struct {
	State    SyncState
	LastSync time.Time // end of the latest completed cycle; zero while pending
	// Error summarizes the latest cycle's failures ("" on success), e.g.
	// "3 resources failed; first: dns app.example.com: conflict: ...".
	Error string
}

// Engine is the exported surface of *Reconciler used by cmd and api.
type Engine interface {
	// Run executes cycles until ctx is cancelled: one immediately, then on
	// triggers (debounced), every Settings.Interval, and at the earliest
	// pending cleanup deadline. Starts Local.Watch. Returns nil on ctx
	// cancellation, an error only if the loop cannot run at all.
	Run(ctx context.Context) error
	// Trigger requests a cycle; never blocks; safe from any goroutine.
	Trigger()
	// Status returns the latest SyncStatus; safe from any goroutine.
	Status() SyncStatus
}

var _ Engine = (*Reconciler)(nil)
