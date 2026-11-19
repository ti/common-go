package background

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/ti/common-go/graceful"
	"github.com/ti/common-go/log"
)

// RunnerOptions configure a [Runner]. The zero value is ready to use.
type RunnerOptions struct {
	// Grace is how long Shutdown lets running tasks finish before it cancels
	// their contexts. Default [DefaultGrace].
	Grace time.Duration
	// Standalone detaches the runner from package graceful: it does not
	// register a closer and ignores graceful.Draining. The owner calls
	// Shutdown. Meant for tests and for programs that do not use graceful.
	Standalone bool
}

// Runner supervises goroutines and periodic tasks and stops them at
// shutdown. Most programs use the process-wide one through [Go] and [Every].
type Runner struct {
	life  *lifecycle
	grace time.Duration
	track tracker
}

// NewRunner returns a runner. Unless opts.Standalone is set, it registers
// its Shutdown with graceful.AddWorkCloser, so it is shut down after the
// servers have stopped serving and before the dependencies are closed,
// wherever in the program it is created.
func NewRunner(opts RunnerOptions) *Runner {
	r := &Runner{life: newLifecycle(opts.Standalone), grace: opts.Grace}
	if r.grace <= 0 {
		r.grace = DefaultGrace
	}
	if !opts.Standalone {
		graceful.AddWorkCloser(r.Shutdown)
	}
	return r
}

var (
	defaultMu     sync.Mutex
	defaultRunner *Runner
)

// Default returns the process-wide runner used by [Go], [Loop] and [Every].
// It is created on first use, which may be anywhere, also inside a request
// handler: package graceful shuts it down after the servers have stopped
// serving.
//
// Once the process is draining, no runner is created any more: if there is
// none, or the one there was has been shut down, Default returns a runner
// that starts nothing (Go reports false), because nothing would supervise
// or stop that work. A new lifecycle (a later graceful.Start in a test
// binary) gets a new runner.
func Default() *Runner {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultRunner != nil && !defaultRunner.track.isClosed() {
		return defaultRunner
	}
	if graceful.IsDraining() {
		return stoppedRunner()
	}
	defaultRunner = NewRunner(RunnerOptions{})
	return defaultRunner
}

// stoppedRunner is a runner that has been shut down and starts nothing.
var stoppedRunner = sync.OnceValue(func() *Runner {
	r := NewRunner(RunnerOptions{Standalone: true})
	_ = r.Shutdown(context.Background())
	return r
})

// Go runs fn on the process-wide runner. See [Runner.Go].
func Go(ctx context.Context, name string, fn func(ctx context.Context) error) bool {
	return Default().Go(ctx, name, fn)
}

// Every runs fn periodically on the process-wide runner. See [Runner.Every].
func Every(name string, interval time.Duration, fn func(ctx context.Context) error, opts ...EveryOption) (stop func()) {
	return Default().Every(name, interval, fn, opts...)
}

// Go runs fn in a supervised goroutine and returns immediately.
//
//   - The context given to fn carries the values of ctx (so request-scoped
//     data such as the authenticated subject stays readable) but not its
//     cancellation or deadline: the work outlives the request. It is
//     cancelled, with cause [ErrShuttingDown], when Shutdown runs out of
//     grace. fn must return soon after that.
//   - The context has its own logger (log.Extract) tagged with the task name,
//     and a new trace span linked to the span in ctx.
//   - A panic in fn is recovered, logged with its stack and counted in
//     background_panics_recovered_total; a returned error is logged.
//
// name identifies the task in logs and metrics; use a static string.
//
// Go reports whether the task was started. It is false only after Shutdown
// has finished waiting, when the process is about to exit.
func (r *Runner) Go(ctx context.Context, name string, fn func(ctx context.Context) error) bool {
	if !r.track.add() {
		metrics().taskRuns.WithLabelValues(name, "skipped").Inc()
		logWith("background.Go", map[string]any{"task": name}).Warn("not started: shutting down")
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	taskCtx, cancel := r.detach(ctx, name)
	go func() {
		defer r.track.done()
		defer cancel()
		_ = r.runTask(taskCtx, ctx, name, 0, fn)
	}()
	return true
}

// detach derives the context of a task: the values of origin, a task logger,
// and cancellation by the runner's hard stop only.
func (r *Runner) detach(origin context.Context, name string) (context.Context, context.CancelFunc) {
	base := log.NewContext(context.WithoutCancel(origin), map[string]any{"task": name})
	ctx, cancel := context.WithCancelCause(base)
	stop := context.AfterFunc(r.life.hard, func() { cancel(context.Cause(r.life.hard)) })
	return ctx, func() {
		stop()
		cancel(nil)
	}
}

// runTask runs fn once with a span, metrics, panic recovery and logging.
func (r *Runner) runTask(ctx, origin context.Context, name string, timeout time.Duration,
	fn func(ctx context.Context) error,
) error {
	m := metrics()
	m.tasksRunning.WithLabelValues(name).Inc()
	defer m.tasksRunning.WithLabelValues(name).Dec()

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ctx, span := startSpan(ctx, origin, "background "+name, attribute.String("background.task", name))
	start := time.Now()
	err := protect(name, func() error { return fn(ctx) })
	m.taskDuration.WithLabelValues(name).Observe(time.Since(start).Seconds())
	endSpan(span, err)

	result := "ok"
	if err != nil {
		result = "error"
		logger := logWith("background.Task", mergeFields(map[string]any{"task": name}, errorFields(err)))
		if _, isPanic := errors.AsType[*PanicError](err); isPanic {
			result = "panic"
			logger.Error("background task panicked")
		} else {
			logger.Warn("background task failed")
		}
	}
	m.taskRuns.WithLabelValues(name, result).Inc()
	return err
}

// logWith returns a logger with an action and fields.
func logWith(action string, fields map[string]any) log.StdLogger {
	fields["action"] = action
	return log.With(fields)
}

func mergeFields(a, b map[string]any) map[string]any {
	maps.Copy(a, b)
	return a
}

// Loop runs fn on the process-wide runner until shutdown. See [Runner.Loop].
func Loop(name string, fn func(ctx context.Context) error) (stop func()) {
	return Default().Loop(name, fn)
}

// Loop runs a long-lived function, such as a subscription or a consumer,
// that is meant to run until the process shuts down.
//
//   - Its context is cancelled as soon as the process starts draining (cause
//     [ErrShuttingDown]), not after the grace period: a loop has nothing to
//     finish, and waiting for it would only delay the shutdown. Use [Runner.Go]
//     for work that should be given time to complete.
//   - If fn returns an error or panics, it is logged, counted and started
//     again after a backoff ([DefaultBackoff], reset once a run lasted a
//     minute). If fn returns nil, the loop ends.
//
// stop cancels the context and ends the loop; it does not wait.
func (r *Runner) Loop(name string, fn func(ctx context.Context) error) (stop func()) {
	if !r.track.add() {
		return func() {}
	}
	ctx, release := r.detach(context.Background(), name)
	ctx, cancel := context.WithCancelCause(ctx)
	var once sync.Once
	stop = func() { once.Do(func() { cancel(context.Canceled) }) }
	go func() {
		defer r.track.done()
		defer release()
		// Turn the drain signals into cancellation. The watcher ends with
		// the loop, which waits for it.
		watcher := make(chan struct{})
		go func() {
			defer close(watcher)
			select {
			case <-r.life.own:
			case <-r.life.process():
			case <-ctx.Done():
			}
			cancel(ErrShuttingDown)
		}()
		defer func() {
			stop()
			<-watcher
		}()

		for attempt := 1; ; attempt++ {
			start := time.Now()
			err := r.runTask(ctx, nil, name, 0, fn)
			if err == nil || ctx.Err() != nil {
				return
			}
			if time.Since(start) >= time.Minute {
				attempt = 1
			}
			timer := time.NewTimer(DefaultBackoff(attempt))
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
	}()
	return stop
}

// Drain stops periodic tasks from starting new runs and cancels loops
// started with Loop. Other running work is not affected. It is idempotent.
func (r *Runner) Drain() { r.life.drain() }

// Running returns the number of supervised goroutines (including periodic
// task loops) that have not returned yet.
func (r *Runner) Running() int { return r.track.count() }

// Shutdown stops the runner: periodic tasks stop ticking, loops are
// cancelled, running tasks get
// the grace period (bounded by ctx) to finish, then their contexts are
// cancelled and Shutdown waits a few seconds for them to return. It returns
// an error if some task still has not returned; such a goroutine is leaked.
// After Shutdown, Go and Every start nothing.
func (r *Runner) Shutdown(ctx context.Context) error {
	r.Drain()
	cancelled, stuck := r.life.stopWork(ctx, &r.track, r.grace, ErrShuttingDown)
	if cancelled > 0 {
		log.Action("background.Shutdown").Warn("cancelled %d background tasks that were still running after %s", cancelled, r.grace.String())
	}
	if stuck > 0 {
		return fmt.Errorf("background: %d tasks ignored cancellation and are still running", stuck)
	}
	return nil
}

// Locker gives one holder at a time the right to run a named periodic task.
//
// TryLock does not wait. It returns [ErrLockNotAcquired] when another holder
// has the lock. On success the returned context is derived from ctx and is
// cancelled when the lock is lost (for example because the lock service
// became unreachable); the returned function releases the lock.
//
// The signature matches rueidislock.Locker.TryWithContext, so the Redis
// dependency's Locker() can be adapted with [LockerFunc]:
//
//	locker := background.LockerFunc(func(ctx context.Context, name string) (context.Context, context.CancelFunc, error) {
//		lockCtx, unlock, err := deps.Redis.Locker().TryWithContext(ctx, "periodic:"+name)
//		if errors.Is(err, rueidislock.ErrNotLocked) {
//			err = background.ErrLockNotAcquired
//		}
//		return lockCtx, unlock, err
//	})
type Locker interface {
	TryLock(ctx context.Context, name string) (context.Context, context.CancelFunc, error)
}

// LockerFunc adapts a function to [Locker].
type LockerFunc func(ctx context.Context, name string) (context.Context, context.CancelFunc, error)

// TryLock calls f.
func (f LockerFunc) TryLock(ctx context.Context, name string) (context.Context, context.CancelFunc, error) {
	return f(ctx, name)
}

// LocalLocker is an in-process [Locker]: it excludes tasks with the same
// name inside one process only. Use it in tests and single-replica
// deployments; it gives no exclusion across replicas.
type LocalLocker struct {
	mu   sync.Mutex
	held map[string]struct{}
}

// NewLocalLocker returns an in-process locker.
func NewLocalLocker() *LocalLocker { return &LocalLocker{held: map[string]struct{}{}} }

// TryLock implements [Locker].
func (l *LocalLocker) TryLock(ctx context.Context, name string) (context.Context, context.CancelFunc, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.held[name]; ok {
		return nil, nil, ErrLockNotAcquired
	}
	l.held[name] = struct{}{}
	lockCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	return lockCtx, func() {
		once.Do(func() {
			l.mu.Lock()
			delete(l.held, name)
			l.mu.Unlock()
			cancel()
		})
	}, nil
}

type everyOptions struct {
	jitter    float64
	immediate bool
	timeout   time.Duration
	locker    Locker
}

// EveryOption tunes a periodic task.
type EveryOption func(*everyOptions)

// WithJitter spreads each interval by ±fraction (0.1 = ±10%, the default),
// so replicas started together do not tick together. 0 disables jitter.
func WithJitter(fraction float64) EveryOption {
	return func(o *everyOptions) { o.jitter = min(max(fraction, 0), 1) }
}

// WithImmediate runs the task once right away instead of waiting one
// interval first.
func WithImmediate() EveryOption { return func(o *everyOptions) { o.immediate = true } }

// WithTimeout bounds one run of the task.
func WithTimeout(d time.Duration) EveryOption { return func(o *everyOptions) { o.timeout = d } }

// WithLocker makes the task run on one holder of the lock at a time: the
// loop that acquires the lock named after the task keeps it across ticks and
// runs every tick; loops that do not hold it skip their ticks and try again
// on the next one, so another replica takes over within about one interval
// after the holder stops or loses the lock. The lock is released at shutdown.
//
// This is leadership, not a transaction: around a lock loss (network
// partition, long GC pause) two replicas can run the task once at the same
// time. The task must tolerate that.
func WithLocker(l Locker) EveryOption { return func(o *everyOptions) { o.locker = l } }

// Every runs fn every interval until stop is called or the runner shuts
// down. The first run happens after one interval unless [WithImmediate] is
// given.
//
//   - Runs never overlap: the next run is scheduled when the previous one has
//     returned. A run that takes longer than the interval delays the next
//     tick; missed ticks are not made up.
//   - Each run gets the same supervision as [Runner.Go]: panic recovery (the
//     next tick still fires), a named logger, a span and metrics.
//   - Without [WithLocker], every replica runs the task.
//   - When the process starts draining, no further run starts; a run in
//     progress may finish within the shutdown grace period.
//
// stop ends the loop and cancels a run in progress; it does not wait.
func (r *Runner) Every(name string, interval time.Duration, fn func(ctx context.Context) error, opts ...EveryOption) (stop func()) {
	if interval <= 0 {
		panic("background: Every needs a positive interval")
	}
	o := everyOptions{jitter: 0.1}
	for _, opt := range opts {
		opt(&o)
	}
	if !r.track.add() {
		return func() {}
	}
	ctx, cancel := r.detach(context.Background(), name)
	stopped := make(chan struct{})
	var stopOnce sync.Once
	stop = func() {
		stopOnce.Do(func() {
			close(stopped)
			cancel()
		})
	}
	go func() {
		defer r.track.done()
		defer stop()
		r.loop(ctx, stopped, name, interval, fn, &o)
	}()
	return stop
}

func (r *Runner) loop(ctx context.Context, stopped <-chan struct{}, name string, interval time.Duration,
	fn func(ctx context.Context) error, o *everyOptions,
) {
	var (
		lockCtx context.Context
		unlock  context.CancelFunc
	)
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	first := jittered(interval, o.jitter)
	if o.immediate {
		first = 0
	}
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
		case <-stopped:
			return
		case <-r.life.own:
			return
		case <-r.life.process():
			return
		case <-ctx.Done():
			return
		}
		if r.life.draining() { // the timer and the drain signal fired together
			return
		}
		start := time.Now()
		runCtx := ctx
		if o.locker != nil {
			if lockCtx == nil || lockCtx.Err() != nil {
				if unlock != nil {
					unlock() // lost: release our side before trying again
					unlock = nil
				}
				var err error
				lockCtx, unlock, err = o.locker.TryLock(ctx, name)
				if err != nil {
					lockCtx, unlock = nil, nil
					metrics().taskRuns.WithLabelValues(name, "skipped").Inc()
					if !errors.Is(err, ErrLockNotAcquired) {
						logWith("background.Every", map[string]any{"task": name, "error": err.Error()}).Warn("lock unavailable; skipping this run")
					}
					timer.Reset(jittered(interval, o.jitter))
					continue
				}
			}
			runCtx = lockCtx
		}
		_ = r.runTask(runCtx, nil, name, o.timeout, fn)
		timer.Reset(max(jittered(interval, o.jitter)-time.Since(start), 0))
	}
}

// jittered returns d spread by ±fraction.
func jittered(d time.Duration, fraction float64) time.Duration {
	if fraction <= 0 {
		return d
	}
	return time.Duration(float64(d) * (1 + fraction*(2*rand.Float64()-1)))
}
