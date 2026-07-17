package async

import (
	"context"
	"errors"
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
