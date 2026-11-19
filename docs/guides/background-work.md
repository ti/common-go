# Background work

Package `background` runs work that outlives the request that started it, inside the server process, and shuts it down together with the process. It is small on purpose: four primitives, one shutdown model, one storage seam.

| You need | Use |
|---|---|
| Finish something after the response was sent (send a mail, write an audit record, warm a cache) | [`background.Go`](#go-work-that-outlives-the-request) |
| A function that runs until the process stops (a subscription, a consumer, a watch) | [`background.Loop`](#loop-run-until-shutdown) |
| A task every N minutes (cleanup, refresh, sync) | [`background.Every`](#every-periodic-tasks) |
| A job the caller can follow: status, progress, result, cancel | [`background.Queue`](#queue-jobs-with-observable-status) |
| Fan out inside one request and wait for all results | `async.Future` (not this package) |

Read [When this is the wrong tool](#when-this-is-the-wrong-tool) before you build on the queue.

## Go: work that outlives the request

```go
func (s *Service) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.CreateUserResponse, error) {
	user, err := s.store.Create(ctx, req.GetUser())
	if err != nil {
		return nil, err
	}
	background.Go(ctx, "user.welcome-mail", func(ctx context.Context) error {
		return s.mailer.SendWelcome(ctx, user) // ctx is not the request's context any more
	})
	return &pb.CreateUserResponse{User: user}, nil
}
```

What `Go` does that a bare `go func()` does not:

- **Detached context.** The function's context carries the *values* of the request context (the authenticated subject, for example) but not its cancellation or deadline. It is cancelled only when the process shuts down and the grace period is over; `context.Cause(ctx)` is then `background.ErrShuttingDown`.
- **Panic recovery.** A panic is logged with its stack (`action: background.Task`, level error), counted in `background_panics_recovered_total{name}` and does not crash the process. `PANIC_RECOVERY=false` disables recovery, as it does for grpcmux.
- **A name.** The static name labels the logs (`task`) and the metrics. `log.Extract(ctx)` inside the function returns a logger tagged with it. The request's logger is not reused: the request may still be writing to it.
- **Tracing.** The function runs in a new root span (`background <name>`) with a *link* to the request's span, so the request's trace stays short and you can still navigate from one to the other.
- **Shutdown.** The goroutine is tracked. At shutdown it gets the grace period to finish, then its context is cancelled, and the process waits for it to return.

A returned error is logged at warn level; nobody else sees it. If somebody must see the outcome, use the queue.

`Go` returns `false` only after shutdown has finished waiting (the process is exiting); the function is then not started.

## Loop: run until shutdown

```go
background.Loop("catalog.watch", func(ctx context.Context) error {
	return s.catalog.Subscribe(ctx, s.onChange) // blocks; returns when ctx is done or the connection breaks
})
```

A loop is cancelled as soon as the process starts **draining**, without a grace period: it has nothing to finish. If the function returns an error or panics, it is started again after a backoff (1s, 2s, 4s ... capped at one minute, reset once a run lasted a minute). If it returns `nil`, the loop ends. Do not use `Go` for such functions: a `Go` task that waits for its context holds every shutdown for the whole grace period.

## Every: periodic tasks

```go
stop := background.Every("sessions.cleanup", 5*time.Minute, func(ctx context.Context) error {
	return s.store.DeleteExpiredSessions(ctx)
}, background.WithTimeout(time.Minute))
```

- **Jitter.** Each interval is spread by ±10% so replicas that started together do not tick together. `WithJitter(0.25)` changes the fraction, `WithJitter(0)` disables it.
- **No overlap.** The next run is scheduled when the previous one has returned. A run that takes longer than the interval delays the next tick; missed ticks are not made up.
- **First run** after one interval; `WithImmediate()` runs once right away.
- **A failing or panicking run** is logged and counted; the next tick still fires.
- **Shutdown.** No new run starts once the process drains; a run in progress may finish within the grace period.

### One runner across replicas

Without further options **every replica runs the task**. That is fine for idempotent, cheap work (deleting expired rows). When only one replica should run it, pass a `Locker`:

```go
locker := background.LockerFunc(func(ctx context.Context, name string) (context.Context, context.CancelFunc, error) {
	lockCtx, unlock, err := deps.Redis.Locker().TryWithContext(ctx, "periodic:"+name)
	if errors.Is(err, rueidislock.ErrNotLocked) {
		err = background.ErrLockNotAcquired
	}
	return lockCtx, unlock, err
})
background.Every("billing.rollup", time.Hour, rollup, background.WithLocker(locker))
```

The loop that acquires the lock keeps it across ticks and runs every tick (it is the leader for that task); the others skip their ticks and try again on the next one, so another replica takes over within about one interval after the leader stops. The lock is released at shutdown.

This is leadership, not a transaction. Around a lock loss (network partition, long pause) two replicas can run the task once at the same time, so the task must tolerate that. If it cannot, it needs a fencing token in the database, which is outside this package.

The library ships the interface, `LockerFunc` and an in-process `LocalLocker` (tests, single replica). The Redis adapter above is not shipped as code because it could not be tested against a real Redis in the library's CI; it is compiled as an example (`ExampleWithLocker`).

## Queue: jobs with observable status

A job is a unit of work with an ID and a state that a client can fetch, watch and cancel.

```
queued ──▶ running ──▶ succeeded
  ▲           │──────▶ failed        handler error that is not retried, timeout, panic
  └─ retry ───┤──────▶ cancelled     Queue.Cancel
              └──────▶ interrupted   the process shut down first
```

### Set up

```go
// Anywhere; dependencies its handlers use must exist before gs.Start().
q := background.NewQueue(background.QueueOptions{
	Name:    "exports", // metrics label
	Workers: 2,         // jobs running at once (default 4)
	Backlog: 20,        // jobs waiting (default 100); beyond that Submit fails
})
q.Handle("export", s.runExport, background.HandlerOptions{
	Timeout:     10 * time.Minute, // one attempt
	MaxAttempts: 3,                // total attempts for errors marked Retryable
})
```

### The handler

```go
func (s *Service) runExport(ctx context.Context, run *background.Run) ([]byte, error) {
	var req pb.StartExportRequest
	if err := proto.Unmarshal(run.Job().Payload, &req); err != nil {
		return nil, err // fails the job: the payload will not get better
	}
	rows, err := s.store.Rows(ctx, req.GetFilter())
	if err != nil {
		return nil, background.Retryable(err) // try again after a backoff
	}
	for i, row := range rows {
		if err := s.write(ctx, row); err != nil { // pass ctx to every blocking call
			return nil, err
		}
		run.SetProgress(int64(i+1), int64(len(rows)), "")
	}
	return []byte(s.objectURL(req)), nil // becomes Job.Result
}
```

Rules for handlers:

- **Honor the context.** It is cancelled by `Cancel`, by the attempt timeout and at shutdown. A handler that ignores it cannot be stopped ("no goroutine can kill another"); the queue reports it at shutdown and leaks the goroutine.
- **Return an error when cancelled** (`ctx.Err()` or `context.Cause(ctx)`). A handler that returns `nil` has succeeded, whatever happened to its context.
- **Errors fail the job unless marked** with `background.Retryable(err)`. Retries use `HandlerOptions.Backoff` (default 1s, 2s, 4s ... capped at 1m, ±20%). While it waits, the job is `queued` again with `Error` holding the last error and `NextAttemptAt` set.
- **Keep the payload small**: an ID and parameters, not the data. It is stored with the job.
- **Assume it can run twice** if you use retries or `RestartRequeue`.

### Submit, get, watch, cancel

```go
job, created, err := q.Submit(ctx, background.SubmitRequest{
	Kind:    "export",
	Key:     req.GetRequestId(),               // idempotency key, optional
	Payload: payload,
	Labels:  map[string]string{"owner": subject}, // for your own authorization checks
})
```

| Call | Result |
|---|---|
| `Submit` | The job snapshot; it runs when a worker is free. With a `Key`, a second submission of the same `(Kind, Key)` returns the first job (`created == false`) for as long as the job is retained. |
| `Get(ctx, id)` | The current snapshot. |
| `Watch(ctx, id)` | An iterator: the current snapshot, then every change, ending after a snapshot whose `State.Done()` is true. A slow consumer skips intermediate snapshots and always gets the last one. Costs no goroutine. |
| `Cancel(ctx, id)` | A queued job becomes `cancelled` at once. A running job has its context cancelled and becomes `cancelled` when the handler returns; if the handler completes anyway the job is `succeeded`. Cancelling a finished job changes nothing. |

Errors carry their gRPC code (`status.FromError` understands them), so a handler returns them as they are:

| Error | gRPC code | Meaning |
|---|---|---|
| `ErrQueueFull` | `RESOURCE_EXHAUSTED` | Workers and backlog are exhausted (`OverflowReject`, the default) |
| `ErrShuttingDown` | `UNAVAILABLE` | The process is draining; retry (against another replica) |
| `ErrNotFound` | `NOT_FOUND` | Unknown ID, or the job was pruned after `Retention` (default 1h) |
| `ErrNotOwner` | `FAILED_PRECONDITION` | `Cancel` for a job that runs in another process (durable store) |
| `ErrUnknownKind` | `INTERNAL` | No handler registered for the kind |

**Overflow is explicit.** With `OverflowReject` the caller sheds load. `OverflowBlock` makes `Submit` wait for room until its context ends; use it for internal producers that should slow down, never for request handlers without a deadline.

**Authorization is yours.** The queue does not know who may see a job. Put the owner in `Labels` and compare it in your RPCs before returning a job.

### The two RPCs

A unary method starts the job, a server-streaming method watches it. The full example is `examples/internal/reportservice` (proto: `examples/proto/example/report/v1/report.proto`).

```go
func (s *Service) StartReport(ctx context.Context, req *reportv1.StartReportRequest) (*reportv1.StartReportResponse, error) {
	payload, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}
	job, _, err := s.queue.Submit(ctx, background.SubmitRequest{Kind: kind, Key: req.GetRequestId(), Payload: payload})
	if err != nil {
		return nil, err
	}
	return &reportv1.StartReportResponse{Report: toProto(job)}, nil
}

func (s *Service) WatchReport(req *reportv1.WatchReportRequest, stream grpc.ServerStreamingServer[reportv1.WatchReportResponse]) error {
	for job, err := range s.queue.Watch(stream.Context(), req.GetReportId()) {
		if err != nil {
			return err
		}
		if err := stream.Send(&reportv1.WatchReportResponse{Report: toProto(job)}); err != nil {
			return err
		}
	}
	return nil
}
```

Client contract of the watch stream:

1. The first message is the current state (so a client that connects late misses nothing).
2. Messages follow on every change; progress messages may be coalesced.
3. After a message whose state is done, the stream ends with OK.
4. If the stream ends with `UNAVAILABLE`, the server is shutting down: call the method again. `NOT_FOUND` means the job is gone.

### Mapping to AIP-151 long-running operations

The queue maps onto `google.longrunning.Operation` if an API wants that shape instead of its own resource. The library does not ship an `Operations` service; the mapping is a dozen lines in the service.

| `Operation` | From the job |
|---|---|
| `name` | `"operations/" + Job.ID` |
| `done` | `Job.State.Done()` |
| `metadata` | Your metadata message built from `Job.Progress`, `Job.CreatedAt`, `Job.Attempt` |
| `response` | Your response message, from `Job.Result`, when `succeeded` |
| `error` | `google.rpc.Status`: `failed` → the code you choose (often `INTERNAL`) with `Job.Error`; `cancelled` → `CANCELLED`; `interrupted` → `ABORTED` |
| `GetOperation` / `CancelOperation` | `Queue.Get` / `Queue.Cancel` |
| `WaitOperation` | A loop over `Queue.Watch` with the request's timeout |
| expiry ("operations may expire") | `QueueOptions.Retention` |

AIP-151 has no streaming; a `Watch` RPC next to the operation is an addition for browsers, not a replacement. Request deduplication (AIP-155 `request_id`) is `SubmitRequest.Key`.

## Shutdown

`background` follows package `graceful`. With a grpcmux server the timeline after SIGTERM is:

| Step | Server | Background work |
|---|---|---|
| 1. SIGTERM | `/readyz` turns 503, waits `PRE_STOP` | Nothing changes: jobs are accepted and run |
| 2. Drain (`graceful.Draining()` closes) | Stops accepting, lets requests in flight finish | `Submit` → `UNAVAILABLE`; `Watch` streams end with `UNAVAILABLE`; queued jobs are no longer started; periodic ticks stop; loops are cancelled |
| 3. Server closed | | Running jobs and `Go` tasks are still running |
| 4. Queue/runner closers (always here, whenever they were created) | | Queued jobs → `interrupted`. Running jobs get `Grace` (default 10s) to finish normally |
| 5. Grace over | | Contexts are cancelled (cause `ErrShuttingDown`); jobs become `interrupted` when their handlers return (waited for up to 5s) |
| 6. Dependency closers | | Databases, Redis, ... close after the work that used them |

Things to get right:

- **Where you create a queue or first call `background.Go` does not matter**, at startup or on first use inside a request handler: their closers are registered with `graceful.AddWorkCloser`, which runs them after the server has stopped serving and before the dependency closers. Dependencies that the work uses must be initialized before `gs.Start()` (as `config.Init` does), or they are closed first.
- **Budget.** `terminationGracePeriodSeconds` must cover `PRE_STOP` + request drain + `Grace` + 5s. `GRACEFUL_TIMEOUT` bounds everything; when it expires, waiting stops.
- **Watch streams never hold the shutdown**: they end at step 2. A client that wants the final state calls `Watch` again; with the in-memory store and one replica it will find the job `interrupted` only if it reconnects to the same process before it exits, otherwise `NOT_FOUND` after the restart.
- A second SIGTERM kills the process immediately, as always.

In a process without a server (a worker that calls `graceful.Start` itself), step 2 happens at step 1.

`grpcmux.Draining(ctx)` is the per-call view of the same moment for your own stream handlers; `graceful.Draining()` is the process-wide one for code without a request context.

## What survives a restart

**By default nothing does.** Jobs live in process memory (`MemoryStore`):

- A restart, a crash or a rollout loses every job, including finished ones and the idempotency keys. Clients get `NOT_FOUND`.
- Finished jobs are kept for `Retention` (default 1h) but at most 10,000 of them (`DefaultMaxDone`; `NewMemoryStoreSize(n)` changes it): beyond that the ones that finished first are dropped early, with their keys, so memory stays bounded under a high job rate. Unfinished jobs are never dropped.
- Each replica has its own jobs. `Get`/`Watch`/`Cancel` for a job answer `NOT_FOUND` on any replica but the one that accepted it; behind a load balancer that is every second call with two replicas. Use the in-memory default with **one replica**, or with sticky routing, or for jobs nobody looks up by ID.
- The same key submitted to two replicas creates two jobs.

That is acceptable for work that is cheap to lose and easy to start again (a preview, a cache warm-up, an export the user can click again). When it is not, give the queue a durable `Store`.

### The storage seam

```go
type Store interface {
	Create(ctx context.Context, job Job) (stored Job, created bool, err error) // atomic per (kind, key)
	Update(ctx context.Context, job Job, prevRevision int64) error             // compare-and-swap on Revision
	Get(ctx context.Context, id string) (Job, error)
	ListRecoverable(ctx context.Context, staleBefore time.Time, limit int) ([]Job, error)
	DeleteDone(ctx context.Context, before time.Time) (int, error)
}
```

One table or collection is enough: the `Job` struct (it has JSON tags) keyed by `id`, a unique index on `(kind, key)` for non-empty keys, and indexes on `state` + `heartbeat_at` and on `finished_at`. `background/storetest` checks an implementation against the contract; run it against the real database:

```go
func TestJobStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) background.Store { return newJobStore(t) })
}
```

The library does **not** ship a database-backed store yet: the interface and the conformance test are there, an implementation over `database.Database` belongs with the data adapters and needs to be tested against real MySQL, PostgreSQL and MongoDB.

What a durable store gives you:

| | In-memory | Durable store |
|---|---|---|
| Finished jobs, idempotency keys after a restart | lost | kept for `Retention` |
| `Get`/`Watch` on another replica | `NOT_FOUND` | works (`Watch` polls the store once per second) |
| `Cancel` on another replica | `NOT_FOUND` | `FAILED_PRECONDITION` (`ErrNotOwner`): route the call to the owner, or let the job finish |
| Jobs running or queued when the process stopped | lost | failed or queued again, see below |
| One backlog shared by all replicas | no | **no** — a job still runs in the process that accepted it |

### Recovery

Set `QueueOptions.LeaseTTL` (for example 30s) together with a durable store. The queue then writes a heartbeat for its jobs every `LeaseTTL/3`, and every `LeaseTTL` takes over jobs that no live process owns: jobs in state `interrupted` (a clean shutdown) and jobs `queued` or `running` whose heartbeat is older than `LeaseTTL` (a crash). What happens to such a job is the handler's choice:

| `HandlerOptions.OnRestart` | Effect |
|---|---|
| `RestartFail` (default) | The job becomes `failed` with an error that says it was interrupted. Safe for handlers that must not run twice. |
| `RestartRequeue` | The job is queued again in the process that found it and starts **from the beginning** with the same payload. `Job.Attempt` starts again at 1, so the handler has its full `MaxAttempts` after every restart; `Job.Restarts` counts the restarts, and after `MaxRestarts` (3) the job fails, so a job that crashes the process cannot do so forever. |

The queue does not checkpoint. A handler that wants to resume in the middle stores its own position (keyed by `Job.ID`) and reads it when it starts.

If the store cannot be written when a job finishes, the queue keeps the job in memory with its true final state (so `Get`, `Watch` and key deduplication on that process stay correct), logs an error, and tries again on every maintenance tick until the write succeeds or the retention period is over; with a lease, those retries are also what keeps another replica from taking the finished job over. During shutdown every store write is bounded by the closer's deadline (2s per write, none after the deadline); `Shutdown` then returns an error naming how many final states the store does not have, and those jobs are left to the lease recovery of the next process.

If a process is wrongly considered dead (it stalled for longer than `LeaseTTL`) and another replica took its job, the first process notices at its next write, cancels its handler (cause `ErrConflict`) and lets go. For a moment both ran: at-least-once, not exactly-once.

## Observability

Metrics (default registry, served on the metrics port):

| Metric | Labels |
|---|---|
| `background_task_runs_total` | `name`, `result` = `ok` \| `error` \| `panic` \| `skipped` |
| `background_task_duration_seconds` | `name` |
| `background_tasks_running` | `name` |
| `background_panics_recovered_total` | `name` (task name or job kind) |
| `background_jobs_submitted_total` | `queue`, `kind`, `result` = `accepted` \| `duplicate` \| `rejected_full` \| `rejected_shutdown` \| `error` |
| `background_jobs_finished_total` | `queue`, `kind`, `state` |
| `background_job_retries_total` | `queue`, `kind` |
| `background_job_duration_seconds` | `queue`, `kind` (one attempt) |
| `background_jobs_queued`, `background_jobs_running` | `queue` |

Task names, job kinds and queue names are label values: keep them static. Never put an ID in a name. Two queues with the same `Name` share their series: the counters and the `queued`/`running` gauges are the sums over both.

Logs use the actions `background.Task`, `background.Job`, `background.Every`, `background.Store`, `background.Recover` and `background.Shutdown`, with the fields `task`, or `queue`, `job_id`, `job_kind`, `state`, `attempt`, and `error` (plus `stack` for a panic). Inside a handler, `log.Extract(ctx)` is tagged with `job_id` and `job_kind`.

Traces: every task run and job attempt is a root span (`background <name>`, `background job <kind>`) linked to the span of the request that started it. Jobs recovered after a restart have no link.

Alert suggestions: `rate(background_panics_recovered_total[5m]) > 0`, `background_jobs_queued` close to the backlog, `increase(background_jobs_finished_total{state="interrupted"}[1h])` outside deployments.

## Testing

`RunnerOptions.Standalone` and `QueueOptions.Standalone` detach an instance from package `graceful`; the test calls `Shutdown` itself. The package's own tests use `testing/synctest` for everything that involves time (retries, grace periods, intervals), which also proves that no goroutine is left behind: a synctest bubble fails if one is.

```go
func TestExport(t *testing.T) {
	q := background.NewQueue(background.QueueOptions{Standalone: true})
	defer func() { _ = q.Shutdown(context.Background()) }()
	// q.Handle(...); q.Submit(...); for job, err := range q.Watch(...) { ... }
}
```

## When this is the wrong tool

The queue is not a workflow engine and not a distributed queue. A job runs in the process that accepted it, work is not rebalanced between replicas, and a job starts from the beginning when it is retried or recovered.

| Situation | Use instead |
|---|---|
| The job must not be lost when the pod dies, and cannot simply be run again from the start; multi-step processes with waits, signals, compensation; human-in-the-loop | A durable workflow engine (Temporal) |
| The job runs longer than a rollout can wait (`terminationGracePeriodSeconds`), or needs resources the API server does not have (GPU, a lot of memory or CPU) | A Kubernetes Job (or a dedicated worker Deployment) started by the API server |
| Many replicas must share one backlog; producers and consumers scale separately; strict ordering; high volume (more than a few jobs per second, sustained) | A broker (`dependencies/broker`, Kafka) or a database-backed queue |
| Work must happen exactly once | None of the above alone: make the work idempotent |
| A schedule that must fire even if no replica is up at that moment, or cron expressions | A Kubernetes CronJob |

Rules of thumb for staying here: the job finishes in seconds to a few minutes (well inside the termination grace period), losing it is tolerable or it can be requeued from the start, and one process can carry the load.
