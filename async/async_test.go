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
	// full generic version (suggest)
	resp := Async(future, fn, &fnRequest{"test"})
	// interface version
	resp2 := future.Async(fn, &fnRequest{"test2"}).(*fnResponse)
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
