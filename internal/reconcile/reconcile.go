package reconcile

import (
	"context"
	"errors"
)

var errNotImplemented = errors.New("reconcile: not implemented")

// Reconciler serializes all Cloudflare mutations in one goroutine.
//
// One cycle (see dev/docs/v0.3-design.md §6):
//
//	collect -> parse/validate -> resolve claims -> observe live state ->
//	plan (pure) -> write-ahead claims -> execute -> persist per-resource status
//
// Suggested files (all owned by WP4): loop.go (Run/Trigger/debounce/timers),
// collect.go, validate.go (agent policy, SSRF guard, target resolution),
// resolve.go (claims, conflicts, access binding), observe.go, plan.go (pure),
// tunnelmerge.go (pure GET-merge-PUT), execute.go, status.go.
type Reconciler struct{}

// New validates deps (non-nil Cloudflare, Store, PublicIP, Agents; valid
// InstanceID) and returns a Reconciler. It performs no I/O.
func New(d Deps) (*Reconciler, error) { return nil, errNotImplemented }

// Run implements Engine.
func (r *Reconciler) Run(ctx context.Context) error { return errNotImplemented }

// Trigger implements Engine.
func (r *Reconciler) Trigger() {}

// Status implements Engine.
func (r *Reconciler) Status() SyncStatus { return SyncStatus{State: SyncPending} }
