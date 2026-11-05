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
// The signal handler is installed before any function starts, and it is
// removed as soon as shutdown begins, so a second SIGINT/SIGTERM terminates
// the process immediately.
package graceful

import (
	"context"
	"errors"
	"os"
	"os/signal"
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
	closers []func(ctx context.Context) error

	// closeRequests carries Close() calls to a running Start.
	closeRequests = make(chan struct{}, 1)
)

// Fn is a long-running function, typically a server's serve loop. It should
// block while serving and return nil once it has been stopped by its closer.
type Fn func(context.Context) error

// AddCloser registers closer to run during shutdown. Closers run in reverse
// registration order. AddCloser is safe for concurrent use.
func AddCloser(closer func(ctx context.Context) error) {
	mu.Lock()
	closers = append(closers, closer)
	mu.Unlock()
}

// Close asks a running [Start] to shut down. It does not wait for the
// shutdown to finish. Calling it more than once is harmless.
func Close() {
	select {
	case closeRequests <- struct{}{}:
	default: // a request is already pending
	}
}

// Start runs fns concurrently and blocks until shutdown is requested (see the
// package documentation), then runs the registered closers and returns.
//
// A function that returns nil does not trigger a shutdown by itself; Start
// keeps waiting for a signal, Close, or an error.
func Start(ctx context.Context, fns ...Fn) {
	sigCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	errs := make(chan error, len(fns))
	for _, fn := range fns {
		go func() { errs <- fn(sigCtx) }()
	}

	var reason string
wait:
	for {
		select {
		case <-sigCtx.Done():
			reason = context.Cause(sigCtx).Error()
			break wait
		case <-closeRequests:
			reason = "graceful.Close called"
			break wait
		case err := <-errs:
			if err != nil {
				reason = err.Error()
				break wait
			}
		}
	}
	// From here on, a second signal kills the process right away.
	stopSignals()
	log.Action("graceful.Shutdown").Warn(reason)
	runClosers(timeoutFromEnv())
}

// runClosers runs and forgets every registered closer, newest first, sharing
// one deadline. A failing closer does not stop the others.
func runClosers(timeout time.Duration) {
	mu.Lock()
	pending := closers
	closers = nil
	mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for _, p := range slices.Backward(pending) {
		err := p(ctx)
		if err == nil {
			continue
		}
		// Closing stdout/stderr (for example by a logger) is not worth a warning.
		if pathErr, ok := errors.AsType[*os.PathError](err); ok && strings.HasPrefix(pathErr.Path, "/dev/std") {
			continue
		}
		log.Action("graceful.Closer").Warn(err.Error())
	}
}

func timeoutFromEnv() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("GRACEFUL_TIMEOUT")); err == nil && d > 0 {
		return d
	}
	return defaultTimeout
}
