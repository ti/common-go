# Upgrade note: background work, `graceful` and `async`

Applies to the release that adds package `background` (branch `feat/platform-capabilities`). Nothing here breaks the build of an existing consumer. Guide: [background-work.md](../guides/background-work.md).

## 1. Behavior changes for existing code

| Package | Change | What to do |
|---|---|---|
| `graceful` | A closer that **panics** is recovered and logged (`graceful.Closer`, with the stack); the remaining closers still run. Before, the panic crashed the process in the middle of the shutdown and skipped them. | Nothing. If you relied on the crash for a non-zero exit code, return an error and check your logs instead. |
| `graceful` | The shared deadline (`GRACEFUL_TIMEOUT`, default 6m) is now **enforced**. A closer that is still running 2 seconds after it is abandoned with an error log (its goroutine keeps running until the process exits), and **every closer that starts after the deadline has passed gets only those 2 seconds**, with an already expired context. Before, each closer ran to completion however long it took, so one stuck closer blocked the others until the kubelet's SIGKILL, and a slow but finite closer late in the list (flushing a buffer, closing a pool) always finished. | Make closers honor their context. Check the sum: `PRE_STOP` + request drain + background grace + what your dependency closers need must fit into `GRACEFUL_TIMEOUT`; raise it (and `terminationGracePeriodSeconds`) if a late closer legitimately needs more than 2s. |
| `graceful` | Closers now run in their own goroutine (one at a time, still last-in first-out). | Nothing, unless a closer depended on running on the goroutine that called `graceful.Start` (for example `runtime.LockOSThread` state). |
| `graceful` | `Close()` now stops **every** running `Start`/`Run` (it woke only one). A `Close()` that finds none running is handled by lifecycle: before the **first** `Start` of the process it is remembered and that `Start` shuts down at once (as before); **after some `Start` has already shut down it is dropped**. Before, such a late `Close` stayed pending and made the next `Start` in the process return immediately (servers that "never became ready" in suites that start several servers). The price: from the second lifecycle on, a `Close()` issued after `go gs.Start()` but before that `Start` has actually begun is lost, and that `Start` **keeps running** until another shutdown request. Production processes have one lifecycle and are not affected. | In tests that start more than one server per process: call `graceful.Close()` only after the server is known to run (wait for `/readyz`, as `examples/internal/app` does), or stop it by cancelling the context it was created with. Do not write `go gs.Start(); defer graceful.Close()` without waiting. Helpers that called `Close()` twice to stop two servers keep working. |
| `graceful` | With several `Start`/`Run` calls shutting down at once (two servers in one process), the second now **waits until the first has run the closers** before it returns; before, it returned at once because the closer list was already taken. | Nothing. |
| `graceful` | New process-wide drain signal: `Draining()`, `IsDraining()`, `Drain()`, `DeferDrain()`. `graceful.Start` closes `Draining()` when shutdown is requested. A server calls `release := graceful.DeferDrain()` right after registering its closer and releases it when it stops accepting (with several servers the signal closes when the last one does). The deferral is also released when that closer has returned, panicked or been abandoned, so background work is always closed before the dependencies. | Nothing. Optional: long-lived code without a request context can select on `graceful.Draining()`. |
| `graceful` | **`Drain()` called by your own code closes the signal immediately**, also in a process with a grpcmux server that deferred it: background queues stop accepting and watch streams end right then, while the server keeps serving until its own shutdown. Only a `Drain()` issued from inside the closer that deferred the signal (what `grpcmux.Server.Close` does during a shutdown) counts as that server's release; repeating it changes nothing. | Call `Drain()` yourself only when you mean "this process takes no new background work from now on". |
| `graceful` | New `AddWorkCloser`: closers of background work, run after the process has drained and the server's closer has returned, before the dependency closers, regardless of registration order. | Nothing; `background` uses it. |
| `graceful` | A closer registered with `AddCloser` **while the shutdown is running** is now run by that shutdown, after the closers registered before it. Before, it was never run and stayed registered for a later `Start` in the same process. | Nothing. |
| `async` | `Future.Async` with a function that returns a **nil pointer and a nil error** no longer panics inside the errgroup goroutine (`reflect: call of reflect.Value.Set on zero Value`, which crashed the process); the pre-allocated result keeps its zero value. | Nothing. Note that the returned pointer is never nil; a "not found" result is the zero value. |
| `async` | `Future.Await` is documented as it behaves: it waits for every function and does not return early on context cancellation. No code change. | Nothing. |

## 2. New

- `async.Future.Limit(n)`: bounds the number of functions a Future runs at once (`errgroup.SetLimit`).
- Package `background`: `Go`, `Loop`, `Every` (with `Locker`), `Queue` (jobs with status, progress, watch, cancel, retries, timeouts, idempotent submission), `Store` + `MemoryStore`, and `background/storetest`.
- New Prometheus metrics, all prefixed `background_` (listed in the guide). They are registered on the default registry the first time the package is used.

## 3. Adopting it

Replace, in this order of value:

1. **Bare `go func()` in request handlers and at startup** with `background.Go(ctx, "name", fn)`. You gain panic recovery, a context that is cancelled at shutdown, and shutdown that waits. Hand-written `safego`-style helpers can be deleted. Use `background.Loop` for functions that run until the process stops, otherwise every shutdown waits the whole grace period (10s) for them.
2. **`time.NewTicker` loops** with `background.Every(name, interval, fn)`. Note the differences: intervals get ±10% jitter (`WithJitter(0)` to disable), and the context is cancelled at shutdown.
3. **Channel-plus-worker code with a status document** (the `videoJobs` pattern) with a `background.Queue`: `Submit` in the RPC, `Watch` in a server-streaming RPC. Keep your own status document only if you need to query jobs by something other than their ID.

Wiring: none. A queue or runner may be created anywhere, including on first use inside a request handler (`background.Go` creates the process-wide runner lazily). Its closer is registered with `graceful.AddWorkCloser`, which always runs after the server has stopped serving and before the dependency closers, so background work keeps being accepted during `PRE_STOP` and still finds its database open while it finishes. The one thing to keep: dependencies used by background work must be registered with `graceful.AddCloser` *before* the server starts (which `config.Init` does); a dependency created lazily after `gs.Start()` is closed before the server and therefore before the work.

Kubernetes: `terminationGracePeriodSeconds` must now also cover the background grace period: `PRE_STOP` + request drain + `Grace` (default 10s) + 5s.

## 4. What it is not

In-memory by default: jobs are lost on restart and are local to one replica. Read "What survives a restart" and "When this is the wrong tool" in the guide before using the queue for anything a user would miss. The library ships the `Store` interface and its conformance test, not a database-backed store.
