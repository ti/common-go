package background

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newTestQueue(opts QueueOptions) *Queue {
	opts.Standalone = true
	return NewQueue(opts)
}

// await follows the job until it is Done and returns every snapshot.
func await(t *testing.T, q *Queue, id string) []Job {
	t.Helper()
	var seen []Job
	for job, err := range q.Watch(t.Context(), id) {
		if err != nil {
			t.Fatalf("Watch(%s): %v (after %d snapshots)", id, err, len(seen))
		}
		seen = append(seen, job)
	}
	return seen
}

func last(jobs []Job) Job { return jobs[len(jobs)-1] }

func mustSubmit(t *testing.T, q *Queue, req SubmitRequest) Job {
	t.Helper()
	job, _, err := q.Submit(t.Context(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return job
}

func shutdown(t *testing.T, q *Queue) {
	t.Helper()
	if err := q.Shutdown(bg()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// The happy path: queued → running → progress → succeeded, observable by
// Get and Watch, with the handler seeing payload, labels and request values.
func TestQueueJobLifecycle(t *testing.T) {
	noLeaks(t)
	q := newTestQueue(QueueOptions{})
	gate := make(chan struct{})
	q.Handle("export", func(ctx context.Context, run *Run) ([]byte, error) {
		<-gate
		job := run.Job()
		if ctx.Value(ctxKey("subject")) != "alice" || job.Labels["owner"] != "alice" {
			return nil, errors.New("request values or labels are missing")
		}
		run.SetProgress(1, 2, "half")
		run.SetProgress(2, 2, "done")
		return append([]byte("result of "), job.Payload...), nil
	}, HandlerOptions{})

	reqCtx, endRequest := context.WithCancel(context.WithValue(bg(), ctxKey("subject"), "alice"))
	job, created, err := q.Submit(reqCtx, SubmitRequest{Kind: "export", Payload: []byte("p1"), Labels: map[string]string{"owner": "alice"}})
	endRequest() // the job outlives the request
	if err != nil || !created {
		t.Fatalf("Submit: created=%v err=%v", created, err)
	}
	if job.ID == "" || job.State.Done() || job.CreatedAt.IsZero() {
		t.Fatalf("submitted job = %+v", job)
	}
	if got, err := q.Get(bg(), job.ID); err != nil || got.State.Done() {
		t.Fatalf("Get before the gate opened: %+v, %v", got, err)
	}
	close(gate)
	seen := await(t, q, job.ID)
	final := last(seen)
	if final.State != StateSucceeded || string(final.Result) != "result of p1" || final.Attempt != 1 ||
		final.Progress != (Progress{2, 2, "done"}) || final.FinishedAt.IsZero() || final.Error != "" {
		t.Fatalf("final = %+v", final)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i].Revision <= seen[i-1].Revision {
			t.Fatalf("snapshots out of order: %d then %d", seen[i-1].Revision, seen[i].Revision)
		}
	}
	// After it is done the job is served from the store, and watching it
	// again yields exactly the final snapshot.
	if got, err := q.Get(bg(), job.ID); err != nil || got.State != StateSucceeded {
		t.Fatalf("Get after done: %+v, %v", got, err)
	}
	if again := await(t, q, job.ID); len(again) != 1 || again[0].State != StateSucceeded {
		t.Fatalf("second watch = %+v", again)
	}
	if _, err := q.Get(bg(), "missing"); !errors.Is(err, ErrNotFound) || status.Code(err) != codes.NotFound {
		t.Fatalf("Get(missing) = %v", err)
	}
	for _, err := range q.Watch(bg(), "missing") {
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Watch(missing) = %v", err)
		}
	}
	if _, _, err := q.Submit(bg(), SubmitRequest{Kind: "nope"}); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("Submit(unknown kind) = %v", err)
	}
	shutdown(t, q)
	if st := q.Stats(); st != (Stats{}) {
		t.Fatalf("stats after shutdown = %+v", st)
	}
}

// A watcher sees every state in order: queued, running, progress, final.
func TestWatchSequence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newTestQueue(QueueOptions{Workers: 1})
		q.Handle("k", func(_ context.Context, run *Run) ([]byte, error) {
			time.Sleep(time.Second)
			run.SetProgress(1, 3, "")
			time.Sleep(time.Second)
			run.SetProgress(2, 3, "")
			time.Sleep(time.Second)
			return nil, errors.New("disk full")
		}, HandlerOptions{})
		blocker := mustSubmit(t, q, SubmitRequest{Kind: "k"})
		job := mustSubmit(t, q, SubmitRequest{Kind: "k"}) // waits behind the first

		var got []string
		for j, err := range q.Watch(t.Context(), job.ID) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, fmt.Sprintf("%s %d/%d", j.State, j.Progress.Done, j.Progress.Total))
		}
		want := []string{"queued 0/0", "running 0/0", "running 1/3", "running 2/3", "failed 2/3"}
		if strings.Join(got, ", ") != strings.Join(want, ", ") {
			t.Fatalf("watched %v, want %v", got, want)
		}
		if final, _ := q.Get(bg(), job.ID); final.Error != "disk full" {
			t.Fatalf("error = %q", final.Error)
		}
		_ = blocker
		shutdown(t, q)
	})
}

// Never more than Workers jobs run at once, and jobs start in submission
// order.
func TestQueueBoundedConcurrency(t *testing.T) {
	noLeaks(t)
	q := newTestQueue(QueueOptions{Workers: 3, Backlog: 50})
	var running, peak atomic.Int32
	var mu sync.Mutex
	var order []string
	q.Handle("k", func(_ context.Context, run *Run) ([]byte, error) {
		n := running.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		mu.Lock()
		order = append(order, string(run.Job().Payload))
		mu.Unlock()
		time.Sleep(3 * time.Millisecond)
		running.Add(-1)
		return nil, nil
	}, HandlerOptions{})
	var ids []string
	for i := range 30 {
		ids = append(ids, mustSubmit(t, q, SubmitRequest{Kind: "k", Payload: fmt.Appendf(nil, "%02d", i)}).ID)
	}
	for _, id := range ids {
		if final := last(await(t, q, id)); final.State != StateSucceeded {
			t.Fatalf("job %s: %+v", id, final)
		}
	}
	if peak.Load() != 3 {
		t.Fatalf("peak concurrency = %d, want 3", peak.Load())
	}
	// With 3 workers a job can be overtaken by at most the 2 next to it.
	for i, p := range order {
		n, err := strconv.Atoi(p)
		if err != nil {
			t.Fatal(err)
		}
		if n < i-2 || n > i+2 {
			t.Fatalf("job %d started at position %d: not first in first out (%v)", n, i, order)
		}
	}
	shutdown(t, q)
}

// Overflow is explicit: reject with RESOURCE_EXHAUSTED, or block until
// there is room or the caller gives up.
func TestQueueOverflow(t *testing.T) {
	t.Run("reject", func(t *testing.T) {
		noLeaks(t)
		q := newTestQueue(QueueOptions{Workers: 1, Backlog: 1})
		release := make(chan struct{})
		q.Handle("k", func(context.Context, *Run) ([]byte, error) { <-release; return nil, nil }, HandlerOptions{})
		a := mustSubmit(t, q, SubmitRequest{Kind: "k"}) // running
		b := mustSubmit(t, q, SubmitRequest{Kind: "k"}) // backlog
		_, _, err := q.Submit(bg(), SubmitRequest{Kind: "k"})
		if !errors.Is(err, ErrQueueFull) || status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("third Submit = %v, want ErrQueueFull / RESOURCE_EXHAUSTED", err)
		}
		if st := q.Stats(); st != (Stats{Queued: 1, Running: 1}) {
			t.Fatalf("stats = %+v", st)
		}
		close(release)
		await(t, q, a.ID)
		await(t, q, b.ID)
		mustSubmit(t, q, SubmitRequest{Kind: "k"}) // room again
		shutdown(t, q)
	})
	t.Run("no backlog", func(t *testing.T) {
		noLeaks(t)
		q := newTestQueue(QueueOptions{Workers: 1, Backlog: -1})
		release := make(chan struct{})
		q.Handle("k", func(context.Context, *Run) ([]byte, error) { <-release; return nil, nil }, HandlerOptions{})
		mustSubmit(t, q, SubmitRequest{Kind: "k"})
		if _, _, err := q.Submit(bg(), SubmitRequest{Kind: "k"}); !errors.Is(err, ErrQueueFull) {
			t.Fatalf("Submit with a busy worker and no backlog = %v", err)
		}
		close(release)
		shutdown(t, q)
	})
	t.Run("block", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			q := newTestQueue(QueueOptions{Workers: 1, Backlog: 1, Overflow: OverflowBlock})
			q.Handle("k", func(context.Context, *Run) ([]byte, error) { time.Sleep(10 * time.Second); return nil, nil }, HandlerOptions{})
			mustSubmit(t, q, SubmitRequest{Kind: "k"})
			mustSubmit(t, q, SubmitRequest{Kind: "k"})

			// A caller with a deadline gives up.
			ctx, cancel := context.WithTimeout(bg(), 3*time.Second)
			defer cancel()
			if _, _, err := q.Submit(ctx, SubmitRequest{Kind: "k"}); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("blocked Submit with deadline = %v", err)
			}
			// A patient caller gets in when the first job ends (at 10s).
			start := time.Now()
			job := mustSubmit(t, q, SubmitRequest{Kind: "k"})
			if waited := time.Since(start); waited != 7*time.Second {
				t.Fatalf("blocked Submit waited %s, want 7s", waited)
			}
			// A blocked caller is released by shutdown.
			errs := make(chan error, 1)
			go func() {
				_, _, err := q.Submit(bg(), SubmitRequest{Kind: "k"})
				errs <- err
			}()
			synctest.Wait()
			q.Drain()
			if err := <-errs; !errors.Is(err, ErrShuttingDown) {
				t.Fatalf("blocked Submit at drain = %v", err)
			}
			_ = job
			shutdown(t, q)
		})
	})
}

// Errors marked Retryable are retried with backoff up to MaxAttempts;
// others fail at once.
func TestQueueRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newTestQueue(QueueOptions{})
		var attempts []time.Duration
		begin := time.Now()
		opts := HandlerOptions{MaxAttempts: 3, Backoff: func(attempt int) time.Duration { return time.Duration(attempt) * 10 * time.Second }}
		q.Handle("flaky", func(_ context.Context, run *Run) ([]byte, error) {
			attempts = append(attempts, time.Since(begin))
			if run.Job().Attempt < 3 {
				return nil, Retryable(errors.New("upstream 503"))
			}
			return []byte("ok"), nil
		}, opts)
		q.Handle("hopeless", func(context.Context, *Run) ([]byte, error) {
			return nil, Retryable(fmt.Errorf("wrapped: %w", errors.New("still 503")))
		}, opts)
		q.Handle("fatal", func(context.Context, *Run) ([]byte, error) { return nil, errors.New("bad input") }, opts)

		flaky := mustSubmit(t, q, SubmitRequest{Kind: "flaky"})
		seen := await(t, q, flaky.ID)
		if final := last(seen); final.State != StateSucceeded || final.Attempt != 3 || final.Error != "" {
			t.Fatalf("flaky final = %+v", final)
		}
		if len(attempts) != 3 || attempts[0] != 0 || attempts[1] != 10*time.Second || attempts[2] != 30*time.Second {
			t.Fatalf("attempts at %v, want [0s 10s 30s]", attempts)
		}
		var waitedForRetry bool
		for _, j := range seen {
			if j.State == StateQueued && j.Attempt == 1 && j.Error == "upstream 503" && !j.NextAttemptAt.IsZero() {
				waitedForRetry = true
			}
		}
		if !waitedForRetry {
			t.Fatalf("no snapshot showed the job queued for a retry: %+v", seen)
		}

		hopeless := mustSubmit(t, q, SubmitRequest{Kind: "hopeless"})
		if final := last(await(t, q, hopeless.ID)); final.State != StateFailed || final.Attempt != 3 || final.Error != "wrapped: still 503" {
			t.Fatalf("hopeless final = %+v", final)
		}
		fatal := mustSubmit(t, q, SubmitRequest{Kind: "fatal"})
		if final := last(await(t, q, fatal.ID)); final.State != StateFailed || final.Attempt != 1 {
			t.Fatalf("fatal final = %+v", final)
		}
		shutdown(t, q)
	})
	if !IsRetryable(fmt.Errorf("ctx: %w", Retryable(errors.New("x")))) || IsRetryable(errors.New("x")) || Retryable(nil) != nil {
		t.Fatal("Retryable/IsRetryable")
	}
	if d := DefaultBackoff(1); d < 800*time.Millisecond || d > 1200*time.Millisecond {
		t.Fatalf("DefaultBackoff(1) = %s", d)
	}
	if d := DefaultBackoff(50); d < 48*time.Second || d > 72*time.Second {
		t.Fatalf("DefaultBackoff(50) = %s", d)
	}
}

// A per-attempt timeout fails the job; a panic fails the job and is counted.
func TestQueueTimeoutAndPanic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newTestQueue(QueueOptions{})
		q.Handle("slow", func(ctx context.Context, _ *Run) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}, HandlerOptions{Timeout: 30 * time.Second})
		q.Handle("panics", func(context.Context, *Run) ([]byte, error) { panic("nil map") }, HandlerOptions{MaxAttempts: 5})

		start := time.Now()
		slow := mustSubmit(t, q, SubmitRequest{Kind: "slow"})
		final := last(await(t, q, slow.ID))
		if final.State != StateFailed || !strings.HasPrefix(final.Error, "timed out after 30s") || time.Since(start) != 30*time.Second {
			t.Fatalf("slow final = %+v after %s", final, time.Since(start))
		}
		before := metricValue(t, "background_panics_recovered_total", map[string]string{"name": "panics"})
		p := mustSubmit(t, q, SubmitRequest{Kind: "panics"})
		final = last(await(t, q, p.ID))
		if final.State != StateFailed || final.Error != "panic: nil map" || final.Attempt != 1 {
			t.Fatalf("panic final = %+v", final)
		}
		if got := metricValue(t, "background_panics_recovered_total", map[string]string{"name": "panics"}) - before; got != 1 {
			t.Fatalf("panic counter grew by %v", got)
		}
		shutdown(t, q)
	})
}

// Cancel: a queued job is cancelled at once, a running one when its handler
// returns, a job waiting for a retry never runs again, and a finished job
// is left alone.
func TestQueueCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newTestQueue(QueueOptions{Workers: 1})
		var causes []error
		q.Handle("k", func(ctx context.Context, _ *Run) ([]byte, error) {
			select {
			case <-ctx.Done():
				causes = append(causes, context.Cause(ctx))
				return nil, ctx.Err()
			case <-time.After(time.Minute):
				return []byte("finished"), nil
			}
		}, HandlerOptions{})
		var retried int
		q.Handle("retry", func(context.Context, *Run) ([]byte, error) {
			retried++
			return nil, Retryable(errors.New("again"))
		}, HandlerOptions{MaxAttempts: 5, Backoff: func(int) time.Duration { return time.Hour }})

		running := mustSubmit(t, q, SubmitRequest{Kind: "k"})
		queued := mustSubmit(t, q, SubmitRequest{Kind: "k"})
		synctest.Wait()

		// Queued: cancelled immediately, never runs.
		got, err := q.Cancel(bg(), queued.ID)
		if err != nil || got.State != StateCancelled || got.Attempt != 0 {
			t.Fatalf("Cancel(queued) = %+v, %v", got, err)
		}
		// Running: still running when Cancel returns, cancelled afterwards.
		got, err = q.Cancel(bg(), running.ID)
		if err != nil || got.State != StateRunning {
			t.Fatalf("Cancel(running) = %+v, %v", got, err)
		}
		final := last(await(t, q, running.ID))
		if final.State != StateCancelled || len(causes) != 1 || !errors.Is(causes[0], ErrCancelled) {
			t.Fatalf("running job after cancel = %+v, causes %v", final, causes)
		}
		// Done: no-op.
		if got, err = q.Cancel(bg(), running.ID); err != nil || got.State != StateCancelled {
			t.Fatalf("Cancel(done) = %+v, %v", got, err)
		}
		if _, err = q.Cancel(bg(), "missing"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Cancel(missing) = %v", err)
		}
		// Waiting for a retry.
		r := mustSubmit(t, q, SubmitRequest{Kind: "retry"})
		synctest.Wait()
		if got, err = q.Cancel(bg(), r.ID); err != nil || got.State != StateCancelled {
			t.Fatalf("Cancel(retrying) = %+v, %v", got, err)
		}
		time.Sleep(2 * time.Hour)
		synctest.Wait()
		if retried != 1 || q.Stats() != (Stats{}) {
			t.Fatalf("retried %d times after cancel; stats %+v", retried, q.Stats())
		}
		// A handler that completes despite the cancellation succeeds.
		q.Handle("stubborn", func(ctx context.Context, _ *Run) ([]byte, error) {
			<-ctx.Done()
			return []byte("done anyway"), nil
		}, HandlerOptions{})
		s := mustSubmit(t, q, SubmitRequest{Kind: "stubborn"})
		synctest.Wait()
		if _, err := q.Cancel(bg(), s.ID); err != nil {
			t.Fatal(err)
		}
		if final := last(await(t, q, s.ID)); final.State != StateSucceeded {
			t.Fatalf("stubborn final = %+v", final)
		}
		shutdown(t, q)
	})
}

// Submitting with the same key returns the same job, also concurrently and
// after the job finished, until the retention period has passed.
func TestQueueIdempotentSubmit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newTestQueue(QueueOptions{Retention: 10 * time.Minute})
		var runs atomic.Int32
		q.Handle("k", func(context.Context, *Run) ([]byte, error) {
			runs.Add(1)
			time.Sleep(time.Second)
			return nil, nil
		}, HandlerOptions{})
		q.Handle("other", func(context.Context, *Run) ([]byte, error) { return nil, nil }, HandlerOptions{})

		var wg sync.WaitGroup
		var createdCount atomic.Int32
		ids := make([]string, 20)
		for i := range ids {
			wg.Go(func() {
				job, created, err := q.Submit(bg(), SubmitRequest{Kind: "k", Key: "req-1"})
				if err != nil {
					t.Error(err)
				}
				if created {
					createdCount.Add(1)
				}
				ids[i] = job.ID
			})
		}
		wg.Wait()
		for _, id := range ids {
			if id != ids[0] {
				t.Fatalf("different jobs for one key: %v", ids)
			}
		}
		if createdCount.Load() != 1 {
			t.Fatalf("created %d jobs, want 1", createdCount.Load())
		}
		await(t, q, ids[0])

		// Finished but retained: still the same job, in its final state.
		again, created, err := q.Submit(bg(), SubmitRequest{Kind: "k", Key: "req-1"})
		if err != nil || created || again.ID != ids[0] || again.State != StateSucceeded {
			t.Fatalf("resubmit after done = %+v created=%v err=%v", again, created, err)
		}
		// The key is scoped by kind; an empty key never deduplicates.
		if other, created, _ := q.Submit(bg(), SubmitRequest{Kind: "other", Key: "req-1"}); !created || other.ID == ids[0] {
			t.Fatal("key must be scoped by kind")
		}
		a := mustSubmit(t, q, SubmitRequest{Kind: "other"})
		b := mustSubmit(t, q, SubmitRequest{Kind: "other"})
		if a.ID == b.ID {
			t.Fatal("jobs without a key must be distinct")
		}
		// After the retention period the job is gone and the key is free.
		time.Sleep(12 * time.Minute)
		synctest.Wait()
		if _, err := q.Get(bg(), ids[0]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get after retention = %v, want ErrNotFound", err)
		}
		fresh, created, err := q.Submit(bg(), SubmitRequest{Kind: "k", Key: "req-1"})
		if err != nil || !created || fresh.ID == ids[0] {
			t.Fatalf("submit after retention = %+v created=%v err=%v", fresh, created, err)
		}
		await(t, q, fresh.ID)
		if runs.Load() != 2 {
			t.Fatalf("handler ran %d times, want 2", runs.Load())
		}
		shutdown(t, q)
	})
}

// Shutdown: stop accepting, let running jobs finish within the grace
// period, interrupt the rest (queued, waiting for retry, still running),
// and end every goroutine. (synctest fails the test if a goroutine of the
// bubble is still blocked when the function returns.)
func TestQueueShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := NewMemoryStore()
		q := newTestQueue(QueueOptions{Workers: 3, Grace: 10 * time.Second, Store: store})
		var causes sync.Map
		work := func(d time.Duration) Handler {
			return func(ctx context.Context, run *Run) ([]byte, error) {
				select {
				case <-time.After(d):
					return []byte("finished"), nil
				case <-ctx.Done():
					causes.Store(run.Job().ID, context.Cause(ctx))
					return nil, ctx.Err()
				}
			}
		}
		q.Handle("quick", work(5*time.Second), HandlerOptions{})
		q.Handle("long", work(time.Hour), HandlerOptions{})
		q.Handle("retry", func(context.Context, *Run) ([]byte, error) { return nil, Retryable(errors.New("503")) },
			HandlerOptions{MaxAttempts: 9, Backoff: func(int) time.Duration { return time.Hour }})

		retrying := mustSubmit(t, q, SubmitRequest{Kind: "retry"})
		synctest.Wait() // failed once, now waiting an hour
		quick := mustSubmit(t, q, SubmitRequest{Kind: "quick"})
		long1 := mustSubmit(t, q, SubmitRequest{Kind: "long"})
		long2 := mustSubmit(t, q, SubmitRequest{Kind: "long"})
		queued := mustSubmit(t, q, SubmitRequest{Kind: "quick"}) // no free worker
		synctest.Wait()

		// A watcher of a running job is told to go away when draining starts.
		watchErr := make(chan error, 1)
		go func() {
			for _, err := range q.Watch(bg(), long1.ID) {
				if err != nil {
					watchErr <- err
					return
				}
			}
			watchErr <- nil
		}()
		synctest.Wait()

		start := time.Now()
		if err := q.Shutdown(bg()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		if took := time.Since(start); took != 10*time.Second {
			t.Fatalf("Shutdown took %s, want the 10s grace period", took)
		}
		if err := <-watchErr; !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("watcher ended with %v", err)
		}
		if _, _, err := q.Submit(bg(), SubmitRequest{Kind: "quick"}); !errors.Is(err, ErrShuttingDown) || status.Code(err) != codes.Unavailable {
			t.Fatalf("Submit after shutdown = %v, want ErrShuttingDown / UNAVAILABLE", err)
		}

		want := map[string]State{
			quick.ID:    StateSucceeded,   // finished within the grace period
			long1.ID:    StateInterrupted, // cancelled when the grace period ended
			long2.ID:    StateInterrupted,
			queued.ID:   StateInterrupted, // never started
			retrying.ID: StateInterrupted, // was waiting for a retry
		}
		for id, state := range want {
			job, err := q.Get(bg(), id)
			if err != nil || job.State != state || job.FinishedAt.IsZero() {
				t.Errorf("job %s (%s): state=%s err=%v, want %s", id, job.Kind, job.State, err, state)
			}
			if state == StateInterrupted && !strings.HasPrefix(job.Error, "interrupted:") {
				t.Errorf("interrupted job error = %q", job.Error)
			}
		}
		if job, _ := q.Get(bg(), queued.ID); job.Attempt != 0 {
			t.Errorf("the queued job was started during shutdown: %+v", job)
		}
		for _, id := range []string{long1.ID, long2.ID} {
			if cause, _ := causes.Load(id); cause != ErrShuttingDown { //nolint:errorlint // exact cause
				t.Errorf("job %s cancellation cause = %v, want ErrShuttingDown", id, cause)
			}
		}
		if _, ok := causes.Load(quick.ID); ok {
			t.Error("the quick job was cancelled although it finished within the grace period")
		}
		if store.Len() != 5 || q.Stats() != (Stats{}) {
			t.Errorf("store has %d jobs, stats %+v", store.Len(), q.Stats())
		}
		// Shutdown is idempotent.
		if err := q.Shutdown(bg()); err != nil {
			t.Fatal(err)
		}
	})
}

// Draining (as graceful.Draining does) stops accepting and starting, but
// lets the running job go on.
func TestQueueDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newTestQueue(QueueOptions{Workers: 1, Grace: time.Minute})
		q.Handle("k", func(context.Context, *Run) ([]byte, error) { time.Sleep(20 * time.Second); return nil, nil }, HandlerOptions{})
		running := mustSubmit(t, q, SubmitRequest{Kind: "k"})
		queued := mustSubmit(t, q, SubmitRequest{Kind: "k"})
		synctest.Wait()
		q.Drain()
		if _, _, err := q.Submit(bg(), SubmitRequest{Kind: "k"}); !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("Submit while draining = %v", err)
		}
		time.Sleep(30 * time.Second) // the running job ends; the queued one must not start
		synctest.Wait()
		if job, _ := q.Get(bg(), running.ID); job.State != StateSucceeded {
			t.Fatalf("running job = %+v", job)
		}
		if job, _ := q.Get(bg(), queued.ID); job.State != StateQueued {
			t.Fatalf("queued job = %+v, want still queued", job)
		}
		shutdown(t, q)
		if job, _ := q.Get(bg(), queued.ID); job.State != StateInterrupted {
			t.Fatalf("queued job after shutdown = %+v", job)
		}
	})
}

// A handler that ignores cancellation is reported, and its job is recorded
// as interrupted anyway.
func TestQueueShutdownStuckHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newTestQueue(QueueOptions{Grace: time.Second})
		release := make(chan struct{})
		q.Handle("stuck", func(context.Context, *Run) ([]byte, error) { <-release; return nil, nil }, HandlerOptions{})
		job := mustSubmit(t, q, SubmitRequest{Kind: "stuck"})
		synctest.Wait()
		start := time.Now()
		err := q.Shutdown(bg())
		if err == nil || time.Since(start) != time.Second+cancelWait {
			t.Fatalf("Shutdown = %v after %s", err, time.Since(start))
		}
		got, _ := q.Get(bg(), job.ID)
		if got.State != StateInterrupted || !strings.Contains(got.Error, "ignored cancellation") {
			t.Fatalf("job = %+v", got)
		}
		close(release) // the late return must not change the record
		synctest.Wait()
		if got, _ = q.Get(bg(), job.ID); got.State != StateInterrupted {
			t.Fatalf("job after the late return = %+v", got)
		}
	})
}
