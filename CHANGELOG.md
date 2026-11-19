# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Releases are identified by commit until semver tags are introduced.

## 2026-Q4 modernization (unreleased)

Upgrade guide: [docs/upgrades/2026-Q4.md](docs/upgrades/2026-Q4.md).

### Security
- Fixed 10 vulnerabilities reachable from library code by upgrading grpc (GO-2026-6061, -6348, -6443), x/net (GO-2026-6603, -6610, -6611, -6612, -6617), x/text (GO-2026-5970) and the toolchain to go1.27.3 (crypto/x509, GO-2026-6809). mongo-driver v2.9.3 also includes fixes for CVE-2026-81521, -88031, -107324 and -107325.

### Changed
- `dependencies/oidc`: access rules no longer alias `group` and `groups`. A rule reads exactly the claim it names, so `groups=labs%20admin` reads only the `groups` claim; rename `group=` rules to `groups=` for issuers (account.nx.run, Cognito, Keycloak) that emit `groups`. `Claims.AllGroups` is removed; use `Claims.Groups` or `Claims.Strings("groups")`.

### Fixed

- **A signed-in request could answer `Internal` from the metrics interceptor.** The Prometheus exemplar
  was built from every request-metadata field (`client_id`, `user_id`, `device_id`, `request_id`) plus
  the trace id, with no regard for the client library's limit of 128 runes over all label names and
  values; `AddWithExemplar` panics beyond it, and also on a value that is not valid UTF-8. Four ordinary
  identifiers and a trace id already exceed the limit, and `device_id` / `request_id` come from request
  headers, so any caller could trigger it with one long header. The exemplar is now bounded
  (`grpcmux/exemplar.go`): the trace id first, then each field only if it fits whole; oversized and
  invalid values are left out. Server and client interceptors share the rule.
- `config.Init` data race (found by `go test -race` in a consumer): the file watcher was given the caller's struct, so it re-read and rewrote the whole struct, dependency fields included, while `Init` was still setting them and later while handlers used them (it also JSON-encoded and deep-compared the live dependency clients on every change). The watcher now works on a private copy. It never touches the caller's struct before `Init` returns, never reads or writes dependency fields, and on a change assigns only the top-level fields that changed, as fresh values, under a lock that the new `config.RLock()` / `config.RUnlock()` take for readers of hot-reloaded fields. `Init` now returns an error for a config that is not a pointer to a struct (it panicked).
- `log.SetLevel` panicked on an empty or unknown level name, so `config.Init` crashed at startup when the config struct had a `Log` field and the file no `log.level`, and a typo in a hot-reloaded level killed the process. The level is now optional; an unknown name is an error from `config.Init` and a logged no-op on reload.
- gRPC port: validation ran before authentication, so an unauthenticated caller learned whether its request was valid. Auth and authz now run first.
- Streams on `gs.ConnectServer()` (Connect, gRPC-Web, gRPC on the HTTP port) ran no interceptors: no protovalidate, no `grpc_server_*` metrics, no custom stream interceptors. They now run the same chain as on the gRPC port. `grpc.SetHeader`/`SetTrailer` in connect-go unary handlers were dropped.
- Auth rejections were logged with `status: 401` whatever the response status (403, ...), and without the error.
- CORS sent no `Access-Control-Expose-Headers` unless configured and did not allow `X-Grpc-Web` / `X-User-Agent`, so cross-origin gRPC-Web clients failed; `CORSConfig` had no YAML tags.
- `grpc.health.v1`: an unknown service name answered `SERVING` (now `NOT_FOUND` / `SERVICE_UNKNOWN`); an open `Watch` stream held the shutdown until the deadline.
- With `logBody`, POST/PATCH bodies over about 1 MB on gateway/REST routes were truncated before the handler read them; the handler now always gets the whole body.
- Every HEAD request was answered with an empty `200` by the CORS step, whatever the path, without authentication or a log line. A HEAD is now served as the GET it mirrors, without a body: the GET's status and headers on gateway, REST and `Handle` routes and on `/healthz`, `/readyz`, `/favicon.ico`; the auth error on a protected route; what an unrouted GET answers on an unrouted path; `405` with `Allow` on RPC paths. Handlers see `GET`; `mux.IsHead(r)` tells the difference ([table](docs/upgrades/server-platform.md)).
- `config.Init` with a `Log` field that has no string `Level` panicked; it is now ignored. `Log` may be a pointer (left nil when the source has no `log` key) and `Level` a named string type; both are applied at startup and on reload.
- `/debug/**` served `http.DefaultServeMux` (pprof, expvar) on every server, without authentication when there was no AuthFunc or one that admits everyone (guest mode); it is now off unless `WithDebugHandlers(true)` / `debugHandlers: true`.
- `X-HTTP-Method-Override` could turn any request, including a GET, into any method.
- **Graceful shutdown never ran**: `graceful.Start` installed its signal handler only after every server had returned, so SIGTERM killed the process with no health flip, no `PRE_STOP` wait, no drain and no closers.
- Data race in `graceful.AddCloser` during concurrent dependency initialization.
- `graceful`: a panicking closer crashed the process mid-shutdown and skipped the remaining closers; a closer that ignored its context blocked the others forever. Panics are now recovered and logged, and a closer still running 2s after the shared deadline is abandoned so that the rest run.
- `graceful.Close()` called after a `Start` had already shut down stayed pending and made the next `Start` in the process exit immediately; with two `Start`s running, one `Close` stopped only one of them. `Close` now wakes every running `Start` and is ignored when none is running after a shutdown.
- `async.Future.Async` crashed the process (`reflect.Value.Set on zero Value` in the errgroup goroutine) when the function returned a nil pointer with a nil error; the result now keeps its zero value.
- `dependencies/http`: `?proxy=` mutated the process-global `http.DefaultTransport`. A client passed with `WithHTTPClient` was mutated too. `?tracing=true` never wrapped the transport. `WithProxy(ctx)` built a new transport, and leaked its connections, on every request.
- gRPC client: any URI scheme ending in "s" (including `dns://`) enabled TLS; `dns:///host` targets were empty.
- `Server.Conn()` (in-memory connection) only reached the health service.
- A second `NewServer` or `NewClientConn` with metrics in the same process panicked on duplicate Prometheus registration; client exemplar labels were dropped.
- An empty metrics address bound port 80 instead of disabling the metrics listener; the metrics server was never shut down.
- REST error responses copied raw gRPC trailers (binary `grpc-status-details-bin`) into HTTP headers, which HTTP/1.1 clients rejected as malformed.
- JSON for caller-owned types (HTTP client bodies, mqlru, SQL params, Mongo page results, codecs) silently changed to json/v2 semantics (case-sensitive fields, `[]` for nil); restored v1 semantics.
- `chainUnaryInterceptors` broke interceptors that call the handler more than once; the in-memory listener raced and blocked after Close.
- Mock database: errors had no gRPC code, and `FindRows` decoded to `map[string]any` instead of the requested type.
- `routerlimit.UnaryServerInterceptor`/`StreamServerInterceptor` panicked without a `PersistenceFn`; they now default to the in-memory store like the HTTP middleware.
- Rate limiting ([docs/upgrades/rest-and-rate-limiting.md](docs/upgrades/rest-and-rate-limiting.md)): `redis.RateLimitN`'s fallback while Redis is down refilled "period in seconds" tokens per second instead of `limit` per `period` (1000 per 10 minutes became 600 per second) and had a data race; `routerlimit`'s default in-memory store admitted more than the quota under concurrency and never dropped a key; `redisrate.Limiter` divided by zero for periods below one second; `ratelimit.RateLimiter.Allow` was not atomic. A request limited by `routerlimit.NewHandler` now carries `Retry-After`.
- `log.Fatal` dropped the logger's fields (it used the standard library logger).
- `dependencies/sql`: `log=` was ignored, and package options were left in the driver DSN unless another option was set.
- Storage layer, verified against PostgreSQL 17, MySQL 8.4 and MongoDB 8 ([docs/upgrades/2026-Q4-storage.md](docs/upgrades/2026-Q4-storage.md)):
  - `dependencies/mongo`: `Lt`/`Lte`/`Gt`/`Gte` conditions were silently treated as equality; transactions never started (writes were applied immediately, `Commit`/`Rollback` failed).
  - `dependencies/sql`: times were truncated to whole seconds on write; reads on a transaction handle ran outside the transaction, and reading a time column there panicked; the project of a `GetDatabase` view was pasted into the SQL text (injection); `Exist` needed an `_id` column; reads without a condition were a syntax error; bulk `Replace` ignored its index keys; MySQL duplicate keys were `Unknown` instead of `AlreadyExists`.
  - Mock: `Find` rejected `*[]*T`; times and mixed numeric types did not compare; `Rollback` kept the writes; a stored nil panicked on read.
  - `query.PageQuery`/`StreamQuery` rejected a `*database.DB` dependency field.

### Added
- `grpcmux.Server.Run()` and `graceful.Run()`: like `Start`, but return the error of the server that failed to start or serve, so `main` can exit non-zero. With `Start` a process whose listener could not bind shut down and exited 0; `Start` is unchanged (it now logs that failure at error level).
- Drain hook ([guide](docs/guides/server.md#long-lived-streams-and-the-drain), [upgrade note](docs/upgrades/server-platform.md)): `grpcmux.Draining(ctx)` / `gs.Draining()` signal when the server begins to drain (after `PRE_STOP`), and `WithDrainStreams(grace)` / `drainStreams` ends open streams with `UNAVAILABLE "server is draining"` as a regular end of stream on gRPC, Connect and gRPC-Web, instead of holding the shutdown until `GRACEFUL_TIMEOUT`. `graceful.Draining()` fires at the same moment.
- `grpcmux.RegisterConnectStreams`: streaming methods on the HTTP port (Connect, gRPC-Web, gRPC) from the generated gRPC handlers, without connect-go codegen.
- `WithReflection()` / `reflection`: gRPC server reflection (v1 and v1alpha) on the gRPC port and through the HTTP port; authenticated like any method, `ReflectionMethods` to exempt it.
- `WithNoAuthMethods` / `noAuthMethods`: exact and whole-service auth exemptions (`/pkg.Service/Method`, `/pkg.Service/*`); `mux.MatchPath`.
- `WithProtocolErrors()` / `protocolErrors`: auth failures and recovered panics on Connect streams, gRPC and gRPC-Web calls are answered in that protocol instead of an HTTP 401 JSON body.
- `WithoutQueryAccessToken()`, `WithoutMethodOverride()`, `WithDebugHandlers(bool)` and their config keys.
- Package `tracing` ([guide](docs/guides/tracing.md)): `tracing.Init` installs a TracerProvider with an OTLP exporter (gRPC or HTTP), sampler and resource from the standard `OTEL_*` environment, drops health-probe spans and flushes on shutdown. With `tracing: true` request log lines carry `trace_id` and `span_id` on the HTTP and gRPC paths (`mux.TraceFields`), and probes create no spans.
- `log.TrySetLevel`, `log.ParseLevel`, `log.Level`, `log.SetOutput`.
- `dependencies/oidc` (from the scaffold project): OIDC relying party configured by an `oidc://` URI. It covers discovery, JWKS with rotation, RS256 validation, claim access rules (`groups=a%20b`, dotted paths, AND/OR), OAuth exchange/authorization/logout URLs, `net/http` middleware, and `Provider.AuthFunc()` for grpcmux. With `AuthFunc`, the claims go into the context and `user_id`/`client_id`/`project_id` go into the logs; a nil provider means auth is off. `WithLazyDiscovery(ttl)` and `Provider.Discover` are for issuers that are unreachable at startup (for example, the service itself), with TTL refetch and a stale-cache fallback.
- Interceptor parity: the unary interceptor chain (protovalidate, PGV, authz, metrics, custom interceptors, recovery) runs on the ConnectRPC bridge, auto-HTTP routes and connect-go v2 handlers; `WithoutHTTPInterceptors()` / `disableHTTPInterceptors` opts out.
- protovalidate (`buf.validate`) enforcement on every protocol.
- Connect bridge: gzip, `Connect-Timeout-Ms`, protocol-version check, GET for `NO_SIDE_EFFECTS` methods, error details, header/trailer metadata, 415 for streaming content types, `WithMaxRequestBodyBytes`.
- `Server.ConnectServer()`: connect-go v2 `connect.Server` mounted on the HTTP port (Connect, gRPC-Web, gRPC), with the interceptor chain and gRPC-status-to-`connect.Error` conversion.
- `/readyz` (readiness, follows drain and health checkers); `/healthz` stays liveness.
- Server options: `WithHTTPTimeouts`, `WithMaxConcurrentStreams`, `WithKeepalive` (default enforcement accepts 10s pings).
- gRPC client URI options: `keepalive=`, `waitForReady=`, `retryPolicy=native`; `loadBalancingConfig` service config.
- HTTP client: tuned per-client transport, `maxIdlePerHost=`, `idleTimeout=`, `h2c=`, `backoff=`, `retryNonIdempotent=`, idempotency-aware retries of 502/503/504.
- `async.Future.Go`; `database.IsNotFound` / `IsAlreadyExists`; `graceful` `GRACEFUL_TIMEOUT`.
- REST from annotations ([guide](docs/guides/rest.md), [upgrade note](docs/upgrades/rest-and-rate-limiting.md)): `grpcmux.WithREST()` / `rest: true` serves every registered method that has a `google.api.http` annotation as a REST route, built from the descriptors at registration: no generated gateway code, no per-service call. Path, query, body, `response_body`, PATCH field masks and `additional_bindings` as in grpc-gateway; the HTTP bridges' interceptor chain; auth exemptions, rate limits and the log action by RPC name; server streaming as newline-delimited JSON. With it, an unrouted path answers HTTP 404 (it answers 200 with a `not_found` body otherwise) and error `details` are typed. The examples generate their OpenAPI document with pinned buf plugins, in the server's JSON naming, and test it against the served routes.
- Declarative rate limiting ([guide](docs/guides/rate-limiting.md)): `grpcmux.WithRateLimit` / `rateLimit` with a default and per-method or per-service rules (rate, burst), keyed by authenticated subject with fallback to the client IP, or by IP before authentication for public methods; token buckets (GCRA) in memory or, with `WithRateLimitStore(redis.RateLimiter())`, in Redis so limits hold across replicas (falling back to memory during an outage). A limited call is `RESOURCE_EXHAUSTED` with `google.rpc.RetryInfo` and `ErrorInfo` details in each protocol's own form (HTTP 429 + `Retry-After` for REST and Connect unary), counted once per request and once per stream; metrics `ratelimit_requests_total` and `ratelimit_store_errors_total`. The Redis script is one file (`tools/ratelimit/gcra.lua`) and the algorithm is pinned by language-neutral cases (`tools/ratelimit/testdata/gcra_cases.json`), for backends in other languages. The client IP is the connection's peer unless `trustedProxies` names the proxies whose `X-Forwarded-For` is believed (`tools/ip.TrustedProxies`; nobody by default), and IPv6 clients are limited per /64 (`ipv6Prefix`).
- REST `PATCH`: a JSON body member the message does not declare is ignored and left out of the derived update mask, at any depth, instead of failing the request with 400 (also when an explicit `update_mask` was given). Unknown members were already ignored in every other JSON body, so a newer client can now `PATCH` an older server.
- `grpcmux.WithTypedErrorDetails()` / `typedErrorDetails` (and `mux.WithTypedErrorDetails`): each `details` entry of a JSON error body carries `"@type"`, so a client can find the `google.rpc.ErrorInfo` among them as it can in a Connect error. Off by default: the default body is unchanged, byte for byte. Implied by `WithREST`.
- `grpcmux.WithTimeouts` / `timeouts`: server-side deadlines for unary calls per method or service on every protocol; streams are never bounded.
- Package `background` for work that outlives the request ([guide](docs/guides/background-work.md), [upgrade note](docs/upgrades/background-work.md)): `Go` (supervised goroutine: panic recovery with stack and metric, context detached from the request and cancelled at shutdown, trace link to the request), `Loop` (runs until shutdown, restarted with backoff), `Every` (jitter, no overlap, optional single runner across replicas through a `Locker`), and `Queue` (bounded in-process job queue: status, progress, `Watch` for streaming RPCs, cancel, retries with backoff for `Retryable` errors, per-attempt timeout, idempotent submission by key, explicit overflow). Jobs are in memory by default; `Store` is the seam for a durable store, with lease-based recovery (`LeaseTTL`, `RestartFail`/`RestartRequeue`) and a conformance test in `background/storetest`. Example: `examples/internal/reportservice` (start + watch RPCs).
- `graceful.Draining()`, `IsDraining()`, `Drain()`, `DeferDrain()`: a process-wide "stop taking new work" signal that background work and servers share. `DeferDrain` returns its release function and is tied to the closer registered before it. `graceful.AddWorkCloser`: closers of background work that run after the servers have stopped serving and before the dependency closers, whatever the registration order (so a queue or runner first used inside a request handler is not shut down during `PRE_STOP`).
- `async.Future.Limit(n)` bounds a Future's concurrency.
- `examples/` module (UserService on all protocols with streaming, protovalidate, AIP-193 errors, Kubernetes and Envoy manifests, browser client) and `docs/guides/`.
- CI (tidy, build, vet, race tests, golangci-lint v2.15.0, `go fix -diff`, govulncheck, examples, English-only check), tracked `go.sum`, quarterly maintenance playbook and scan script.
- Repository layer on `dependencies/database` ([guide](docs/guides/database.md#repositories), worked example `examples/internal/notestore`):
  - `database.Collection[T]` / `Repo[T]`: typed get/create/replace/patch/delete/list per entity; `Scope(owner)` restricts every operation to one owner or tenant and `Unscoped()` is the explicit exception.
  - Optimistic concurrency: `Replace`/`Patch`/`Delete` compare-and-swap on a version field in one statement; a stale version is `database.IsConflict` (gRPC `ABORTED`).
  - Keyset paging: `Repo.List` with opaque, sealed page tokens bound to the query (AIP-158), `Repo.ListAfter` and `database.FindAfter`, identical on SQL, MongoDB and the mock; `database.Contains` for array membership.
  - `dependencies/sql/migrate` and `(*sql.SQL).Migrate`: embedded forward-only migrations with checksums and a cross-replica lock; `database.EnsureIndexes` for MongoDB, the mock (unique constraints) and SQL.
  - `database.Transact`, `database.HealthCheck` (for `grpcmux.AddHealthChecker`), `database.IsConflict`, and a span plus the `db_client_operation_duration_seconds` histogram per repository operation.
  - `dependencies/database/dbtest`: the conformance suite all four backends pass; real engines run with `TEST_POSTGRES_URI`, `TEST_MYSQL_URI`, `TEST_MONGODB_URI`.

### Changed
- Interceptor order on the gRPC port is auth, authz, validation, metrics, access log, rate limit, drain hook, custom, recovery (validation used to come first).
- `X-HTTP-Method-Override` is honored on POST only and for GET/PUT/PATCH/DELETE only. `CORSConfig.ExposeHeaders` adds to a built-in list. `/debug/**` is served only with `WithDebugHandlers(true)` / `debugHandlers: true`.
- **Breaking:** `WithConnectOptions` (connect-go v1) removed; use `gs.ConnectServer()` with connect-go v2 generated code. connect-go v1 is no longer a dependency.
- HTTP/2 on the HTTP port uses only the standard library (`http.Protocols`, `http.HTTP2Config`); `golang.org/x/net/http2` is no longer used by the server.
- `PRE_STOP=0` means no wait (was 1 minute); values over 5 minutes are capped (were reset to 1 minute).
- `graceful.Start` handles signals in any goroutine, returns after shutdown, and shuts down when a server fails to start; closers share one deadline.
- HTTP client metrics drop the query string from the path label.
- All dependencies upgraded (grpc v1.85, grpc-gateway v2.33, OpenTelemetry v1.47, mongo-driver v2.9.3, sarama v1.61, prometheus client v1.25, ...); `go fix` modernizers applied.

### Removed
- `docs/tutorial` and `docs/examples` (replaced by `examples/` and `docs/guides/`).
- `dependencies/proxy` (a `package main` forward proxy and a Cloudflare VLESS script) and `dependencies/http/example` (now `examples/cmd/httpclient`).
