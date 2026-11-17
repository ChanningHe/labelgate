package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// group runs components under one cancellable context and collects failures.
// A component that returns while its context is still live (error or not) is a
// fatal failure and wakes the supervisor via failed.
type group struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	errs   []error
	failed chan struct{}
	once   sync.Once
	log    *slog.Logger
}

func newGroup(failed chan struct{}, log *slog.Logger) *group {
	ctx, cancel := context.WithCancel(context.Background())
	return &group{ctx: ctx, cancel: cancel, failed: failed, log: log}
}

// start launches r; nil runners are skipped (disabled components).
func (g *group) start(name string, r runner) {
	if r == nil {
		return
	}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		err := r.Run(g.ctx)
		if g.ctx.Err() != nil {
			g.record(ignoreCanceled(err), name, false)
			return
		}
		g.record(err, name, true)
	}()
}

func ignoreCanceled(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// record stores a failure; early marks a return before shutdown was requested.
func (g *group) record(err error, name string, early bool) {
	if err == nil && !early {
		return
	}
	if err == nil {
		err = errors.New("exited unexpectedly")
	}
	g.log.Error("component failed", "component", name, "error", err)
	g.mu.Lock()
	g.errs = append(g.errs, fmt.Errorf("%s: %w", name, err))
	g.mu.Unlock()
	g.once.Do(func() { close(g.failed) })
}

// stop cancels the group and waits for every component, returning their errors.
func (g *group) stop() error {
	g.cancel()
	g.wg.Wait()
	g.mu.Lock()
	defer g.mu.Unlock()
	return errors.Join(g.errs...)
}

// supervise runs the reconciler, agent hub and API until ctx ends or any
// component fails, then shuts down in order: API + hub first (stop accepting),
// then the reconciler. The caller closes the store afterwards.
func (a *app) supervise(ctx context.Context, log *slog.Logger, rec runner, h hub, srv runner) error {
	failed := make(chan struct{})
	edge := newGroup(failed, log)
	engine := newGroup(failed, log)

	engine.start("reconciler", rec)
	if h != nil {
		edge.start("agent hub", h)
	}
	edge.start("api", srv)

	select {
	case <-ctx.Done():
		log.Info("Shutdown requested")
	case <-failed:
		log.Error("Shutting down after component failure")
	}
	edgeErr := edge.stop()
	log.Info("API and agent hub stopped")
	engineErr := engine.stop()
	log.Info("Reconciler stopped")
	return errors.Join(edgeErr, engineErr)
}
