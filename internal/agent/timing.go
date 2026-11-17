package agent

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/channinghe/labelgate/internal/config"
)

// reportDebounce coalesces bursts of source change notifications.
const reportDebounce = time.Second

// timings groups the protocol timings so tests can shorten them. Production
// code always uses defaultTimings().
type timings struct {
	handshake time.Duration // whole handshake (dial + hello..welcome)
	write     time.Duration // per-frame write deadline
	ping      time.Duration // ping period
	pongWait  time.Duration // read deadline, refreshed by every frame and pong
	debounce  time.Duration // agent: change notification debounce
}

func defaultTimings() timings {
	return timings{
		handshake: HandshakeTimeout,
		write:     WriteWait,
		ping:      PingInterval,
		pongWait:  PongWait,
		debounce:  reportDebounce,
	}
}

// validateRetry rejects retry policies that would spin or never grow.
func validateRetry(r config.RetryConfig) error {
	switch {
	case r.Delay <= 0:
		return errors.New("agent: retry.delay must be > 0")
	case r.MaxDelay < r.Delay:
		return errors.New("agent: retry.max_delay must be >= retry.delay")
	case r.Backoff < 1:
		return errors.New("agent: retry.backoff must be >= 1")
	}
	return nil
}

// backoff is an exponential reconnect backoff with equal jitter. It is owned
// by a single goroutine (not safe for concurrent use).
type backoff struct {
	base, max time.Duration
	factor    float64
	cur       time.Duration
}

func newBackoff(r config.RetryConfig) *backoff {
	return &backoff{base: r.Delay, max: r.MaxDelay, factor: r.Backoff}
}

// next returns the delay before the next attempt: the current step grows
// geometrically up to max; the returned value is uniformly in [step/2, step].
func (b *backoff) next() time.Duration {
	if b.cur == 0 {
		b.cur = b.base
	} else {
		grown := time.Duration(float64(b.cur) * b.factor)
		if grown <= 0 || grown > b.max { // <= 0 catches float overflow
			grown = b.max
		}
		b.cur = grown
	}
	half := b.cur / 2
	return half + rand.N(b.cur-half+1)
}

// reset restarts the sequence (after a successful session).
func (b *backoff) reset() { b.cur = 0 }

// sleepCtx waits for d or until ctx is done (returns ctx.Err()).
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
