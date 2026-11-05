package graceful

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// reset restores package state between tests.
func reset(t *testing.T) {
	t.Helper()
	drain := func() {
		mu.Lock()
		closers = nil
		mu.Unlock()
		select {
		case <-closeRequests:
		default:
		}
	}
	drain()
	t.Cleanup(drain)
}

// AddCloser is called concurrently by dependencies.Init; it must be race-free
// (run with -race).
func TestAddCloserConcurrent(t *testing.T) {
	reset(t)
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			AddCloser(func(context.Context) error { return nil })
		})
	}
	wg.Wait()
	if n := countClosers(); n != 50 {
		t.Fatalf("closers = %d, want 50", n)
	}
}

func countClosers() int {
	mu.Lock()
	defer mu.Unlock()
	return len(closers)
}

// Closers run last-in-first-out, each exactly once, and errors don't stop
// the remaining closers.
func TestRunClosersLIFO(t *testing.T) {
	reset(t)
	var order []int
	for i := range 3 {
		AddCloser(func(context.Context) error {
			order = append(order, i)
			if i == 1 {
				return errors.New("closer 1 failed")
			}
			return nil
		})
	}
	runClosers(time.Second)
	if !slices.Equal(order, []int{2, 1, 0}) {
		t.Fatalf("order = %v", order)
	}
	runClosers(time.Second) // already drained: nothing runs twice
	if len(order) != 3 {
		t.Fatalf("closers ran twice: %v", order)
	}
}

// All closers share one overall deadline.
func TestRunClosersSharedDeadline(t *testing.T) {
	reset(t)
	var deadlines []time.Time
	for range 2 {
		AddCloser(func(ctx context.Context) error {
			d, _ := ctx.Deadline()
			deadlines = append(deadlines, d)
			return nil
		})
	}
	runClosers(time.Minute)
	if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("deadlines = %v", deadlines)
	}
}

// Close() stops Start, which then runs the closers and returns.
func TestStartReturnsAfterClose(t *testing.T) {
	reset(t)
	ran := make(chan struct{})
	AddCloser(func(context.Context) error { close(ran); return nil })
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		Start(context.Background(), func(context.Context) error {
			<-release // a long-running server
			return nil
		})
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after Close")
	}
	select {
	case <-ran:
	default:
		t.Fatal("closer did not run")
	}
	close(release)
}

// A server that fails to start (for example, the port is taken) triggers a
// shutdown instead of leaving the process half-alive.
func TestStartShutsDownWhenAServerFails(t *testing.T) {
	reset(t)
	ran := make(chan struct{})
	AddCloser(func(context.Context) error { close(ran); return nil })
	done := make(chan struct{})
	go func() {
		Start(context.Background(),
			func(context.Context) error { return errors.New("listen: address already in use") },
			func(ctx context.Context) error { <-ctx.Done(); return nil },
		)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after a server error")
	}
	select {
	case <-ran:
	default:
		t.Fatal("closer did not run")
	}
}

// Servers that return nil (a clean stop) do not trigger a shutdown by
// themselves; Start keeps waiting for a signal or Close.
func TestStartKeepsWaitingWhenServersReturnNil(t *testing.T) {
	reset(t)
	done := make(chan struct{})
	go func() {
		Start(context.Background(), func(context.Context) error { return nil })
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Start returned although nothing asked it to stop")
	case <-time.After(100 * time.Millisecond):
	}
	Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after Close")
	}
}

func TestTimeoutFromEnv(t *testing.T) {
	cases := map[string]time.Duration{
		"":      defaultTimeout,
		"30s":   30 * time.Second,
		"bogus": defaultTimeout,
		"-1s":   defaultTimeout,
	}
	for in, want := range cases {
		t.Setenv("GRACEFUL_TIMEOUT", in)
		if got := timeoutFromEnv(); got != want {
			t.Errorf("GRACEFUL_TIMEOUT=%q: got %v, want %v", in, got, want)
		}
	}
}
