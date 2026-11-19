// Package background runs work that outlives the request that started it,
// inside the server process, and shuts it down together with the process.
//
// It offers four things and deliberately nothing more:
//
//   - [Go]: a supervised goroutine for work that should finish. Panics are
//     recovered, logged with the stack and counted; the context keeps the
//     request's values but not its cancellation, and is cancelled when the
//     process shuts down; the task has a name for logs and metrics and a
//     trace span linked to the request.
//   - [Loop]: a supervised long-lived function (a subscription, a consumer)
//     that runs until shutdown and is restarted with backoff when it fails.
//   - [Every]: a periodic task with jitter that never overlaps itself and,
//     with a [Locker], runs on one replica at a time.
//   - [Queue]: a bounded in-process job queue. Jobs have an ID, a state
//     (queued, running, succeeded, failed, cancelled, interrupted), progress
//     and an error; they can be fetched, watched, cancelled and submitted
//     idempotently by key. Failures marked [Retryable] are retried with
//     backoff, each attempt can have a timeout, and a [Store] decides what
//     survives a restart (by default: nothing, jobs live in memory).
//
// # Shutdown
//
// Everything is tied to package graceful, in two steps.
//
// Draining: when graceful.Draining closes (a grpcmux server closes it when
// it stops accepting requests, after its PRE_STOP wait; a process without a
// server when shutdown is requested), queues reject new jobs with
// [ErrShuttingDown], watchers end with the same error, queued jobs are no
// longer started, periodic tasks stop ticking and loops are cancelled.
// Running jobs and tasks are not touched.
//
// Closing: each [Runner] and [Queue] registers a closer with
// graceful.AddWorkCloser. It runs after the servers have stopped serving and
// before the dependencies are closed, wherever the runner or queue was
// created (at startup, or on first use inside a request handler). It waits
// up to its grace period for running work to finish, cancels what is left
// (jobs end as [StateInterrupted]), and returns once every goroutine has
// ended.
//
// # When this is the wrong tool
//
// The queue is not a workflow engine and not a distributed queue: a job runs
// in the process that accepted it, and work is not rebalanced between
// replicas. Use something else when
//
//   - a job must not be lost when the pod dies and cannot be made safe to run
//     again from the start (use a durable workflow engine such as Temporal);
//   - a job runs longer than a deployment's termination grace period, or
//     needs other resources than the API server (GPU, much memory): start a
//     Kubernetes Job;
//   - producers and consumers scale separately, or many replicas must share
//     one backlog (use a broker such as Kafka, or a database-backed queue);
//   - the work is a multi-step process with waits, signals or compensation.
//
// See docs/guides/background-work.md for the full guide.
package background

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/graceful"
)

// Error is a background error that carries the gRPC code a service should
// answer with, so a handler can return it unchanged.
type Error struct {
	code codes.Code
	msg  string
}

func (e *Error) Error() string { return e.msg }

// GRPCStatus makes status.FromError (and therefore grpcmux) map the error.
func (e *Error) GRPCStatus() *status.Status { return status.New(e.code, e.msg) }

// Errors returned by this package. Compare with errors.Is.
var (
	// ErrShuttingDown: the process is draining; nothing new is accepted
	// (gRPC UNAVAILABLE, so clients retry against another replica).
	ErrShuttingDown = &Error{codes.Unavailable, "background: shutting down"}
	// ErrQueueFull: the backlog is full and the queue rejects on overflow
	// (gRPC RESOURCE_EXHAUSTED).
	ErrQueueFull = &Error{codes.ResourceExhausted, "background: queue is full"}
	// ErrNotFound: no job with that ID, or it was pruned after the retention
	// period (gRPC NOT_FOUND).
	ErrNotFound = &Error{codes.NotFound, "background: job not found"}
	// ErrUnknownKind: no handler is registered for the job kind (gRPC
	// INTERNAL; it is a programming error).
	ErrUnknownKind = &Error{codes.Internal, "background: no handler for job kind"}
	// ErrNotOwner: the job is running in another process, which is the only
	// one that can cancel it (gRPC FAILED_PRECONDITION).
	ErrNotOwner = &Error{codes.FailedPrecondition, "background: job is owned by another process"}
	// ErrConflict is returned by a [Store] when the stored revision is not
	// the expected one (gRPC ABORTED).
	ErrConflict = &Error{codes.Aborted, "background: job was modified concurrently"}

	// ErrCancelled is the cancellation cause of a job's context after
	// [Queue.Cancel].
	ErrCancelled = errors.New("background: job cancelled")
	// ErrLockNotAcquired is returned by a [Locker] when another holder has
	// the lock.
	ErrLockNotAcquired = errors.New("background: lock not acquired")
)

// PanicError is the error a recovered panic is turned into.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

// noRecovery mirrors grpcmux: PANIC_RECOVERY=false lets panics crash the
// process (useful to get a core dump while debugging).
var noRecovery = os.Getenv("PANIC_RECOVERY") == "false"

// protect runs fn and turns a panic into a *PanicError, counting it under name.
func protect(name string, fn func() error) (err error) {
	if !noRecovery {
		defer func() {
			if r := recover(); r != nil {
				metrics().panics.WithLabelValues(name).Inc()
				err = &PanicError{Value: r, Stack: debug.Stack()}
			}
		}()
	}
	return fn()
}

// errorFields returns log fields for a task error, including the stack of a
// recovered panic.
func errorFields(err error) map[string]any {
	fields := map[string]any{"error": err.Error()}
	if p, ok := errors.AsType[*PanicError](err); ok {
		fields["stack"] = string(p.Stack)
	}
	return fields
}

const tracerName = "github.com/ti/common-go/background"

// startSpan starts a new root span for background work, linked to the span
// of the request that started it (if any). A background task usually ends
// long after the request; a link keeps the request's trace short and still
// navigable to the work it caused.
func startSpan(ctx, origin context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	opts := []trace.SpanStartOption{trace.WithNewRoot(), trace.WithAttributes(attrs...)}
	if origin != nil {
		if sc := trace.SpanContextFromContext(origin); sc.IsValid() {
			opts = append(opts, trace.WithLinks(trace.Link{SpanContext: sc}))
		}
	}
	return otel.Tracer(tracerName).Start(ctx, name, opts...)
}

func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, err.Error())
	}
	span.End()
}

// lifecycle is the drain state shared by Runner and Queue: an own signal
// (Drain, Shutdown) and, unless standalone, the process-wide graceful one.
type lifecycle struct {
	standalone bool
	once       sync.Once
	own        chan struct{}

	hard       context.Context // cancelled when running work must stop
	hardCancel context.CancelCauseFunc
}

func newLifecycle(standalone bool) *lifecycle {
	l := &lifecycle{standalone: standalone, own: make(chan struct{})}
	l.hard, l.hardCancel = context.WithCancelCause(context.Background())
	return l
}

func (l *lifecycle) drain() { l.once.Do(func() { close(l.own) }) }

// process returns the graceful channel, or nil (blocks forever in a select)
// for a standalone instance.
func (l *lifecycle) process() <-chan struct{} {
	if l.standalone {
		return nil
	}
	return graceful.Draining()
}

func (l *lifecycle) draining() bool {
	select {
	case <-l.own:
		return true
	default:
	}
	return !l.standalone && graceful.IsDraining()
}

// tracker counts running goroutines and lets Shutdown wait for them without
// spawning a goroutine of its own (so a task that never returns leaks only
// itself).
type tracker struct {
	mu     sync.Mutex
	n      int
	closed bool
	zero   chan struct{} // non-nil while someone waits; closed when n hits 0
}

// add registers one goroutine; false after close.
func (t *tracker) add() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.n++
	return true
}

func (t *tracker) done() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.n--
	if t.n == 0 && t.zero != nil {
		close(t.zero)
		t.zero = nil
	}
}

func (t *tracker) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.n
}

func (t *tracker) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// close refuses further add calls.
func (t *tracker) close() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
}

// wait blocks until no goroutine is tracked, d has elapsed, or ctx is done.
// It reports whether the tracker is idle.
func (t *tracker) wait(ctx context.Context, d time.Duration) bool {
	t.mu.Lock()
	if t.n == 0 {
		t.mu.Unlock()
		return true
	}
	if t.zero == nil {
		t.zero = make(chan struct{})
	}
	zero := t.zero
	t.mu.Unlock()

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-zero:
		return true
	case <-timer.C:
	case <-ctx.Done():
	}
	return t.count() == 0
}

const (
	// DefaultGrace is how long Shutdown lets running work finish before it
	// cancels it, unless configured otherwise.
	DefaultGrace = 10 * time.Second
	// cancelWait is how long Shutdown waits for cancelled work to return.
	cancelWait = 5 * time.Second
)

// stopWork implements the common end of Shutdown: let running work finish
// within grace (and ctx), cancel the rest with cause, and wait for it to
// return. It reports how many goroutines were cancelled and how many are
// still running at the end.
func (l *lifecycle) stopWork(ctx context.Context, t *tracker, grace time.Duration, cause error) (cancelled, stuck int) {
	if !t.wait(ctx, grace) {
		cancelled = t.count()
	}
	t.close()
	l.hardCancel(cause)
	if !t.wait(context.Background(), cancelWait) {
		stuck = t.count()
	}
	return cancelled, stuck
}
