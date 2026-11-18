package reconcile

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/labels"
	"github.com/channinghe/labelgate/internal/logchange"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/source"
)

// Default debounce windows (Settings.Debounce / MaxDebounce zero values).
const (
	defaultDebounce    = 2 * time.Second
	defaultMaxDebounce = 10 * time.Second
)

// Reconciler serializes all Cloudflare mutations in one goroutine.
//
// One cycle (see dev/docs/v0.3-design.md §6):
//
//	collect -> parse/validate -> resolve claims -> observe live state ->
//	plan (pure) -> write-ahead claims -> execute -> persist per-resource status
type Reconciler struct {
	settings Settings
	cf       cloudflare.API
	store    Store
	local    source.Source
	publicIP model.PublicIPFunc
	now      func() time.Time
	log      *slog.Logger
	// parse is labels.Parse; replaceable in tests.
	parse func(prefix string, labels map[string]string) model.ParsedLabels

	trigger chan struct{}
	running atomic.Bool

	mu     sync.Mutex
	status SyncStatus

	// problems decides which cycle problems are logged (loop goroutine only).
	problems *logchange.Tracker

	// tagged records accounts whose ownership tag exists (loop goroutine only).
	tagged map[string]bool
}

// New validates deps (non-nil Cloudflare, Store, PublicIP, Agents; valid
// InstanceID) and returns a Reconciler. It performs no I/O.
func New(d Deps) (*Reconciler, error) {
	if err := validateDeps(d); err != nil {
		return nil, err
	}
	s := d.Settings
	if s.LabelPrefix == "" {
		s.LabelPrefix = labels.DefaultPrefix
	}
	if s.Debounce <= 0 {
		s.Debounce = defaultDebounce
	}
	if s.MaxDebounce <= 0 {
		s.MaxDebounce = defaultMaxDebounce
	}
	if s.MaxDebounce < s.Debounce {
		s.MaxDebounce = s.Debounce
	}
	now, log := d.Now, d.Log
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Reconciler{
		settings: s, cf: d.Cloudflare, store: d.Store, local: d.Local, publicIP: d.PublicIP,
		now: now, log: log, parse: labels.Parse,
		trigger: make(chan struct{}, 1),
		status:  SyncStatus{State: SyncPending},
		tagged:  map[string]bool{},

		problems: logchange.New(problemRepeatInterval),
	}, nil
}

func validateDeps(d Deps) error {
	s := d.Settings
	switch {
	case d.Cloudflare == nil:
		return errors.New("reconcile: Cloudflare is required")
	case d.Store == nil:
		return errors.New("reconcile: Store is required")
	case d.PublicIP == nil:
		return errors.New("reconcile: PublicIP is required")
	case s.Agents == nil:
		return errors.New("reconcile: Settings.Agents is required")
	case !model.ValidInstanceID(s.InstanceID):
		return errors.New("reconcile: invalid instance ID")
	case s.Interval <= 0:
		return errors.New("reconcile: Settings.Interval must be positive")
	case s.RemoveDelay < 0 || s.OrphanTTL < 0 || s.AgentGrace < 0:
		return errors.New("reconcile: durations must not be negative")
	}
	return nil
}

// Status implements Engine.
func (r *Reconciler) Status() SyncStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func (r *Reconciler) setStatus(s SyncStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = s
}

// Trigger implements Engine: it requests a cycle and never blocks.
func (r *Reconciler) Trigger() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}
