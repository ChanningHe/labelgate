package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/channinghe/labelgate/internal/model"
)

// ListAgents returns every known agent ordered by ID.
func (s *SQLite) ListAgents(ctx context.Context) ([]model.AgentRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, default_tunnel, public_ip, public_ipv6,
		last_seen, created_at, updated_at FROM agents ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list agents: %w", err)
	}
	defer rows.Close()
	out := []model.AgentRecord{}
	for rows.Next() {
		var a model.AgentRecord
		var seen sql.NullInt64
		var created, updated int64
		if err := rows.Scan(&a.ID, &a.Name, &a.DefaultTunnel, &a.PublicIP, &a.PublicIPv6,
			&seen, &created, &updated); err != nil {
			return nil, fmt.Errorf("store: scan agent: %w", err)
		}
		a.LastSeen, a.CreatedAt, a.UpdatedAt = timeFromNull(seen), timeFromUnix(created), timeFromUnix(updated)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read agents: %w", err)
	}
	return out, nil
}

// canonicalSnapshot returns the canonical JSON of a container list and its
// SHA-256. Containers are sorted by (ID, name) and nil maps are normalized, so
// the hash depends only on content. The input is not modified.
func canonicalSnapshot(containers []model.Container) (string, string, error) {
	cs := make([]model.Container, len(containers))
	for i, c := range containers {
		if c.Labels == nil {
			c.Labels = map[string]string{}
		}
		cs[i] = c
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].ID != cs[j].ID {
			return cs[i].ID < cs[j].ID
		}
		return cs[i].Name < cs[j].Name
	})
	b, err := json.Marshal(cs) // map keys are emitted sorted
	if err != nil {
		return "", "", fmt.Errorf("store: encode snapshot: %w", err)
	}
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:]), nil
}

// RecordAgentReport upserts the agent row. A non-nil snap replaces the stored
// container list; nil keeps the previous one. changed is true when a snapshot
// was stored whose content differs from the previous one (or none existed).
func (s *SQLite) RecordAgentReport(ctx context.Context, rec model.AgentRecord, snap *model.Snapshot) (bool, error) {
	if !model.ValidAgentID(rec.ID) {
		return false, fmt.Errorf("%w: agent id %q", model.ErrInvalid, rec.ID)
	}
	var snapJSON any
	var hash string
	if snap != nil {
		js, h, err := canonicalSnapshot(snap.Containers)
		if err != nil {
			return false, err
		}
		snapJSON, hash = js, h
	}
	now := s.nowUnix()
	var changed bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var prev sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT CASE WHEN snapshot_json IS NULL THEN NULL ELSE snapshot_hash END FROM agents WHERE id = ?`,
			rec.ID).Scan(&prev)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		changed = snap != nil && (!prev.Valid || prev.String != hash)
		_, err = tx.ExecContext(ctx, upsertAgentSQL, rec.ID, rec.Name, rec.DefaultTunnel,
			rec.PublicIP, rec.PublicIPv6, unixOrNull(rec.LastSeen), snapJSON, hash, now, now)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("store: record agent report %q: %w", rec.ID, err)
	}
	return changed, nil
}

// upsertAgentSQL keeps the stored snapshot when the new one is NULL (agent's
// Docker unavailable); created_at is never overwritten.
const upsertAgentSQL = `INSERT INTO agents
	(id, name, default_tunnel, public_ip, public_ipv6, last_seen, snapshot_json, snapshot_hash, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name = excluded.name,
		default_tunnel = excluded.default_tunnel,
		public_ip = excluded.public_ip,
		public_ipv6 = excluded.public_ipv6,
		last_seen = excluded.last_seen,
		snapshot_json = COALESCE(excluded.snapshot_json, agents.snapshot_json),
		snapshot_hash = CASE WHEN excluded.snapshot_json IS NULL
			THEN agents.snapshot_hash ELSE excluded.snapshot_hash END,
		updated_at = excluded.updated_at`

// AgentSnapshots returns one snapshot per known agent, ordered by agent ID.
func (s *SQLite) AgentSnapshots(ctx context.Context) ([]model.Snapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, public_ip, public_ipv6, last_seen, snapshot_json
		FROM agents ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list agent snapshots: %w", err)
	}
	defer rows.Close()
	out := []model.Snapshot{}
	for rows.Next() {
		var sn model.Snapshot
		var seen sql.NullInt64
		var raw sql.NullString
		if err := rows.Scan(&sn.AgentID, &sn.PublicIP, &sn.PublicIPv6, &seen, &raw); err != nil {
			return nil, fmt.Errorf("store: scan agent snapshot: %w", err)
		}
		sn.TakenAt = timeFromNull(seen)
		if raw.Valid {
			sn.Complete = true
			if err := json.Unmarshal([]byte(raw.String), &sn.Containers); err != nil {
				return nil, fmt.Errorf("store: decode snapshot of agent %q: %w", sn.AgentID, err)
			}
		}
		out = append(out, sn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read agent snapshots: %w", err)
	}
	return out, nil
}
