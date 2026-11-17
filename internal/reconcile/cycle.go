package reconcile

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

// runCycle executes one cycle, publishes its SyncStatus and returns the
// earliest future deadline (cleanup, purge, agent grace expiry).
func (r *Reconciler) runCycle(ctx context.Context) time.Time {
	issues, deadline, err := r.cycle(ctx, r.now())
	if ctx.Err() != nil {
		return time.Time{} // shutting down: the cycle is incomplete, keep the last status
	}
	if err != nil { // an aborted cycle is the most important problem: report it first
		issues = append([]string{err.Error()}, issues...)
	}
	for _, msg := range issues {
		r.log.Warn("reconcile problem", "problem", msg)
	}
	st := SyncStatus{State: SyncSuccess, LastSync: r.now()}
	if len(issues) > 0 {
		st.State = SyncError
		st.Error = fmt.Sprintf("%d problems; first: %s", len(issues), issues[0])
	}
	r.setStatus(st)
	return deadline
}

// cycle runs collect -> resolve -> observe -> plan -> execute.
func (r *Reconciler) cycle(ctx context.Context, now time.Time) ([]string, time.Time, error) {
	col, err := r.collect(ctx, now)
	if err != nil {
		return nil, time.Time{}, storeError{err}
	}
	issues := col.Issues
	all, err := r.store.AllResources(ctx)
	if err != nil {
		return issues, col.Deadline, storeError{fmt.Errorf("load resources: %w", err)}
	}
	tracked, prevRejected := splitRows(all)
	held := newHoldSet(col.Sources, col.Holds, tracked, col.Claims)
	res := resolve(col.Claims, tracked, held)
	rejected := dedupeRejected(concatRows(col.Rejected, res.Rejected, heldRejected(prevRejected, held)))
	issues = append(issues, rejectionIssues(rejected)...)
	if !rejectedEqual(prevRejected, rejected) {
		if err := r.store.ReplaceRejected(ctx, rejected); err != nil {
			return issues, col.Deadline, storeError{fmt.Errorf("replace rejected: %w", err)}
		}
	}
	winners, live, err := r.observe(ctx, res.Winners, res.Tracked, held)
	if err != nil {
		return issues, col.Deadline, err
	}
	p := buildPlan(planInput{
		Now: now, InstanceID: r.settings.InstanceID, RemoveDelay: r.settings.RemoveDelay,
		OrphanTTL: r.settings.OrphanTTL, Winners: winners, Tracked: res.Tracked, Held: held, Live: live,
	})
	e := newExecutor(r, now, res.Tracked)
	err = e.run(ctx, res.Rekeyed, p)
	return append(issues, e.issues...), earliest(p.NextDeadline, col.Deadline), err
}

func splitRows(all []model.Resource) (tracked, rejected []model.Resource) {
	for _, r := range all {
		if r.Rejected {
			rejected = append(rejected, r)
		} else {
			tracked = append(tracked, r)
		}
	}
	return tracked, rejected
}

func concatRows(parts ...[]model.Resource) []model.Resource {
	var out []model.Resource
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// heldRejected keeps the previous rejections of held sources: they made no
// claims this cycle, which must not look like a resolved conflict.
func heldRejected(prev []model.Resource, held holdSet) []model.Resource {
	var out []model.Resource
	for _, r := range prev {
		if held.agents[r.Owner.AgentID] {
			out = append(out, r)
		}
	}
	return out
}

func rejectionIssues(rows []model.Resource) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprintf("rejected %s (%s): %s", keyOf(r), describeOwner(r.Owner), r.LastError))
	}
	return out
}

// rejectedEqual reports whether persisting next would change nothing.
func rejectedEqual(prev, next []model.Resource) bool {
	if len(prev) != len(next) {
		return false
	}
	a, b := sortRows(prev), sortRows(next)
	for i := range a {
		if a[i].Owner != b[i].Owner || a[i].LastError != b[i].LastError ||
			!reflect.DeepEqual(specOf(a[i]), specOf(b[i])) {
			return false
		}
	}
	return true
}

func earliest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero() || a.Before(b):
		return a
	}
	return b
}
