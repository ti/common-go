// Package graceful runs a process's long-lived servers and shuts it down
// cleanly.
//
// [Start] runs the given functions (typically servers) and blocks until one of:
//
//   - SIGINT or SIGTERM is received,
//   - the context passed to Start is canceled,
//   - [Close] is called,
//   - one of the functions returns a non-nil error (for example, a port that
//     is already in use).
//
// It then runs every closer registered with [AddCloser], last-in first-out,
// under one shared deadline, and returns. Servers register their own Close
// (see grpcmux.Server.Start), and dependencies register theirs when they are
// initialized, so a server stops accepting work before the databases it uses
// are closed.
//
// [Draining] is the process-wide "stop taking new work" signal. Long-lived
// work (streams, background jobs, periodic tasks) selects on it to wind down.
// It is not cancellation: the closers that run afterwards still give running
// work time to finish. It is closed by [Drain]:
//
//   - Start calls Drain when shutdown is requested, before the closers run.
//   - A server that keeps serving for a while after the shutdown request
//     (grpcmux waits PRE_STOP so that load balancers catch up) calls
//     [DeferDrain] before Start and Drain itself at the moment it stops
//     accepting requests. Start then calls Drain only after the closers.
//
// Closers registered with [AddWorkCloser] (background work) run once the
// process has drained and the closer that drained it has returned, whenever
// they were registered: after the servers stopped serving and before the
// dependencies are closed.
package graceful

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ti/common-go/log"
)

// defaultTimeout bounds the whole shutdown (all closers together). It is long
// enough for a PRE_STOP wait (capped at 5 minutes by grpcmux) plus draining.
// Override it with the GRACEFUL_TIMEOUT environment variable, e.g. "90s".
const defaultTimeout = 6 * time.Minute

var (
	mu      sync.Mutex
	closers []closerEntry

	// closeCh is closed by Close and wakes every running Start. A Start that
	// begins a new lifecycle (no other Start running, an earlier one already
	// shut down) replaces a closed channel.
	closeCh = make(chan struct{})
	// active counts Starts that have not begun to shut down; shutdownSeen
	// records that some Start has.
	active       int
	shutdownSeen bool

	// draining is closed by Drain. Start replaces a closed channel with a
	// fresh one, so a process (or a test binary) can run Start more than once.
	draining = make(chan struct{})
	// deferrals are the DeferDrain calls of this lifecycle; current is the
	// one whose closer is running right now. Both are cleared when a
	// shutdown ends.
	deferrals []*deferral
	current   *deferral

	// workClosers run once draining is closed; see AddWorkCloser.
	workClosers []func(ctx context.Context) error

	// closing serializes shutdowns: with several Starts running, the second
	// one waits for the first to finish the closers.
	closing sync.Mutex
)

// Draining returns a channel that is closed when the process stops taking
// new work: when shutdown is requested, or, with a server that called
// [DeferDrain], when that server stops accepting requests. Running work is
// not cancelled by it. The channel belongs to the current [Start]; obtain it
// again instead of caching it across Start calls.
func Draining() <-chan struct{} {
	mu.Lock()
	defer mu.Unlock()
	return draining
}

// IsDraining reports whether [Draining] is closed.
func IsDraining() bool {
	select {
	case <-Draining():
		return true
	default:
		return false
	}
}

// closerEntry is a registered closer and, if DeferDrain was called right
// after it was registered, the deferral it is expected to release.
type closerEntry struct {
	fn       func(ctx context.Context) error
	deferral *deferral
}

// deferral is one DeferDrain call.
type deferral struct{ released bool }

// outstandingLocked reports whether some deferral has not been released.
func outstandingLocked() bool {
	for _, d := range deferrals {
		if !d.released {
			return true
		}
	}
	return false
}

// releaseLocked releases d; the signal closes with the last deferral.
func releaseLocked(d *deferral) {
	if d.released {
		return
	}
	d.released = true
	if !outstandingLocked() {
		closeDrainingLocked()
	}
}

// Drain closes the [Draining] channel. It is idempotent and safe for
// concurrent use. [Start] calls it; servers see [DeferDrain].
//
// Called by anything else than a closer that deferred the signal, it closes
// the signal at once, whatever was deferred: the caller says that the
// process must stop taking new work now. Called while such a closer is
// running (the way grpcmux.Server.Close does), it stands for that closer's
// own release: with several servers the signal closes when the last one has
// drained, and calling it twice from the same closer changes nothing.
func Drain() {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		releaseLocked(current)
		return
	}
	forceDrainLocked()
}

// forceDrainLocked closes the signal whatever was deferred.
func forceDrainLocked() {
	for _, d := range deferrals {
		d.released = true
	}
	closeDrainingLocked()
}

func closeDrainingLocked() {
	select {
	case <-draining:
	default:
		close(draining)
	}
}

// DeferDrain tells [Start] not to close [Draining] as soon as shutdown is
// requested, because the closer registered just before this call (a
// server's) will do it at a later, better moment: after its pre-stop wait,
// when it stops accepting. Call it right after the [AddCloser] of that
// closer.
//
// The returned function releases the deferral; it is idempotent. The closer
// calls it at that moment (calling [Drain] from inside the closer does the
// same). With several servers the signal closes when the last deferral is
// released. The signal is never lost: a deferral is released at the latest
// when its closer has returned, panicked or been abandoned, and Start closes
// the signal in any case once all closers have run. A deferral applies to
// one shutdown.
func DeferDrain() (release func()) {
	mu.Lock()
	defer mu.Unlock()
	d := &deferral{}
	deferrals = append(deferrals, d)
	if n := len(closers); n > 0 && closers[n-1].deferral == nil {
		closers[n-1].deferral = d
	}
	return func() {
		mu.Lock()
		defer mu.Unlock()
		releaseLocked(d)
	}
}

// resetDraining starts a new lifecycle: a channel closed by an earlier Start
// is replaced by an open one.
func resetDraining() {
	mu.Lock()
	defer mu.Unlock()
	select {
	case <-draining:
		draining = make(chan struct{})
	default:
	}
}

// Fn is a long-running function, typically a server's serve loop. It should
// block while serving and return nil once it has been stopped by its closer.
type Fn func(context.Context) error

// AddCloser registers closer to run during shutdown. Closers run in reverse
// registration order. AddCloser is safe for concurrent use. A closer that is
// registered while the shutdown is already running the closers is run by
// that shutdown, after the ones registered before it.
func AddCloser(closer func(ctx context.Context) error) {
	mu.Lock()
	closers = append(closers, closerEntry{fn: closer})
	mu.Unlock()
}

// AddWorkCloser registers the closer of background work (a job queue, a
// task runner). Unlike [AddCloser] closers, its place in the shutdown does
// not depend on when it is registered: work closers run, newest first, as
// soon as [Draining] is closed and the closer that closed it has returned
// (the servers have stopped serving), before the remaining AddCloser closers
// (the dependencies). One that is registered while the shutdown is already
// waiting for the servers (a lazily created runner) is still run.
//
// So background work can be created anywhere, also on first use inside a
// request handler, and is still asked to stop only after the last request
// has been served.
func AddWorkCloser(closer func(ctx context.Context) error) {
	mu.Lock()
	workClosers = append(workClosers, closer)
	mu.Unlock()
}

// Close asks every running [Start] to shut down. It does not wait for the
// shutdown to finish. Calling it more than once is harmless.
//
// A Close before the first Start of the process is remembered: that Start
// shuts down as soon as it is called. Once a Start has shut down, a Close
// that finds no Start running does nothing: it does not leak into a later
// Start. The price is a race that only programs with several lifecycles
// (test suites) can hit: a Close issued after "go Start()" but before that
// Start has begun is lost, and the Start keeps running. Close only after the
// Start is known to run (its function was called, the server is ready), or
// cancel the context given to Start instead.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if active == 0 && shutdownSeen {
		return
	}
	select {
	case <-closeCh: // already requested
	default:
		close(closeCh)
	}
}

// begin registers a Start and returns the channel Close will close for it.
func begin() <-chan struct{} {
	mu.Lock()
	defer mu.Unlock()
	if active == 0 && shutdownSeen {
		select {
		case <-closeCh: // consumed by the previous lifecycle
			closeCh = make(chan struct{})
		default:
		}
	}
	active++
	return closeCh
}

// end marks that a Start has begun to shut down.
func end() {
	mu.Lock()
	active--
	shutdownSeen = true
	mu.Unlock()
}

// Start runs fns concurrently and blocks until shutdown is requested (see the
// package documentation), then runs the registered closers and returns.
//
// A function that returns nil does not trigger a shutdown by itself; Start
// keeps waiting for a signal, Close, or an error.
//
// Start does not report why it shut down: when a function failed (a server
// that could not bind its port, for example) it logs the error and returns
// like after a signal, so a main that ends with Start exits with status 0.
// Use [Run] to get the error and exit non-zero.
func Start(ctx context.Context, fns ...Fn) {
	_ = Run(ctx, fns...)
}

// Run is [Start] that reports a failure: it returns the error of the function
// that made it shut down, and nil after a shutdown that was asked for (a
// signal, Close, or a canceled ctx). The closers have run in both cases.
//
//	if err := graceful.Run(ctx, worker.Run); err != nil {
//		log.Action("Run").Fatal(err.Error()) // exit status 1
//	}
func Run(ctx context.Context, fns ...Fn) error {
	resetDraining()
	closeRequested := begin()
	sigCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	errs := make(chan error, len(fns))
	for _, fn := range fns {
		go func() { errs <- fn(sigCtx) }()
	}

	var (
		reason string
		failed error
	)
wait:
	for {
		select {
		case <-sigCtx.Done():
			reason = context.Cause(sigCtx).Error()
			break wait
		case <-closeRequested:
			reason = "graceful.Close called"
			break wait
		case err := <-errs:
			if err != nil {
				failed = err
				break wait
			}
		}
	}
	end()
	// From here on, a second signal kills the process right away.
	stopSignals()
	if failed != nil {
		log.Action("graceful.Shutdown").Error("shutting down because a server failed: " + failed.Error())
	} else {
		log.Action("graceful.Shutdown").Warn(reason)
	}
	runClosers(timeoutFromEnv())
	return failed
}

// runClosers runs and forgets every registered closer, newest first, sharing
// one deadline. A closer that fails or panics does not stop the others. A
// closer that is still running abandonGrace after the deadline is abandoned
// (it keeps its goroutine; the process is about to exit) and the remaining
// closers run with the expired context, so one stuck closer cannot keep the
// others from running.
func runClosers(timeout time.Duration) {
	// One shutdown at a time: a second Start that shuts down concurrently
	// (two servers in one process) waits here until the closers have run
	// instead of deciding about the drain signal on its own.
	closing.Lock()
	defer closing.Unlock()

	// Draining starts here, unless a server asked to choose the moment
	// itself (DeferDrain).
	mu.Lock()
	if !outstandingLocked() {
		forceDrainLocked()
	}
	mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	run := func(closer func(ctx context.Context) error) {
		finished, err := runCloser(ctx, closer)
		if !finished {
			log.Action("graceful.Closer").Error("a closer did not return within the shutdown deadline (%s); abandoning it", timeout.String())
			return
		}
		if err == nil {
			return
		}
		// Closing stdout/stderr (for example by a logger) is not worth a warning.
		if pathErr, ok := errors.AsType[*os.PathError](err); ok && strings.HasPrefix(pathErr.Path, "/dev/std") {
			return
		}
		log.Action("graceful.Closer").Warn(err.Error())
	}
	// runWork runs the work closers registered so far, newest first,
	// including ones registered while it runs.
	runWork := func() {
		for {
			mu.Lock()
			n := len(workClosers)
			if n == 0 {
				mu.Unlock()
				return
			}
			closer := workClosers[n-1]
			workClosers = workClosers[:n-1]
			mu.Unlock()
			run(closer)
		}
	}
	// Closers registered while the shutdown runs form further batches, so
	// none is left behind for a later lifecycle.
	for {
		mu.Lock()
		pending := closers
		closers = nil
		mu.Unlock()
		if len(pending) == 0 {
			break
		}
		for _, p := range slices.Backward(pending) {
			if IsDraining() {
				runWork()
			}
			mu.Lock()
			current = p.deferral
			mu.Unlock()
			run(p.fn)
			// The closer that deferred the signal is done, however it ended
			// (returned without draining, panicked, abandoned): its drain is
			// due now, so that background work is closed before the closers
			// registered earlier (the dependencies).
			mu.Lock()
			current = nil
			if p.deferral != nil {
				releaseLocked(p.deferral)
			}
			mu.Unlock()
		}
	}
	mu.Lock()
	forceDrainLocked()
	deferrals = nil
	mu.Unlock()
	runWork()
}

// abandonGrace is how long a closer may still run once the shared deadline
// has passed. It gives closers that honor the context time to return, and
// closers that start after the deadline a chance to do quick work.
const abandonGrace = 2 * time.Second

// runCloser runs one closer and waits for it until ctx is done plus
// abandonGrace. A panic is turned into an error.
func runCloser(ctx context.Context, closer func(ctx context.Context) error) (finished bool, err error) {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("closer panicked: %v\n%s", r, debug.Stack())
			}
		}()
		done <- closer(ctx)
	}()
	select {
	case err = <-done:
		return true, err
	case <-ctx.Done():
	}
	grace := time.NewTimer(abandonGrace)
	defer grace.Stop()
	select {
	case err = <-done:
		return true, err
	case <-grace.C:
		return false, nil
	}
}

func timeoutFromEnv() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("GRACEFUL_TIMEOUT")); err == nil && d > 0 {
		return d
	}
	return defaultTimeout
}
