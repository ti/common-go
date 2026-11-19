package background

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"go.opentelemetry.io/otel/attribute"

	"github.com/ti/common-go/graceful"
	"github.com/ti/common-go/log"
)

// Overflow says what Submit does when the backlog is full.
type Overflow int

const (
	// OverflowReject makes Submit fail with [ErrQueueFull] (the default).
	// The caller sheds load: an RPC answers RESOURCE_EXHAUSTED.
	OverflowReject Overflow = iota
	// OverflowBlock makes Submit wait for room until its context is done or
	// the process drains. Use it for internal producers that should be
	// slowed down, not for request handlers without a deadline.
	OverflowBlock
)

// RestartPolicy says what happens to a job found in a durable [Store] whose
// process stopped before the job finished.
type RestartPolicy int

const (
	// RestartFail ends the job as failed (the default). Safe for handlers
	// that must not run twice.
	RestartFail RestartPolicy = iota
	// RestartRequeue queues the job again in the process that found it, up
	// to [MaxRestarts] times. The handler starts from the beginning with the
	// same payload, so it must be safe to run again.
	RestartRequeue
)

// MaxRestarts bounds how often a job is queued again after its process
// stopped, so that a job that crashes the process cannot do so forever.
const MaxRestarts = 3

const (
	storeTimeout = 5 * time.Second
	// shutdownStoreTimeout bounds one store write while shutting down.
	shutdownStoreTimeout = 2 * time.Second
	watchPoll            = time.Second
	progressPersist      = time.Second
	recoverBatch         = 100
	msgShutdown          = "interrupted: the process shut down before the job finished"
	msgShutdownStuck     = "interrupted: the process shut down and the handler ignored cancellation"
)

// QueueOptions configure a [Queue]. The zero value gives 4 workers, a
// backlog of 100 that rejects on overflow, and an in-memory store.
type QueueOptions struct {
	// Name labels metrics and logs. Default "default".
	Name string
	// Workers is the number of jobs that run at the same time. Default 4.
	Workers int
	// Backlog is the number of jobs that may wait for a worker (including
	// jobs waiting for a retry). Default 100; a negative value means none:
	// a job is accepted only if a worker is free.
	Backlog int
	// Overflow is the behavior when Workers and Backlog are exhausted.
	Overflow Overflow
	// Store keeps the jobs. Default [NewMemoryStore], which also caps the
	// number of finished jobs it keeps ([DefaultMaxDone]).
	Store Store
	// Retention is how long a finished job stays retrievable (and its key
	// keeps deduplicating). Default one hour. With the in-memory store a
	// finished job can disappear earlier, when the store's cap is reached.
	Retention time.Duration
	// Grace is how long Shutdown lets running jobs finish before it cancels
	// them. Default [DefaultGrace].
	Grace time.Duration
	// LeaseTTL enables recovery with a durable store shared by several
	// processes: the queue confirms every LeaseTTL/3 that its jobs are
	// alive, and takes over (see [RestartPolicy]) jobs that are interrupted
	// or have not been confirmed for LeaseTTL. 0 (the default) disables
	// both. Set it to at least 15s with a durable store; leave it at 0 with
	// the in-memory store.
	LeaseTTL time.Duration
	// Standalone detaches the queue from package graceful: no closer is
	// registered and graceful.Draining is ignored. The owner calls Shutdown.
	Standalone bool
}

// HandlerOptions configure the handling of one job kind.
type HandlerOptions struct {
	// Timeout bounds one attempt; 0 means no limit. A timed-out attempt
	// fails the job unless the handler returns a [Retryable] error.
	Timeout time.Duration
	// MaxAttempts is the total number of attempts for errors marked
	// [Retryable]. Default 1 (no retry).
	MaxAttempts int
	// Backoff returns the delay before the next attempt, given the number
	// of attempts made so far (1 after the first failure). Default
	// [DefaultBackoff].
	Backoff func(attempt int) time.Duration
	// OnRestart applies to jobs recovered from a durable store.
	OnRestart RestartPolicy
}

// DefaultBackoff is exponential: 1s, 2s, 4s, ... capped at one minute, each
// spread by ±20%.
func DefaultBackoff(attempt int) time.Duration {
	d := time.Minute
	if attempt >= 1 && attempt < 7 {
		d = time.Second << (attempt - 1)
	}
	return jittered(d, 0.2)
}

// Handler executes one attempt of a job. It must return soon after ctx is
// done. The returned bytes become [Job.Result] when err is nil.
//
// ctx carries the values of the context the job was submitted with (not its
// cancellation), a logger tagged with the job, and a span linked to the
// submitting request. Its cancellation cause (context.Cause) is
// [ErrCancelled] after Queue.Cancel and [ErrShuttingDown] at shutdown.
type Handler func(ctx context.Context, run *Run) (result []byte, err error)

// Run is the handler's view of the job it is executing.
type Run struct {
	q *Queue
	e *entry
}

// Job returns the current snapshot (ID, Payload, Attempt, ...).
func (r *Run) Job() Job {
	r.q.mu.Lock()
	defer r.q.mu.Unlock()
	return r.e.job
}

// SetProgress publishes progress to watchers. total is 0 when unknown.
// Watchers that are slower than the handler see the latest value, not every
// value. The store is updated at most once per second.
func (r *Run) SetProgress(done, total int64, message string) {
	q, e := r.q, r.e
	q.mu.Lock()
	if e.job.State != StateRunning || e.lost {
		q.mu.Unlock()
		return
	}
	e.job.Progress = Progress{Done: done, Total: total, Message: message}
	q.touchLocked(e)
	q.mu.Unlock()
	if time.Since(time.Unix(0, e.lastPersist.Load())) >= progressPersist {
		q.persist(e)
	}
}

// SubmitRequest describes a job to run.
type SubmitRequest struct {
	// Kind selects the handler registered with [Queue.Handle].
	Kind string
	// Key makes the submission idempotent: while a job with the same Kind
	// and Key is retained (running, or finished less than Retention ago),
	// Submit returns that job instead of creating a new one. Use the
	// client's request ID.
	Key string
	// Payload is passed to the handler. Keep it small (an ID, parameters);
	// it is stored with the job.
	Payload []byte
	// Labels are stored with the job, for example {"owner": subject}.
	Labels map[string]string
}

type handler struct {
	fn          Handler
	timeout     time.Duration
	maxAttempts int
	backoff     func(int) time.Duration
	onRestart   RestartPolicy
}

// entry is a job owned by this queue. All fields except persistMu,
// storedRev and lastPersist are guarded by Queue.mu.
type entry struct {
	job     Job
	h       *handler
	changed chan struct{}           // closed and replaced on every visible change
	origin  context.Context         // values of the submitting context
	cancel  context.CancelCauseFunc // non-nil while running
	timer   *time.Timer             // retry delay
	delayed bool                    // waiting for timer
	lost    bool                    // another process took the job over
	unsaved bool                    // Done, but the store does not know yet

	persistMu   sync.Mutex // serializes store writes of this entry
	storedRev   int64      // revision in the store (guarded by persistMu)
	lastPersist atomic.Int64
}

// Queue is a bounded in-process job queue. See the package documentation.
type Queue struct {
	name     string
	id       string // owner ID written to jobs
	workers  int
	backlog  int
	overflow Overflow
	store    Store

	retention time.Duration
	grace     time.Duration
	leaseTTL  time.Duration

	life  *lifecycle
	track tracker // job goroutines

	mu       sync.Mutex
	handlers map[string]*handler
	live     map[string]*entry // jobs owned by this queue, until persisted as done
	keys     map[jobKey]*entry
	pending  []*entry // ready to run, first in first out
	delayed  int      // waiting for a retry
	reserved int      // Submit calls between the capacity check and the insert
	running  int
	space    chan struct{} // closed and replaced when capacity is freed

	gaugeQueued, gaugeRunning int // last values published to the gauges

	closed  bool            // Shutdown swept the backlog
	stopCtx context.Context // the closer's context, bounds store writes during Shutdown

	maintStop    chan struct{}
	maintDone    chan struct{}
	shutdownOnce sync.Once
	shutdownErr  error
}

// NewQueue returns a running queue. Unless opts.Standalone is set, it
// registers its Shutdown with graceful.AddWorkCloser: the queue is shut down
// after the servers have stopped serving and before the dependencies are
// closed, wherever it is created.
func NewQueue(opts QueueOptions) *Queue {
	q := &Queue{
		name:      opts.Name,
		id:        uuid.New().String(),
		workers:   opts.Workers,
		backlog:   opts.Backlog,
		overflow:  opts.Overflow,
		store:     opts.Store,
		retention: opts.Retention,
		grace:     opts.Grace,
		leaseTTL:  opts.LeaseTTL,
		life:      newLifecycle(opts.Standalone),
		handlers:  map[string]*handler{},
		live:      map[string]*entry{},
		keys:      map[jobKey]*entry{},
		space:     make(chan struct{}),
		maintStop: make(chan struct{}),
		maintDone: make(chan struct{}),
	}
	if q.name == "" {
		q.name = "default"
	}
	if q.workers <= 0 {
		q.workers = 4
	}
	switch {
	case q.backlog == 0:
		q.backlog = 100
	case q.backlog < 0:
		q.backlog = 0
	}
	if q.store == nil {
		q.store = NewMemoryStore()
	}
	if q.retention <= 0 {
		q.retention = time.Hour
	}
	if q.grace <= 0 {
		q.grace = DefaultGrace
	}
	go q.maintain()
	if !opts.Standalone {
		graceful.AddWorkCloser(q.Shutdown)
	}
	return q
}

// Handle registers the handler for a job kind. Register every kind before
// submitting jobs of it; with a durable store, register before LeaseTTL has
// elapsed since start so that recovered jobs find their handler. It panics
// if the kind is already registered.
func (q *Queue) Handle(kind string, fn Handler, opts HandlerOptions) {
	if kind == "" || fn == nil {
		panic("background: Handle needs a kind and a handler")
	}
	h := &handler{fn: fn, timeout: opts.Timeout, maxAttempts: opts.MaxAttempts, backoff: opts.Backoff, onRestart: opts.OnRestart}
	if h.maxAttempts <= 0 {
		h.maxAttempts = 1
	}
	if h.backoff == nil {
		h.backoff = DefaultBackoff
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, dup := q.handlers[kind]; dup {
		panic("background: handler already registered for kind " + kind)
	}
	q.handlers[kind] = h
}

// Submit accepts a job and returns its snapshot; the job runs as soon as a
// worker is free. created is false when req.Key matched an existing job,
// which is returned unchanged (the payload of the new request is ignored).
//
// Errors: [ErrShuttingDown] while the process drains, [ErrQueueFull] when
// Workers and Backlog are exhausted (with [OverflowBlock]: the context's
// error if it ends first), [ErrUnknownKind], or the store's error. A
// duplicate of a job that already finished can also get ErrQueueFull while
// the queue is full.
//
// The handler's context inherits the values of ctx, not its cancellation.
func (q *Queue) Submit(ctx context.Context, req SubmitRequest) (job Job, created bool, err error) {
	result := func(r string) { metrics().jobsSubmitted.WithLabelValues(q.name, req.Kind, r).Inc() }

	q.mu.Lock()
	h, ok := q.handlers[req.Kind]
	if !ok {
		q.mu.Unlock()
		return Job{}, false, fmt.Errorf("%w %q", ErrUnknownKind, req.Kind)
	}
	for {
		if q.closed || q.life.draining() {
			q.mu.Unlock()
			result("rejected_shutdown")
			return Job{}, false, ErrShuttingDown
		}
		if req.Key != "" {
			if e := q.keys[jobKey{req.Kind, req.Key}]; e != nil {
				job = e.job
				q.mu.Unlock()
				result("duplicate")
				return job, false, nil
			}
		}
		if q.running+len(q.pending)+q.delayed+q.reserved < q.workers+q.backlog {
			break
		}
		if q.overflow == OverflowReject {
			q.mu.Unlock()
			result("rejected_full")
			return Job{}, false, ErrQueueFull
		}
		space := q.space
		q.mu.Unlock()
		select {
		case <-space:
		case <-q.life.own:
		case <-q.life.process():
		case <-ctx.Done():
			result("rejected_full")
			return Job{}, false, context.Cause(ctx)
		}
		q.mu.Lock()
	}
	q.reserved++
	q.mu.Unlock()

	now := time.Now()
	job = Job{
		ID: uuid.New().String(), Kind: req.Kind, Key: req.Key, Payload: req.Payload, Labels: req.Labels,
		State: StateQueued, CreatedAt: now, UpdatedAt: now,
		Owner: q.id, HeartbeatAt: now, Revision: 1,
	}
	stored, created, err := q.store.Create(ctx, job)

	q.mu.Lock()
	q.reserved--
	if err != nil || !created {
		q.signalSpaceLocked()
		if e := q.live[stored.ID]; err == nil && e != nil {
			stored = e.job // fresher than the store
		}
		q.mu.Unlock()
		if err != nil {
			result("error")
			return Job{}, false, err
		}
		result("duplicate")
		return stored, false, nil
	}
	e := q.adoptLocked(job, h, context.WithoutCancel(ctx))
	job, done := e.job, e.job.State.Done()
	q.mu.Unlock()
	if done { // Shutdown swept the backlog while the job was being stored
		q.finalize(e)
		result("rejected_shutdown")
		return job, true, ErrShuttingDown
	}
	result("accepted")
	return job, true, nil
}

// adoptLocked makes the queue the owner of a stored, queued job and
// schedules it. If the queue is already closed, the job ends as interrupted
// and the caller must finalize the entry.
func (q *Queue) adoptLocked(job Job, h *handler, origin context.Context) *entry {
	e := &entry{job: job, h: h, changed: make(chan struct{}), origin: origin, storedRev: job.Revision}
	e.lastPersist.Store(time.Now().UnixNano())
	q.live[job.ID] = e
	if job.Key != "" {
		q.keys[jobKey{job.Kind, job.Key}] = e
	}
	if q.closed {
		q.finishLocked(e, StateInterrupted, msgShutdown, nil)
		return e
	}
	q.pending = append(q.pending, e)
	q.dispatchLocked()
	q.gaugesLocked()
	return e
}

// dispatchLocked starts queued jobs while workers are free.
func (q *Queue) dispatchLocked() {
	for q.running < q.workers && len(q.pending) > 0 && !q.life.draining() {
		if !q.track.add() {
			return
		}
		e := q.pending[0]
		q.pending = q.pending[1:]
		q.running++

		// The transition to running happens here, under the lock, so that
		// Cancel never sees a job that is neither queued nor running.
		base := log.NewContext(e.origin, map[string]any{"job_id": e.job.ID, "job_kind": e.job.Kind})
		ctx, cancel := context.WithCancelCause(base)
		e.cancel = cancel
		e.job.State = StateRunning
		e.job.Attempt++
		e.job.StartedAt = time.Now()
		e.job.NextAttemptAt = time.Time{}
		q.touchLocked(e)
		go q.run(ctx, e, cancel)
	}
}

// run executes one attempt.
func (q *Queue) run(ctx context.Context, e *entry, cancel context.CancelCauseFunc) {
	defer q.track.done()
	stopHard := context.AfterFunc(q.life.hard, func() { cancel(ErrShuttingDown) })
	q.persist(e)

	kind := e.job.Kind // immutable
	runCtx := ctx
	if e.h.timeout > 0 {
		var cancelTimeout context.CancelFunc
		runCtx, cancelTimeout = context.WithTimeout(ctx, e.h.timeout)
		defer cancelTimeout()
	}
	spanCtx, span := startSpan(runCtx, e.origin, "background job "+kind,
		attribute.String("background.queue", q.name),
		attribute.String("background.job.kind", kind),
		attribute.String("background.job.id", e.job.ID))
	start := time.Now()
	var result []byte
	err := protect(kind, func() (err error) {
		result, err = e.h.fn(spanCtx, &Run{q: q, e: e})
		return err
	})
	metrics().jobDuration.WithLabelValues(q.name, kind).Observe(time.Since(start).Seconds())
	endSpan(span, err)

	if err != nil && ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("timed out after %s: %w", e.h.timeout, err)
	}
	cause := context.Cause(ctx)
	stopHard()
	cancel(nil)
	q.settle(e, result, err, cause)
}

// settle records the outcome of an attempt.
func (q *Queue) settle(e *entry, result []byte, err, cause error) {
	q.mu.Lock()
	e.cancel = nil
	q.running--
	switch {
	case e.lost || e.job.State.Done(): // taken over, or force-finished by Shutdown
	case err == nil:
		q.finishLocked(e, StateSucceeded, "", result)
	case errors.Is(cause, ErrCancelled):
		q.finishLocked(e, StateCancelled, "cancelled", nil)
	case errors.Is(cause, ErrShuttingDown):
		q.finishLocked(e, StateInterrupted, msgShutdown, nil)
	case IsRetryable(err) && e.job.Attempt < e.h.maxAttempts:
		if q.closed || q.life.draining() {
			q.finishLocked(e, StateInterrupted, msgShutdown, nil)
			break
		}
		delay := e.h.backoff(e.job.Attempt)
		e.job.State = StateQueued
		e.job.Error = err.Error()
		e.job.NextAttemptAt = time.Now().Add(delay)
		q.touchLocked(e)
		e.delayed = true
		q.delayed++
		e.timer = time.AfterFunc(delay, func() { q.wake(e) })
		metrics().jobRetries.WithLabelValues(q.name, e.job.Kind).Inc()
	default:
		q.finishLocked(e, StateFailed, err.Error(), nil)
	}
	job, lost := e.job, e.lost
	q.dispatchLocked()
	q.signalSpaceLocked()
	q.gaugesLocked()
	q.mu.Unlock()

	if lost {
		q.forget(e)
		return
	}
	if job.State.Done() {
		q.finalize(e)
	} else {
		q.persist(e)
	}
	if err != nil {
		fields := mergeFields(map[string]any{
			"queue": q.name, "job_id": job.ID, "job_kind": job.Kind, "state": string(job.State), "attempt": job.Attempt,
		}, errorFields(err))
		if _, isPanic := errors.AsType[*PanicError](err); isPanic {
			logWith("background.Job", fields).Error("job handler panicked")
		} else {
			logWith("background.Job", fields).Warn("job attempt did not succeed")
		}
	}
}

// wake moves a job whose retry delay has passed back to the ready list.
func (q *Queue) wake(e *entry) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !e.delayed {
		return // cancelled or swept meanwhile
	}
	e.delayed = false
	q.delayed--
	q.pending = append(q.pending, e)
	q.dispatchLocked()
	q.gaugesLocked()
}

// unqueueLocked removes a queued entry from the ready list or its timer.
func (q *Queue) unqueueLocked(e *entry) {
	if e.delayed {
		e.delayed = false
		q.delayed--
		e.timer.Stop()
		return
	}
	if i := slices.Index(q.pending, e); i >= 0 {
		q.pending = slices.Delete(q.pending, i, i+1)
	}
}

// touchLocked marks a change that watchers must see.
func (q *Queue) touchLocked(e *entry) {
	e.job.Revision++
	e.job.UpdatedAt = time.Now()
	close(e.changed)
	e.changed = make(chan struct{})
}

// finishLocked moves an entry to a Done state. The caller finalizes it
// (persist, then forget) after releasing the lock.
func (q *Queue) finishLocked(e *entry, state State, errText string, result []byte) {
	e.job.State = state
	e.job.Error = errText
	e.job.Result = result
	e.job.NextAttemptAt = time.Time{}
	e.job.FinishedAt = time.Now()
	q.touchLocked(e)
	metrics().jobsFinished.WithLabelValues(q.name, e.job.Kind, string(state)).Inc()
}

func (q *Queue) signalSpaceLocked() {
	close(q.space)
	q.space = make(chan struct{})
}

// gaugesLocked publishes the queue's load as a change against what it
// published last, so that queues with the same name add up instead of
// overwriting each other.
func (q *Queue) gaugesLocked() {
	m := metrics()
	queued, running := len(q.pending)+q.delayed, q.running
	m.jobsQueued.WithLabelValues(q.name).Add(float64(queued - q.gaugeQueued))
	m.jobsRunning.WithLabelValues(q.name).Add(float64(running - q.gaugeRunning))
	q.gaugeQueued, q.gaugeRunning = queued, running
}

// persist writes the entry's current snapshot to the store. A conflict
// means another process took the job over: this queue lets go of it.
func (q *Queue) persist(e *entry) bool {
	e.persistMu.Lock()
	defer e.persistMu.Unlock()
	q.mu.Lock()
	snap, lost, stopCtx := e.job, e.lost, q.stopCtx
	q.mu.Unlock()
	if lost {
		return false
	}
	if snap.Revision == e.storedRev {
		return true
	}
	if stopCtx != nil && stopCtx.Err() != nil {
		return false // the shutdown deadline has passed: no more store writes
	}
	ctx, cancel := q.storeContext()
	defer cancel()
	err := q.store.Update(ctx, snap, e.storedRev)
	switch {
	case err == nil:
		e.storedRev = snap.Revision
		e.lastPersist.Store(time.Now().UnixNano())
		return true
	case errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound):
		q.lose(e, err)
	default:
		logWith("background.Store", map[string]any{"queue": q.name, "job_id": snap.ID, "error": err.Error()}).
			Warn("could not store job state")
	}
	return false
}

// storeContext bounds one store call: storeTimeout, and during Shutdown
// also the closer's deadline.
func (q *Queue) storeContext() (context.Context, context.CancelFunc) {
	q.mu.Lock()
	parent := q.stopCtx
	q.mu.Unlock()
	if parent == nil {
		return context.WithTimeout(context.Background(), storeTimeout)
	}
	return context.WithTimeout(context.WithoutCancel(parent), storeTimeoutWithin(parent))
}

// storeTimeoutWithin returns the timeout of a store write during Shutdown:
// shutdownStoreTimeout (shorter than the wait for cancelled handlers, so a
// slow store is not mistaken for a stuck handler), and no longer than what
// is left of parent's deadline.
func storeTimeoutWithin(parent context.Context) time.Duration {
	d := shutdownStoreTimeout
	if deadline, ok := parent.Deadline(); ok {
		d = min(d, max(time.Until(deadline), time.Millisecond))
	}
	return d
}

// finalize stores a Done entry, retrying briefly, and forgets it. If the
// store cannot be written, the entry is kept: this process still answers
// for the job with its true state, and maintain tries again (retryUnsaved)
// until the write succeeds or the job's retention is over. During Shutdown
// there is one attempt, bounded by the closer's deadline.
func (q *Queue) finalize(e *entry) {
	attempts := 3
	q.mu.Lock()
	if q.closed {
		attempts = 1
	}
	q.mu.Unlock()
	for attempt := range attempts {
		if q.persist(e) {
			q.forget(e)
			return
		}
		q.mu.Lock()
		lost := e.lost
		q.mu.Unlock()
		if lost {
			q.forget(e)
			return
		}
		if attempt < attempts-1 {
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
		}
	}
	q.mu.Lock()
	e.unsaved = true
	job := e.job
	q.mu.Unlock()
	logWith("background.Store", map[string]any{"queue": q.name, "job_id": job.ID, "job_kind": job.Kind, "state": string(job.State)}).
		Error("the final state of a job could not be stored; keeping it in memory and trying again")
}

// retryUnsaved writes Done entries whose final state is not in the store
// yet. An entry is given up when its retention has passed.
func (q *Queue) retryUnsaved() {
	q.mu.Lock()
	var entries []*entry
	for _, e := range q.live {
		if e.unsaved {
			entries = append(entries, e)
		}
	}
	q.mu.Unlock()
	for _, e := range entries {
		q.mu.Lock()
		expired := time.Since(e.job.FinishedAt) > q.retention
		q.mu.Unlock()
		if q.persist(e) || expired {
			q.forget(e)
		}
	}
}

// forget drops an entry from the queue's memory; from now on the store
// answers for the job.
func (q *Queue) forget(e *entry) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e.unsaved = false
	if q.live[e.job.ID] == e {
		delete(q.live, e.job.ID)
	}
	if k := (jobKey{e.job.Kind, e.job.Key}); q.keys[k] == e {
		delete(q.keys, k)
	}
}

// lose gives up a job that the store says belongs to someone else.
func (q *Queue) lose(e *entry, reason error) {
	q.mu.Lock()
	if e.lost {
		q.mu.Unlock()
		return
	}
	e.lost = true
	running := e.cancel != nil
	if running {
		e.cancel(reason)
	} else if e.job.State == StateQueued {
		q.unqueueLocked(e)
		q.signalSpaceLocked()
		q.gaugesLocked()
	}
	close(e.changed) // wake watchers; they continue from the store
	e.changed = make(chan struct{})
	id := e.job.ID
	q.mu.Unlock()
	if !running {
		q.forget(e) // a running entry is forgotten when its handler returns
	}
	logWith("background.Store", map[string]any{"queue": q.name, "job_id": id, "error": reason.Error()}).
		Warn("job is no longer owned by this process; letting go of it")
}

// Get returns the job's current snapshot, or [ErrNotFound].
func (q *Queue) Get(ctx context.Context, id string) (Job, error) {
	q.mu.Lock()
	if e := q.live[id]; e != nil && !e.lost {
		job := e.job
		q.mu.Unlock()
		return job, nil
	}
	q.mu.Unlock()
	return q.store.Get(ctx, id)
}

// Watch returns the job's snapshots as they change, starting with the
// current one. The sequence ends after a snapshot whose State is Done, or
// with a non-nil error:
//
//   - [ErrNotFound] if the job does not exist (any more);
//   - [ErrShuttingDown] when the process starts draining. The job may still
//     finish within the shutdown grace period; clients watch again (against
//     another replica) to learn the outcome;
//   - the context's error.
//
// A slow consumer skips intermediate snapshots but always gets the last
// one. Watching costs no goroutine. Jobs owned by another process (durable
// store) are polled from the store once per second.
//
// A server-streaming RPC relays it directly:
//
//	for job, err := range q.Watch(stream.Context(), id) {
//		if err != nil {
//			return err
//		}
//		if err := stream.Send(toProto(job)); err != nil {
//			return err
//		}
//	}
func (q *Queue) Watch(ctx context.Context, id string) iter.Seq2[Job, error] {
	return func(yield func(Job, error) bool) {
		q.mu.Lock()
		e := q.live[id]
		q.mu.Unlock()

		var last Job
		sent := false
		for {
			var (
				snap    Job
				changed <-chan struct{}
			)
			if e != nil {
				q.mu.Lock()
				if e.lost {
					e = nil
				} else {
					snap, changed = e.job, e.changed
				}
				q.mu.Unlock()
			}
			if e == nil {
				var err error
				if snap, err = q.store.Get(ctx, id); err != nil {
					yield(Job{}, err)
					return
				}
			}
			if !sent || !sameView(last, snap) {
				if !yield(snap, nil) {
					return
				}
				last, sent = snap, true
			}
			if snap.State.Done() {
				return
			}

			var poll <-chan time.Time
			var timer *time.Timer
			if e == nil {
				timer = time.NewTimer(watchPoll)
				poll = timer.C
			}
			var err error
			select {
			case <-changed:
			case <-poll:
			case <-ctx.Done():
				err = context.Cause(ctx)
			case <-q.life.own:
				err = ErrShuttingDown
			case <-q.life.process():
				err = ErrShuttingDown
			}
			if timer != nil {
				timer.Stop()
			}
			if err != nil {
				yield(Job{}, err)
				return
			}
		}
	}
}

// Cancel asks a job to stop and returns its snapshot. A queued job becomes
// cancelled at once. A running job has its context cancelled (cause
// [ErrCancelled]) and becomes cancelled when its handler returns; if the
// handler completes anyway, the job is succeeded. Cancelling a job that is
// already Done changes nothing and is not an error.
//
// It returns [ErrNotFound], or [ErrNotOwner] when the job is running in
// another process.
func (q *Queue) Cancel(ctx context.Context, id string) (Job, error) {
	q.mu.Lock()
	e := q.live[id]
	if e == nil || e.lost {
		q.mu.Unlock()
		job, err := q.store.Get(ctx, id)
		if err != nil {
			return Job{}, err
		}
		if job.State.Done() {
			return job, nil
		}
		return job, ErrNotOwner
	}
	switch e.job.State {
	case StateRunning:
		e.cancel(ErrCancelled)
	case StateQueued:
		q.unqueueLocked(e)
		q.finishLocked(e, StateCancelled, "cancelled before it started", nil)
		q.signalSpaceLocked()
		q.gaugesLocked()
		job := e.job
		q.mu.Unlock()
		q.finalize(e)
		return job, nil
	}
	job := e.job
	q.mu.Unlock()
	return job, nil
}

// Stats is a point-in-time view of the queue's load.
type Stats struct {
	// Queued jobs wait for a worker or for a retry delay.
	Queued int
	// Running jobs are being executed.
	Running int
}

// Stats returns the queue's load.
func (q *Queue) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return Stats{Queued: len(q.pending) + q.delayed, Running: q.running}
}

// Drain makes the queue stop accepting and starting jobs, as
// graceful.Draining does for the whole process. Running jobs continue. It
// is idempotent.
func (q *Queue) Drain() { q.life.drain() }

// Shutdown stops the queue:
//
//  1. Submit fails with [ErrShuttingDown]; watchers end with the same error.
//  2. Jobs that are queued (or waiting for a retry) become interrupted.
//  3. Running jobs get the grace period, bounded by ctx, to finish normally.
//  4. The contexts of the remaining jobs are cancelled (cause
//     [ErrShuttingDown]); they become interrupted when their handlers
//     return, which Shutdown awaits for a few seconds.
//
// It returns an error if a handler ignored the cancellation (that job is
// recorded as interrupted and its goroutine is leaked), or if the store
// could not be written: store writes during Shutdown are bounded by ctx, and
// a job whose final state could not be stored stays unfinished in a durable
// store, where the lease recovery of the next process deals with it. Every other goroutine
// of the queue has ended when Shutdown returns. Further calls return the
// first result.
func (q *Queue) Shutdown(ctx context.Context) error {
	q.shutdownOnce.Do(func() { q.shutdownErr = q.shutdown(ctx) })
	return q.shutdownErr
}

func (q *Queue) shutdown(ctx context.Context) error {
	q.life.drain()

	q.mu.Lock()
	q.closed = true
	q.stopCtx = ctx
	var swept []*entry
	for _, e := range q.live {
		if e.job.State == StateQueued && !e.lost {
			q.unqueueLocked(e)
			q.finishLocked(e, StateInterrupted, msgShutdown, nil)
			swept = append(swept, e)
		}
	}
	q.signalSpaceLocked()
	q.gaugesLocked()
	q.mu.Unlock()
	for _, e := range swept {
		q.finalize(e)
	}

	cancelled, stuck := q.life.stopWork(ctx, &q.track, q.grace, ErrShuttingDown)

	close(q.maintStop)
	<-q.maintDone

	if cancelled > 0 {
		logWith("background.Shutdown", map[string]any{"queue": q.name}).
			Warn("cancelled %d jobs that were still running after %s", cancelled, q.grace.String())
	}
	if stuck == 0 {
		return q.unsavedError()
	}
	// Handlers that ignore cancellation: record the truth and move on.
	q.mu.Lock()
	var abandoned []*entry
	for _, e := range q.live {
		if e.job.State == StateRunning && !e.lost {
			q.finishLocked(e, StateInterrupted, msgShutdownStuck, nil)
			abandoned = append(abandoned, e)
		}
	}
	q.mu.Unlock()
	for _, e := range abandoned {
		q.finalize(e)
	}
	return errors.Join(
		fmt.Errorf("background: queue %s: %d job handlers ignored cancellation and are still running", q.name, stuck),
		q.unsavedError())
}

// unsavedError makes a last attempt to store final states that could not be
// written, and reports the jobs whose state the store still does not have.
func (q *Queue) unsavedError() error {
	q.retryUnsaved()
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, e := range q.live {
		if e.unsaved {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return fmt.Errorf("background: queue %s: the final state of %d jobs could not be stored; the store still shows them unfinished", q.name, n)
}

// maintain prunes finished jobs and, with a lease, confirms and recovers
// jobs. It is the queue's only long-lived goroutine.
func (q *Queue) maintain() {
	defer close(q.maintDone)
	prune := time.NewTicker(min(max(q.retention/10, time.Second), time.Minute))
	defer prune.Stop()
	var beat, sweep <-chan time.Time
	if q.leaseTTL > 0 {
		b := time.NewTicker(q.leaseTTL / 3)
		defer b.Stop()
		s := time.NewTicker(q.leaseTTL)
		defer s.Stop()
		beat, sweep = b.C, s.C
	}
	for {
		select {
		case <-q.maintStop:
			return
		case <-prune.C:
			q.retryUnsaved()
			ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
			if _, err := q.store.DeleteDone(ctx, time.Now().Add(-q.retention)); err != nil {
				logWith("background.Store", map[string]any{"queue": q.name, "error": err.Error()}).Warn("could not prune finished jobs")
			}
			cancel()
		case <-beat:
			q.heartbeat()
		case <-sweep:
			q.recoverJobs()
		}
	}
}

// heartbeat confirms in the store that this process still owns its jobs.
func (q *Queue) heartbeat() {
	now := time.Now()
	q.mu.Lock()
	entries := make([]*entry, 0, len(q.live))
	for _, e := range q.live {
		if !e.job.State.Done() && !e.lost {
			e.job.HeartbeatAt = now
			e.job.Revision++ // not a visible change: watchers are not woken
			entries = append(entries, e)
		}
	}
	q.mu.Unlock()
	for _, e := range entries {
		q.persist(e)
	}
	// A finished job whose final state is not stored looks abandoned to the
	// other replicas; writing it is also what keeps them from taking it over.
	q.retryUnsaved()
}

// recoverJobs takes over jobs that no live process owns.
func (q *Queue) recoverJobs() {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	jobs, err := q.store.ListRecoverable(ctx, time.Now().Add(-q.leaseTTL), recoverBatch)
	if err != nil {
		logWith("background.Store", map[string]any{"queue": q.name, "error": err.Error()}).Warn("could not list recoverable jobs")
		return
	}
	for _, job := range jobs {
		q.mu.Lock()
		h := q.handlers[job.Kind]
		_, mine := q.live[job.ID]
		free := q.workers + q.backlog - (q.running + len(q.pending) + q.delayed + q.reserved)
		stop := q.closed || q.life.draining()
		q.mu.Unlock()
		if stop {
			return
		}
		if h == nil || mine {
			continue // another version of the service may know the kind
		}
		requeue := h.onRestart == RestartRequeue && job.Restarts < MaxRestarts
		if requeue && free <= 0 {
			continue // try again on the next sweep
		}
		prev, was, now := job.Revision, job.State, time.Now()
		job.Revision++
		job.UpdatedAt = now
		job.NextAttemptAt = time.Time{}
		if requeue {
			job.State = StateQueued
			job.Attempt = 0 // the retry budget starts again; Restarts keeps the history
			job.Restarts++
			job.Owner = q.id
			job.HeartbeatAt = now
			job.FinishedAt = time.Time{}
			job.Progress = Progress{}
		} else {
			job.State = StateFailed
			job.FinishedAt = now
			job.Error = fmt.Sprintf("interrupted: the process running the job stopped while it was %s", was)
			if h.onRestart == RestartRequeue {
				job.Error += fmt.Sprintf(" (restarted %d times already)", job.Restarts)
			}
		}
		if err := q.store.Update(ctx, job, prev); err != nil {
			if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) { // otherwise another replica was faster
				logWith("background.Store", map[string]any{"queue": q.name, "job_id": job.ID, "error": err.Error()}).Warn("could not take over job")
			}
			continue
		}
		logWith("background.Recover", map[string]any{"queue": q.name, "job_id": job.ID, "job_kind": job.Kind, "state": string(job.State)}).
			Warn("took over a job whose process stopped while it was %s", string(was))
		if !requeue {
			metrics().jobsFinished.WithLabelValues(q.name, job.Kind, string(StateFailed)).Inc()
			continue
		}
		q.mu.Lock()
		e := q.adoptLocked(job, h, context.Background())
		done := e.job.State.Done()
		q.mu.Unlock()
		if done {
			q.finalize(e)
		}
	}
}
