# Upgrade note: server drain, stream interceptors, reflection, auth exemptions, tracing

Applies to the release that adds the drain hook, `RegisterConnectStreams`, `WithReflection`, `WithNoAuthMethods` and package `tracing` (branch `feat/platform-capabilities`). Nothing here breaks the build of an existing consumer: the reference consumer (`auth/backend`) and the scaffold Go backend build, vet and pass their tests against this release without code changes. Guides: [server.md](../guides/server.md), [connectrpc.md](../guides/connectrpc.md), [tracing.md](../guides/tracing.md), [configuration.md](../guides/configuration.md), [logging.md](../guides/logging.md).

## 1. Behavior changes for existing code

Read the whole table once; most rows need no action.

| # | Area | Change | What to do |
|---|---|---|---|
| S1 | gRPC port, interceptor order | **Auth and the authz policy now run before validation** (they ran after it). A call without valid credentials gets `UNAUTHENTICATED` even when its request is invalid; before, it got `INVALID_ARGUMENT` and so learned something about the API without being allowed to call it. The HTTP port always behaved this way. | Update tests that expected `INVALID_ARGUMENT` from an unauthenticated invalid call. |
| S2 | HTTP port, streams | **Streams served by `gs.ConnectServer()` now run the interceptor chain**, as unary calls already did: authz policy, protovalidate / `Validate()` on every received message, `grpc_server_*` metrics, your `WithGRPCStreamServerInterceptors` interceptors, panic recovery. | Stream requests that skipped validation over Connect / gRPC-Web are now validated. Dashboards on `grpc_server_*` show the streams of the HTTP port too. Check that custom stream interceptors do not assume a real gRPC transport (they get a `grpc.ServerStream` with metadata, peer and working header/trailer calls, but no TLS `AuthInfo`). `WithoutHTTPInterceptors()` / `disableHTTPInterceptors: true` turns the chain off for unary calls and streams alike. |
| S3 | `gs.ConnectServer()` unary handlers | `grpc.SetHeader` / `grpc.SetTrailer` called in a handler now reach the client; they were silently dropped. | Nothing. |
| S4 | gRPC health | A service name that is **not registered** on the server gets `NOT_FOUND` from `Check` and `SERVICE_UNKNOWN` from `Watch`, as the health protocol specifies; before, every name answered `SERVING`. The empty name and registered service names are unchanged. `List` returns the empty name and each registered service (it returned one entry, `*`). An open `Watch` stream now ends with `UNAVAILABLE` when the server drains instead of holding the shutdown. | Kubernetes `grpc` probes without `service:` are unaffected. If a probe or client sets a service name, it must be one that is registered (`grpcurl ... list`), or empty. |
| S5 | CORS | The allowed request headers now include `X-Grpc-Web`, `X-User-Agent` and the Connect/gRPC encoding headers. `Access-Control-Expose-Headers` is now always sent for allowed origins, with `Grpc-Status`, `Grpc-Message`, `Grpc-Status-Details-Bin`, the encoding headers and `X-Request-Id`; `CORSConfig.ExposeHeaders` **adds** to that list (it used to be the whole list). `CORSConfig` has YAML tags (`disabled`, `allowedOrigins`, `allowedHeaders`, `exposeHeaders`). | Nothing; cross-origin gRPC-Web clients start working. Move a hard-coded `WithCORS` to the `apis.cors` block if you want it configurable (section 3.6). |
| S6 | Access log | A request rejected by the AuthFunc is logged with the HTTP status the client received (`403` for `PERMISSION_DENIED`, ...) plus `code` and `error`; before, the line always said `status: 401` and had no error. | Update alerts that counted `status=401` as "all auth failures". |
| S7 | `X-HTTP-Method-Override` | Honored only on **POST** requests and only for `GET`, `PUT`, `PATCH`, `DELETE`; the header value is matched case-insensitively (`delete` selects `DELETE`). Before, any request could be turned into any method, including a GET into a DELETE, and the value was used verbatim. | Clients that tunnel through POST are unaffected. |
| S8 | `/debug/**` | **Off unless enabled.** The route served `http.DefaultServeMux` (`net/http/pprof`, `expvar`) on the HTTP port of every server. It is now served only with `grpcmux.WithDebugHandlers(true)` / `apis.debugHandlers: true`; when enabled and an AuthFunc is configured it stays behind it. The presence of an AuthFunc is deliberately not treated as protection: the AuthFunc of a nil `oidc.Provider` (guest mode) admits everyone. | A service that imports `net/http/pprof` and uses `/debug/pprof` through the HTTP port (the `auth` backend does) adds `debugHandlers: true` to its `apis` config, after checking that its AuthFunc rejects strangers. |
| S9 | `log.SetLevel`, `config.Init` | `log.SetLevel` no longer panics: an empty name changes nothing, an unknown name changes nothing and logs an error. It also trims surrounding whitespace and accepts `warning` for `warn`. `config.Init`: `log.level` is optional (absent or empty keeps the current level, it used to panic); an unknown name makes `Init` return an error, and on a hot reload it is logged and the previous level stays. `Log` may be a struct or a pointer to a struct, and `Level` a `string` or a named string type; in every combination the level is applied at startup and on reload. `Init` does not allocate a nil `*Log`: without a `log:` key the pointer stays nil, and a level added to the source later is still applied. A `Log` field that has no string `Level` field is ignored; it used to panic at startup. | Remove workarounds that pre-set `Log.Level` before `config.Init` (section 3.7). |
| S10 | Tracing (`tracing: true`) | No server spans for `/healthz`, `/readyz`, `/favicon.ico` and `grpc.health.v1.Health`. Request log lines (HTTP and gRPC) gain `trace_id` and `span_id` when a TracerProvider is installed. | If you inject `trace_id` / `span_id` into the request logger yourself, **delete that code** when you bump: both would write the keys, and the JSON line would contain them twice (section 3.5). |
| S11 | `graceful.Draining()` with a grpcmux server | The process-wide drain signal (used by package `background`) now fires when the server begins to drain, **after** the `PRE_STOP` wait, together with `gs.Draining()`; without a grpcmux server it fires when shutdown is requested. | Nothing. Background loops keep working during `PRE_STOP`, like request handling does. |
| S13 | HEAD requests | A HEAD is now served as the GET it mirrors, without a body: it is routed, authenticated and logged, and answers with that GET's status and headers. Before, the CORS step answered **every** HEAD with an empty `200`, whatever the path, without authentication and without a log line. The table below this one lists what each kind of route answers. Handlers receive the request with `r.Method == "GET"` (net/http discards the body); `mux.IsHead(r)` tells the two apart. The access log line still says `HEAD`. | `curl -I`, link checkers and HEAD uptime monitors keep getting the GET's status. Monitors that sent HEAD to a **protected** route without credentials now get `401`; ones that sent it to an RPC path get `405`. Point them at `/healthz` or `/readyz`. |
| S14 | `logBody` | With body logging on, POST/PATCH bodies larger than the logged part (about 1 MB) were **cut off at that size before the handler read them** on gateway/REST routes. The handler now always receives the whole body; only the log entry is limited. | Nothing. |
| S12 | Module graph | `go.opentelemetry.io/otel/sdk` and the OTLP trace exporters (gRPC and HTTP) are new dependencies of the library module. They are linked into your binary only if you import `github.com/ti/common-go/tracing`. | `go mod tidy`. |

What a HEAD request answers (S13):

| HEAD on | Before | Now |
|---|---|---|
| `/healthz`, `/readyz`, `/favicon.ico` | `200`, empty | The GET's status (`200`; `/readyz` `503` while not ready), no body |
| A gateway route (`gs.HandleFunc(GET, ...)`, generated gateway code, a REST route of `WithREST`) | `200`, empty, handler not run | The GET's status and headers, no body |
| A `gs.Handle("/path", h)` or `gs.Handle("GET /path", h)` route | `200`, empty, handler not run | The GET's status and headers, no body |
| Any of the above behind an AuthFunc, without valid credentials | `200`, empty | The auth error (`401` / `403`), no body |
| A path no route serves | `200`, empty | What an unrouted GET answers, no body: `404` with `WithREST`, otherwise still `200` (the long-standing not-found behavior of the gateway mux) |
| An RPC path of `RegisterConnectService` | `200`, empty | `405` with `Allow: POST` (`GET, POST` for `NO_SIDE_EFFECTS` methods); behind an AuthFunc without credentials, the auth error |
| An RPC path of a connect-go handler (`gs.ConnectServer()`, `RegisterConnectStreams`) | `200`, empty | What connect-go answers a GET for that procedure (`405` with `Allow`) |

No HEAD is answered with a 5xx because of its method.

Unchanged on purpose: `?access_token=` and `X-HTTP-Method-Override` are still accepted by default (new opt-outs, section 2); open streams still hold the shutdown until `GRACEFUL_TIMEOUT` unless you opt in to `WithDrainStreams`; auth failures on the HTTP port are still answered as HTTP 401/403 JSON unless you opt in to `WithProtocolErrors`; `WithNoAuthPrefixes` keeps its prefix semantics.

## 2. New

| API | Config key | Purpose |
|---|---|---|
| `grpcmux.Draining(ctx)`, `(*Server).Draining()` | | Channel closed when the server begins to drain (after `PRE_STOP`) |
| `grpcmux.WithDrainStreams(grace)`, `grpcmux.ErrServerDraining` | `drainStreams`, `drainStreamsGrace` | End open streaming calls at drain with `UNAVAILABLE "server is draining"` |
| `grpcmux.RegisterConnectStreams(gs, desc, impl)` | | Streaming methods on the HTTP port (Connect, gRPC-Web, gRPC) from the gRPC handlers, no connect-go codegen |
| `grpcmux.WithReflection()`, `grpcmux.ReflectionMethods` | `reflection` | gRPC server reflection v1 + v1alpha, both ports |
| `grpcmux.WithNoAuthMethods(patterns...)`, `mux.WithNoAuthMethods`, `mux.MatchPath` | `noAuthMethods` | Exact (`/pkg.Service/Method`) and whole-service (`/pkg.Service/*`) auth exemptions |
| `grpcmux.WithProtocolErrors()`, `mux.WithProtocolErrors` | `protocolErrors` | Middleware rejections in the Connect-stream / gRPC / gRPC-Web wire format |
| `grpcmux.WithoutQueryAccessToken()`, `WithoutMethodOverride()` (and in `mux`) | `disableQueryAccessToken`, `disableMethodOverride` | Turn off `?access_token=` and `X-HTTP-Method-Override` |
| `grpcmux.WithDebugHandlers(bool)` | `debugHandlers` | Serve `/debug/**` on the HTTP port (off by default) |
| Package `tracing`: `Init`, `Enabled`, `IsProbeSpan`, options | `OTEL_*` environment | TracerProvider + OTLP exporter + probe-dropping sampler |
| `mux.TraceFields(ctx)` | | `trace_id` / `span_id` of the span in ctx, for your own log lines |
| `log.TrySetLevel`, `log.ParseLevel`, `log.Level`, `log.SetOutput` | | Level handling without panics; redirect output (tests) |
| `(*Server).Run() error`, `graceful.Run(ctx, fns...) error` | | `Start` that returns the start/serve failure (3.9) |

## 3. Adopting it

### 3.1 Streams that end cleanly at shutdown

The smallest change is configuration only:

```yaml
# before: an open watch stream holds the pod until GRACEFUL_TIMEOUT, then the connection is cut
apis:
  httpAddr: ":8080"

# after: streams end with UNAVAILABLE "server is draining" right after PRE_STOP
apis:
  httpAddr: ":8080"
  drainStreams: true
  drainStreamsGrace: 2s     # optional: time for handlers to end on their own first
```

or in code: `grpcmux.WithDrainStreams(2 * time.Second)`. Handlers need no change, as long as they return when their context ends (every handler that selects on `ctx.Done()` or passes `ctx` to what it blocks on).

A handler that should end with a normal end of stream (status OK) instead of `UNAVAILABLE` watches the signal:

```go
// before
select {
case <-ctx.Done():
	return status.FromContextError(ctx.Err()).Err()
case ev := <-events:
	...
}

// after
select {
case <-ctx.Done():
	return status.FromContextError(ctx.Err()).Err()
case <-grpcmux.Draining(ctx):
	return nil // the client sees a clean end and subscribes again, on another pod
case ev := <-events:
	...
}
```

Clients must treat both ends as "subscribe again". Keep `terminationGracePeriodSeconds` > `PRE_STOP` + `drainStreamsGrace` + your longest unary call. WebSocket handlers (`HandleWebSocket`) are not RPC streams: close them from `gs.Draining()`.

### 3.2 Auth exemptions

```go
// before: also exempts /auth.v1.AuthService/TokenAdmin, /auth.v1.AuthService/Token/anything, ...
grpcmux.WithNoAuthPrefixes(
	"/healthz",
	"/auth.v1.AuthService/Token",
	"/public.v1.PublicService/",
)

// after: exactly these
grpcmux.WithNoAuthMethods(
	"/auth.v1.AuthService/Token",   // one method
	"/public.v1.PublicService/*",   // a whole service
)
```

`/healthz`, `/readyz` and gRPC health never needed an entry. Keep `WithNoAuthPrefixes` for real path trees (`/static/`, `/v1/public/`); both options work together. Review every existing prefix for methods it exempts by accident: `grep -n WithNoAuthPrefixes -A12` and compare each entry with the method list of the service.

### 3.3 Streaming without a second handler type

If a service is registered with `RegisterConnectService` and its streaming methods are served by hand (`gs.Handle`) or by an adapter with connect-go's signature:

```go
// before
pb.RegisterNoteServiceServer(gs, h)                                         // gRPC
notev1connect.RegisterNoteServiceHandler(gs.ConnectServer(), connectAdapter{h}) // Connect; adapter re-implements WatchNotes

// after: no adapter, no connect-go generated code needed
grpcmux.RegisterConnectService(gs, &pb.NoteService_ServiceDesc, h) // gRPC + Connect unary
grpcmux.RegisterConnectStreams(gs, &pb.NoteService_ServiceDesc, h) // WatchNotes over Connect, gRPC-Web, gRPC on :8080
```

Keep the connect-go generated handler if clients call **unary** methods over gRPC-Web or gRPC on the HTTP port: `RegisterConnectService` serves unary methods over the Connect protocol only.

### 3.4 Reflection

```yaml
apis:
  reflection: true
```

With an AuthFunc, reflection requires credentials. To open it on an internal server: `grpcmux.WithNoAuthMethods(grpcmux.ReflectionMethods...)`.

### 3.5 Tracing: delete the hand-rolled provider and log correlation

```go
// before: a local package building exporter, resource, sampler, propagator; a helper
// that calls log.Inject(ctx, {"trace_id", "span_id"}) from handlers and middleware
shutdownTracing, err := tracing.Init(ctx) // yourproject/internal/tracing
if err != nil { ... } else { defer func() { _ = shutdownTracing(context.Background()) }() }

// after
import "github.com/ti/common-go/tracing"

if _, err := tracing.Init(ctx, tracing.WithServiceName("backend")); err != nil {
	log.Action("InitTracing").Warn("tracing disabled: " + err.Error())
}
```

Then delete the local package, the log-correlation helper and every call to it (S10), and run `go mod tidy` (the OpenTelemetry SDK and exporter become indirect). Differences to check against a hand-rolled setup:

- configuration is the standard environment only; the endpoint may be `http://host:4317` (plaintext) or `https://...`; `OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf` is supported;
- the default sampler is the specification's `parentbased_always_on`; an unknown `OTEL_TRACES_SAMPLER` is an error, not a silent fallback;
- probe spans are dropped for `/healthz`, `/readyz`, `/livez`, `/favicon.ico` and every `grpc.health.v1.Health` method;
- the flush on shutdown is registered with `graceful`; no `defer` is needed.

### 3.6 Configuration instead of code

```go
// before
grpcmux.WithCORS(grpcmux.CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}),
```

```yaml
# after
apis:
  cors:
    allowedOrigins: ["https://app.example.com"]
```

### 3.7 Log level

```go
// before: a default so that a config without log.level does not panic
cfg := app.DefaultConfig() // sets cfg.Log.Level = "info"
err := config.Init(ctx, "", &cfg)

// after: log.level is optional (the level stays at its default, debug), and
// an unknown name is an error from Init. Keep a default only if you want
// "info" rather than "debug" when the key is absent.
var cfg app.Config
err := config.Init(ctx, "", &cfg)
```

### 3.8 Protocol-correct auth errors

```yaml
apis:
  protocolErrors: true
```

Before: an unauthenticated Connect stream gets `HTTP 401` + `{"error":"unauthenticated",...}`. After: `HTTP 200`, `Content-Type: application/connect+json`, and an end-of-stream envelope `{"error":{"code":"unauthenticated","message":"..."}}`; gRPC and gRPC-Web calls on the HTTP port get `grpc-status: 16` with the message. Unary Connect and REST requests are unchanged. Check browser code that inspects `response.status === 401` on streams.

### 3.9 Exit non-zero when a server cannot start

```go
// before: a port that cannot be bound shuts the process down and main returns: exit status 0
gs.Start()

// after
if err := gs.Run(); err != nil {
	log.Action("Server").Fatal(err.Error()) // exit status 1
}
```

`graceful.Start(ctx, fns...)` has the same counterpart, `graceful.Run(ctx, fns...) error`. `Start` keeps its signature and behavior (the failure is now logged at error level instead of warn). Note that `Fatal` exits without running deferred functions; the closers have already run when `Run` returns.

### 3.10 Hardening (optional)

```yaml
apis:
  disableQueryAccessToken: true   # when no client needs ?access_token=
  disableMethodOverride: true     # when no client sends X-HTTP-Method-Override
```

## 4. Worked example: the scaffold Go backend

`~/Workspace/projects/foundation/scaffold/backend` builds and passes `go test ./...` against this release unchanged. To adopt:

| File | Change |
|---|---|
| `go.mod` | Bump `github.com/ti/common-go`; after the tracing change `go mod tidy` drops the direct `go.opentelemetry.io/otel/sdk` and `otlptracegrpc` requirements. |
| `cmd/server/main.go` | `app.New(&cfg, cookies).Start()` becomes `if err := app.New(&cfg, cookies).Run(); err != nil { log.Action("Server").Fatal(err.Error()) }` (3.9). |
| `internal/tracing/` | Delete. `cmd/server/main.go`: `tracing.Init(ctx, tracing.WithServiceName("backend"))` from `github.com/ti/common-go/tracing`, without the `defer` (3.5). |
| `internal/server/tracelog.go` | Delete, with its six call sites (`auth_service.go` ×4, `auth_middleware.go`, `irsa_service.go`). Required when bumping, not optional: see S10. |
| `internal/app/app.go` | `WithNoAuthPrefixes("/healthz", "/auth.v1.AuthService/Authorize", ".../Token", ".../Logout")` becomes `WithNoAuthMethods("/auth.v1.AuthService/Authorize", "/auth.v1.AuthService/Token", "/auth.v1.AuthService/Logout")` (3.2). Remove the hard-coded `WithCORS` (allow-all is the default; restrict in `apis.cors`). Add `grpcmux.WithDrainStreams(2*time.Second)` and `grpcmux.WithProtocolErrors()`, or the config keys. `DefaultConfig` is no longer needed to avoid the panic (3.7). Update the "A request passes" comment: gRPC is now `AuthFunc -> protovalidate -> ...`, and streams on `:8080` run `protovalidate -> metrics -> recovery` too. |
| `internal/note/handler.go` | Nothing required: with `WithDrainStreams`, `WatchNotes` ends with `UNAVAILABLE` after `PRE_STOP`. To end it like the Rust backend does (a normal end), add `case <-grpcmux.Draining(ctx): return nil` to the select in `watch` (3.1). Optional: drop `connectHandler` in favor of `RegisterConnectStreams` (3.3) if unary gRPC-Web on `:8080` is not needed. |
| `configs/config.yaml`, chart values | `apis.drainStreams: true`, `apis.drainStreamsGrace: 2s`; optionally `apis.reflection: true` for development. |
| `docs/adding-a-service.md`, `backend/README.md` | "Known limitations", Go backend: the five items (unary-only chain on `:8080`, validation before auth on `:8081`, streams holding shutdown, `log.level` panic, prefix exemptions) are resolved by the changes above. "Observed differences": unauthenticated `WatchNotes` (with `protocolErrors`), CORS for gRPC-Web, health for an unknown service and the open watch at shutdown now match Python and Rust. |

## 5. Verify

```bash
go build ./... && go vet ./... && go test -race ./...
```

Then, against a running instance: open a stream (`buf curl --protocol connect ... /note.v1.NoteService/WatchNotes`), send SIGTERM, and confirm the stream ends with `unavailable: server is draining` about `PRE_STOP` later and the process exits right after, not at `GRACEFUL_TIMEOUT`.
