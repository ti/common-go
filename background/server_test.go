package background_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ti/common-go/background"
	"github.com/ti/common-go/graceful"
	"github.com/ti/common-go/grpcmux"
)

// Background work that is first used inside a request handler (the runner
// and the queue are created there, after the server registered its closer)
// must keep working while the server is still serving during its PRE_STOP
// wait, and must be shut down only after the server stopped serving. A real
// grpcmux server; the shutdown is the one SIGTERM triggers.
func TestLazyFirstUseInHandlerWithRealServer(t *testing.T) {
	t.Setenv("PRE_STOP", "400ms")
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	g, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcAddr := g.Addr().String()
	_ = g.Close()

	var (
		runner         *background.Runner
		queue          *background.Queue
		tasksStarted   atomic.Int32
		tasksCompleted atomic.Int32
		tasksCancelled atomic.Int32
	)
	gs := grpcmux.NewServer(grpcmux.WithHTTPAddr(addr), grpcmux.WithGrpcAddr(grpcAddr), grpcmux.WithMetricsAddr(""))
	gs.Handle("/work", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First use: created inside the handler, long after gs.Run
		// registered the server's closer.
		if runner == nil {
			runner = background.NewRunner(background.RunnerOptions{Grace: 2 * time.Second})
			queue = background.NewQueue(background.QueueOptions{Grace: 2 * time.Second})
			queue.Handle("k", func(context.Context, *background.Run) ([]byte, error) { return []byte("ok"), nil },
				background.HandlerOptions{})
		}
		started := runner.Go(r.Context(), "test.lazy", func(ctx context.Context) error {
			tasksStarted.Add(1)
			select {
			case <-time.After(50 * time.Millisecond):
				tasksCompleted.Add(1)
			case <-ctx.Done():
				tasksCancelled.Add(1)
			}
			return nil
		})
		job, _, err := queue.Submit(r.Context(), background.SubmitRequest{Kind: "k"})
		_, _ = fmt.Fprintf(w, "started=%v submit=%v job=%s", started, err, job.ID)
	}))
	done := make(chan struct{})
	go func() { gs.Start(); close(done) }()

	call := func() string {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/work", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "request failed: " + err.Error()
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}
	var first string
	for range 100 { // wait for the listener
		if first = call(); first[:8] == "started=" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if first[:len("started=true submit=<nil>")] != "started=true submit=<nil>" {
		t.Fatalf("before shutdown: %s", first)
	}

	graceful.Close() // SIGTERM: the server keeps serving for PRE_STOP
	time.Sleep(100 * time.Millisecond)
	var jobIDs []string
	for i := range 5 {
		got := call()
		const want = "started=true submit=<nil> job="
		if len(got) < len(want) || got[:len(want)] != want {
			t.Fatalf("request %d during PRE_STOP: %s; background work must still be accepted", i, got)
		}
		jobIDs = append(jobIDs, got[len(want):])
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the server did not shut down")
	}
	if tasksStarted.Load() != 6 || tasksCompleted.Load() != 6 || tasksCancelled.Load() != 0 {
		t.Fatalf("tasks started=%d completed=%d cancelled=%d, want 6/6/0", tasksStarted.Load(), tasksCompleted.Load(), tasksCancelled.Load())
	}
	for _, id := range jobIDs {
		if job, err := queue.Get(context.Background(), id); err != nil || job.State != background.StateSucceeded {
			t.Fatalf("job %s accepted during PRE_STOP = %+v, %v; want succeeded", id, job, err)
		}
	}
	// After the server stopped serving, the work was shut down.
	if runner.Running() != 0 || runner.Go(context.Background(), "late", func(context.Context) error { return nil }) {
		t.Fatal("the runner was not shut down after the server")
	}
}

// The process-wide runner is replaced after a shutdown, so a binary that
// runs several lifecycles (a test suite) keeps working.
func TestDefaultRunnerSurvivesLifecycles(t *testing.T) {
	for i := range 2 {
		ctx, stop := context.WithCancel(context.Background())
		started, done := make(chan struct{}), make(chan struct{})
		go func() {
			graceful.Start(ctx, func(context.Context) error { close(started); return nil })
			close(done)
		}()
		<-started
		ran := make(chan struct{})
		if !background.Go(context.Background(), "test.default", func(context.Context) error { close(ran); return nil }) {
			t.Fatalf("lifecycle %d: background.Go did not start", i)
		}
		<-ran
		stop()
		<-done
	}
}

// Once the process-wide runner has been shut down, a late background.Go
// (from a dependency's closer, or from code that runs after graceful.Start
// returned) is refused instead of being started on a new runner: it would
// cost the shutdown a full grace period, or never be supervised at all.
func TestDefaultRunnerRefusesWorkAfterItsShutdown(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	started, done := make(chan struct{}), make(chan struct{})
	var startedInDependencyCloser atomic.Bool
	graceful.AddCloser(func(context.Context) error { // a dependency: closed after the work
		startedInDependencyCloser.Store(background.Go(context.Background(), "test.late", func(ctx context.Context) error {
			<-ctx.Done() // would hold the shutdown for the whole grace period
			return nil
		}))
		return nil
	})
	go func() {
		graceful.Start(ctx, func(context.Context) error { close(started); return nil })
		close(done)
	}()
	<-started
	ran := make(chan struct{})
	if !background.Go(context.Background(), "test.early", func(context.Context) error { close(ran); return nil }) {
		t.Fatal("background.Go before shutdown did not start")
	}
	<-ran

	begin := time.Now()
	stop()
	<-done
	if took := time.Since(begin); took > 3*time.Second {
		t.Fatalf("shutdown took %s: a task started after the runner's shutdown held it", took)
	}
	if startedInDependencyCloser.Load() {
		t.Fatal("background.Go started a task after the default runner was shut down")
	}
	if background.Go(context.Background(), "test.after", func(context.Context) error { return nil }) {
		t.Fatal("background.Go started a task after graceful.Start returned")
	}
}
