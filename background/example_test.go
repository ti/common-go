package background_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/rueidis/rueidislock"

	"github.com/ti/common-go/background"
	"github.com/ti/common-go/dependencies/redis"
)

// A job queue behind a pair of RPCs: one submits, one watches.
func ExampleQueue() {
	q := background.NewQueue(background.QueueOptions{Name: "exports", Workers: 2, Backlog: 10, Standalone: true})
	q.Handle("export", func(ctx context.Context, run *background.Run) ([]byte, error) {
		for i := range int64(3) {
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			run.SetProgress(i+1, 3, "")
		}
		return []byte("3 rows"), nil
	}, background.HandlerOptions{Timeout: time.Minute, MaxAttempts: 3})

	// In the "start" RPC:
	job, _, err := q.Submit(context.Background(), background.SubmitRequest{Kind: "export", Key: "client-request-id"})
	if err != nil {
		fmt.Println(err) // carries its gRPC code: return it as it is
		return
	}

	// In the server-streaming "watch" RPC:
	for snapshot, err := range q.Watch(context.Background(), job.ID) {
		if err != nil {
			fmt.Println(err)
			return
		}
		if snapshot.State.Done() {
			fmt.Println(snapshot.State, string(snapshot.Result))
		}
	}
	_ = q.Shutdown(context.Background())
	// Output: succeeded 3 rows
}

// A periodic task that runs on one replica at a time, using the lock of the
// Redis dependency. (Compiled, not run: it needs a Redis server.)
func ExampleWithLocker() {
	var deps struct{ Redis *redis.Redis } // initialized by config.Init

	locker := background.LockerFunc(func(ctx context.Context, name string) (context.Context, context.CancelFunc, error) {
		lockCtx, unlock, err := deps.Redis.Locker().TryWithContext(ctx, "periodic:"+name)
		if errors.Is(err, rueidislock.ErrNotLocked) {
			err = background.ErrLockNotAcquired
		}
		return lockCtx, unlock, err
	})
	background.Every("cleanup.expired-sessions", 5*time.Minute, func(ctx context.Context) error {
		return nil // delete expired rows
	}, background.WithLocker(locker))
}
