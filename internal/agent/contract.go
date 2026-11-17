package agent

import (
	"context"

	"github.com/channinghe/labelgate/internal/model"
)

// HubStore is the persistence subset the Hub uses (satisfied by *store.SQLite).
type HubStore interface {
	RecordAgentReport(ctx context.Context, rec model.AgentRecord, snap *model.Snapshot) (changed bool, err error)
}

// Presence reports live agent sessions (used by the API).
type Presence interface {
	// Connected reports whether agentID has an authenticated live session.
	Connected(agentID string) bool
}

var _ Presence = (*Hub)(nil)
