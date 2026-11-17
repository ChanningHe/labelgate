package reconcile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

// storeError marks a persistence failure: the cycle aborts (§6.10).
type storeError struct{ err error }

func (e storeError) Error() string { return "store: " + e.err.Error() }
func (e storeError) Unwrap() error { return e.err }

// executor runs a plan. CF failures are recorded per row and never abort;
// store failures and ctx cancellation abort.
type executor struct {
	r        *Reconciler
	now      time.Time
	rows     map[rowKey]model.Resource // persisted state
	issues   []string
	deleted  map[string]bool // DNS record IDs deleted this cycle
	tunnelOK map[string]bool // tunnels whose configuration is in place
}

func newExecutor(r *Reconciler, now time.Time, tracked []model.Resource) *executor {
	return &executor{
		r: r, now: now, rows: indexRows(tracked),
		deleted: map[string]bool{}, tunnelOK: map[string]bool{},
	}
}

// run executes the plan in the order of §6.10.
func (e *executor) run(ctx context.Context, rekeyed []model.Resource, p plan) error {
	phases := []func(context.Context) error{
		func(ctx context.Context) error { return e.rekey(ctx, rekeyed) },
		func(ctx context.Context) error { return e.writeRows(ctx, p.Upserts, true) },
		func(ctx context.Context) error { return e.writeRows(ctx, p.Transitions, false) },
		func(ctx context.Context) error { return e.dnsOps(ctx, p.Superseded) },
		func(ctx context.Context) error { return e.tunnelOps(ctx, p.Tunnels) },
		func(ctx context.Context) error { return e.dnsOps(ctx, p.DNS) },
		func(ctx context.Context) error { return e.accessOps(ctx, p.Access) },
		func(ctx context.Context) error { return e.dnsOps(ctx, p.CleanupDNS) },
		func(ctx context.Context) error { return e.accessDeletes(ctx, p.CleanupAccess) },
		func(ctx context.Context) error { return e.purge(ctx, p.Purge) },
	}
	for _, phase := range phases {
		if err := phase(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) writeRows(ctx context.Context, rows []model.Resource, upsert bool) error {
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !upsert {
			if err := e.persist(ctx, row); err != nil {
				return err
			}
			continue
		}
		saved, err := e.r.store.UpsertResource(ctx, row)
		if err != nil {
			return storeError{fmt.Errorf("upsert %s: %w", keyOf(row), err)}
		}
		e.rows[keyOf(saved)] = saved
	}
	return nil
}

// rekey persists legacy rows whose key was rewritten (always written: the
// in-memory state already carries the new key).
func (e *executor) rekey(ctx context.Context, rows []model.Resource) error {
	for _, row := range rows {
		if err := e.r.store.UpdateResource(ctx, row); err != nil {
			return storeError{fmt.Errorf("rekey %s: %w", keyOf(row), err)}
		}
	}
	return nil
}

// persist writes row if it differs from the persisted state.
func (e *executor) persist(ctx context.Context, row model.Resource) error {
	k := keyOf(row)
	if old, ok := e.rows[k]; ok && sameRow(old, row) {
		return nil
	}
	if err := e.r.store.UpdateResource(ctx, row); err != nil {
		return storeError{fmt.Errorf("update %s: %w", k, err)}
	}
	e.rows[k] = row
	return nil
}

func (e *executor) drop(ctx context.Context, k rowKey) error {
	row, ok := e.rows[k]
	if !ok {
		return nil
	}
	if err := e.r.store.DeleteResource(ctx, row.ID); err != nil && !errors.Is(err, model.ErrNotFound) {
		return storeError{fmt.Errorf("delete %s: %w", k, err)}
	}
	delete(e.rows, k)
	return nil
}

// succeed marks a row active. cfid "" keeps the current Cloudflare ID.
func (e *executor) succeed(ctx context.Context, k rowKey, cfid string, mutated bool) error {
	row, ok := e.rows[k]
	if !ok {
		return nil
	}
	return e.succeedRow(ctx, row, cfid, mutated)
}

func (e *executor) succeedRow(ctx context.Context, row model.Resource, cfid string, mutated bool) error {
	if cfid != "" {
		row.CFID = cfid
	}
	row.Status, row.LastError, row.Legacy = model.StatusActive, "", false
	if mutated || row.SyncedAt.IsZero() {
		row.SyncedAt = e.now
	}
	return e.persist(ctx, row)
}

// fail records msg on the row and as a cycle issue.
func (e *executor) fail(ctx context.Context, k rowKey, msg string) error {
	row, ok := e.rows[k]
	if !ok {
		e.issues = append(e.issues, k.String()+": "+msg)
		return nil
	}
	return e.failRow(ctx, row, msg)
}

func (e *executor) failRow(ctx context.Context, row model.Resource, msg string) error {
	e.issues = append(e.issues, keyOf(row).String()+": "+msg)
	row.Status, row.LastError = model.StatusError, msg
	return e.persist(ctx, row)
}

func (e *executor) purge(ctx context.Context, keys []rowKey) error {
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.drop(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) tunnelOps(ctx context.Context, ops []tunnelOp) error {
	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.tunnel(ctx, op); err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) tunnel(ctx context.Context, op tunnelOp) error {
	for _, f := range op.Failed {
		if err := e.fail(ctx, f.Key, f.Msg); err != nil {
			return err
		}
	}
	if op.Blocked {
		return nil
	}
	if op.Put {
		if err := e.r.cf.PutTunnelConfig(ctx, op.Tunnel, op.Config); err != nil {
			msg := "put tunnel configuration: " + err.Error()
			for _, k := range append(append([]rowKey(nil), op.Applied...), op.Deletes...) {
				if err := e.fail(ctx, k, msg); err != nil {
					return err
				}
			}
			return nil
		}
	}
	e.tunnelOK[op.Tunnel.ID] = true
	for _, k := range op.Applied {
		if err := e.succeed(ctx, k, "", op.Put); err != nil {
			return err
		}
	}
	for _, k := range op.Deletes {
		if err := e.drop(ctx, k); err != nil {
			return err
		}
	}
	return nil
}
