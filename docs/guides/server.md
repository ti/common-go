# The server (`grpcmux`)

`grpcmux.NewServer(opts...)` builds one gRPC server and one HTTP server around the same service implementations.

| Port (default) | Option | Serves |
|---|---|---|
| `:8081` | `WithGrpcAddr` | Native gRPC (HTTP/2) and gRPC health |
| `:8080` | `WithHTTPAddr` | REST (grpc-gateway), ConnectRPC, auto-HTTP, custom handlers, WebSockets, `/healthz`, `/readyz`, `/debug/**` |
| `:9090` | `WithMetricsAddr` | Prometheus `/metrics` (OpenMetrics); an empty address disables it |

The HTTP port speaks HTTP/1.1 and HTTP/2. Without TLS it is h2c (cleartext HTTP/2, for running behind a TLS-terminating proxy such as Envoy Gateway); with `WithTLS(cert, key)` (or `tlsCertFile`/`tlsKeyFile`) it serves HTTPS with h2. HTTP/2 is configured with the standard library only (`http.Server.Protocols` and `http.HTTP2Config`).

## Registering services

| Call | Result |
|---|---|
| `pb.RegisterXxxServer(gs, impl)` | Native gRPC (and the in-memory connection, `gs.Conn()`) |
| `grpcmux.RegisterConnectService(gs, &pb.Xxx_ServiceDesc, impl)` | Native gRPC **plus** ConnectRPC at `/pkg.Service/Method` on the HTTP port ([connectrpc.md](connectrpc.md)) |
| `pb.RegisterXxxHandlerServer(ctx, gs.ServeMux(), impl)` | REST routes from `google.api.http` annotations (grpc-gateway, in-process) |
| `pb.RegisterXxxHandlerClient(ctx, gs.ServeMux(), pb.NewXxxClient(gs.Conn()))` | REST routes that go through the full gRPC interceptor chain via the in-memory connection |
| `WithAutoHTTP()` + `pb.RegisterXxxServer(gs, impl)` | Also `POST /pkg.Service/Method` with a JSON body for every unary method |
| `pbconnect.RegisterXxxHandler(gs.ConnectServer(), impl)` | connect-go v2 generated handlers: Connect, gRPC-Web and gRPC on the HTTP port, including streaming |
| `grpcmux.RegisterConnectHandler(gs, pattern, h)` | Any `http.Handler` (for example connect-go v1 handlers), behind the HTTP middleware only |
| `gs.Handle(pattern, h)` / `gs.HandleFunc(method, path, h)` | Custom `net/http` handlers / gateway-style routes with path templates |
| `gs.HandleWebSocket(pattern, h)` | WebSocket upgrades (bypass the HTTP middleware; do your own auth) |

Call `RegisterConnectService` **or** `pb.RegisterXxxServer` for a service, not both (both register it on the gRPC server).

## Request flow on the HTTP port

```
otelhttp (WithTracing)
└─ WebSocket dispatch (HandleWebSocket routes)
   └─ pre-HTTP middlewares (WithPreHTTPMiddleWares)      e.g. cookie → Authorization header
      └─ default middleware                               CORS, X-HTTP-Method-Override,
         │                                                request ID (x-request-id), access log,
         │                                                panic recovery, headers → gRPC metadata,
         │                                                auth (WithAuthFunc, except WithNoAuthPrefixes)
         └─ HTTP middlewares (WithHTTPMiddleWares)        run after auth
            └─ custom handlers (Handle), falling back to the grpc-gateway mux
```

Request headers starting with `x-` and standard headers (`authorization`, `cookie`, `user-agent`, ...) are copied into incoming gRPC metadata, so handlers read them the same way on every protocol. The request ID is taken from `x-request-id` or generated, and added to every log line.

## Interceptors

Native gRPC requests run this unary/stream chain (outermost first):

1. validation: **protovalidate** (`buf.validate` rules) and generated `Validate()` methods from protoc-gen-validate; failures return `INVALID_ARGUMENT`;
2. auth (`WithAuthFunc`), skipped for gRPC health and full-method prefixes in `WithNoAuthPrefixes`;
3. authz policy (`WithAuthzPolicy`, a gRPC authorization policy JSON), with audit logging;
4. Prometheus metrics (when metrics are enabled);
5. access logging (`WithLoggingOptions`);
6. rate limiting (`WithLimiter`);
7. your interceptors (`WithGRPCUnaryServerInterceptor`, `WithGRPCStreamServerInterceptors`);
8. panic recovery (unless `PANIC_RECOVERY=false`).

**The HTTP bridges run the same chain**: `RegisterConnectService`, `WithAutoHTTP` routes and connect-go v2 handlers on `gs.ConnectServer()` (unary calls) all apply steps 1, 3, 4, 7 and 8. Auth, access logging and rate limiting are left to HTTP middleware there: the HTTP middleware already authenticates and logs every request, and HTTP traffic is rate-limited by `routerlimit.NewHandler(limiter)` in `WithHTTPMiddleWares` (pass the same `Limiter` as `WithLimiter`). Running the interceptor as well would count each Connect call twice under the same key. Opt out with `WithoutHTTPInterceptors()` (or `disableHTTPInterceptors: true`).

grpc-gateway's generated `RegisterXxxHandlerServer` calls the implementation directly and cannot run interceptors; use `RegisterXxxHandlerClient` with `gs.Conn()` when REST routes must go through the chain.

Handlers on the bridges can use `grpc.SetHeader`, `grpc.SetTrailer`, `grpc.Method` and `peer.FromContext` as on native gRPC.

## Auth

`WithAuthFunc(func(ctx context.Context) (context.Context, error))` authenticates both gRPC and HTTP requests. Read credentials from incoming metadata (for example with `auth.AuthFromMD(ctx, "bearer")` from go-grpc-middleware), and return a context that carries the identity. If you store a `mux.AuthInfo` with `mux.NewAuthInfoContext`, its project/client/user/device/organization IDs are added to access logs, and handlers read it with `mux.AuthInfoFromContext`.

`WithNoAuthPrefixes(prefixes...)` skips auth for HTTP paths and gRPC full methods starting with any prefix (for example `/v1/public/`, `/myservice.v1.PublicService/`). On HTTP, `?access_token=` is accepted as a bearer token when there is no `Authorization` header.

## Errors

gRPC status errors are mapped to HTTP for REST and Connect. REST error bodies look like:

```json
{"error": "not_found", "error_code": 5, "error_description": "user 42 not found", "details": [...]}
```

(camelCase keys with `useCamelCase`). Connect uses the Connect error format ([connectrpc.md](connectrpc.md)). Prefer standard codes with `google.rpc.ErrorInfo` details for machine-readable reasons. For legacy numeric codes, `mux.RegisterErrorCodes(pb.ErrorCode_name)` registers names for custom codes: codes 200-598 map to that HTTP status, codes starting with 2 (e.g. 2001) to 200, with 4 to 400, others to 500.

## Options reference

| Option | Default | Purpose |
|---|---|---|
| `WithConfig(*Config)` | | Apply a [`grpcmux.Config`](configuration.md#grpcmuxconfig) |
| `WithHTTPAddr`, `WithGrpcAddr`, `WithMetricsAddr` | `:8080`, `:8081`, `:9090` | Listen addresses |
| `WithTLS(cert, key)` | h2c | HTTPS on the HTTP port |
| `WithHTTPTimeouts(readHeader, idle)` | 10s, 5m | HTTP header and idle timeouts. There is no read/write timeout on purpose: it would cut streaming RPCs; use deadlines |
| `WithMaxConcurrentStreams(n)` | 1000 | HTTP/2 streams per connection on the HTTP port |
| `WithMaxRequestBodyBytes(n)` | 32 MiB | Body limit on the Connect and auto-HTTP bridges |
| `WithKeepalive(params, policy)` | `MaxConnectionAge` 1m; policy `MinTime` 10s, `PermitWithoutStream` | gRPC keepalive. Connections are recycled every minute so clients rebalance; `MaxConnectionAgeGrace` stays infinite, so in-flight streams are never cut |
| `WithoutKeepAliveParams()` | | Use gRPC's own keepalive defaults |
| `WithGRPCServerOption(opts...)` | | Extra `grpc.ServerOption`s (applied last) |
| `WithAuthFunc`, `WithNoAuthPrefixes` | | Auth, see above |
| `WithAuthzPolicy(json)` | | gRPC authorization policy |
| `WithLimiter(*routerlimit.Limiter)` | | Per-route rate limiting |
| `WithGRPCUnaryServerInterceptor`, `WithGRPCStreamServerInterceptors` | | Your interceptors |
| `WithoutHTTPInterceptors()` | chain on | Do not run the chain on HTTP bridges |
| `WithPreHTTPMiddleWares`, `WithHTTPMiddleWares` | | HTTP middleware before / after the default middleware |
| `WithCORS(CORSConfig)` | allow all | CORS; `CORSConfig{Disabled: true}` when a proxy handles it |
| `WithUseCamelCase()` | snake_case | JSON naming ([json-format.md](json-format.md)) |
| `WithAutoHTTP()` | off | JSON POST route per unary method |
| `WithTracing()` | off | OpenTelemetry server spans on gRPC and HTTP |
| `WithMetaTags(tags)` | client_id, user_id, device_id, request_id | Metadata keys attached to metrics exemplars |
| `WithLoggingOptions(...)` | | Access-log decider / body logging for gRPC |
| `WithDisableClientPriority()` | | Serve HTTP/2 streams round-robin instead of by RFC 9218 priority |
| `SetDefaultOptions(opts...)` | | Change defaults for every later `NewServer` |

## Health

| Endpoint | Meaning | Use for |
|---|---|---|
| `GET /healthz` (HTTP port) | Always `200 SERVING` while the process serves HTTP | Liveness |
| `GET /readyz` (HTTP port) | gRPC health status: `503 NOT_SERVING` before the gRPC listener is up, during shutdown, or when a health checker fails | Readiness |
| `grpc.health.v1.Health/Check` (gRPC port) | Same status as `/readyz` | Readiness (Kubernetes `grpc` probe) |

`gs.AddHealthChecker(func(ctx context.Context) error)` adds checks (for example a database ping); any error reports NOT_SERVING. Don't use dependency checks for liveness.

## In-memory connection

`gs.Conn()` is a `*grpc.ClientConn` to this server's own services without a network hop, through the full gRPC chain (including auth). Use it for `RegisterXxxHandlerClient` or to call one service from another in the same process.

## Graceful shutdown

`gs.Start()` runs the gRPC, HTTP and metrics servers through `graceful.Start` and blocks. It returns after a shutdown, which starts on the first of: SIGINT/SIGTERM, cancellation, `graceful.Close()`, or a server failing (for example, a port already in use). A second signal kills the process immediately.

Shutdown (`gs.Close`, then every other closer registered with `graceful.AddCloser`, newest first):

1. health switches to NOT_SERVING (`/readyz` and gRPC health), so readiness probes take the pod out of rotation;
2. wait `PRE_STOP` while load balancers notice; requests are still served;
3. HTTP `Shutdown` and gRPC `GracefulStop` run in parallel and wait for in-flight requests and streams;
4. the metrics and in-memory servers stop;
5. dependency closers run (database pools, gRPC client connections, ...).

`PRE_STOP`: unset, `0` or `0s` means no wait; a duration means that wait, capped at `5m`; an unparsable value means `1m`. All of shutdown shares one deadline, `GRACEFUL_TIMEOUT` (default `6m`); when it expires, remaining connections are closed forcibly.

`graceful` also runs processes without a `grpcmux.Server` (workers, consumers). `gs.Start()` already calls `graceful.Start`, so use one or the other:

```go
graceful.AddCloser(worker.Stop)          // func(ctx context.Context) error
graceful.Start(ctx, worker.Run)          // func(ctx context.Context) error; blocks until shutdown
```

## Concurrency helper: `async`

```go
f := async.New(ctx)
for _, id := range ids {
	f.Go(func(ctx context.Context) error { return refresh(ctx, id) }) // no result
}
user := f.Async(loadUser, userID) // typed result, valid after Await
if err := f.Await(); err != nil { ... } // first error; ctx is canceled for the others
```
