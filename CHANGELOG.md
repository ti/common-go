# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

There are no version tags yet, so nothing is "released": consumers pin a commit. Each section therefore names the commits it covers (first and last, both included, in history order on `main`) and their commit dates; `git log --reverse <first>^..<last>` lists them. Sections are newest first. To upgrade, find the section that contains your pinned commit and read it and every section above it. An entry that changes behaviour for a consumer ends with what to do about it.

The four sections dated 2026-11 together are the 2026-Q4 modernization. Its upgrade guide is [docs/upgrades/2026-Q4.md](docs/upgrades/2026-Q4.md), with one note per area: [server-platform.md](docs/upgrades/server-platform.md), [rest-and-rate-limiting.md](docs/upgrades/rest-and-rate-limiting.md), [2026-Q4-storage.md](docs/upgrades/2026-Q4-storage.md) and [background-work.md](docs/upgrades/background-work.md). Numbers such as "B6" or "S8" below are row numbers in the note that is linked next to them.

| Section | Commits | Dates |
|---|---|---|
| [Platform capabilities](#platform-capabilities-af101ba-to-fd97b61-2026-11-18-to-2026-11-19) | `af101ba` to `fd97b61` | 2026-11-18 to 2026-11-19 |
| [OIDC relying party](#oidc-relying-party-1247dd8-to-80d3e63-2026-11-06-to-2026-11-16) | `1247dd8` to `80d3e63` | 2026-11-06 to 2026-11-16 |
| [2026-Q4 modernization](#2026-q4-modernization-d1943b1-to-299d0fc-2026-11-05) | `d1943b1` to `299d0fc` | 2026-11-05 |
| [Go 1.27 and request tracing](#go-127-and-request-tracing-6c0a7ab-to-74162f8-2026-07-12-to-2026-08-03) | `6c0a7ab` to `74162f8` | 2026-07-12 to 2026-08-03 |
| [Go 1.26 cleanup](#go-126-cleanup-3b6ceaa-to-e005efd-2026-06-08) | `3b6ceaa` to `e005efd` | 2026-06-08 |
| [ConnectRPC, CORS, mongo-driver v2](#connectrpc-cors-mongo-driver-v2-0e201fa-to-4b40ff4-2026-03-03-to-2026-04-24) | `0e201fa` to `4b40ff4` | 2026-03-03 to 2026-04-24 |
| [Mock database and camelCase JSON](#mock-database-and-camelcase-json-4a9b6a4-to-bba6d1a-2026-01-30-to-2026-02-02) | `4a9b6a4` to `bba6d1a` | 2026-01-30 to 2026-02-02 |
| [Initial publication](#initial-publication-fdfddd6-to-41d53e1-2025-01-08-to-2025-07-04) | `fdfddd6` to `41d53e1` | 2025-01-08 to 2025-07-04 |

## Platform capabilities (`af101ba` to `fd97b61`, 2026-11-18 to 2026-11-19)

Unreleased. Commits: `af101ba` (the feature commit), `1d615f6`, `35133b6`, `443a83f`, `fd97b61`. Nothing here breaks the build of an existing consumer; the entries under Security, Changed and Fixed that change behaviour say what to do.

### Security

- The toolchain is pinned to go1.27.4 (`443a83f`). go1.27.3 carries GO-2026-6859: an HTTP/1 server that uses `MaxBytesReader` or `MaxBytesHandler` can crash when its handler reads the body while writing the response header. Build consumers with Go 1.27.4 or newer.
- **A signed-in request could answer `Internal` from the metrics interceptor** (`fd97b61`). The Prometheus exemplar was built from every request-metadata field (`client_id`, `user_id`, `device_id`, `request_id`) plus the trace id, with no regard for the client library's limit of 128 runes over all label names and values; `AddWithExemplar` panics beyond it, and also on a value that is not valid UTF-8. Four ordinary identifiers and a trace id already exceed the limit, and `device_id` / `request_id` come from request headers, so any caller could trigger it with one long header. The exemplar is now bounded (`grpcmux/exemplar.go`): the trace id first, then each field only if it fits whole; oversized and invalid values are left out. Server and client interceptors share the rule. Nothing to do but bump; it affects every server with a metrics address since exemplars exist, most visibly with tracing on.
- `/debug/**` served `http.DefaultServeMux` (pprof, expvar) on the HTTP port of every server, without authentication when there was no AuthFunc or one that admits everyone (guest mode). It is now off unless `WithDebugHandlers(true)` / `debugHandlers: true`. A service that uses `/debug/pprof` through the HTTP port enables it, after checking that its AuthFunc rejects strangers ([server-platform.md](docs/upgrades/server-platform.md) S8).
- `dependencies/sql`: identifiers taken from the caller (table names, condition, update, sort and select keys) were pasted into the statement between backticks, and the project of a `GetDatabase` view, the key of `GetCounter` and the value of a `-key[*].sub` update were pasted in as text, so a name or project that came from a request could inject SQL. Identifiers must now match a strict grammar (letters, digits, underscores, the documented JSON key forms, `schema.table`) or the call fails with `InvalidArgument` before a statement is built; the three values are bound parameters. Rename columns or tables that use other characters, or query them through `(*sql.SQL).QueryContext`; keep validating request-supplied field names against an allow-list ([2026-Q4-storage.md](docs/upgrades/2026-Q4-storage.md) S10).
- gRPC port: validation ran before authentication, so an unauthenticated caller learned whether its request was valid. Auth and authz now run first. Update tests that expected `INVALID_ARGUMENT` from an unauthenticated invalid call; they get `UNAUTHENTICATED` ([server-platform.md](docs/upgrades/server-platform.md) S1).
- `X-HTTP-Method-Override` could turn any request, including a GET, into any method. It is now honored on POST only and for GET/PUT/PATCH/DELETE only, matched case-insensitively. Clients that tunnel through POST are unaffected; `WithoutMethodOverride()` turns it off.
- Every HEAD request was answered with an empty `200` by the CORS step, whatever the path, without authentication or a log line (see Changed for what a HEAD answers now).
- `redis.RateLimitN`'s fallback while Redis is down refilled "period in seconds" tokens per second instead of `limit` per `period` (1000 per 10 minutes became 600 per second), so an outage lifted the limits in practice. It now enforces the configured limit. Nothing to do.

### Changed

Behaviour of existing code, without opting in to anything.

- Interceptor order on the gRPC port is: rate-limit rules keyed by IP (`WithRateLimit`), auth, authz, validation (protovalidate, then generated `Validate()`), metrics, access log, `WithLimiter`, rate-limit rules keyed by subject, drain hook, deadlines (`WithTimeouts`, unary only), custom interceptors, recovery. Validation used to come first.
- Streams on `gs.ConnectServer()` (Connect, gRPC-Web, gRPC on the HTTP port) ran no interceptors: no protovalidate, no `grpc_server_*` metrics, no custom stream interceptors. They now run the same chain as on the gRPC port, and `grpc.SetHeader`/`SetTrailer` in connect-go unary handlers reach the client (they were dropped). Stream requests over Connect / gRPC-Web are now validated and counted in `grpc_server_*`; check that custom stream interceptors do not assume a real gRPC transport (no TLS `AuthInfo`). `WithoutHTTPInterceptors()` / `disableHTTPInterceptors: true` turns the chain off for unary calls and streams alike ([server-platform.md](docs/upgrades/server-platform.md) S2, S3).
- A HEAD is served as the GET it mirrors, without a body: the GET's status and headers on gateway, REST and `Handle` routes and on `/healthz`, `/readyz`, `/favicon.ico`; the auth error on a protected route; what an unrouted GET answers on an unrouted path; `405` with `Allow` on RPC paths. Handlers see `GET`; `mux.IsHead(r)` tells the difference, and the access log still says `HEAD`. Monitors that sent HEAD to a protected route without credentials now get `401`, and `405` on an RPC path: point them at `/healthz` or `/readyz` ([table](docs/upgrades/server-platform.md), S13).
- `grpc.health.v1`: a service name that is not registered answers `NOT_FOUND` from `Check` and `SERVICE_UNKNOWN` from `Watch` (every name answered `SERVING`); `List` returns the empty name and each registered service (it returned one entry, `*`); an open `Watch` stream ends with `UNAVAILABLE` when the server drains instead of holding the shutdown until the deadline. Kubernetes `grpc` probes without `service:` are unaffected; a probe or client that sets a service name must use a registered one or the empty name ([server-platform.md](docs/upgrades/server-platform.md) S4).
- CORS: the allowed request headers include `X-Grpc-Web`, `X-User-Agent` and the Connect/gRPC encoding headers, and `Access-Control-Expose-Headers` is always sent for allowed origins with a built-in list (`Grpc-Status`, `Grpc-Message`, `Grpc-Status-Details-Bin`, the encoding headers, `X-Request-Id`), so cross-origin gRPC-Web clients work. `CORSConfig.ExposeHeaders` adds to that list (it used to be the whole list), and `CORSConfig` has YAML tags (`disabled`, `allowedOrigins`, `allowedHeaders`, `exposeHeaders`). Nothing to do.
- Access log: a request rejected by the AuthFunc is logged with the HTTP status the client received (`403` for `PERMISSION_DENIED`, ...) plus `code` and `error`; the line always said `status: 401` and had no error. Update alerts that counted `status=401` as "all auth failures" ([server-platform.md](docs/upgrades/server-platform.md) S6).
- Tracing (`tracing: true`): no server spans for `/healthz`, `/readyz`, `/favicon.ico` and `grpc.health.v1.Health`, and request log lines on the HTTP and gRPC paths gain `trace_id` and `span_id` when a TracerProvider is installed. If you inject `trace_id` / `span_id` into the request logger yourself, delete that code when you bump: both would write the keys and the JSON line would contain them twice ([server-platform.md](docs/upgrades/server-platform.md) S10, 3.5).
- `config.Init` live reload works on a private copy of the config (see Fixed). A reload assigns only the top-level fields whose value changed, each as a new value, so a map, slice or pointer you kept from the struct is not updated in place: read it from the struct again, under the new `config.RLock()` / `config.RUnlock()`. Dependency fields are never touched by a reload. `config.Binder()` is bound to the private copy: its hooks keep working, but `Binder().Save` writes the copy, not edits you made to your struct ([server-platform.md](docs/upgrades/server-platform.md) S15).
- `log.SetLevel` trims surrounding whitespace and accepts `warning` for `warn`; `log.level` in the config is optional (see Fixed). Workarounds that pre-set `Log.Level` before `config.Init` can go.
- `graceful`, all in [background-work.md](docs/upgrades/background-work.md):
  - The shared deadline (`GRACEFUL_TIMEOUT`, default 6m) is enforced. A closer still running 2s after it is abandoned with an error log, and every closer that starts after the deadline gets only those 2s with an expired context. Before, each closer ran to completion. Make closers honor their context, and check that `PRE_STOP` + request drain + background grace + what the dependency closers need fits into `GRACEFUL_TIMEOUT`; raise it (and `terminationGracePeriodSeconds`) if a late closer legitimately needs more.
  - A panicking closer is recovered and logged and the remaining closers run. If you relied on the crash for a non-zero exit code, return an error instead.
  - Closers run in their own goroutine (one at a time, still last-in first-out). Matters only to a closer that depended on running on the goroutine that called `graceful.Start`.
  - A closer registered with `AddCloser` while the shutdown is running is run by that shutdown (it was never run).
  - `Close()` stops every running `Start`/`Run` (it woke only one), and a `Close()` issued after some `Start` has already shut down and while none is running is dropped (it stayed pending and made the next `Start` return immediately). With several `Start`s shutting down at once, the later ones wait until the closers have run. In tests that start more than one server per process, call `graceful.Close()` only after the server is known to run (wait for `/readyz`) or cancel its context; `go gs.Start(); defer graceful.Close()` without waiting can lose the `Close`. Production processes have one lifecycle and are not affected.
- `dependencies/sql` and `dependencies/mongo` behaviour, all in [2026-Q4-storage.md](docs/upgrades/2026-Q4-storage.md):
  - SQL times are written with microseconds, and on PostgreSQL with the UTC offset of the `loc` zone; conditions, `database.D` updates and `sql.TransformSQLArgs` format times the same way. A column without fractional precision (`datetime`, `timestamp(0)`) now rounds where it received a truncated value; truncate in the application if that matters (S2, S12).
  - Error codes: MySQL duplicate key is `AlreadyExists` (was `Unknown`); deadlock, lock-wait timeout, serialization failure and MongoDB write conflicts are `Aborted`; a context deadline is `DeadlineExceeded`; the SQL `NotFound` message is "record not found" (was empty); `StartTransaction` failures are gRPC status errors (they were raw driver errors, so `errors.Is` against a driver error no longer matches). Replace checks on MySQL error text or `codes.Unknown` with `database.IsAlreadyExists` / `database.IsConflict` (S5).
  - SQL `Update`, `UpdateOne` and `Delete` with no condition and no project return `InvalidArgument`, as do `Update`, `UpdateOne` and `ReplaceOne` with no column to set, and `In`/`Nin` with a value that is not a slice (it panicked). All of these were engine syntax errors before. An empty `In` is false and an empty `Nin` true. To delete everything on SQL, pass a condition that is always true or run the statement yourself (S6, S11).
  - MongoDB transactions are real and need a replica set or mongos; on a standalone server the first operation in a transaction fails (S4).
  - Mock: `Eq`, `Ne`, `In` compare times as instants, numbers by value and named types like their underlying type; `Lt`/`Gt` and sorting work for times, bools and mixed numeric types; `Rollback` restores the whole mock to its state at `StartTransaction`. Tests that passed because a condition silently did not match may now see the rows (S7, S8).
- `tools/routerlimit`: a request limited by `routerlimit.NewHandler` now carries `Retry-After`. Rules, keys and fixed-window semantics are unchanged.
- `grpcmux.Server.Start` logs a server that failed to start or serve at error level (was warn). It still exits 0; use the new `Run()` to exit non-zero.
- Module graph: `go.opentelemetry.io/otel/sdk` and the OTLP trace exporters (gRPC and HTTP) are new dependencies of the library module, linked into a binary only if it imports `github.com/ti/common-go/tracing`. Run `go mod tidy`.
- New Prometheus collectors on the default registry: `db_client_operation_duration_seconds` the first time a `database.Repo` is used, the `background_*` series the first time package `background` is used, and `ratelimit_requests_total` / `ratelimit_store_errors_total` when a limiter is configured. Nothing to do unless a metric of the same name is already registered with other labels.

### Fixed

- `config.Init` data race (`1d615f6`, found by `go test -race` in a consumer): the file watcher was given the caller's struct, so it re-read and rewrote the whole struct, dependency fields included, while `Init` was still setting them and later while handlers used them (it also JSON-encoded and deep-compared the live dependency clients on every change). The watcher now works on a private copy. It never touches the caller's struct before `Init` returns, never reads or writes dependency fields, and on a change assigns only the top-level fields that changed, as fresh values, under a lock that the new `config.RLock()` / `config.RUnlock()` take for readers of hot-reloaded fields. `Init` now returns an error for a config that is not a pointer to a struct (it panicked).
- REST `PATCH` (`35133b6`): a JSON body member the message does not declare is ignored and left out of the derived update mask, at any depth, instead of failing the request with 400 (also when an explicit `update_mask` was given). Unknown members were already ignored in every other JSON body, so a newer client can now `PATCH` an older server.
- `log.SetLevel` panicked on an empty or unknown level name, so `config.Init` crashed at startup when the config struct had a `Log` field and the file no `log.level`, and a typo in a hot-reloaded level killed the process. The level is now optional; an unknown name is an error from `config.Init` and a logged no-op on reload.
- `config.Init` with a `Log` field that has no string `Level` panicked; it is now ignored. `Log` may be a pointer (left nil when the source has no `log` key) and `Level` a named string type; both are applied at startup and on reload.
- With `logBody`, POST/PATCH bodies over about 1 MB on gateway/REST routes were truncated before the handler read them; the handler now always gets the whole body.
- `graceful`: a panicking closer crashed the process mid-shutdown and skipped the remaining closers; a closer that ignored its context blocked the others forever. Panics are now recovered and logged, and a closer still running 2s after the shared deadline is abandoned so that the rest run.
- `graceful.Close()` called after a `Start` had already shut down stayed pending and made the next `Start` in the process exit immediately; with two `Start`s running, one `Close` stopped only one of them. `Close` now wakes every running `Start` and is ignored when none is running after a shutdown.
- `async.Future.Async` crashed the process (`reflect.Value.Set on zero Value` in the errgroup goroutine) when the function returned a nil pointer with a nil error; the result now keeps its zero value. The returned pointer is never nil, so a "not found" result is the zero value.
- Rate limiting ([docs/upgrades/rest-and-rate-limiting.md](docs/upgrades/rest-and-rate-limiting.md)): `redis.RateLimitN`'s fallback had a data race (and the wrong refill rate, see Security); `routerlimit`'s default in-memory store admitted more than the quota under concurrency and never dropped a key; `redisrate.Limiter` divided by zero for periods below one second; `ratelimit.RateLimiter.Allow` was not atomic.
- Storage layer, verified against PostgreSQL 17, MySQL 8.4 and MongoDB 8 ([docs/upgrades/2026-Q4-storage.md](docs/upgrades/2026-Q4-storage.md)):
  - `dependencies/mongo`: `Lt`/`Lte`/`Gt`/`Gte` conditions were silently treated as equality, so queries that use them now return the intended rows, and workarounds that put raw operators in the value (`bson.M{"$gt": x}`) keep working and can be simplified; transactions never started (writes were applied immediately, `Commit`/`Rollback` failed); `Commit` and `Rollback` use a context detached from the request, so rolling back after a cancelled request reaches the server; `Find` accepts `*[]T`.
  - `dependencies/sql`: times were truncated to whole seconds on write; reads on a transaction handle ran outside the transaction, and reading a time column there panicked; `Exist` needed an `_id` column; reads without a condition were a syntax error; bulk `Replace` ignored its index keys; MySQL duplicate keys were `Unknown` instead of `AlreadyExists`; MySQL times with fractional seconds read without `parseTime` were silently zero; `Find` accepts `*[]T` and a wrong result type is `InvalidArgument` instead of a panic.
  - Mock: `Find` rejected `*[]*T`; times and mixed numeric types did not compare; `Rollback` kept the writes; a stored nil panicked on read.
  - `query.PageQuery`/`StreamQuery` rejected a `*database.DB` dependency field.

### Added

- `grpcmux.Server.Run()` and `graceful.Run()`: like `Start`, but return the error of the server that failed to start or serve, so `main` can exit non-zero. With `Start` a process whose listener could not bind shut down and exited 0.
- Drain hook ([guide](docs/guides/server.md#long-lived-streams-and-the-drain), [upgrade note](docs/upgrades/server-platform.md)): `grpcmux.Draining(ctx)` / `gs.Draining()` signal when the server begins to drain (after `PRE_STOP`), and `WithDrainStreams(grace)` / `drainStreams`, `drainStreamsGrace` ends open streams with `UNAVAILABLE "server is draining"` (`grpcmux.ErrServerDraining`) as a regular end of stream on gRPC, Connect and gRPC-Web, instead of holding the shutdown until `GRACEFUL_TIMEOUT`. `graceful.Draining()` fires at the same moment. Off by default: without it, open streams still hold the shutdown.
- `grpcmux.RegisterConnectStreams`: streaming methods on the HTTP port (Connect, gRPC-Web, gRPC) from the generated gRPC handlers, without connect-go codegen.
- `WithReflection()` / `reflection`: gRPC server reflection (v1 and v1alpha) on the gRPC port and through the HTTP port; authenticated like any method, `ReflectionMethods` to exempt it.
- `WithNoAuthMethods` / `noAuthMethods`: exact and whole-service auth exemptions (`/pkg.Service/Method`, `/pkg.Service/*`); `mux.MatchPath`. `WithNoAuthPrefixes` keeps its prefix semantics; review existing prefixes for methods they exempt by accident ([server-platform.md](docs/upgrades/server-platform.md) 3.2).
- `WithProtocolErrors()` / `protocolErrors`: auth failures and recovered panics on Connect streams, gRPC and gRPC-Web calls are answered in that protocol instead of an HTTP 401 JSON body.
- `WithoutQueryAccessToken()`, `WithoutMethodOverride()`, `WithDebugHandlers(bool)` and their config keys (`disableQueryAccessToken`, `disableMethodOverride`, `debugHandlers`).
- Package `tracing` ([guide](docs/guides/tracing.md)): `tracing.Init` installs a TracerProvider with an OTLP exporter (gRPC or HTTP), sampler and resource from the standard `OTEL_*` environment, drops health-probe spans and flushes on shutdown; `tracing.Enabled`, `tracing.IsProbeSpan`. With `tracing: true` request log lines carry `trace_id` and `span_id` on the HTTP and gRPC paths (`mux.TraceFields`), and probes create no spans.
- `log.TrySetLevel`, `log.ParseLevel`, `log.Level`, `log.SetOutput`.
- REST from annotations ([guide](docs/guides/rest.md), [upgrade note](docs/upgrades/rest-and-rate-limiting.md)): `grpcmux.WithREST()` / `rest: true` serves every registered method that has a `google.api.http` annotation as a REST route, built from the descriptors at registration: no generated gateway code, no per-service call. Path, query, body, `response_body`, PATCH field masks and `additional_bindings` as in grpc-gateway; the HTTP bridges' interceptor chain; auth exemptions, rate limits and the log action by RPC name; server streaming as newline-delimited JSON. Opting in changes three things on that server: an unrouted path answers HTTP 404 (it answers 200 with a `not_found` body otherwise), so point monitors that expect 200 from `/` at `/healthz`; error `details` are typed on the whole HTTP port, so tell published REST clients that decode `details` strictly; the access log's `action` of a REST call is the RPC's full method instead of the first two path segments. The examples generate their OpenAPI document with pinned buf plugins, in the server's JSON naming, and test it against the served routes.
- `grpcmux.WithTypedErrorDetails()` / `typedErrorDetails` (and `mux.WithTypedErrorDetails`): each `details` entry of a JSON error body carries `"@type"`, so a client can find the `google.rpc.ErrorInfo` among them as it can in a Connect error. Off by default: the default body is unchanged, byte for byte. Implied by `WithREST`.
- Declarative rate limiting ([guide](docs/guides/rate-limiting.md)): `grpcmux.WithRateLimit` / `rateLimit` with a default and per-method or per-service rules (rate, burst), keyed by authenticated subject with fallback to the client IP (`WithRateLimitSubject` for another identity), or by IP before authentication for public methods; token buckets (GCRA) in memory or, with `WithRateLimitStore(redis.RateLimiter())`, in Redis so limits hold across replicas (falling back to memory during an outage). A limited call is `RESOURCE_EXHAUSTED` with `google.rpc.RetryInfo` and `ErrorInfo` details in each protocol's own form (HTTP 429 + `Retry-After` for REST and Connect unary), counted once per request and once per stream; metrics `ratelimit_requests_total` and `ratelimit_store_errors_total`. The Redis script is one file (`tools/ratelimit/gcra.lua`) and the algorithm is pinned by language-neutral cases (`tools/ratelimit/testdata/gcra_cases.json`), for backends in other languages. The client IP is the connection's peer unless `trustedProxies` names the proxies whose `X-Forwarded-For` is believed (`tools/ip.TrustedProxies`, `ParseTrustedProxies`; nobody by default), and IPv6 clients are limited per /64 (`ipv6Prefix`). Behind an L7 proxy, set `trustedProxies` or all clients share one bucket. `WithLimiter` / `routerlimit` is independent and not deprecated; do not run both on the same traffic.
- `grpcmux.WithTimeouts` / `timeouts`: server-side deadlines for unary calls per method or service on every protocol; streams are never bounded.
- `mux.WithRouteResolver`, `mux.WithPreAuth`, `mux.WithNotFoundStatus`: the hooks grpcmux uses for REST, rate limiting and the 404.
- `ratelimit.RateLimiter.Stop()` ends its eviction goroutine; call it on limiters that are created and dropped at run time.
- Package `background` for work that outlives the request ([guide](docs/guides/background-work.md), [upgrade note](docs/upgrades/background-work.md)): `Go` (supervised goroutine: panic recovery with stack and metric, context detached from the request and cancelled at shutdown, trace link to the request), `Loop` (runs until shutdown, restarted with backoff), `Every` (jitter, no overlap, optional single runner across replicas through a `Locker`), and `Queue` (bounded in-process job queue: status, progress, `Watch` for streaming RPCs, cancel, retries with backoff for `Retryable` errors, per-attempt timeout, idempotent submission by key, explicit overflow). Jobs are in memory by default; `Store` is the seam for a durable store, with lease-based recovery (`LeaseTTL`, `RestartFail`/`RestartRequeue`) and a conformance test in `background/storetest`. Example: `examples/internal/reportservice` (start + watch RPCs). `terminationGracePeriodSeconds` must also cover the background grace period (default 10s).
- `graceful.Draining()`, `IsDraining()`, `Drain()`, `DeferDrain()`: a process-wide "stop taking new work" signal that background work and servers share. With a grpcmux server it fires when the server begins to drain, after the `PRE_STOP` wait; without one, when shutdown is requested. `DeferDrain` returns its release function and is tied to the closer registered before it. `graceful.AddWorkCloser`: closers of background work that run after the servers have stopped serving and before the dependency closers, whatever the registration order (so a queue or runner first used inside a request handler is not shut down during `PRE_STOP`).
- `async.Future.Limit(n)` bounds a Future's concurrency.
- Repository layer on `dependencies/database` ([guide](docs/guides/database.md#repositories), worked example `examples/internal/notestore`):
  - `database.Collection[T]` / `Repo[T]` (`database.NewCollection`): typed get/create/replace/patch/delete/list per entity; `Scope(owner)` restricts every operation to one owner or tenant and `Unscoped()` is the explicit exception.
  - Optimistic concurrency: `Replace`/`Patch`/`Delete` compare-and-swap on a version field in one statement; a stale version is `database.IsConflict` (gRPC `ABORTED`).
  - Keyset paging: `Repo.List` with opaque, sealed page tokens bound to the query (AIP-158), `Repo.ListAfter` and `database.FindAfter`, identical on SQL, MongoDB and the mock; `database.Contains` for array membership.
  - `dependencies/sql/migrate` and `(*sql.SQL).Migrate`: embedded forward-only migrations with checksums and a cross-replica lock; `database.EnsureIndexes` for MongoDB, the mock (unique constraints) and SQL.
  - `database.Transact`, `database.HealthCheck` (for `grpcmux.AddHealthChecker`), `database.IsConflict`, and a span plus the `db_client_operation_duration_seconds` histogram per repository operation.
  - `dependencies/database/dbtest`: the conformance suite all four backends pass; real engines run with `TEST_POSTGRES_URI`, `TEST_MYSQL_URI`, `TEST_MONGODB_URI`.

### Removed

- `examples/gen/go/example/user/v1/user.pb.gw.go`: the example serves REST with `WithREST` and no longer generates gateway code. The library still works with generated gateways.

## OIDC relying party (`1247dd8` to `80d3e63`, 2026-11-06 to 2026-11-16)

Unreleased. Commits: `1247dd8`, `66d306a`, `0eafc30`, `9b6736f`, and two documentation commits (`3b1246e`, `80d3e63`).

### Added

- `dependencies/oidc` (from the scaffold project): OIDC relying party configured by an `oidc://` URI ([guide](docs/guides/oidc.md)). It covers discovery, JWKS with rotation, RS256 validation, claim access rules (`groups=a%20b`, dotted paths, AND/OR), OAuth exchange/authorization/logout URLs, `net/http` middleware, and `Provider.AuthFunc()` for grpcmux. With `AuthFunc`, the claims go into the context and `user_id`/`client_id`/`project_id` go into the logs; a nil provider means auth is off. `WithLazyDiscovery(ttl)` and `Provider.Discover` are for issuers that are unreachable at startup (for example, the service itself), with TTL refetch and a stale-cache fallback. The default stays eager and fails closed.

### Changed

- **Breaking**, within this section (`0eafc30`): `dependencies/oidc` access rules no longer alias `group` and `groups`. A rule reads exactly the claim it names, so `groups=labs%20admin` reads only the `groups` claim; rename `group=` rules to `groups=` for issuers (account.nx.run, Cognito, Keycloak) that emit `groups`. `Claims.AllGroups` is removed; use `Claims.Groups` or `Claims.Strings("groups")`. Only consumers pinned to `1247dd8` or `66d306a` ever had the alias.
- `AGENTS.md` is the agent entry file; `CLAUDE.md` points to it.

### Fixed

- `grpcmux/mux` (`9b6736f`): the response writer the interceptor wraps around a request has `Unwrap`, so `http.ResponseController` reaches `SetWriteDeadline` / `SetReadDeadline`. Without it a streaming handler could not bound a write to a peer that stopped reading.
- Mock database (`9b6736f`): `Find`, `FindOne`, `FindRows`, `Exist` and `Count` created the table on first touch while holding only the read lock, a data race between two concurrent readers of a table nobody had written yet. Reads never create tables now.

## 2026-Q4 modernization (`d1943b1` to `299d0fc`, 2026-11-05)

Unreleased. Seventeen commits, all dated 2026-11-05. Upgrade guide: [docs/upgrades/2026-Q4.md](docs/upgrades/2026-Q4.md); its section 2 (B1 to B13) is the list of what a consumer must check, and its section 3 the Kubernetes settings that make the drain work.

### Security

- Fixed 10 vulnerabilities reachable from library code by upgrading grpc (GO-2026-6061, -6348, -6443), x/net (GO-2026-6603, -6610, -6611, -6612, -6617), x/text (GO-2026-5970) and the toolchain to go1.27.3 (crypto/x509, GO-2026-6809). mongo-driver v2.9.3 also includes fixes for CVE-2026-81521, -88031, -107324 and -107325. Bump and run `govulncheck` on the consumer.

### Fixed

- **Graceful shutdown never ran**: `graceful.Start` installed its signal handler only after every server had returned, so SIGTERM killed the process with no health flip, no `PRE_STOP` wait, no drain and no closers. The drain now happens, so configure it: `terminationGracePeriodSeconds` > `PRE_STOP` + the longest request, and readiness on `/readyz` or gRPC health ([2026-Q4.md](docs/upgrades/2026-Q4.md) section 3).
- Data race in `graceful.AddCloser` during concurrent dependency initialization.
- `dependencies/http`: `?proxy=` mutated the process-global `http.DefaultTransport`. A client passed with `WithHTTPClient` was mutated too. `?tracing=true` never wrapped the transport. `WithProxy(ctx)` built a new transport, and leaked its connections, on every request. If other clients relied (unknowingly) on one client's proxy, give them their own `?proxy=`; `WithHTTPClient(&http.Client{})` now opts out of the tuned transport, so drop it unless you need a custom one (B7).
- gRPC client: any URI scheme ending in "s" (including `dns://`) enabled TLS; `dns:///host` targets were empty. TLS is now used only for `secure=true`, `https://` or `grpcs://`. If a `dns://` target relied on the implied TLS, add `?secure=true` (B3).
- `Server.Conn()` (in-memory connection) only reached the health service.
- A second `NewServer` or `NewClientConn` with metrics in the same process panicked on duplicate Prometheus registration; client exemplar labels were dropped.
- An empty metrics address bound port 80 instead of disabling the metrics listener; the metrics server was never shut down.
- REST error responses copied raw gRPC trailers (binary `grpc-status-details-bin`) into HTTP headers, which HTTP/1.1 clients rejected as malformed. Header and trailer metadata now pass the same filter as on success (`x-*` and standard names). Nothing to do unless a client read `Grpc-Trailer-*` headers (B11).
- JSON for caller-owned types (HTTP client bodies, mqlru, SQL params, Mongo page results, codecs) silently changed to json/v2 semantics (case-sensitive fields, `[]` for nil) in `c9386fc`; restored v1 semantics. Revert workarounds added for the json/v2 behaviour (B8).
- `chainUnaryInterceptors` broke interceptors that call the handler more than once; the in-memory listener raced and blocked after Close.
- Mock database: errors had no gRPC code, and `FindRows` decoded to `map[string]any` instead of the requested type. Tests that expected `codes.Unknown` from the mock now see the real code, and `map[string]any` handling written for the mock can go (B9, B10).
- `routerlimit.UnaryServerInterceptor`/`StreamServerInterceptor` panicked without a `PersistenceFn`; they now default to the in-memory store like the HTTP middleware.
- `log.Fatal` dropped the logger's fields (it used the standard library logger). It now writes a JSON error line with `fatal: true` and exits 1.
- `dependencies/sql`: `log=` was ignored, and package options were left in the driver DSN unless another option was set. An invalid `log=` value is now an error from `Init`. `Init` pings with its context.

### Added

- Interceptor parity: the unary interceptor chain (protovalidate, PGV, authz, metrics, custom interceptors, recovery) runs on the ConnectRPC bridge, auto-HTTP routes and connect-go v2 handlers; `WithoutHTTPInterceptors()` / `disableHTTPInterceptors` opts out. Requests that skipped validation over Connect/HTTP are now validated, and Connect and auto-HTTP calls appear in the `grpc_server_*` series; check that custom unary interceptors do not assume a real gRPC transport (B2). The `WithLimiter` rate limiter is not part of the chain on the HTTP port: HTTP traffic is limited by `routerlimit.NewHandler`, so each Connect call is counted once.
- protovalidate (`buf.validate`) enforcement on every protocol, answering `INVALID_ARGUMENT` with violation details; generated `Validate()` methods from protoc-gen-validate are still honored. Hand-written `req.Validate()` calls in handlers become redundant.
- Connect bridge: gzip, `Connect-Timeout-Ms`, protocol-version check, GET for `NO_SIDE_EFFECTS` methods, error details, header/trailer metadata, 415 for streaming content types, `WithMaxRequestBodyBytes` / `maxRequestBodyBytes` (default 32 MiB).
- `Server.ConnectServer()`: connect-go v2 `connect.Server` mounted on the HTTP port (Connect, gRPC-Web, gRPC), with the interceptor chain and gRPC-status-to-`connect.Error` conversion.
- `/readyz` (readiness, follows drain and health checkers); `/healthz` stays liveness. gRPC health is `NOT_SERVING` until the server serves and during the drain. Point load-balancer health checks at `/readyz`.
- Server options: `WithHTTPTimeouts`, `WithMaxConcurrentStreams`, `WithKeepalive` (default enforcement accepts 10s pings).
- gRPC client URI options: `keepalive=`, `waitForReady=`, `retryPolicy=native`; `loadBalancingConfig` service config; `unix://` targets.
- HTTP client: tuned per-client transport, `maxIdlePerHost=`, `idleTimeout=`, `h2c=`, `backoff=`, `retryNonIdempotent=`.
- `async.Future.Go`; `database.IsNotFound` / `IsAlreadyExists`; `graceful` `GRACEFUL_TIMEOUT`.
- `examples/` module (UserService on all protocols with streaming, protovalidate, AIP-193 errors, Kubernetes and Envoy manifests, browser client) and `docs/guides/`.
- CI (tidy, build, vet, race tests, golangci-lint v2.15.0, `go fix -diff`, govulncheck, examples, English-only check), tracked `go.sum` (it was gitignored), quarterly maintenance playbook and scan script.

### Changed

- **Breaking:** `WithConnectOptions` (connect-go v1) removed; use `gs.ConnectServer()` with connect-go v2 generated code (`connect-go-v2-migrate` rewrites handlers, B1). `RegisterConnectHandler` still mounts any `http.Handler`, including v1 handlers, without the interceptor chain. connect-go v1 is no longer a dependency. Services that only use `RegisterConnectService` need no change.
- `dependencies/http` retries: `try=N` retries with jittered exponential backoff (`backoff=`, default 100ms) and honors the context; 502/503/504 and dial errors are retried; non-idempotent requests (POST, PATCH) are retried only after dial errors, with an `Idempotency-Key` header, or with `retryNonIdempotent=true`. Before, every error was retried immediately, POST included, and 5xx never. If a POST endpoint relied on blind retries, send an `Idempotency-Key` or add `retryNonIdempotent=true` to the URI (B6).
- HTTP/2 on the HTTP port uses only the standard library (`http.Protocols`, `http.HTTP2Config`); `golang.org/x/net/http2` is no longer used by the server.
- `PRE_STOP=0` means no wait (was 1 minute); values over 5 minutes are capped (were reset to 1 minute). Matters only if you set either (B5).
- `graceful.Start` handles signals in any goroutine, returns after shutdown, and shuts down when a server fails to start; closers run last-in first-out, once, and share one deadline. Code after `gs.Start()` in `main` now runs at exit: keep it short or remove it (B4).
- HTTP client metrics drop the query string from the path label. Update dashboards or alerts that matched on query strings (B12).
- All dependencies upgraded (grpc v1.85, grpc-gateway v2.33, OpenTelemetry v1.47, mongo-driver v2.9.3, sarama v1.61, prometheus client v1.25, ...); `go fix` modernizers applied. Upstream behaviour that reaches consumers: grpc-gateway rejects duplicate query parameters for non-repeated fields with 400, and grpc load-balancing policy names are case-sensitive (use `round_robin`) (B13).

### Removed

- `docs/tutorial` and `docs/examples` (replaced by `examples/` and `docs/guides/`); `docs/GO_1_27_UPGRADE.md` moved to `docs/upgrades/go-1.27.md`.
- `dependencies/proxy` (a `package main` forward proxy and a Cloudflare VLESS script) and `dependencies/http/example` (now `examples/cmd/httpclient`).

## Go 1.27 and request tracing (`6c0a7ab` to `74162f8`, 2026-07-12 to 2026-08-03)

Commits: `6c0a7ab`, `043886a`, `c9386fc`, `bd78c3f` (merged by `74162f8`).

### Changed

- **Breaking** (`c9386fc`): the module requires Go 1.27. `async.Future.Async` is a generic method: `f.Async(fn, in)` replaces the package function `async.Async(f, fn, in)` and the reflection-based `f.Async(fn any, params ...any) any`. Set `go 1.27` in the consumer's `go.mod` and rewrite the call sites ([docs/upgrades/go-1.27.md](docs/upgrades/go-1.27.md)).
- Internal JSON moved to `encoding/json/v2`, and `github.com/google/uuid` to the standard library `uuid` package. The json/v2 defaults also reached caller-owned types by mistake; that part is reverted in the 2026-Q4 modernization section. `tools/snowflake` seeds `math/rand/v2` from `crypto/rand`.
- The access log's `action` for hand-mounted HTTP routes is the first two path segments instead of the whole path (`bd78c3f`), so `/pkg.Service/Method/{id}/...` no longer creates one action per id. gRPC and Connect paths are unchanged. Dashboards that grouped such routes by full path now group by prefix.

### Added

- With `tracing: true`, the HTTP handler is wrapped with `otelhttp` (span `<METHOD> <path>`), so Connect and gRPC-Web calls on the HTTP port produce spans; before, only native gRPC was traced (`6c0a7ab`). Nothing is exported unless the application installs a TracerProvider.
- `grpcmux.WithDisableClientPriority()`: turns off RFC 9218 HTTP/2 client priority handling.

### Fixed

- Requests rejected by auth were logged with only `msg` and `status`. The request fields (`action`, `ip`, `peer`, `protocol`, `request_id`) are now injected before the AuthFunc runs, the identity fields after it succeeds (`043886a`).

## Go 1.26 cleanup (`3b6ceaa` to `e005efd`, 2026-06-08)

Thirteen commits including four merge commits (pull requests #1 to #4), all dated 2026-06-08.

### Added

- `dependencies/http`: `WithProxy(ctx, uri)` and `ProxyFromContext(ctx)` select a proxy per request; it takes priority over the client's `?proxy=`.
- `database.PageResult[T]` and `StreamResult[T]` aliases; `database.PageQuerier` / `StreamQuerier` interfaces for query dispatch.

### Changed

- `async.Future` is built on `errgroup`.
- The server enables h2c through `http.Server.Protocols` instead of `h2c.NewHandler`; internal packages log through `log/slog`.
- `log` methods (`Debug`, `Info`, `Warn`, `Error`, `Fatal`) accept a non-constant message without a `go vet` printf warning, so `logger.Info(method)` needs no `"%s"` wrapper.
- Dependencies upgraded; all Chinese comments and documentation translated to English.

### Fixed

- `async.Future` leaked goroutines.
- All `go vet` and golangci-lint findings; `uri.Unmarshal` guards against a nil target.

## ConnectRPC, CORS, mongo-driver v2 (`0e201fa` to `4b40ff4`, 2026-03-03 to 2026-04-24)

Eighteen commits.

### Added

- `Server.AddHealthChecker`: custom health check functions; if any returns an error, gRPC health answers `NOT_SERVING` (`0e201fa`).
- `grpcmux.RegisterConnectService`: serves a registered gRPC service over the Connect protocol on the HTTP port without connect-go code generation (`08a9c5f`), in JSON and, from `3215fad`, binary protobuf, chosen by `Content-Type`. Error responses are always JSON; the body limit is 32 MB (was 1 MB). `RegisterConnectHandler` mounts a generated connect-go handler. (`WithConnectOptions`, added here, is removed in the 2026-Q4 modernization.)
- `grpcmux.WithTLS(certFile, keyFile)` and `tlsCertFile` / `tlsKeyFile`: the HTTP port serves TLS instead of h2c (`ab233cb`).
- `Server.HandleWebSocket`: WebSocket handlers on the HTTP port, routed before HTTP/2 handling. The auth middleware is not applied to them (`30a5b05`).
- `grpcmux.WithCORS(CORSConfig)` and the `cors` config key: allowed origins, extra allowed headers, exposed headers, or `Disabled` when a proxy handles CORS (`daf0684`).
- `grpcmux.WithPreHTTPMiddleWares` (and `mux.WithPreMiddleWares`): HTTP middleware that runs before the AuthFunc, for example to turn a cookie into a bearer token. `WithHTTPMiddleWares` still runs after it (`4b40ff4`).
- `dependencies/redis`: `rediss://` URIs connect with TLS (`a4cdc59`).

### Changed

- **Breaking** (`8ebb3db`): `dependencies/mongo` uses `go.mongodb.org/mongo-driver/v2`. Code that passes driver types to or from the package (`bson`, options, `primitive.ObjectID`) moves to the v2 import paths and types (`bson.ObjectID`, option builders).
- `tools/routerlimit`: the quota key is the header values joined with `.`, without the path prefix (`68d3ca7`). Counters stored under the old key format start again.

### Deprecated

- `mux.WithOutCORS`: use `WithCORS(CORSConfig{Disabled: true})`.

### Fixed

- `ReadTimeout` and `WriteTimeout` on the HTTP server cut long-lived HTTP/2 streams without an end-of-stream message (`ConnectError: missing EndStreamResponse`); both are removed, `ReadHeaderTimeout` and the idle timeout stay (`d9ecffa`).
- Mock database: `Replace` always inserted and `ReplaceOne` did nothing when no document matched; both now upsert like MongoDB. Numeric fields stored with another numeric type (int for an int64 field) were dropped on read (`2c4824c`).

## Mock database and camelCase JSON (`4a9b6a4` to `bba6d1a`, 2026-01-30 to 2026-02-02)

Seven commits, mostly README and tutorial work (the tutorial is removed in the 2026-Q4 modernization).

### Added

- `dependencies/database/mock`: an in-memory `database.Database` for tests, with CRUD, `PageQuery` / `StreamQuery`, transactions and key normalization between camelCase and snake_case (`10301e7`).
- `grpcmux.WithUseCamelCase()` / `useCamelCase`: camelCase JSON on the HTTP port; the default stays snake_case (`10301e7`).
- Tutorial: UserService CRUD with page and stream queries and custom error codes registered through `mux.RegisterErrorCodes`.

## Initial publication (`fdfddd6` to `41d53e1`, 2025-01-08 to 2025-07-04)

Five commits.

### Added

- First public version (`fdfddd6`, 2025-01-08): `grpcmux` (gRPC server with grpc-gateway on one process, auth, logging, metrics), `config`, `dependencies` (URI-driven clients for SQL, MongoDB, Redis, Kafka, HTTP), `graceful`, `async`, `log`, and `tools` (`ratelimit`, `routerlimit`, `ip`, `snowflake`, `stacktrace`).
- `grpc.health.v1` `List` (`b35cadc`, 2025-06-25).

### Changed

- grpc v1.73, grpc-gateway v2.27, protobuf v1.36.6 (`b35cadc`).

### Fixed

- CORS: a cross-origin request with credentials was answered with `Access-Control-Allow-Origin: *`, which browsers reject together with credentials; the request's origin is echoed instead (`1d87fb0`, 2025-01-09).
- The gateway mux cleared every request header before routing, so handlers registered with `HandlePath` saw none (`41d53e1`, 2025-07-04).
