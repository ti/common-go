package background

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ti/common-go/graceful"
)

// With package graceful (the default wiring): the drain signal stops
// submissions, watchers and periodic ticks; the closers interrupt what is
// left; graceful.Start returns only after every job goroutine ended.
func TestGracefulIntegration(t *testing.T) {
	noLeaks(t)
	ctx, stop := context.WithCancel(bg())
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		graceful.Start(ctx, func(context.Context) error { close(started); return nil })
		close(done)
	}()
	<-started // Start has begun a fresh drain lifecycle

	q := NewQueue(QueueOptions{Workers: 1, Grace: 50 * time.Millisecond})
	r := NewRunner(RunnerOptions{Grace: 50 * time.Millisecond})
	handlerStarted := make(chan struct{}, 1)
	q.Handle("k", func(ctx context.Context, _ *Run) ([]byte, error) {
		handlerStarted <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}, HandlerOptions{})
	running := mustSubmit(t, q, SubmitRequest{Kind: "k"})
	queued := mustSubmit(t, q, SubmitRequest{Kind: "k"})
	<-handlerStarted

	taskCause := make(chan error, 1)
	r.Go(bg(), "test.graceful", func(ctx context.Context) error {
		<-ctx.Done()
		taskCause <- context.Cause(ctx)
		return nil
	})
	ticks := make(chan struct{}, 1000)
	r.Every("test.graceful.tick", time.Millisecond, func(context.Context) error { ticks <- struct{}{}; return nil })
	<-ticks

	watchErr := make(chan error, 1)
	go func() {
		for _, err := range q.Watch(bg(), running.ID) {
			if err != nil {
				watchErr <- err
				return
			}
		}
		watchErr <- nil
	}()

	stop() // what SIGTERM does
	<-done // closers have run

	if err := <-watchErr; !errors.Is(err, ErrShuttingDown) {
		t.Errorf("watcher ended with %v, want ErrShuttingDown", err)
	}
	if cause := <-taskCause; !errors.Is(cause, ErrShuttingDown) {
		t.Errorf("task cancellation cause = %v", cause)
	}
	if _, _, err := q.Submit(bg(), SubmitRequest{Kind: "k"}); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("Submit after shutdown = %v", err)
	}
	for _, id := range []string{running.ID, queued.ID} {
		if job, err := q.Get(bg(), id); err != nil || job.State != StateInterrupted {
			t.Errorf("job %s = %+v, %v; want interrupted", id, job, err)
		}
	}
	if r.Running() != 0 || q.Stats() != (Stats{}) {
		t.Errorf("after graceful.Start returned: %d tasks, stats %+v", r.Running(), q.Stats())
	}
	if r.Go(bg(), "late", func(context.Context) error { return nil }) {
		t.Error("Go started a task after shutdown")
	}
}

const sigtermHelperEnv = "BACKGROUND_SIGTERM_HELPER"

// TestSIGTERM runs a real process, sends it SIGTERM while jobs are running,
// and checks what the process reports after graceful.Start returned.
func TestSIGTERM(t *testing.T) {
	if os.Getenv(sigtermHelperEnv) != "" {
		sigtermHelper()
		return
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSIGTERM$")
	cmd.Env = append(os.Environ(), sigtermHelperEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	report := map[string]string{}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "READY" {
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && strings.HasPrefix(k, "report.") {
			report[strings.TrimPrefix(k, "report.")] = v
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper process: %v", err)
	}
	want := map[string]string{
		"quick":       "succeeded",   // finished within the grace period after SIGTERM
		"long":        "interrupted", // cancelled when the grace period ended
		"long.cause":  ErrShuttingDown.Error(),
		"queued":      "interrupted", // never started
		"submit":      ErrShuttingDown.Error(),
		"task.cause":  ErrShuttingDown.Error(),
		"loop.cause":  ErrShuttingDown.Error(),
		"fast":        "true",
		"goroutines":  "0",
		"leakprofile": "",
	}
	for k, v := range want {
		if report[k] != v {
			t.Errorf("report.%s = %q, want %q (full report: %v)", k, report[k], v, report)
		}
	}
}

func sigtermHelper() {
	q := NewQueue(QueueOptions{Workers: 2, Grace: 300 * time.Millisecond})
	started := make(chan struct{}, 2)
	longCause := make(chan error, 1)
	q.Handle("quick", func(ctx context.Context, _ *Run) ([]byte, error) {
		started <- struct{}{}
		// Outlives the signal, ends within the grace period.
		select {
		case <-graceful.Draining():
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-time.After(50 * time.Millisecond):
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, HandlerOptions{})
	q.Handle("long", func(ctx context.Context, _ *Run) ([]byte, error) {
		started <- struct{}{}
		<-ctx.Done()
		longCause <- context.Cause(ctx)
		return nil, ctx.Err()
	}, HandlerOptions{})
	taskCause := make(chan error, 1)
	loopCause := make(chan error, 1)
	runner := NewRunner(RunnerOptions{Grace: 300 * time.Millisecond})

	var quick, long, queued Job
	begin := time.Now()
	graceful.Start(bg(), func(context.Context) error {
		quick, _, _ = q.Submit(bg(), SubmitRequest{Kind: "quick"})
		long, _, _ = q.Submit(bg(), SubmitRequest{Kind: "long"})
		queued, _, _ = q.Submit(bg(), SubmitRequest{Kind: "quick"})
		<-started
		<-started
		// A task with work to finish: cancelled only after the grace period.
		runner.Go(bg(), "helper.task", func(ctx context.Context) error {
			<-ctx.Done()
			taskCause <- context.Cause(ctx)
			return nil
		})
		// The process-wide runner: a loop (cancelled at drain) and a ticker.
		Go(bg(), "helper.oneshot", func(context.Context) error { return nil })
		Loop("helper.loop", func(ctx context.Context) error {
			<-ctx.Done()
			loopCause <- context.Cause(ctx)
			return ctx.Err()
		})
		Every("helper.tick", time.Millisecond, func(context.Context) error { return nil })
		fmt.Println("READY")
		return nil
	})

	state := func(id string) string {
		job, err := q.Get(bg(), id)
		if err != nil {
			return err.Error()
		}
		return string(job.State)
	}
	fmt.Println("report.quick=" + state(quick.ID))
	fmt.Println("report.long=" + state(long.ID))
	fmt.Println("report.long.cause=" + (<-longCause).Error())
	fmt.Println("report.queued=" + state(queued.ID))
	_, _, err := q.Submit(bg(), SubmitRequest{Kind: "quick"})
	fmt.Println("report.submit=" + fmt.Sprint(err))
	fmt.Println("report.task.cause=" + (<-taskCause).Error())
	fmt.Println("report.loop.cause=" + (<-loopCause).Error())
	// The default runner had only a loop and a ticker: its 10s grace period
	// must not have been waited for.
	fmt.Printf("report.fast=%v\n", time.Since(begin) < 5*time.Second)
	time.Sleep(50 * time.Millisecond)
	fmt.Printf("report.goroutines=%d\n", len(strayGoroutines()))
	fmt.Println("report.leakprofile=" + strings.ReplaceAll(leakProfile(), "\n", " | "))
	os.Exit(0)
}

// With a server that waits before it stops accepting (grpcmux and PRE_STOP),
// jobs are still accepted during that wait and refused from the moment the
// server drains; the queue's closer runs after the server's.
func TestJobsAcceptedDuringPreStop(t *testing.T) {
	noLeaks(t)
	q := NewQueue(QueueOptions{Grace: 50 * time.Millisecond}) // registered before the server: closed after it
	q.Handle("k", func(context.Context, *Run) ([]byte, error) { return nil, nil }, HandlerOptions{})

	var duringPreStop, afterDrain error
	var preStopJob Job
	serverClose := func(context.Context) error { // what grpcmux.Server.Close does
		time.Sleep(20 * time.Millisecond) // PRE_STOP: still serving
		preStopJob, _, duringPreStop = q.Submit(bg(), SubmitRequest{Kind: "k"})
		graceful.Drain() // stops accepting
		_, _, afterDrain = q.Submit(bg(), SubmitRequest{Kind: "k"})
		return nil
	}
	ctx, stop := context.WithCancel(bg())
	done := make(chan struct{})
	started := make(chan struct{})
	go func() {
		graceful.AddCloser(serverClose)
		graceful.DeferDrain() // grpcmux.Server.Start does this
		graceful.Start(ctx, func(context.Context) error { close(started); return nil })
		close(done)
	}()
	<-started
	stop()
	<-done
	if duringPreStop != nil {
		t.Errorf("Submit during the pre-stop wait = %v, want accepted", duringPreStop)
	}
	if !errors.Is(afterDrain, ErrShuttingDown) {
		t.Errorf("Submit after the server drained = %v, want ErrShuttingDown", afterDrain)
	}
	if job, err := q.Get(bg(), preStopJob.ID); err != nil || !job.State.Done() {
		t.Errorf("job accepted during pre-stop = %+v, %v; want done (succeeded, or interrupted if it was still queued)", job, err)
	}
}
