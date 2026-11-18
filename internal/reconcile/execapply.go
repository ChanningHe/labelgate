package reconcile

import (
	"context"
	"errors"

	"github.com/channinghe/labelgate/internal/cloudflare"
)

func (e *executor) dnsOps(ctx context.Context, ops []dnsOp) error {
	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.dns(ctx, op); err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) dns(ctx context.Context, op dnsOp) error {
	switch op.Action {
	case dnsFail:
		return e.fail(ctx, op.Key, op.Msg)
	case dnsDrop:
		if op.Msg != "" {
			e.r.log.Warn(op.Msg, "resource", op.Key.String())
		}
		return e.drop(ctx, op.Key)
	case dnsDelete:
		err := e.r.cf.DeleteDNSRecord(ctx, op.Zone, op.Record.ID)
		if err != nil && !errors.Is(err, cloudflare.ErrNotFound) {
			return e.fail(ctx, op.Key, "cannot delete DNS record: "+err.Error())
		}
		e.deleted[op.Record.ID] = true
		return e.drop(ctx, op.Key)
	}
	if op.WaitTunnel != "" && !e.tunnelOK[op.WaitTunnel] {
		return e.fail(ctx, op.Key, "waiting for tunnel "+op.WaitTunnel+": its configuration could not be updated (see the tunnel route's error)")
	}
	for _, id := range op.Blockers {
		if !e.deleted[id] {
			return e.fail(ctx, op.Key, "waiting for the previous record (id "+id+") on this hostname to be deleted first")
		}
	}
	return e.applyDNS(ctx, op)
}

func (e *executor) applyDNS(ctx context.Context, op dnsOp) error {
	switch op.Action {
	case dnsCreate:
		rec, err := e.r.cf.CreateDNSRecord(ctx, op.Zone, op.Record)
		if errors.Is(err, cloudflare.ErrAlreadyExists) {
			return e.fail(ctx, op.Key, createRefused(describeKey(op.Key), dnsRecordsPage))
		}
		if err != nil {
			return e.fail(ctx, op.Key, "cannot create DNS record: "+err.Error())
		}
		return e.succeed(ctx, op.Key, rec.ID, true)
	case dnsUpdate:
		if _, err := e.r.cf.UpdateDNSRecord(ctx, op.Zone, op.Record); err != nil {
			return e.fail(ctx, op.Key, "cannot update DNS record: "+err.Error())
		}
		return e.succeed(ctx, op.Key, op.Record.ID, true)
	default:
		return e.succeed(ctx, op.Key, op.Record.ID, false)
	}
}

func (e *executor) accessOps(ctx context.Context, ops []accessOp) error {
	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.access(ctx, op); err != nil {
			return err
		}
	}
	return nil
}

// policyIDList is the row's owned policy list: applied IDs in index order
// (missing ones skipped) followed by policies still to detach.
func policyIDList(ids, detach []string) []string {
	var out []string
	for _, id := range ids {
		if id != "" {
			out = append(out, id)
		}
	}
	return append(out, detach...)
}

// access applies one Access op: policies first (IDs persisted right after
// each create), then the app, then detached policies are deleted.
func (e *executor) access(ctx context.Context, op accessOp) error {
	if op.Fail != "" {
		return e.fail(ctx, op.Key, op.Fail)
	}
	ids := make([]string, len(op.Steps))
	for i, s := range op.Steps {
		ids[i] = s.Policy.ID
	}
	for i, s := range op.Steps {
		ok, err := e.writePolicy(ctx, op, s, ids, i)
		if err != nil || !ok {
			return err
		}
	}
	app := op.App
	app.PolicyIDs = append([]string(nil), ids...)
	cfid, ok, err := e.writeApp(ctx, op, app)
	if err != nil || !ok {
		return err
	}
	remaining, err := e.detach(ctx, op)
	if err != nil {
		return err
	}
	row := e.rows[op.Key]
	row.CFID, row.PolicyIDs = cfid, policyIDList(ids, remaining)
	if len(remaining) > 0 {
		return e.failRow(ctx, row, "cannot delete an Access policy that is no longer used; retrying next sync")
	}
	return e.succeedRow(ctx, row, cfid, len(op.Detach) > 0 || op.Mode != appNoop || anyWrite(op.Steps))
}

func anyWrite(steps []policyStep) bool {
	for _, s := range steps {
		if s.Write {
			return true
		}
	}
	return false
}

// writePolicy creates or updates one policy. ok=false: the row failed.
func (e *executor) writePolicy(ctx context.Context, op accessOp, s policyStep, ids []string, i int) (bool, error) {
	if !s.Write {
		return true, nil
	}
	if s.Policy.ID != "" {
		if _, err := e.r.cf.UpdateAccessPolicy(ctx, op.Account, s.Policy); err != nil {
			return false, e.fail(ctx, op.Key, "cannot update Access policy: "+err.Error())
		}
		return true, nil
	}
	p, err := e.r.cf.CreateAccessPolicy(ctx, op.Account, s.Policy)
	if err != nil {
		return false, e.fail(ctx, op.Key, "cannot create Access policy: "+err.Error())
	}
	ids[i] = p.ID
	row := e.rows[op.Key]
	row.PolicyIDs = policyIDList(ids, op.Detach)
	return true, e.persist(ctx, row)
}

// writeApp creates or updates the application; an update failure never
// falls back to creating a second app. ok=false: the row failed.
func (e *executor) writeApp(ctx context.Context, op accessOp, app cloudflare.AccessApp) (string, bool, error) {
	switch op.Mode {
	case appCreate:
		a, err := e.r.cf.CreateAccessApp(ctx, op.Account, app)
		if errors.Is(err, cloudflare.ErrAlreadyExists) {
			return "", false, e.fail(ctx, op.Key, createRefused(describeKey(op.Key), accessAppsPage))
		}
		if err != nil {
			return "", false, e.fail(ctx, op.Key, "cannot create Access application: "+err.Error())
		}
		row := e.rows[op.Key]
		row.CFID = a.ID
		return a.ID, true, e.persist(ctx, row)
	case appUpdate:
		if _, err := e.r.cf.UpdateAccessApp(ctx, op.Account, app); err != nil {
			return "", false, e.fail(ctx, op.Key, "cannot update Access application: "+err.Error())
		}
	}
	return app.ID, true, nil
}

// detach deletes extra owned policies; it returns those that remain.
func (e *executor) detach(ctx context.Context, op accessOp) ([]string, error) {
	var remaining []string
	for _, id := range op.Detach {
		if err := e.r.cf.DeleteAccessPolicy(ctx, op.Account, id); err != nil && !errors.Is(err, cloudflare.ErrNotFound) {
			e.r.log.Warn("cannot delete an Access policy that is no longer used; retrying next sync", "resource", op.Key.String(), "policy", id, "error", err)
			remaining = append(remaining, id)
		}
	}
	return remaining, ctx.Err()
}

func (e *executor) accessDeletes(ctx context.Context, ds []accessDelete) error {
	for _, d := range ds {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.accessDelete(ctx, d); err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) accessDelete(ctx context.Context, d accessDelete) error {
	if d.Fail != "" {
		return e.fail(ctx, d.Key, d.Fail)
	}
	if d.Warn != "" {
		e.r.log.Warn(d.Warn, "resource", d.Key.String())
	}
	if d.AppID != "" {
		err := e.r.cf.DeleteAccessApp(ctx, d.Account, d.AppID)
		if err != nil && !errors.Is(err, cloudflare.ErrNotFound) {
			return e.fail(ctx, d.Key, "cannot delete Access application: "+err.Error())
		}
		row := e.rows[d.Key]
		row.CFID = ""
		if err := e.persist(ctx, row); err != nil {
			return err
		}
	}
	for i, id := range d.Policies {
		err := e.r.cf.DeleteAccessPolicy(ctx, d.Account, id)
		if err != nil && !errors.Is(err, cloudflare.ErrNotFound) {
			row := e.rows[d.Key]
			row.PolicyIDs = append([]string(nil), d.Policies[i:]...)
			return e.failRow(ctx, row, "cannot delete Access policy: "+err.Error())
		}
	}
	return e.drop(ctx, d.Key)
}
