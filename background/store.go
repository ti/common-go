package background

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Store persists jobs. The queue always holds the jobs it is running in
// memory and writes every state change through to the store; the store is
// what remains visible after the job is done, to other replicas, and after
// a restart.
//
// The default, [MemoryStore], keeps jobs in process memory: they are lost on
// restart and invisible to other replicas. A durable implementation (one
// table or collection) must satisfy the contract below; the storetest
// package checks it.
//
// Implementations must be safe for concurrent use and must store the Job as
// given, including Owner, HeartbeatAt and Revision.
type Store interface {
	// Create stores a new job. If job.Key is not empty and a job with the
	// same Kind and Key exists, nothing is written and that job is returned
	// with created == false. This check and the insert must be atomic (a
	// unique index on (kind, key) for non-empty keys).
	Create(ctx context.Context, job Job) (stored Job, created bool, err error)

	// Update replaces the job with the same ID if its stored Revision equals
	// prevRevision (compare-and-swap). It returns [ErrConflict] when the
	// revision differs and [ErrNotFound] when the job does not exist.
	Update(ctx context.Context, job Job, prevRevision int64) error

	// Get returns the job, or [ErrNotFound].
	Get(ctx context.Context, id string) (Job, error)

	// ListRecoverable returns up to limit jobs that no live process is
	// taking care of, oldest first: jobs in StateInterrupted, and jobs in
	// StateQueued or StateRunning whose HeartbeatAt is before staleBefore.
	ListRecoverable(ctx context.Context, staleBefore time.Time, limit int) ([]Job, error)

	// DeleteDone removes jobs whose State is Done (succeeded, failed,
	// cancelled or interrupted) and whose FinishedAt is before the given
	// time, and reports how many. The key of a removed job can be used again.
	DeleteDone(ctx context.Context, before time.Time) (int, error)
}

// DefaultMaxDone is the number of finished jobs [NewMemoryStore] keeps.
const DefaultMaxDone = 10000

// MemoryStore is the default [Store]: a map in process memory.
//
// Its memory is bounded: besides the queue's Retention, it keeps at most a
// fixed number of finished jobs and drops the ones that finished first when
// there are more. Unfinished jobs are never dropped (their number is bounded
// by the queue's Workers and Backlog).
type MemoryStore struct {
	mu      sync.Mutex
	jobs    map[string]Job
	keys    map[jobKey]string
	done    int // number of jobs in a Done state
	maxDone int
}

type jobKey struct{ kind, key string }

// NewMemoryStore returns an empty in-memory store that keeps at most
// [DefaultMaxDone] finished jobs.
func NewMemoryStore() *MemoryStore { return NewMemoryStoreSize(DefaultMaxDone) }

// NewMemoryStoreSize returns an empty in-memory store that keeps at most
// maxDone finished jobs (at least 1).
func NewMemoryStoreSize(maxDone int) *MemoryStore {
	return &MemoryStore{jobs: map[string]Job{}, keys: map[jobKey]string{}, maxDone: max(maxDone, 1)}
}

// putLocked stores job and keeps the count of finished jobs.
func (s *MemoryStore) putLocked(job Job) {
	if old, ok := s.jobs[job.ID]; ok && old.State.Done() {
		s.done--
	}
	if job.State.Done() {
		s.done++
	}
	s.jobs[job.ID] = job
	// Evict in batches (when 10% over the cap) so the scan is amortized.
	if s.done > s.maxDone+s.maxDone/10 {
		s.evictLocked()
	}
}

func (s *MemoryStore) deleteLocked(job Job) {
	delete(s.jobs, job.ID)
	if job.Key != "" {
		delete(s.keys, jobKey{job.Kind, job.Key})
	}
	if job.State.Done() {
		s.done--
	}
}

// evictLocked drops the jobs that finished first until maxDone are left.
func (s *MemoryStore) evictLocked() {
	finished := make([]Job, 0, s.done)
	for _, job := range s.jobs {
		if job.State.Done() {
			finished = append(finished, job)
		}
	}
	slices.SortFunc(finished, func(a, b Job) int { return a.FinishedAt.Compare(b.FinishedAt) })
	for _, job := range finished[:max(len(finished)-s.maxDone, 0)] {
		s.deleteLocked(job)
	}
}

// Create implements [Store].
func (s *MemoryStore) Create(_ context.Context, job Job) (Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.Key != "" {
		k := jobKey{job.Kind, job.Key}
		if id, ok := s.keys[k]; ok {
			return s.jobs[id], false, nil
		}
		s.keys[k] = job.ID
	}
	s.putLocked(job)
	return job, true, nil
}

// Update implements [Store].
func (s *MemoryStore) Update(_ context.Context, job Job, prevRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.jobs[job.ID]
	if !ok {
		return ErrNotFound
	}
	if stored.Revision != prevRevision {
		return ErrConflict
	}
	s.putLocked(job)
	return nil
}

// Get implements [Store].
func (s *MemoryStore) Get(_ context.Context, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return job, nil
}

// ListRecoverable implements [Store].
func (s *MemoryStore) ListRecoverable(_ context.Context, staleBefore time.Time, limit int) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Job
	for _, job := range s.jobs {
		switch {
		case job.State == StateInterrupted:
		case !job.State.Done() && job.HeartbeatAt.Before(staleBefore):
		default:
			continue
		}
		out = append(out, job)
	}
	slices.SortFunc(out, func(a, b Job) int { return a.CreatedAt.Compare(b.CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// DeleteDone implements [Store].
func (s *MemoryStore) DeleteDone(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, job := range s.jobs {
		if job.State.Done() && job.FinishedAt.Before(before) {
			s.deleteLocked(job)
			n++
		}
	}
	return n, nil
}

// Len returns the number of stored jobs.
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs)
}
