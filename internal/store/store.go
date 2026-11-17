// Package store persists labelgate state in SQLite.
//
// store.go is a frozen contract. Consumers (reconcile, agent, api) declare
// narrow subsets of Store in their own packages with identical signatures;
// cmd/labelgate asserts *SQLite satisfies each.
package store

import (
	"context"
	"errors"

	"github.com/channinghe/labelgate/internal/model"
)

// Store is the complete persistence API.
//
// Contract:
//   - Times are stored as INTEGER unix seconds UTC; returned times are UTC with
//     second precision. Zero time <-> NULL.
//   - Single writer: all writes are serialized; methods are safe for concurrent use.
//   - Errors wrap model.ErrNotFound / model.ErrConflict / model.ErrInvalid where applicable.
//   - Returned slices and values are fresh copies.
type Store interface {
	// Close flushes and closes the database.
	Close() error

	// InstanceID returns the persisted instance ID, generating (8 random hex
	// chars, crypto/rand) and persisting one on first call.
	InstanceID(ctx context.Context) (string, error)
	// SetInstanceID persists id (config override). id must satisfy model.ValidInstanceID.
	SetInstanceID(ctx context.Context, id string) error

	// AllResources returns every row (owners and rejected), ordered by ID.
	AllResources(ctx context.Context) ([]model.Resource, error)
	// ListResources returns one page plus the true total for the filter.
	// q.Limit must be 1..MaxLimit and q.Offset >= 0, else ErrInvalid.
	// Ordered by hostname, then ID (stable for pagination).
	ListResources(ctx context.Context, q ResourceQuery) (Page, error)
	// CountResources counts rows grouped by kind and status.
	CountResources(ctx context.Context) (Counts, error)
	// CountResourcesByAgent counts non-rejected rows per agent ID ("" = local).
	CountResourcesByAgent(ctx context.Context) (map[string]int, error)
	// UpsertResource inserts or updates the non-rejected row for (r.Kind,
	// r.Key); r.ID and r.Rejected are ignored (Rejected forced false).
	// CreatedAt is preserved on update; UpdatedAt set to now. Returns the
	// persisted row including its ID.
	UpsertResource(ctx context.Context, r model.Resource) (model.Resource, error)
	// UpdateResource overwrites row r.ID (all columns except CreatedAt).
	// ErrNotFound if no row was affected.
	UpdateResource(ctx context.Context, r model.Resource) error
	// DeleteResource deletes row id. ErrNotFound if no row was affected.
	DeleteResource(ctx context.Context, id int64) error
	// ReplaceRejected atomically replaces ALL rejected rows with rows (each
	// stored with Rejected=true, Status=error). Rows are unique per
	// (Kind, Key, Owner.Key()); duplicates in the input -> ErrInvalid.
	ReplaceRejected(ctx context.Context, rows []model.Resource) error

	// ListAgents returns every known agent ordered by ID.
	ListAgents(ctx context.Context) ([]model.AgentRecord, error)
	// RecordAgentReport upserts the agent row (Name, DefaultTunnel, PublicIP,
	// PublicIPv6, LastSeen from rec). If snap != nil its containers replace
	// the stored snapshot; snap == nil keeps the previous snapshot (agent's
	// Docker unavailable). changed reports whether the stored container set
	// differs from before (by content hash).
	RecordAgentReport(ctx context.Context, rec model.AgentRecord, snap *model.Snapshot) (changed bool, err error)
	// AgentSnapshots returns one snapshot per known agent: TakenAt = LastSeen,
	// Complete = a container list has ever been stored. Ordered by agent ID.
	AgentSnapshots(ctx context.Context) ([]model.Snapshot, error)
}

// Pagination bounds enforced by the API (and validated by ListResources).
const (
	DefaultLimit = 500
	MaxLimit     = 1000
)

// ResourceQuery filters ListResources. Empty fields do not filter.
// AgentID filters only when HasAgentID is set (so "" can select local resources).
type ResourceQuery struct {
	Kind       model.Kind
	Status     model.Status
	Hostname   string
	AgentID    string
	HasAgentID bool
	Limit      int
	Offset     int
}

// Page is one page of resources.
type Page struct {
	Resources []model.Resource
	Total     int // rows matching the filter, ignoring Limit/Offset
}

// Counts maps kind -> status -> number of rows.
type Counts map[model.Kind]map[model.Status]int

// ErrLegacyTooOld is returned when a legacy database cannot be migrated.
var ErrLegacyTooOld = errors.New("store: legacy database schema not recognized")
