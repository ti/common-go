package async

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestAsync Test the async functions
func TestAsync(t *testing.T) {
	ctx, cc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cc()
	future := New(ctx)
	resp := future.Async(fn, &fnRequest{"test"})
	resp2 := future.Async(fn, &fnRequest{"test2"})
	if err := future.Await(); err != nil {
		t.Log(err)
		t.Fail()
	}
	t.Log(resp.Data, resp2.Data)
}

type fnRequest struct {
	Data string
}

type fnResponse struct {
	Data string
}

func fn(_ context.Context, in *fnRequest) (out *fnResponse, err error) {
	time.Sleep(1 * time.Second)
	if in.Data == "" {
		return nil, errors.New("the in string is empty")
	}
	return &fnResponse{
		Data: "success for " + in.Data,
	}, nil
}

// TestAwaitReturnsFirstError verifies that when multiple pipeline functions
// fail, Await returns the error from the one that fails first (by completion
// time), not the latest one. This is the errgroup "first error" semantic.
func TestAwaitReturnsFirstError(t *testing.T) {
	ctx, cc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cc()
	future := New(ctx)

	// Fails first: short delay, this error should win.
	future.Async(failAfter, &failRequest{delay: 50 * time.Millisecond, msg: "first"})
	// Fails later: longer delay, this error should be ignored by Await.
	future.Async(failAfter, &failRequest{delay: 500 * time.Millisecond, msg: "second"})

	err := future.Await()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if err.Error() != "first" {
		t.Fatalf("expected first error %q, got %q", "first", err.Error())
	}
}

type failRequest struct {
	delay time.Duration
	msg   string
}

func failAfter(_ context.Context, in *failRequest) (*fnResponse, error) {
	time.Sleep(in.delay)
	return nil, errors.New(in.msg)
}

func TestGo(t *testing.T) {
	f := New(context.Background())
	var n atomic.Int32
	for range 10 {
		f.Go(func(context.Context) error { n.Add(1); return nil })
	}
	if err := f.Await(); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 10 {
		t.Fatalf("ran %d, want 10", n.Load())
	}
}

func TestGoCancelsSiblingsOnError(t *testing.T) {
	f := New(context.Background())
	boom := errors.New("boom")
	f.Go(func(context.Context) error { return boom })
	f.Go(func(ctx context.Context) error { <-ctx.Done(); return nil })
	if err := f.Await(); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

// A function may return (nil, nil) for a pointer result ("not found"). The
// pre-allocated result must then stay at its zero value instead of panicking
// inside the errgroup goroutine.
func TestAsyncNilPointerResult(t *testing.T) {
	type out struct{ N int }
	f := New(context.Background())
	got := f.Async(func(context.Context, int) (*out, error) { return nil, nil }, 1)
	if err := f.Await(); err != nil {
		t.Fatalf("Await: %v", err)
	}
	if got == nil || got.N != 0 {
		t.Fatalf("got %+v, want a zero value", got)
	}
}

// Limit bounds how many functions run at once.
func TestLimit(t *testing.T) {
	f := New(context.Background()).Limit(2)
	var running, peak atomic.Int32
	for range 10 {
		f.Go(func(context.Context) error {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
			return nil
		})
	}
	if err := f.Await(); err != nil {
		t.Fatalf("Await: %v", err)
	}
	if p := peak.Load(); p != 2 {
		t.Fatalf("peak concurrency = %d, want 2", p)
	}
}

// Await waits for every function even after the context is canceled; the
// functions see the cancellation through their ctx.
func TestAwaitWaitsForAll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := New(ctx)
	var finished atomic.Bool
	f.Go(func(ctx context.Context) error {
		<-ctx.Done()
		time.Sleep(10 * time.Millisecond)
		finished.Store(true)
		return nil
	})
	cancel()
	if err := f.Await(); err != nil {
		t.Fatalf("Await: %v", err)
	}
	if !finished.Load() {
		t.Fatal("Await returned before the function finished")
	}
}
