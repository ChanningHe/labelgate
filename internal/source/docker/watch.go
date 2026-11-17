package docker

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

const (
	fallbackDelay = time.Second
	fallbackMax   = 30 * time.Second
	fallbackScale = 2.0
)

// Container events that can change what we publish.
var watchedActions = []string{"start", "die", "destroy", "rename", "update"}

// Watch implements source.Source. It returns ctx.Err() once ctx is done; stream
// failures never end it, they are retried with backoff.
func (s *Source) Watch(ctx context.Context, notify func()) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	if s.poll > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.pollLoop(ctx, notify)
		}()
	}

	attempt := 0
	for {
		started := time.Now()
		err := s.streamOnce(ctx, notify)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(started) >= s.maxDelay() {
			attempt = 0 // the stream was healthy for a while: start over
		}
		delay := s.backoff(attempt)
		attempt++
		s.log.Warn("docker event stream lost; reconnecting", "error", err, "retry_in", delay)
		if !sleep(ctx, delay) {
			return ctx.Err()
		}
	}
}

// pollLoop signals every poll interval (a safety net for missed events).
func (s *Source) pollLoop(ctx context.Context, notify func()) {
	t := time.NewTicker(s.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			notify()
		}
	}
}

// streamOnce subscribes to events, signals that a full resync is due (the
// subscription is already active, so any change from now on produces an
// event), then forwards events until the stream ends. It always returns a
// non-nil error unless ctx ended.
func (s *Source) streamOnce(ctx context.Context, notify func()) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	res := s.cli.Events(sctx, client.EventsListOptions{Filters: s.eventFilters()})
	notify()
	for {
		select {
		case <-sctx.Done():
			return sctx.Err()
		case <-res.Messages:
			notify()
		case err, ok := <-res.Err:
			if !ok || err == nil {
				return io.EOF
			}
			return err
		}
	}
}

func (s *Source) eventFilters() client.Filters {
	f := client.Filters{}.Add("type", "container")
	f.Add("event", watchedActions...)
	if s.filter != "" {
		f.Add("label", s.filter)
	}
	return f
}

// backoff returns Delay * Backoff^attempt capped at MaxDelay.
func (s *Source) backoff(attempt int) time.Duration {
	delay, limit, scale := s.retry.Delay, s.maxDelay(), s.retry.Backoff
	if delay <= 0 {
		delay = fallbackDelay
	}
	if scale < 1 {
		scale = fallbackScale
	}
	d := float64(delay)
	for i := 0; i < attempt && d < float64(limit); i++ {
		d *= scale
	}
	return min(time.Duration(d), limit)
}

func (s *Source) maxDelay() time.Duration {
	limit := s.retry.MaxDelay
	if limit <= 0 {
		limit = fallbackMax
	}
	return max(limit, s.retry.Delay)
}

// sleep waits d or until ctx is done; it reports whether the full wait elapsed.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
