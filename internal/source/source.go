// Package source defines where container snapshots come from.
//
// source.go is a frozen contract. The local Docker implementation is in
// source/docker; agents deliver snapshots through the agent package instead.
package source

import (
	"context"

	"github.com/channinghe/labelgate/internal/model"
)

// Source lists containers and signals changes.
type Source interface {
	// Snapshot returns all running containers (filtered by docker.filter_label)
	// with Complete=true and AgentID=model.LocalAgentID. On error the caller
	// must treat the source as unknown (hold its resources), never as empty.
	Snapshot(ctx context.Context) (model.Snapshot, error)
	// Watch blocks until ctx is done, calling notify (must not block) whenever
	// containers may have changed: on start/die/destroy/rename/update events,
	// every poll interval, and once right after each (re)subscription to the
	// event stream so nothing between a list and the subscription is lost.
	// Stream errors are retried with backoff forever; Watch returns ctx.Err()
	// when ctx is cancelled, and a non-nil error only for unrecoverable setup failures.
	Watch(ctx context.Context, notify func()) error
}
