package graceful

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// reset restores package state between tests.
func reset(t *testing.T) {
	t.Helper()
	drain := func() {
		mu.Lock()
		closers = nil
		deferrals, current = nil, nil
		workClosers = nil
		mu.Unlock()
		mu.Lock()
		closeCh = make(chan struct{})
		active, shutdownSeen = 0, false
		mu.Unlock()
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

// A panicking closer is reported and does not stop the remaining closers.
func TestRunClosersPanicDoesNotStopOthers(t *testing.T) {
	reset(t)
	var ran []string
	AddCloser(func(context.Context) error { ran = append(ran, "first"); return nil })
	AddCloser(func(context.Context) error { panic("boom") })
	AddCloser(func(context.Context) error { ran = append(ran, "last"); return nil })
	runClosers(time.Second)
	if !slices.Equal(ran, []string{"last", "first"}) {
		t.Fatalf("ran = %v, want [last first]", ran)
	}
}

// A closer that ignores its context is abandoned after the deadline plus
// abandonGrace, and the closers after it still run.
func TestRunClosersAbandonsStuckCloser(t *testing.T) {
	reset(t)
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var firstRan bool
		AddCloser(func(context.Context) error { firstRan = true; return nil })
		AddCloser(func(context.Context) error { <-release; return nil }) // ignores ctx
		start := time.Now()
		runClosers(10 * time.Second)
		if got, want := time.Since(start), 10*time.Second+abandonGrace; got != want {
			t.Fatalf("runClosers took %s, want %s", got, want)
		}
		if !firstRan {
			t.Fatal("the closer registered before the stuck one did not run")
		}
		close(release) // let the abandoned goroutine end before the bubble does
	})
}

// Draining is closed when shutdown begins (before the closers run), stays
// closed, and a later Start gets a fresh channel.
func TestDrainingSignal(t *testing.T) {
	reset(t)
	resetDraining()
	if IsDraining() {
		t.Fatal("draining before any shutdown")
	}
	ch := Draining()
	var drainingInCloser bool
	AddCloser(func(context.Context) error { drainingInCloser = IsDraining(); return nil })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Start(ctx); close(done) }()
	select {
	case <-ch:
		t.Fatal("draining closed before shutdown was requested")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	<-done
	select {
	case <-ch:
	default:
		t.Fatal("Draining channel not closed after shutdown")
	}
	if !drainingInCloser {
		t.Fatal("closers ran before Draining was closed")
	}
	Drain() // idempotent
	if !IsDraining() {
		t.Fatal("IsDraining = false after shutdown")
	}

	// A second Start begins a new lifecycle.
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	started := make(chan struct{})
	go func() {
		Start(ctx2, func(context.Context) error { close(started); return nil })
		close(done2)
	}()
	<-started
	if IsDraining() {
		t.Fatal("a new Start must reset the draining signal")
	}
	cancel2()
	<-done2
}

// Drain is safe to call concurrently with readers.
func TestDrainConcurrent(t *testing.T) {
	reset(t)
	resetDraining()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(Drain)
		wg.Go(func() { _ = IsDraining() })
	}
	wg.Wait()
	if !IsDraining() {
		t.Fatal("not draining after Drain")
	}
	resetDraining()
}

// With DeferDrain, Start leaves the signal to the server: it is still open
// while the closers before the server's Drain call run (the pre-stop wait),
// and closed for the closers after it. Without a Drain call it is closed
// once the closers ran. The deferral applies to one shutdown only.
func TestDeferDrain(t *testing.T) {
	reset(t)
	resetDraining()
	var duringPreStop, afterServer bool
	AddCloser(func(context.Context) error { afterServer = IsDraining(); return nil }) // e.g. a job queue
	AddCloser(func(context.Context) error {                                           // the server
		duringPreStop = IsDraining()
		Drain()
		return nil
	})
	DeferDrain()
	runClosers(time.Second)
	if duringPreStop || !afterServer {
		t.Fatalf("draining during pre-stop = %v (want false), after the server drained = %v (want true)", duringPreStop, afterServer)
	}

	// A server that forgets to call Drain: closed after the closers.
	resetDraining()
	var inCloser bool
	AddCloser(func(context.Context) error { inCloser = IsDraining(); return nil })
	DeferDrain()
	runClosers(time.Second)
	if inCloser || !IsDraining() {
		t.Fatalf("draining in closer = %v (want false), after closers = %v (want true)", inCloser, IsDraining())
	}

	// The next shutdown is not deferred any more.
	resetDraining()
	AddCloser(func(context.Context) error { inCloser = IsDraining(); return nil })
	runClosers(time.Second)
	if !inCloser {
		t.Fatal("DeferDrain must apply to one shutdown only")
	}
	resetDraining()
}

// startAsync runs Start in a goroutine and returns once its function is
// running, with a channel that is closed when Start returns.
func startAsync(t *testing.T) <-chan struct{} {
	t.Helper()
	started, done := make(chan struct{}), make(chan struct{})
	go func() {
		Start(context.Background(), func(context.Context) error { close(started); return nil })
		close(done)
	}()
	<-started
	return done
}

func returned(done <-chan struct{}, within time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(within):
		return false
	}
}

// A Close that arrives after Start has shut down (a second Close in a test
// cleanup, a late error handler) must not make the next Start exit at once.
func TestCloseDoesNotLeakIntoNextStart(t *testing.T) {
	reset(t)
	done := startAsync(t)
	Close()
	Close() // the second request finds the first one pending
	if !returned(done, 5*time.Second) {
		t.Fatal("Start did not return after Close")
	}
	Close() // no Start is running: must be forgotten

	done = startAsync(t)
	if returned(done, 100*time.Millisecond) {
		t.Fatal("a Close from before this Start shut it down")
	}
	Close()
	if !returned(done, 5*time.Second) {
		t.Fatal("Start did not return after Close")
	}
}

// A Close before the first Start is still honored by that Start.
func TestCloseBeforeFirstStart(t *testing.T) {
	reset(t)
	Close()
	done := make(chan struct{})
	go func() { Start(context.Background()); close(done) }()
	if !returned(done, 5*time.Second) {
		t.Fatal("Start ignored a Close that was requested before it began")
	}
}

// One Close stops every running Start (two servers in one process).
func TestCloseStopsEveryStart(t *testing.T) {
	reset(t)
	a, b := startAsync(t), startAsync(t)
	Close()
	if !returned(a, 5*time.Second) || !returned(b, 5*time.Second) {
		t.Fatal("one Close must stop both running Starts")
	}
}

// Run reports the failure that made it shut down, so main can exit non-zero;
// a requested shutdown is not an error. Start discards it (and so a process
// whose server could not start used to exit 0 with no way to tell).
func TestRunReturnsTheFailure(t *testing.T) {
	boom := errors.New("listen tcp :8080: bind: address already in use")
	var closed atomic.Bool
	AddCloser(func(context.Context) error { closed.Store(true); return nil })
	err := Run(context.Background(), func(context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("Run = %v, want the function's error", err)
	}
	if !closed.Load() {
		t.Fatal("closers did not run before Run returned")
	}

	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), func(ctx context.Context) error { <-ctx.Done(); return nil })
	}()
	time.Sleep(50 * time.Millisecond)
	Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after Close = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Close")
	}
}

// Work closers run after the closer that drained the process (the server)
// and before the closers registered earlier (the dependencies), whenever
// they were registered: before the server, after it, or during its pre-stop
// wait.
func TestWorkClosersRunAfterDrain(t *testing.T) {
	reset(t)
	resetDraining()
	var order []string
	note := func(name string) func(context.Context) error {
		return func(context.Context) error { order = append(order, name); return nil }
	}
	AddWorkCloser(note("work-early"))
	AddCloser(note("database"))
	AddCloser(func(context.Context) error { // the server
		order = append(order, "server:pre-stop")
		AddWorkCloser(note("work-during-pre-stop")) // first use inside a late request
		Drain()
		order = append(order, "server:stopped")
		return nil
	})
	DeferDrain()
	AddWorkCloser(note("work-late")) // registered after the server: must not run before it
	runClosers(time.Second)
	want := []string{"server:pre-stop", "server:stopped", "work-during-pre-stop", "work-late", "work-early", "database"}
	if !slices.Equal(order, want) {
		t.Fatalf("order = %v\nwant    %v", order, want)
	}

	// Without a server: draining starts at once, work stops before the
	// dependencies.
	resetDraining()
	order = nil
	AddCloser(note("database"))
	AddWorkCloser(note("work"))
	runClosers(time.Second)
	if !slices.Equal(order, []string{"work", "database"}) {
		t.Fatalf("order without a server = %v", order)
	}
	resetDraining()
}

// Two servers in one process, each in its own Run with a deferred drain: the
// signal must stay open until the last server drains, although the Run that
// finds no closers left finishes its part first.
func TestDeferDrainWithTwoRuns(t *testing.T) {
	reset(t)
	resetDraining()
	inPreStop := make(chan struct{})
	release := make(chan struct{})
	var drainedEarly [2]bool
	for i := range 2 {
		AddCloser(func(context.Context) error { // a server's Close
			if i == 1 { // runs first (newest)
				close(inPreStop)
				<-release
			}
			drainedEarly[i] = IsDraining()
			Drain()
			return nil
		})
		DeferDrain()
	}
	a, b := startAsync(t), startAsync(t)
	Close()
	<-inPreStop
	time.Sleep(50 * time.Millisecond) // the other Run has nothing to close and must wait
	if IsDraining() {
		t.Fatal("draining closed while a server was still in its pre-stop wait")
	}
	if returned(a, 0) || returned(b, 0) {
		t.Fatal("a Run returned before the closers had run")
	}
	close(release)
	if !returned(a, 5*time.Second) || !returned(b, 5*time.Second) {
		t.Fatal("Runs did not return")
	}
	if drainedEarly[1] || drainedEarly[0] || !IsDraining() {
		t.Fatalf("draining before each server's own Drain = %v; after shutdown = %v", drainedEarly, IsDraining())
	}
}

// If the server's closer panics, or returns without ever calling Drain,
// background work is still closed before the dependencies.
func TestWorkClosersBeforeDependenciesWhenServerCloserFails(t *testing.T) {
	for name, server := range map[string]func(context.Context) error{
		"panics":          func(context.Context) error { panic("server close failed") },
		"returns early":   func(context.Context) error { return errors.New("could not stop") },
		"is abandoned":    nil, // blocks until the test releases it; see below
		"drains normally": func(context.Context) error { Drain(); return nil },
	} {
		t.Run(name, func(t *testing.T) {
			reset(t)
			// The signal's channel must belong to the bubble that uses it.
			fresh := func() {
				mu.Lock()
				draining = make(chan struct{})
				mu.Unlock()
			}
			defer fresh()
			synctest.Test(t, func(t *testing.T) {
				fresh()
				var omu sync.Mutex
				var order []string
				add := func(n string) {
					omu.Lock()
					order = append(order, n)
					omu.Unlock()
				}
				note := func(n string) func(context.Context) error {
					return func(context.Context) error { add(n); return nil }
				}
				AddCloser(note("database"))
				stuck := make(chan struct{})
				defer close(stuck) // lets the abandoned closer end with the bubble
				AddCloser(func(ctx context.Context) error {
					add("server")
					if server == nil {
						<-stuck // ignores its context
						return nil
					}
					return server(ctx)
				})
				DeferDrain()
				AddWorkCloser(note("work"))
				runClosers(10 * time.Second)
				omu.Lock()
				defer omu.Unlock()
				if !slices.Equal(order, []string{"server", "work", "database"}) {
					t.Fatalf("order = %v, want [server work database]", order)
				}
			})
		})
	}
}

// Releasing one server's deferral twice (its Close called twice, directly
// or through Drain inside its closer) must not stand for the other server.
// A Drain from outside a deferring closer closes the signal at once.
func TestDeferralsAreIdempotentPerServer(t *testing.T) {
	reset(t)
	resetDraining()
	releaseA := DeferDrain()
	releaseB := DeferDrain()
	releaseA()
	releaseA()
	releaseA()
	if IsDraining() {
		t.Fatal("releasing one server's deferral repeatedly closed the signal while the other server still serves")
	}
	releaseB()
	if !IsDraining() {
		t.Fatal("the signal must close with the last deferral")
	}

	// Inside a closer, Drain is that closer's release, also when repeated.
	resetDraining()
	mu.Lock()
	deferrals = nil
	mu.Unlock()
	var afterFirst, afterSecond bool
	AddCloser(func(context.Context) error { // server A: runs last
		Drain()
		return nil
	})
	DeferDrain()
	AddCloser(func(context.Context) error { // server B: runs first, drains twice
		Drain()
		Drain()
		afterFirst = IsDraining()
		return nil
	})
	DeferDrain()
	AddCloser(func(context.Context) error { afterSecond = IsDraining(); return nil }) // newest: before both
	runClosers(time.Second)
	if afterFirst || afterSecond {
		t.Fatalf("draining after server B drained twice = %v (want false: server A still serves)", afterFirst)
	}

	// A Drain by the program itself is immediate, deferral or not.
	resetDraining()
	DeferDrain()
	Drain()
	if !IsDraining() {
		t.Fatal("a Drain from outside a deferring closer must close the signal at once")
	}
	reset(t)
	resetDraining()
}

// A closer registered while the shutdown runs (by another closer, or by a
// late request) is run by this shutdown and does not leak into the next.
func TestCloserRegisteredDuringShutdownRuns(t *testing.T) {
	reset(t)
	resetDraining()
	var order []string
	AddCloser(func(context.Context) error { order = append(order, "first"); return nil })
	AddCloser(func(context.Context) error {
		order = append(order, "second")
		AddCloser(func(context.Context) error { order = append(order, "late"); return nil })
		return nil
	})
	runClosers(time.Second)
	if !slices.Equal(order, []string{"second", "first", "late"}) {
		t.Fatalf("order = %v, want [second first late]", order)
	}
	if n := countClosers(); n != 0 {
		t.Fatalf("%d closers left for the next lifecycle", n)
	}
	resetDraining()
}
