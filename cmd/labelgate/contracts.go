package main

import (
	"github.com/channinghe/labelgate/internal/agent"
	"github.com/channinghe/labelgate/internal/api"
	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/reconcile"
	"github.com/channinghe/labelgate/internal/source"
	"github.com/channinghe/labelgate/internal/source/docker"
	"github.com/channinghe/labelgate/internal/store"
)

// Compile-time wiring checks (frozen): each consumer's narrow interface must
// be satisfied by the concrete implementation.
var (
	_ reconcile.Store      = (*store.SQLite)(nil)
	_ agent.HubStore       = (*store.SQLite)(nil)
	_ api.Store            = (*store.SQLite)(nil)
	_ api.SyncReporter     = (*reconcile.Reconciler)(nil)
	_ api.AgentPresence    = (*agent.Hub)(nil)
	_ api.CloudflareHealth = (cloudflare.API)(nil)
	_ cloudflare.API       = (*cloudflare.Client)(nil)
	_ source.Source        = (*docker.Source)(nil)
)
