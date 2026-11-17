package reconcile

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

// memStore is an in-memory Store with the semantics of store.Store:
// one non-rejected row per (kind, key), second-precision UTC times, copies
// in and out, ErrNotFound on missing rows and one-shot failure injection.
type memStore struct {
	mu       sync.Mutex
	now      func() time.Time
	rows     map[int64]model.Resource
	nextID   int64
	snaps    []model.Snapshot
	failures map[string]error
	writes   int
}

func newMemStore(now func() time.Time) *memStore {
	return &memStore{now: now, rows: map[int64]model.Resource{}, failures: map[string]error{}}
}

var _ Store = (*memStore)(nil)

func (s *memStore) failNext(op string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[op] = err
}

func (s *memStore) fail(op string) error {
	err := s.failures[op]
	delete(s.failures, op)
	return err
}

func secs(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Truncate(time.Second)
}

func cloneRow(r model.Resource) model.Resource {
	if len(r.PolicyIDs) == 0 {
		r.PolicyIDs = nil
	} else {
		r.PolicyIDs = append([]string(nil), r.PolicyIDs...)
	}
	r.CreatedAt, r.UpdatedAt = secs(r.CreatedAt), secs(r.UpdatedAt)
	r.OrphanedAt, r.SyncedAt = secs(r.OrphanedAt), secs(r.SyncedAt)
	return r
}

func (s *memStore) AllResources(ctx context.Context) ([]model.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("AllResources"); err != nil {
		return nil, err
	}
	return s.sorted(), ctx.Err()
}

func (s *memStore) sorted() []model.Resource {
	out := make([]model.Resource, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, cloneRow(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *memStore) UpsertResource(ctx context.Context, r model.Resource) (model.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("UpsertResource"); err != nil {
		return model.Resource{}, err
	}
	s.writes++
	r = cloneRow(r)
	r.Rejected = false
	r.UpdatedAt = secs(s.now())
	for id, ex := range s.rows {
		if !ex.Rejected && ex.Kind == r.Kind && ex.Key == r.Key {
			r.ID, r.CreatedAt = id, ex.CreatedAt
			s.rows[id] = r
			return cloneRow(r), nil
		}
	}
	s.nextID++
	r.ID, r.CreatedAt = s.nextID, secs(s.now())
	s.rows[r.ID] = r
	return cloneRow(r), nil
}

func (s *memStore) UpdateResource(ctx context.Context, r model.Resource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("UpdateResource"); err != nil {
		return err
	}
	ex, ok := s.rows[r.ID]
	if !ok {
		return fmt.Errorf("row %d: %w", r.ID, model.ErrNotFound)
	}
	s.writes++
	r = cloneRow(r)
	r.CreatedAt = ex.CreatedAt
	s.rows[r.ID] = r
	return nil
}

func (s *memStore) DeleteResource(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("DeleteResource"); err != nil {
		return err
	}
	if _, ok := s.rows[id]; !ok {
		return fmt.Errorf("row %d: %w", id, model.ErrNotFound)
	}
	s.writes++
	delete(s.rows, id)
	return nil
}

func (s *memStore) ReplaceRejected(ctx context.Context, rows []model.Resource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("ReplaceRejected"); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, r := range rows {
		id := string(r.Kind) + "|" + r.Key + "|" + r.Owner.Key()
		if seen[id] {
			return fmt.Errorf("duplicate rejected row %s: %w", id, model.ErrInvalid)
		}
		seen[id] = true
	}
	s.writes++
	for id, r := range s.rows {
		if r.Rejected {
			delete(s.rows, id)
		}
	}
	for _, r := range rows {
		r = cloneRow(r)
		s.nextID++
		r.ID, r.Rejected, r.Status = s.nextID, true, model.StatusError
		r.CreatedAt, r.UpdatedAt = secs(s.now()), secs(s.now())
		s.rows[r.ID] = r
	}
	return nil
}

func (s *memStore) AgentSnapshots(ctx context.Context) ([]model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("AgentSnapshots"); err != nil {
		return nil, err
	}
	return append([]model.Snapshot(nil), s.snaps...), nil
}

func (s *memStore) setAgents(snaps ...model.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snaps = append([]model.Snapshot(nil), snaps...)
}

// seed inserts a row as-is (tests of pre-existing / legacy state).
func (s *memStore) seed(r model.Resource) model.Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	r = cloneRow(r)
	r.ID = s.nextID
	if r.CreatedAt.IsZero() {
		r.CreatedAt, r.UpdatedAt = secs(s.now()), secs(s.now())
	}
	s.rows[r.ID] = r
	return r
}

// list returns non-rejected (rejected=false) or rejected rows ordered by kind, key.
func (s *memStore) list(rejected bool) []model.Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Resource
	for _, r := range s.sorted() {
		if r.Rejected == rejected {
			out = append(out, r)
		}
	}
	return sortRows(out)
}

func (s *memStore) get(kind model.Kind, key string) (model.Resource, bool) {
	for _, r := range s.list(false) {
		if r.Kind == kind && r.Key == key {
			return r, true
		}
	}
	return model.Resource{}, false
}

func (s *memStore) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}
