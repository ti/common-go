package background

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func newTestRunner(grace time.Duration) *Runner {
	return NewRunner(RunnerOptions{Grace: grace, Standalone: true})
}

// A panic is recovered, counted, and does not take the process down.
func TestGoRecoversPanic(t *testing.T) {
	noLeaks(t)
	r := newTestRunner(time.Second)
	labels := map[string]string{"name": "test.panic"}
	before := metricValue(t, "background_panics_recovered_total", labels)
	if !r.Go(bg(), "test.panic", func(context.Context) error { panic("boom") }) {
		t.Fatal("Go did not start the task")
	}
	if err := r.Shutdown(bg()); err != nil {
		t.Fatal(err)
	}
	if got := metricValue(t, "background_panics_recovered_total", labels) - before; got != 1 {
		t.Fatalf("panics counter grew by %v, want 1", got)
	}
	if got := metricValue(t, "background_task_runs_total", map[string]string{"name": "test.panic", "result": "panic"}); got < 1 {
		t.Fatalf("task_runs{result=panic} = %v", got)
	}
	if got := metricValue(t, "background_tasks_running", labels); got != 0 {
		t.Fatalf("tasks_running = %v after the task ended", got)
	}
}

// The task context keeps the request's values, ignores its cancellation and
// deadline, has its own logger, and is cancelled by Shutdown only after the
// grace period, with ErrShuttingDown as the cause.
func TestGoContextDetachedFromRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newTestRunner(10 * time.Second)
		reqCtx, endRequest := context.WithTimeout(context.WithValue(bg(), ctxKey("subject"), "alice"), time.Second)

		var subject any
		var cause error
		var cancelledAfter time.Duration
		start := time.Now()
		r.Go(reqCtx, "test.detached", func(ctx context.Context) error {
			subject = ctx.Value(ctxKey("subject"))
			<-ctx.Done()
			cause = context.Cause(ctx)
			cancelledAfter = time.Since(start)
			return nil
		})
		endRequest()
		time.Sleep(time.Minute) // far beyond the request's deadline
		synctest.Wait()
		if r.Running() != 1 {
			t.Fatal("the task ended with the request")
		}
		if err := r.Shutdown(bg()); err != nil {
			t.Fatal(err)
		}
		if subject != "alice" {
			t.Fatalf("request value = %v, want alice", subject)
		}
		if !errors.Is(cause, ErrShuttingDown) {
			t.Fatalf("cancellation cause = %v, want ErrShuttingDown", cause)
		}
		if want := time.Minute + 10*time.Second; cancelledAfter != want {
			t.Fatalf("cancelled after %s, want %s (after the grace period)", cancelledAfter, want)
		}
	})
}

// Shutdown lets tasks finish within the grace period without cancelling
// them, is bounded by its context, and reports tasks that ignore
// cancellation.
func TestRunnerShutdown(t *testing.T) {
	t.Run("finishes within grace", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newTestRunner(10 * time.Second)
			var cancelled, finished bool
			r.Go(bg(), "test.fast", func(ctx context.Context) error {
				select {
				case <-time.After(3 * time.Second):
					finished = true
				case <-ctx.Done():
					cancelled = true
				}
				return nil
			})
			start := time.Now()
			if err := r.Shutdown(bg()); err != nil {
				t.Fatal(err)
			}
			if cancelled || !finished || time.Since(start) != 3*time.Second {
				t.Fatalf("cancelled=%v finished=%v after %s", cancelled, finished, time.Since(start))
			}
		})
	})
	t.Run("bounded by the closer context", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newTestRunner(time.Hour)
			r.Go(bg(), "test.slow", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
			ctx, cancel := context.WithTimeout(bg(), 2*time.Second)
			defer cancel()
			start := time.Now()
			if err := r.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			if time.Since(start) != 2*time.Second {
				t.Fatalf("Shutdown took %s, want 2s", time.Since(start))
			}
		})
	})
	t.Run("task ignores cancellation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newTestRunner(time.Second)
			release := make(chan struct{})
			r.Go(bg(), "test.stuck", func(context.Context) error { <-release; return nil })
			start := time.Now()
			err := r.Shutdown(bg())
			if err == nil {
				t.Fatal("Shutdown must report the task that did not stop")
			}
			if want := time.Second + cancelWait; time.Since(start) != want {
				t.Fatalf("Shutdown took %s, want %s", time.Since(start), want)
			}
			close(release)
		})
	})
	t.Run("nothing starts afterwards", func(t *testing.T) {
		noLeaks(t)
		r := newTestRunner(time.Second)
		if err := r.Shutdown(bg()); err != nil {
			t.Fatal(err)
		}
		if r.Go(bg(), "test.late", func(context.Context) error { t.Error("ran after Shutdown"); return nil }) {
			t.Fatal("Go reported a start after Shutdown")
		}
		stop := r.Every("test.late", time.Millisecond, func(context.Context) error { t.Error("ticked after Shutdown"); return nil })
		stop()
	})
}

// recordingTracer captures span start options.
type recordingTracer struct {
	noop.Tracer
	mu      sync.Mutex
	configs map[string]trace.SpanConfig
}

func (r *recordingTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	r.mu.Lock()
	r.configs[name] = trace.NewSpanStartConfig(opts...)
	r.mu.Unlock()
	return r.Tracer.Start(ctx, name, opts...)
}

type recordingProvider struct {
	noop.TracerProvider
	tracer *recordingTracer
}

func (p recordingProvider) Tracer(string, ...trace.TracerOption) trace.Tracer { return p.tracer }

// The task's span is a new root linked to the request's span.
func TestSpanLinksToRequest(t *testing.T) {
	rec := &recordingTracer{configs: map[string]trace.SpanConfig{}}
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(recordingProvider{tracer: rec})
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	reqSpan := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3}, SpanID: trace.SpanID{4, 5, 6}, TraceFlags: trace.FlagsSampled,
	})
	reqCtx := trace.ContextWithSpanContext(bg(), reqSpan)

	r := newTestRunner(time.Second)
	r.Go(reqCtx, "test.span", func(context.Context) error { return nil })
	if err := r.Shutdown(bg()); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	cfg, ok := rec.configs["background test.span"]
	rec.mu.Unlock()
	if !ok {
		t.Fatal("no span started for the task")
	}
	if !cfg.NewRoot() {
		t.Error("the task span must be a new root")
	}
	if links := cfg.Links(); len(links) != 1 || !links[0].SpanContext.Equal(reqSpan) {
		t.Errorf("links = %+v, want one link to the request span", links)
	}
}

// Every: fixed interval, no overlap, a panic does not stop the loop, stop
// ends it.
func TestEvery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newTestRunner(time.Second)
		var starts []time.Duration
		var running, overlaps atomic.Int32
		begin := time.Now()
		stop := r.Every("test.every", 10*time.Second, func(context.Context) error {
			if running.Add(1) > 1 {
				overlaps.Add(1)
			}
			defer running.Add(-1)
			starts = append(starts, time.Since(begin))
			switch len(starts) {
			case 2:
				time.Sleep(25 * time.Second) // longer than the interval
			case 3:
				panic("tick failed")
			}
			return nil
		}, WithJitter(0))

		time.Sleep(60 * time.Second)
		synctest.Wait()
		stop()
		synctest.Wait()
		if r.Running() != 0 {
			t.Fatal("the loop goroutine is still running after stop")
		}
		// Ticks at 10s and 20s; the second run lasts until 45s, so the third
		// starts right then (missed ticks are not made up), panics, and the
		// loop keeps its cadence: 55s.
		want := []time.Duration{10 * time.Second, 20 * time.Second, 45 * time.Second, 55 * time.Second}
		if len(starts) != len(want) {
			t.Fatalf("starts = %v, want %v", starts, want)
		}
		for i := range want {
			if starts[i] != want[i] {
				t.Fatalf("starts = %v, want %v", starts, want)
			}
		}
		if overlaps.Load() != 0 {
			t.Fatal("runs overlapped")
		}
		if err := r.Shutdown(bg()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestEveryOptions(t *testing.T) {
	t.Run("immediate and timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newTestRunner(time.Second)
			var first time.Duration = -1
			var timedOut bool
			begin := time.Now()
			r.Every("test.immediate", time.Minute, func(ctx context.Context) error {
				if first < 0 {
					first = time.Since(begin)
				}
				<-ctx.Done()
				timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
				return ctx.Err()
			}, WithImmediate(), WithTimeout(5*time.Second), WithJitter(0))
			time.Sleep(10 * time.Second)
			synctest.Wait()
			if first != 0 || !timedOut {
				t.Fatalf("first run at %s (want 0), timed out = %v (want true)", first, timedOut)
			}
			if err := r.Shutdown(bg()); err != nil {
				t.Fatal(err)
			}
		})
	})
	t.Run("jitter stays within bounds", func(t *testing.T) {
		lo, hi := time.Hour, time.Duration(0)
		for range 2000 {
			d := jittered(time.Second, 0.1)
			lo, hi = min(lo, d), max(hi, d)
		}
		if lo < 900*time.Millisecond || hi > 1100*time.Millisecond || hi-lo < 100*time.Millisecond {
			t.Fatalf("jittered range [%s, %s], want within [900ms, 1.1s] and spread out", lo, hi)
		}
	})
	t.Run("drain stops ticking but not the running tick", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newTestRunner(time.Minute)
			var runs int
			var cancelled bool
			r.Every("test.drain", 10*time.Second, func(ctx context.Context) error {
				runs++
				select {
				case <-time.After(15 * time.Second):
				case <-ctx.Done():
					cancelled = true
				}
				return nil
			}, WithJitter(0))
			time.Sleep(12 * time.Second) // first run is in progress
			r.Drain()
			start := time.Now()
			if err := r.Shutdown(bg()); err != nil {
				t.Fatal(err)
			}
			if runs != 1 || cancelled || time.Since(start) != 13*time.Second {
				t.Fatalf("runs=%d cancelled=%v shutdown took %s; want 1 run finishing uncancelled after 13s", runs, cancelled, time.Since(start))
			}
		})
	})
}

// With a Locker, one loop is the leader and runs every tick; the other takes
// over when the leader stops.
func TestEveryWithLocker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		locker := NewLocalLocker()
		a, b := newTestRunner(time.Second), newTestRunner(time.Second)
		var runsA, runsB atomic.Int32
		stopA := a.Every("test.leader", 10*time.Second, func(context.Context) error { runsA.Add(1); return nil },
			WithLocker(locker), WithJitter(0), WithImmediate())
		time.Sleep(time.Second)
		b.Every("test.leader", 10*time.Second, func(context.Context) error { runsB.Add(1); return nil },
			WithLocker(locker), WithJitter(0), WithImmediate())

		time.Sleep(60 * time.Second)
		synctest.Wait()
		if runsA.Load() != 7 || runsB.Load() != 0 {
			t.Fatalf("leader ran %d times (want 7), follower %d (want 0)", runsA.Load(), runsB.Load())
		}
		stopA() // releases the lock
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if runsA.Load() != 7 || runsB.Load() != 3 {
			t.Fatalf("after the leader stopped: A=%d (want 7), B=%d (want 3)", runsA.Load(), runsB.Load())
		}
		if err := errors.Join(a.Shutdown(bg()), b.Shutdown(bg())); err != nil {
			t.Fatal(err)
		}
		// Shutdown released the lock.
		if _, unlock, err := locker.TryLock(bg(), "test.leader"); err != nil {
			t.Fatalf("lock still held after shutdown: %v", err)
		} else {
			unlock()
		}
	})
}

// A lock that is lost cancels the run and the loop competes again.
func TestEveryLockLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			mu       sync.Mutex
			lose     context.CancelFunc
			acquired int
			runs     int
		)
		locker := LockerFunc(func(ctx context.Context, _ string) (context.Context, context.CancelFunc, error) {
			mu.Lock()
			defer mu.Unlock()
			acquired++
			if acquired == 2 {
				return nil, nil, errors.New("lock service unreachable")
			}
			lockCtx, cancel := context.WithCancel(ctx)
			lose = cancel
			return lockCtx, cancel, nil
		})
		r := newTestRunner(time.Second)
		r.Every("test.lost", 10*time.Second, func(context.Context) error {
			mu.Lock()
			runs++
			mu.Unlock()
			return nil
		}, WithLocker(locker), WithJitter(0))
		time.Sleep(25 * time.Second) // runs at 10s and 20s holding the lock
		mu.Lock()
		lose()
		mu.Unlock()
		time.Sleep(20 * time.Second) // 30s: lock error, skipped; 40s: re-acquired
		if err := r.Shutdown(bg()); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if acquired != 3 || runs != 3 {
			t.Fatalf("acquired=%d runs=%d, want 3 and 3", acquired, runs)
		}
	})
}

// Loop: restarted with backoff after an error or a panic, cancelled as soon
// as draining starts (no grace period), ended by a nil return.
func TestLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newTestRunner(time.Hour) // the grace period must not matter
		var mu sync.Mutex
		var starts []time.Duration
		var cause error
		begin := time.Now()
		r.Loop("test.loop", func(ctx context.Context) error {
			mu.Lock()
			n := len(starts)
			starts = append(starts, time.Since(begin))
			mu.Unlock()
			switch n {
			case 0:
				return errors.New("connection lost")
			case 1:
				panic("bad message")
			}
			<-ctx.Done()
			mu.Lock()
			cause = context.Cause(ctx)
			mu.Unlock()
			return ctx.Err()
		})
		time.Sleep(time.Minute)
		synctest.Wait()
		mu.Lock()
		if len(starts) != 3 || starts[0] != 0 ||
			starts[1] < 800*time.Millisecond || starts[1] > 1200*time.Millisecond || // DefaultBackoff(1)
			starts[2]-starts[1] < 1600*time.Millisecond || starts[2]-starts[1] > 2400*time.Millisecond { // DefaultBackoff(2)
			t.Fatalf("starts = %v, want restarts after about 1s and 2s more", starts)
		}
		mu.Unlock()

		start := time.Now()
		if err := r.Shutdown(bg()); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if time.Since(start) != 0 || !errors.Is(cause, ErrShuttingDown) {
			t.Fatalf("Shutdown took %s (want 0: loops are cancelled at drain), cause %v", time.Since(start), cause)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		r := newTestRunner(time.Second)
		var runs atomic.Int32
		r.Loop("test.loop.done", func(context.Context) error { runs.Add(1); return nil })
		stop := r.Loop("test.loop.stop", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
		time.Sleep(time.Hour)
		synctest.Wait()
		if runs.Load() != 1 || r.Running() != 1 {
			t.Fatalf("a loop that returned nil ran %d times; %d loops running (want 1 and 1)", runs.Load(), r.Running())
		}
		stop()
		synctest.Wait()
		if r.Running() != 0 {
			t.Fatal("stop did not end the loop")
		}
		if err := r.Shutdown(bg()); err != nil {
			t.Fatal(err)
		}
	})
}
