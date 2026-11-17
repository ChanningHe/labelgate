package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/channinghe/labelgate/internal/model"
)

// resourceFilter is the single static WHERE clause shared by ListResources
// and its total count. Each filter is switched off by an "unset" bind value
// (?N = ”), so no SQL is assembled from caller input.
//
//	?1 kind, ?2 status, ?3 hostname, ?4 agent filter enabled, ?5 agent id
const resourceFilter = `WHERE (?1 = '' OR kind = ?1)
	AND (?2 = '' OR status = ?2)
	AND (?3 = '' OR hostname = ?3)
	AND (?4 = 0 OR agent_id = ?5)`

// AllResources returns every row (owners and rejected), ordered by ID.
func (s *SQLite) AllResources(ctx context.Context) ([]model.Resource, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+selectCols+" FROM resources ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("store: list all resources: %w", err)
	}
	return collectResources(rows)
}

// collectResources drains and closes rows.
func collectResources(rows *sql.Rows) ([]model.Resource, error) {
	defer rows.Close()
	out := []model.Resource{}
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan resource: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read resources: %w", err)
	}
	return out, nil
}

// ListResources returns one page plus the true total for the filter. Page and
// total are read in one transaction so they describe the same state.
func (s *SQLite) ListResources(ctx context.Context, q ResourceQuery) (Page, error) {
	if q.Limit < 1 || q.Limit > MaxLimit || q.Offset < 0 {
		return Page{}, fmt.Errorf("%w: limit %d (want 1..%d) offset %d (want >= 0)",
			model.ErrInvalid, q.Limit, MaxLimit, q.Offset)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page{}, fmt.Errorf("store: begin read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	filter := []any{string(q.Kind), string(q.Status), q.Hostname, boolInt(q.HasAgentID), q.AgentID}
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM resources "+resourceFilter, filter...).Scan(&total); err != nil {
		return Page{}, fmt.Errorf("store: count resources: %w", err)
	}
	rows, err := tx.QueryContext(ctx,
		"SELECT "+selectCols+" FROM resources "+resourceFilter+" ORDER BY hostname, id LIMIT ?6 OFFSET ?7",
		append(filter, q.Limit, q.Offset)...)
	if err != nil {
		return Page{}, fmt.Errorf("store: list resources: %w", err)
	}
	list, err := collectResources(rows)
	if err != nil {
		return Page{}, err
	}
	return Page{Resources: list, Total: total}, nil
}

// CountResources counts rows grouped by kind and status.
func (s *SQLite) CountResources(ctx context.Context) (Counts, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT kind, status, COUNT(*) FROM resources GROUP BY kind, status")
	if err != nil {
		return nil, fmt.Errorf("store: count resources: %w", err)
	}
	defer rows.Close()
	out := Counts{}
	for rows.Next() {
		var kind, status string
		var n int
		if err := rows.Scan(&kind, &status, &n); err != nil {
			return nil, fmt.Errorf("store: scan counts: %w", err)
		}
		k := model.Kind(kind)
		if out[k] == nil {
			out[k] = map[model.Status]int{}
		}
		out[k][model.Status(status)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read counts: %w", err)
	}
	return out, nil
}

// CountResourcesByAgent counts non-rejected rows per agent ID ("" = local).
func (s *SQLite) CountResourcesByAgent(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT agent_id, COUNT(*) FROM resources WHERE rejected = 0 GROUP BY agent_id")
	if err != nil {
		return nil, fmt.Errorf("store: count resources by agent: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("store: scan agent counts: %w", err)
		}
		out[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read agent counts: %w", err)
	}
	return out, nil
}

// UpsertResource inserts or updates the non-rejected row for (r.Kind, r.Key)
// and returns the row as persisted (read back, so ID and times are exact).
func (s *SQLite) UpsertResource(ctx context.Context, r model.Resource) (model.Resource, error) {
	if err := validateResource(r); err != nil {
		return model.Resource{}, err
	}
	now := s.nowUnix()
	args, err := resourceArgs(r, false, now)
	if err != nil {
		return model.Resource{}, err
	}
	var out model.Resource
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRowContext(ctx, upsertSQL, append(args, now)...).Scan(&id); err != nil {
			return err
		}
		var err error
		out, err = scanResource(tx.QueryRowContext(ctx, "SELECT "+selectCols+" FROM resources WHERE id = ?", id))
		return err
	})
	if err != nil {
		return model.Resource{}, fmt.Errorf("store: upsert %s %q: %w", r.Kind, r.Key, classify(err))
	}
	return out, nil
}

// UpdateResource overwrites row r.ID (all columns except ID and CreatedAt);
// UpdatedAt is set to now.
func (s *SQLite) UpdateResource(ctx context.Context, r model.Resource) error {
	if err := validateResource(r); err != nil {
		return err
	}
	args, err := resourceArgs(r, r.Rejected, s.nowUnix())
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, updateSQL, append(args, r.ID)...)
	if err != nil {
		return fmt.Errorf("store: update resource %d: %w", r.ID, classify(err))
	}
	return expectOneRow(res, "update", r.ID)
}

// DeleteResource deletes row id.
func (s *SQLite) DeleteResource(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM resources WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("store: delete resource %d: %w", id, err)
	}
	return expectOneRow(res, "delete", id)
}

// expectOneRow turns "no row affected" into model.ErrNotFound.
func expectOneRow(res sql.Result, op string, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: %s resource %d: rows affected: %w", op, id, err)
	}
	if n != 1 {
		return fmt.Errorf("store: %s resource %d: %w", op, id, model.ErrNotFound)
	}
	return nil
}

// ReplaceRejected atomically replaces all rejected rows with rows.
func (s *SQLite) ReplaceRejected(ctx context.Context, rows []model.Resource) error {
	if err := validateRejected(rows); err != nil {
		return err
	}
	now := s.nowUnix()
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM resources WHERE rejected = 1"); err != nil {
			return err
		}
		for _, r := range rows {
			r.Status = model.StatusError
			args, err := resourceArgs(r, true, now)
			if err != nil {
				return err
			}
			created := now
			if !r.CreatedAt.IsZero() {
				created = r.CreatedAt.Unix()
			}
			if _, err := tx.ExecContext(ctx, insertSQL, append(args, created)...); err != nil {
				return fmt.Errorf("%s %q: %w", r.Kind, r.Key, classify(err))
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("store: replace rejected: %w", err)
	}
	return nil
}

// validateRejected checks each row and rejects duplicate (kind, key, owner).
func validateRejected(rows []model.Resource) error {
	// Same columns as ux_resources_rejected.
	type claim struct{ kind, key, agent, container, service string }
	seen := make(map[claim]struct{}, len(rows))
	for _, r := range rows {
		r.Status = model.StatusError
		if err := validateResource(r); err != nil {
			return err
		}
		k := claim{string(r.Kind), r.Key, r.Owner.AgentID, r.Owner.ContainerName, r.Owner.Service}
		if _, dup := seen[k]; dup {
			return fmt.Errorf("%w: duplicate rejected claim %s %q by %q", model.ErrInvalid, r.Kind, r.Key, r.Owner.Key())
		}
		seen[k] = struct{}{}
	}
	return nil
}

// inTx runs fn in a write transaction on the single connection.
func (s *SQLite) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return inTx(ctx, s.db, fn)
}
