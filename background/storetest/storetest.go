// Package storetest checks that a background.Store implementation satisfies
// the contract the queue relies on. Run it from the implementation's tests,
// against the real database:
//
//	func TestStore(t *testing.T) {
//		storetest.Run(t, func(t *testing.T) background.Store { return newStore(t) })
//	}
package storetest

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/ti/common-go/background"
)

// Run runs the conformance tests. newStore must return an empty store (or
// one whose existing content the tests cannot see) for every call.
func Run(t *testing.T, newStore func(t *testing.T) background.Store) {
	t.Run("CreateGet", func(t *testing.T) { testCreateGet(t, newStore(t)) })
	t.Run("Key", func(t *testing.T) { testKey(t, newStore(t)) })
	t.Run("Update", func(t *testing.T) { testUpdate(t, newStore(t)) })
	t.Run("ListRecoverable", func(t *testing.T) { testListRecoverable(t, newStore(t)) })
	t.Run("DeleteDone", func(t *testing.T) { testDeleteDone(t, newStore(t)) })
}

// base is a time with the precision most databases keep (milliseconds), in
// UTC, so that round trips compare equal.
var base = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newJob(kind string) background.Job {
	return background.Job{
		ID: uuid.New().String(), Kind: kind, State: background.StateQueued,
		CreatedAt: base, UpdatedAt: base, HeartbeatAt: base, Owner: "owner-1", Revision: 1,
	}
}

func create(t *testing.T, s background.Store, job background.Job) background.Job {
	t.Helper()
	stored, created, err := s.Create(context.Background(), job)
	if err != nil || !created {
		t.Fatalf("Create: created=%v err=%v", created, err)
	}
	return stored
}

// equal compares jobs field by field, with time.Time.Equal for times.
func equal(a, b background.Job) bool {
	times := func(j *background.Job) []*time.Time {
		return []*time.Time{&j.CreatedAt, &j.StartedAt, &j.NextAttemptAt, &j.UpdatedAt, &j.FinishedAt, &j.HeartbeatAt}
	}
	ta, tb := times(&a), times(&b)
	for i := range ta {
		if !ta[i].Equal(*tb[i]) {
			return false
		}
		*ta[i], *tb[i] = time.Time{}, time.Time{}
	}
	if len(a.Labels) == 0 && len(b.Labels) == 0 {
		a.Labels, b.Labels = nil, nil
	}
	if len(a.Payload) == 0 && len(b.Payload) == 0 {
		a.Payload, b.Payload = nil, nil
	}
	if len(a.Result) == 0 && len(b.Result) == 0 {
		a.Result, b.Result = nil, nil
	}
	return reflect.DeepEqual(a, b)
}

func testCreateGet(t *testing.T, s background.Store) {
	ctx := context.Background()
	job := newJob("export")
	job.Key = "req-1"
	job.Payload = []byte{0, 1, 2, 0xff}
	job.Labels = map[string]string{"owner": "alice", "tenant": "t1"}
	job.State = background.StateFailed
	job.Attempt = 3
	job.Restarts = 2
	job.Progress = background.Progress{Done: 7, Total: 9, Message: "almost"}
	job.Result = []byte("partial")
	job.Error = "disk full"
	job.StartedAt = base.Add(time.Second)
	job.NextAttemptAt = base.Add(2 * time.Second)
	job.FinishedAt = base.Add(3 * time.Second)
	job.Revision = 42

	stored := create(t, s, job)
	if !equal(stored, job) {
		t.Fatalf("Create returned\n%+v\nwant\n%+v", stored, job)
	}
	got, err := s.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(got, job) {
		t.Fatalf("Get returned\n%+v\nwant every field as stored\n%+v", got, job)
	}
	if _, err := s.Get(ctx, uuid.New().String()); !errors.Is(err, background.ErrNotFound) {
		t.Fatalf("Get(unknown) = %v, want ErrNotFound", err)
	}
}

func testKey(t *testing.T, s background.Store) {
	ctx := context.Background()
	first := newJob("export")
	first.Key = "req-1"
	create(t, s, first)

	dup := newJob("export")
	dup.Key = "req-1"
	stored, created, err := s.Create(ctx, dup)
	if err != nil || created || stored.ID != first.ID {
		t.Fatalf("Create with a used key: id=%s created=%v err=%v; want the first job %s", stored.ID, created, err, first.ID)
	}
	if _, err := s.Get(ctx, dup.ID); !errors.Is(err, background.ErrNotFound) {
		t.Fatalf("the duplicate was stored (Get = %v)", err)
	}

	otherKind := newJob("import")
	otherKind.Key = "req-1"
	create(t, s, otherKind) // keys are scoped by kind
	create(t, s, newJob("export"))
	create(t, s, newJob("export")) // empty keys never collide

	// Concurrent creation with one key: exactly one wins, all see its ID.
	var wg sync.WaitGroup
	var createdCount atomic.Int32
	ids := make([]string, 16)
	for i := range ids {
		wg.Go(func() {
			job := newJob("export")
			job.Key = "req-concurrent"
			stored, created, err := s.Create(ctx, job)
			if err != nil {
				t.Errorf("concurrent Create: %v", err)
				return
			}
			if created {
				createdCount.Add(1)
			}
			ids[i] = stored.ID
		})
	}
	wg.Wait()
	if createdCount.Load() != 1 {
		t.Fatalf("%d concurrent creates won, want exactly 1", createdCount.Load())
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("concurrent creates returned different jobs: %v", ids)
		}
	}
}

func testUpdate(t *testing.T, s background.Store) {
	ctx := context.Background()
	job := create(t, s, newJob("export"))

	job.State = background.StateRunning
	job.Attempt = 1
	job.Revision = 2
	if err := s.Update(ctx, job, 1); err != nil {
		t.Fatalf("Update with the right revision: %v", err)
	}
	if got, _ := s.Get(ctx, job.ID); !equal(got, job) {
		t.Fatalf("after Update: %+v", got)
	}

	stale := job
	stale.State = background.StateFailed
	stale.Revision = 3
	if err := s.Update(ctx, stale, 1); !errors.Is(err, background.ErrConflict) {
		t.Fatalf("Update with a stale revision = %v, want ErrConflict", err)
	}
	if got, _ := s.Get(ctx, job.ID); got.State != background.StateRunning || got.Revision != 2 {
		t.Fatalf("a conflicting Update changed the job: %+v", got)
	}

	// Revisions may jump (a heartbeat and a change in one write).
	job.Revision = 10
	if err := s.Update(ctx, job, 2); err != nil {
		t.Fatal(err)
	}

	missing := newJob("export")
	if err := s.Update(ctx, missing, 1); !errors.Is(err, background.ErrNotFound) {
		t.Fatalf("Update(unknown) = %v, want ErrNotFound", err)
	}

	// Two writers with the same expected revision: one wins.
	var wg sync.WaitGroup
	var won atomic.Int32
	for i := range 8 {
		wg.Go(func() {
			next := job
			next.Revision = int64(100 + i)
			switch err := s.Update(ctx, next, 10); {
			case err == nil:
				won.Add(1)
			case !errors.Is(err, background.ErrConflict):
				t.Errorf("concurrent Update: %v", err)
			}
		})
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("%d concurrent updates won, want exactly 1", won.Load())
	}
}

func testListRecoverable(t *testing.T, s background.Store) {
	ctx := context.Background()
	stale, fresh := base.Add(-time.Hour), base.Add(time.Hour)
	put := func(state background.State, heartbeat time.Time, age time.Duration) string {
		job := newJob("export")
		job.State = state
		job.HeartbeatAt = heartbeat
		job.CreatedAt = base.Add(-age)
		if state.Done() {
			job.FinishedAt = base
		}
		return create(t, s, job).ID
	}
	interrupted := put(background.StateInterrupted, fresh, 4*time.Minute)
	staleRunning := put(background.StateRunning, stale, 3*time.Minute)
	staleQueued := put(background.StateQueued, stale, 2*time.Minute)
	put(background.StateRunning, fresh, time.Minute)   // its process is alive
	put(background.StateQueued, fresh, time.Minute)    // its process is alive
	put(background.StateSucceeded, stale, time.Minute) // done
	put(background.StateFailed, stale, time.Minute)
	put(background.StateCancelled, stale, time.Minute)

	jobs, err := s.ListRecoverable(ctx, base, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, j := range jobs {
		got = append(got, j.ID)
	}
	want := []string{interrupted, staleRunning, staleQueued} // oldest first
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListRecoverable = %v, want %v (interrupted, then stale running, then stale queued)", got, want)
	}
	jobs, err = s.ListRecoverable(ctx, base, 2)
	if err != nil || len(jobs) != 2 || jobs[0].ID != interrupted {
		t.Fatalf("ListRecoverable(limit 2) = %d jobs, err %v", len(jobs), err)
	}
}

func testDeleteDone(t *testing.T, s background.Store) {
	ctx := context.Background()
	put := func(state background.State, finished time.Time, key string) string {
		job := newJob("export")
		job.State = state
		job.Key = key
		job.FinishedAt = finished
		return create(t, s, job).ID
	}
	old, recent := base.Add(-time.Hour), base.Add(time.Hour)
	gone := []string{
		put(background.StateSucceeded, old, "k-old"),
		put(background.StateFailed, old, ""),
		put(background.StateCancelled, old, ""),
		put(background.StateInterrupted, old, ""),
	}
	kept := []string{
		put(background.StateSucceeded, recent, "k-recent"),
		put(background.StateRunning, time.Time{}, ""),
		put(background.StateQueued, time.Time{}, ""),
	}
	n, err := s.DeleteDone(ctx, base)
	if err != nil || n != len(gone) {
		t.Fatalf("DeleteDone = %d, %v; want %d", n, err, len(gone))
	}
	for _, id := range gone {
		if _, err := s.Get(ctx, id); !errors.Is(err, background.ErrNotFound) {
			t.Errorf("job %s still exists (Get = %v)", id, err)
		}
	}
	for _, id := range kept {
		if _, err := s.Get(ctx, id); err != nil {
			t.Errorf("job %s was removed: %v", id, err)
		}
	}
	// The key of a removed job is free again; a kept one still deduplicates.
	reuse := newJob("export")
	reuse.Key = "k-old"
	create(t, s, reuse)
	dup := newJob("export")
	dup.Key = "k-recent"
	if _, created, err := s.Create(ctx, dup); err != nil || created {
		t.Fatalf("Create with the key of a retained job: created=%v err=%v", created, err)
	}
}
