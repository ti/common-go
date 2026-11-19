package background

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// leftover stores a job as a process that died would have left it.
func leftover(t *testing.T, s Store, id, kind string, state State, heartbeat time.Time, restarts int) {
	t.Helper()
	job := Job{
		ID: id, Kind: kind, State: state, Attempt: 1, Restarts: restarts, Payload: []byte(id),
		Progress:  Progress{Done: 5, Total: 10},
		CreatedAt: heartbeat, UpdatedAt: heartbeat, HeartbeatAt: heartbeat, Owner: "dead-process", Revision: 7,
	}
	if state.Done() {
		job.FinishedAt = heartbeat
	}
	if _, created, err := s.Create(bg(), job); err != nil || !created {
		t.Fatalf("leftover %s: created=%v err=%v", id, created, err)
	}
}

// After a restart, a queue with a lease takes over the jobs that no live
// process owns: requeue or fail, by the handler's policy.
func TestRecoveryAfterRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore() // stands in for a database both processes share
		longAgo := time.Now().Add(-10 * time.Minute)
		leftover(t, store, "running-requeue", "resumable", StateRunning, longAgo, 0)
		leftover(t, store, "interrupted-requeue", "resumable", StateInterrupted, time.Now(), 0)
		leftover(t, store, "queued-fail", "strict", StateQueued, longAgo, 0)
		leftover(t, store, "running-fail", "strict", StateRunning, longAgo, 0)
		leftover(t, store, "crash-loop", "resumable", StateRunning, longAgo, MaxRestarts)
		leftover(t, store, "unknown-kind", "from-a-newer-version", StateRunning, longAgo, 0)
		leftover(t, store, "alive-elsewhere", "resumable", StateRunning, time.Now().Add(time.Hour), 0)
		leftover(t, store, "done", "resumable", StateSucceeded, time.Now(), 0)

		q := newTestQueue(QueueOptions{Store: store, LeaseTTL: 30 * time.Second})
		var ran atomic.Int32
		q.Handle("resumable", func(_ context.Context, run *Run) ([]byte, error) {
			ran.Add(1)
			return []byte("resumed"), nil
		}, HandlerOptions{OnRestart: RestartRequeue})
		q.Handle("strict", func(context.Context, *Run) ([]byte, error) {
			t.Error("a RestartFail job was run again")
			return nil, nil
		}, HandlerOptions{})

		time.Sleep(31 * time.Second) // one sweep
		synctest.Wait()

		get := func(id string) Job {
			job, err := q.Get(bg(), id)
			if err != nil {
				t.Fatalf("Get(%s): %v", id, err)
			}
			return job
		}
		for _, id := range []string{"running-requeue", "interrupted-requeue"} {
			job := get(id)
			if job.State != StateSucceeded || job.Restarts != 1 || job.Attempt != 1 || string(job.Result) != "resumed" ||
				job.Owner == "dead-process" || job.Progress.Total != 0 {
				t.Errorf("%s = %+v, want succeeded after one restart by this queue", id, job)
			}
		}
		if ran.Load() != 2 {
			t.Errorf("handler ran %d times, want once for each of the two requeued jobs", ran.Load())
		}
		for id, was := range map[string]string{"queued-fail": "queued", "running-fail": "running"} {
			job := get(id)
			if job.State != StateFailed || !strings.Contains(job.Error, "stopped while it was "+was) || job.FinishedAt.IsZero() {
				t.Errorf("%s = %+v, want failed", id, job)
			}
		}
		if job := get("crash-loop"); job.State != StateFailed || !strings.Contains(job.Error, "restarted 3 times") {
			t.Errorf("crash-loop = %+v, want failed after MaxRestarts", job)
		}
		for _, id := range []string{"unknown-kind", "alive-elsewhere"} {
			if job := get(id); job.State != StateRunning || job.Revision != 7 {
				t.Errorf("%s = %+v, want untouched", id, job)
			}
		}
		// The job of another live process can be watched but not cancelled here.
		if _, err := q.Cancel(bg(), "alive-elsewhere"); !errors.Is(err, ErrNotOwner) || status.Code(err) != codes.FailedPrecondition {
			t.Errorf("Cancel of a foreign job = %v, want ErrNotOwner", err)
		}
		shutdown(t, q)
	})
}

// Two queues on one store (two replicas): the owner keeps its job alive
// with heartbeats, the other replica does not steal it, can watch it by
// polling the store, and sees the final state.
func TestTwoReplicasShareStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore()
		opts := QueueOptions{Store: store, LeaseTTL: 30 * time.Second}
		a, b := newTestQueue(opts), newTestQueue(opts)
		var runsB atomic.Int32
		a.Handle("k", func(_ context.Context, run *Run) ([]byte, error) {
			for i := range int64(5) {
				time.Sleep(time.Minute)
				run.SetProgress(i+1, 5, "")
			}
			return []byte("by a"), nil
		}, HandlerOptions{OnRestart: RestartRequeue})
		b.Handle("k", func(context.Context, *Run) ([]byte, error) { runsB.Add(1); return nil, nil },
			HandlerOptions{OnRestart: RestartRequeue})

		job := mustSubmit(t, a, SubmitRequest{Kind: "k", Key: "once"})

		// The same key submitted to the other replica finds the job.
		dup, created, err := b.Submit(bg(), SubmitRequest{Kind: "k", Key: "once"})
		if err != nil || created || dup.ID != job.ID {
			t.Fatalf("Submit on the other replica: %+v created=%v err=%v", dup, created, err)
		}

		var progress []int64
		var final Job
		for j, err := range b.Watch(t.Context(), job.ID) {
			if err != nil {
				t.Fatalf("Watch on the other replica: %v", err)
			}
			if n := len(progress); n == 0 || progress[n-1] != j.Progress.Done {
				progress = append(progress, j.Progress.Done)
			}
			final = j
		}
		if final.State != StateSucceeded || string(final.Result) != "by a" || final.Restarts != 0 {
			t.Fatalf("final seen by the other replica = %+v", final)
		}
		if len(progress) != 6 { // 0..5
			t.Fatalf("progress seen by the other replica = %v", progress)
		}
		if runsB.Load() != 0 {
			t.Fatal("the other replica ran a job whose owner was alive")
		}
		if !final.HeartbeatAt.After(final.CreatedAt.Add(4 * time.Minute)) {
			t.Fatalf("heartbeat was not kept up: created %s, heartbeat %s", final.CreatedAt, final.HeartbeatAt)
		}
		shutdown(t, a)
		shutdown(t, b)
	})
}

// If the store says the job now belongs to someone else (this process was
// considered dead), the queue cancels its handler and lets go.
func TestOwnershipLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore()
		q := newTestQueue(QueueOptions{Store: store, LeaseTTL: 30 * time.Second})
		var cause error
		q.Handle("k", func(ctx context.Context, _ *Run) ([]byte, error) {
			<-ctx.Done()
			cause = context.Cause(ctx)
			return nil, ctx.Err()
		}, HandlerOptions{})
		job := mustSubmit(t, q, SubmitRequest{Kind: "k"})
		synctest.Wait()

		// Another replica takes the job over.
		stolen, _ := store.Get(bg(), job.ID)
		prev := stolen.Revision
		stolen.Owner, stolen.Revision = "other-replica", prev+1
		if err := store.Update(bg(), stolen, prev); err != nil {
			t.Fatal(err)
		}
		time.Sleep(11 * time.Second) // next heartbeat notices
		synctest.Wait()
		if !errors.Is(cause, ErrConflict) {
			t.Fatalf("handler cancellation cause = %v, want ErrConflict", cause)
		}
		if q.Stats() != (Stats{}) {
			t.Fatalf("stats = %+v", q.Stats())
		}
		got, err := q.Get(bg(), job.ID)
		if err != nil || got.Owner != "other-replica" || got.State != StateRunning {
			t.Fatalf("job after losing it = %+v, %v; the other replica's record must be intact", got, err)
		}
		shutdown(t, q)
	})
}

// failingStore fails writes on demand.
type failingStore struct {
	*MemoryStore
	failCreate, failUpdate atomic.Bool
}

func (s *failingStore) Create(ctx context.Context, job Job) (Job, bool, error) {
	if s.failCreate.Load() {
		return Job{}, false, errors.New("database down")
	}
	return s.MemoryStore.Create(ctx, job)
}

func (s *failingStore) Update(ctx context.Context, job Job, prev int64) error {
	if s.failUpdate.Load() {
		return errors.New("database down")
	}
	return s.MemoryStore.Update(ctx, job, prev)
}

// Store failures: Submit fails without leaking capacity; a job whose state
// cannot be written still runs and is written when the store is back.
func TestStoreErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &failingStore{MemoryStore: NewMemoryStore()}
		q := newTestQueue(QueueOptions{Store: store, Workers: 1, Backlog: -1})
		q.Handle("k", func(context.Context, *Run) ([]byte, error) { time.Sleep(10 * time.Second); return nil, nil }, HandlerOptions{})

		store.failCreate.Store(true)
		for range 3 {
			if _, _, err := q.Submit(bg(), SubmitRequest{Kind: "k"}); err == nil || err.Error() != "database down" {
				t.Fatalf("Submit with a failing store = %v", err)
			}
		}
		store.failCreate.Store(false)
		job := mustSubmit(t, q, SubmitRequest{Kind: "k"}) // the single slot is still free

		store.failUpdate.Store(true)
		time.Sleep(5 * time.Second)
		store.failUpdate.Store(false)
		if final := last(await(t, q, job.ID)); final.State != StateSucceeded {
			t.Fatalf("final = %+v", final)
		}
		synctest.Wait()
		if stored, _ := store.Get(bg(), job.ID); stored.State != StateSucceeded {
			t.Fatalf("stored = %+v, want the final state once the store recovered", stored)
		}
		shutdown(t, q)
	})
}

// A final state that cannot be stored is not thrown away: the queue keeps
// answering with the true state, keeps the lease alive, and stores the
// state when the store is back. No other replica may take the job over in
// between.
func TestFinalStateSurvivesStoreOutage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &failingStore{MemoryStore: NewMemoryStore()}
		opts := QueueOptions{Store: store, LeaseTTL: 30 * time.Second}
		a, b := newTestQueue(opts), newTestQueue(opts)
		var runsB atomic.Int32
		a.Handle("k", func(context.Context, *Run) ([]byte, error) { time.Sleep(time.Second); return []byte("done"), nil },
			HandlerOptions{OnRestart: RestartRequeue})
		b.Handle("k", func(context.Context, *Run) ([]byte, error) { runsB.Add(1); return nil, nil },
			HandlerOptions{OnRestart: RestartRequeue})

		job := mustSubmit(t, a, SubmitRequest{Kind: "k", Key: "once"})
		synctest.Wait()
		store.failUpdate.Store(true)
		time.Sleep(5 * time.Minute) // the job ends; the store is down for many lease periods
		synctest.Wait()

		got, err := a.Get(bg(), job.ID)
		if err != nil || got.State != StateSucceeded || string(got.Result) != "done" {
			t.Fatalf("Get during the outage = %+v, %v; want the true final state", got, err)
		}
		if dup, created, err := a.Submit(bg(), SubmitRequest{Kind: "k", Key: "once"}); err != nil || created || dup.State != StateSucceeded {
			t.Fatalf("resubmit during the outage = %+v created=%v err=%v", dup, created, err)
		}
		if stored, _ := store.Get(bg(), job.ID); stored.State != StateRunning {
			t.Fatalf("stored state during the outage = %s (test setup)", stored.State)
		}

		store.failUpdate.Store(false)
		time.Sleep(time.Minute)
		synctest.Wait()
		stored, _ := store.Get(bg(), job.ID)
		if stored.State != StateSucceeded || stored.Restarts != 0 {
			t.Fatalf("stored after the outage = %+v, want succeeded and never restarted", stored)
		}
		if runsB.Load() != 0 {
			t.Fatal("the other replica ran a job that had already succeeded")
		}
		shutdown(t, a)
		shutdown(t, b)
	})
}

// hangingStore blocks writes until their context ends.
type hangingStore struct {
	*MemoryStore
	hang atomic.Bool
}

func (s *hangingStore) Update(ctx context.Context, job Job, prev int64) error {
	if s.hang.Load() {
		<-ctx.Done()
		return ctx.Err()
	}
	return s.MemoryStore.Update(ctx, job, prev)
}

// Shutdown with a store that hangs: store writes respect the closer's
// deadline, Shutdown returns close to it and reports the jobs whose final
// state is not stored.
func TestShutdownStoreWritesHonorDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &hangingStore{MemoryStore: NewMemoryStore()}
		q := newTestQueue(QueueOptions{Store: store, Workers: 1, Grace: time.Second})
		q.Handle("k", func(ctx context.Context, _ *Run) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }, HandlerOptions{})
		for range 20 { // 1 running, 19 queued
			mustSubmit(t, q, SubmitRequest{Kind: "k"})
		}
		synctest.Wait()
		store.hang.Store(true)

		ctx, cancel := context.WithTimeout(bg(), 3*time.Second)
		defer cancel()
		start := time.Now()
		err := q.Shutdown(ctx)
		took := time.Since(start)
		if err == nil || !strings.Contains(err.Error(), "could not be stored") {
			t.Fatalf("Shutdown = %v, want an error about final states that could not be stored", err)
		}
		// 20 jobs with a 5s store timeout each would be 100s unbounded.
		if took > 3*time.Second+100*time.Millisecond {
			t.Fatalf("Shutdown took %s with a 3s deadline", took)
		}
		// This process still knows the truth.
		if st := q.Stats(); st != (Stats{}) {
			t.Fatalf("stats = %+v", st)
		}
		store.hang.Store(false)
	})
}

// A job that is queued again after a restart gets the handler's full retry
// budget again: the attempts made before the restart do not count.
func TestRequeuedJobGetsFullRetryBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore()
		job := Job{
			ID: "used-up", Kind: "flaky", State: StateRunning, Attempt: 3, // all 3 attempts used before the crash
			CreatedAt: time.Now().Add(-time.Hour), HeartbeatAt: time.Now().Add(-time.Hour), Owner: "dead", Revision: 9,
		}
		if _, _, err := store.Create(bg(), job); err != nil {
			t.Fatal(err)
		}
		q := newTestQueue(QueueOptions{Store: store, LeaseTTL: 30 * time.Second})
		var calls atomic.Int32
		q.Handle("flaky", func(context.Context, *Run) ([]byte, error) {
			if calls.Add(1) < 3 {
				return nil, Retryable(errors.New("503"))
			}
			return []byte("ok"), nil
		}, HandlerOptions{MaxAttempts: 3, OnRestart: RestartRequeue, Backoff: func(int) time.Duration { return time.Second }})
		time.Sleep(time.Minute)
		synctest.Wait()
		got, err := q.Get(bg(), "used-up")
		if err != nil || got.State != StateSucceeded || got.Attempt != 3 || got.Restarts != 1 {
			t.Fatalf("job = %+v, %v; want succeeded on the third attempt after the restart", got, err)
		}
		shutdown(t, q)
	})
}

// The in-memory store keeps a bounded number of finished jobs: the ones
// that finished first go, unfinished jobs never do.
func TestMemoryStoreIsBounded(t *testing.T) {
	store := NewMemoryStoreSize(100)
	base := time.Now()
	put := func(i int, state State, key string) {
		job := Job{ID: strconv.Itoa(i), Kind: "k", Key: key, State: state, Revision: 1, CreatedAt: base}
		if state.Done() {
			job.FinishedAt = base.Add(time.Duration(i) * time.Second)
		}
		if _, created, err := store.Create(bg(), job); err != nil || !created {
			t.Fatalf("Create %d: created=%v err=%v", i, created, err)
		}
	}
	for i := range 50 {
		put(i, StateRunning, "")
	}
	put(50, StateSucceeded, "oldest")
	for i := 51; i < 1000; i++ {
		put(i, StateSucceeded, "")
	}
	if n := store.Len(); n > 50+110 {
		t.Fatalf("store holds %d jobs, want at most 50 unfinished + 110 finished", n)
	}
	for i := range 50 {
		if _, err := store.Get(bg(), strconv.Itoa(i)); err != nil {
			t.Fatalf("unfinished job %d was dropped", i)
		}
	}
	if _, err := store.Get(bg(), "999"); err != nil {
		t.Fatal("the job that finished last was dropped")
	}
	if _, err := store.Get(bg(), "50"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the job that finished first is still there (err=%v)", err)
	}
	// The key of a dropped job is free again.
	if _, created, _ := store.Create(bg(), Job{ID: "again", Kind: "k", Key: "oldest", Revision: 1}); !created {
		t.Fatal("the key of a dropped job still deduplicates")
	}
	// A job that finishes through Update counts too.
	bounded := NewMemoryStoreSize(10)
	for i := range 200 {
		job := Job{ID: strconv.Itoa(i), Kind: "k", State: StateRunning, Revision: 1}
		if _, _, err := bounded.Create(bg(), job); err != nil {
			t.Fatal(err)
		}
		job.State, job.Revision, job.FinishedAt = StateFailed, 2, base.Add(time.Duration(i)*time.Second)
		if err := bounded.Update(bg(), job, 1); err != nil {
			t.Fatal(err)
		}
	}
	if n := bounded.Len(); n > 11 {
		t.Fatalf("store holds %d finished jobs, want at most 11", n)
	}
}

// Queues with the same name add up in the gauges instead of overwriting
// each other.
func TestGaugesOfSameNamedQueuesAddUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		labels := map[string]string{"queue": "shared-name"}
		runningBefore := metricValue(t, "background_jobs_running", labels)
		queuedBefore := metricValue(t, "background_jobs_queued", labels)
		opts := QueueOptions{Name: "shared-name", Workers: 1}
		a, b := newTestQueue(opts), newTestQueue(opts)
		block := func(ctx context.Context, _ *Run) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
		a.Handle("k", block, HandlerOptions{})
		b.Handle("k", block, HandlerOptions{})
		for range 3 { // per queue: 1 running, 2 queued
			mustSubmit(t, a, SubmitRequest{Kind: "k"})
			mustSubmit(t, b, SubmitRequest{Kind: "k"})
		}
		synctest.Wait()
		if got := metricValue(t, "background_jobs_running", labels) - runningBefore; got != 2 {
			t.Fatalf("running gauge = %v, want 2 (one per queue)", got)
		}
		if got := metricValue(t, "background_jobs_queued", labels) - queuedBefore; got != 4 {
			t.Fatalf("queued gauge = %v, want 4", got)
		}
		shutdown(t, a)
		if got := metricValue(t, "background_jobs_running", labels) - runningBefore; got != 1 {
			t.Fatalf("running gauge after one queue stopped = %v, want 1", got)
		}
		shutdown(t, b)
		if r, q := metricValue(t, "background_jobs_running", labels)-runningBefore, metricValue(t, "background_jobs_queued", labels)-queuedBefore; r != 0 || q != 0 {
			t.Fatalf("gauges after shutdown: running %v queued %v, want 0", r, q)
		}
	})
}
