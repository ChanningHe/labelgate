package reconcile

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Run implements Engine: one cycle immediately, then on debounced triggers,
// every Settings.Interval and at the earliest pending deadline. It starts
// Local.Watch and waits for it before returning.
func (r *Reconciler) Run(ctx context.Context) error {
	if !r.running.CompareAndSwap(false, true) {
		return errors.New("reconcile: already running")
	}
	defer r.running.Store(false)
	var wg sync.WaitGroup
	defer wg.Wait()
	if r.local != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := r.local.Watch(ctx, r.Trigger)
			if err != nil && ctx.Err() == nil {
				r.log.Error("watching local Docker events stopped; container changes are picked up only by the periodic sync (sync.interval)", "error", err)
			}
		}()
	}
	r.loop(ctx)
	return nil
}

// debouncer coalesces triggers: fire Debounce after the last trigger, but no
// later than MaxDebounce after the first.
type debouncer struct {
	quiet, max time.Duration
	timer      *time.Timer
	first      time.Time
	armed      bool
}

func (d *debouncer) trigger(now time.Time) {
	if !d.armed {
		d.first, d.armed = now, true
	}
	wait := min(d.quiet, d.first.Add(d.max).Sub(now))
	wait = max(wait, 0)
	if d.timer == nil {
		d.timer = time.NewTimer(wait)
		return
	}
	d.timer.Reset(wait)
}

// C returns the firing channel, nil (blocks forever) while disarmed.
func (d *debouncer) C() <-chan time.Time {
	if !d.armed {
		return nil
	}
	return d.timer.C
}

func (d *debouncer) stop() {
	d.armed = false
	if d.timer != nil {
		d.timer.Stop()
	}
}

func (r *Reconciler) loop(ctx context.Context) {
	deadline := r.runCycle(ctx)
	periodic := time.NewTimer(r.untilNext(deadline))
	defer periodic.Stop()
	deb := &debouncer{quiet: r.settings.Debounce, max: r.settings.MaxDebounce}
	defer deb.stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.trigger:
			deb.trigger(time.Now())
			continue
		case <-deb.C():
			deb.stop()
		case <-periodic.C:
		}
		deadline = r.runCycle(ctx)
		periodic.Reset(r.untilNext(deadline))
	}
}

// untilNext is the wait before the next periodic cycle: Interval, or less
// when a cleanup/purge/grace deadline is due earlier (floored at Debounce).
func (r *Reconciler) untilNext(deadline time.Time) time.Duration {
	d := r.settings.Interval
	if !deadline.IsZero() {
		d = min(d, deadline.Sub(r.now()))
	}
	return max(d, r.settings.Debounce)
}
