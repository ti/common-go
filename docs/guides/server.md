# The server (`grpcmux`)

`grpcmux.NewServer(opts...)` builds one gRPC server and one HTTP server around the same service implementations.

| Port (default) | Option | Serves |
|---|---|---|
| `:8081` | `WithGrpcAddr` | Native gRPC (HTTP/2) and gRPC health |
| `:8080` | `WithHTTPAddr` | REST (grpc-gateway), ConnectRPC, auto-HTTP, custom handlers, WebSockets, `/healthz`, `/readyz`, `/debug/**` (off unless enabled, see [Debug handlers](#debug-handlers)) |
| `:9090` | `WithMetricsAddr` | Prometheus `/metrics` (OpenMetrics); an empty address disables it |

The HTTP port speaks HTTP/1.1 and HTTP/2. Without TLS it is h2c (cleartext HTTP/2, for running behind a TLS-terminating proxy such as Envoy Gateway); with `WithTLS(cert, key)` (or `tlsCertFile`/`tlsKeyFile`) it serves HTTPS with h2. HTTP/2 is configured with the standard library only (`http.Server.Protocols` and `http.HTTP2Config`).

## Registering services

| Call | Result |
|---|---|
| `pb.RegisterXxxServer(gs, impl)` | Native gRPC (and the in-memory connection, `gs.Conn()`) |
| `grpcmux.RegisterConnectService(gs, &pb.Xxx_ServiceDesc, impl)` | Native gRPC **plus** ConnectRPC at `/pkg.Service/Method` on the HTTP port ([connectrpc.md](connectrpc.md)) |
| `pb.RegisterXxxHandlerServer(ctx, gs.ServeMux(), impl)` | REST routes from `google.api.http` annotations (grpc-gateway, in-process) |
| `pb.RegisterXxxHandlerClient(ctx, gs.ServeMux(), pb.NewXxxClient(gs.Conn()))` | REST routes that go through the full gRPC interceptor chain via the in-memory connection |
| `grpcmux.WithREST()` (an option, not a call per service) | REST routes for every registered method that has a `google.api.http` annotation, without generated gateway code, through the HTTP bridges' interceptor chain; see [rest.md](rest.md) |
| `WithAutoHTTP()` + `pb.RegisterXxxServer(gs, impl)` | Also `POST /pkg.Service/Method` with a JSON body for every unary method |
| `grpcmux.RegisterConnectStreams(gs, &pb.Xxx_ServiceDesc, impl)` | The **streaming** methods of the service on the HTTP port (Connect, gRPC-Web, gRPC), from the gRPC stream handlers, no connect-go codegen ([connectrpc.md](connectrpc.md#streaming-without-connect-go-codegen)) |
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
         │                                                request ID (x-request-id), access log
         │                                                (with trace_id/span_id), panic recovery,
         │                                                headers → gRPC metadata, auth (WithAuthFunc,
         │                                                except WithNoAuthMethods / WithNoAuthPrefixes)
         └─ HTTP middlewares (WithHTTPMiddleWares)        run after auth
            └─ custom handlers (Handle), falling back to the grpc-gateway mux
```

Request headers starting with `x-` and standard headers (`authorization`, `cookie`, `user-agent`, ...) are copied into incoming gRPC metadata, so handlers read them the same way on every protocol. The request ID is taken from `x-request-id` or generated, and added to every log line.

A HEAD request is served as the GET it mirrors, without a body: it is routed, authenticated and logged, and answers with the GET's status and headers on gateway, REST and `gs.Handle` routes and on `/healthz` and `/readyz`. Handlers receive it with `r.Method == "GET"` (`mux.IsHead(r)` tells the difference; net/http discards the body). RPC paths have no HEAD form and answer `405` with `Allow`. Only CORS preflights (OPTIONS) are answered by the middleware itself.

Two conveniences of the default middleware can be turned off:

- `X-HTTP-Method-Override` lets a client that can only send POST select `GET`, `PUT`, `PATCH` or `DELETE`. It is honored on POST requests only (a GET is never turned into a write). The value is matched case-insensitively. `WithoutMethodOverride()` / `disableMethodOverride: true` ignores the header.
- `?access_token=` is accepted as the bearer token when a request has no `Authorization` header, for requests that cannot set headers (EventSource, `<img>`, downloads). URLs end up in proxy logs and browser history, so turn it off with `WithoutQueryAccessToken()` / `disableQueryAccessToken: true` when every client can send the header.

## Interceptors

Native gRPC requests run this unary/stream chain (outermost first):

1. auth (`WithAuthFunc`), skipped for gRPC health and the exemptions in `WithNoAuthMethods` / `WithNoAuthPrefixes`;
2. authz policy (`WithAuthzPolicy`, a gRPC authorization policy JSON), with audit logging;
3. validation: **protovalidate** (`buf.validate` rules) and generated `Validate()` methods from protoc-gen-validate; failures return `INVALID_ARGUMENT`. It runs after auth, so a caller without credentials gets `UNAUTHENTICATED` and does not learn whether its request was valid;
4. Prometheus metrics (when metrics are enabled);
5. access logging (`WithLoggingOptions`), with `trace_id`/`span_id` when tracing is on;
6. rate limiting (`WithRateLimit`: rules keyed by client IP run before step 1, rules keyed by subject here; see [rate-limiting.md](rate-limiting.md). The older `WithLimiter` runs here too), then server-side deadlines for unary calls (`WithTimeouts`);
7. the drain hook (see [Graceful shutdown](#graceful-shutdown));
8. your interceptors (`WithGRPCUnaryServerInterceptor`, `WithGRPCStreamServerInterceptors`);
9. panic recovery (unless `PANIC_RECOVERY=false`).

**The HTTP bridges run the same chain**: `RegisterConnectService`, `WithAutoHTTP` routes, `RegisterConnectStreams` and connect-go v2 handlers on `gs.ConnectServer()` all apply steps 2, 3, 4, 8 and 9, for unary calls **and for streams**: a stream called over Connect, gRPC-Web or gRPC on the HTTP port has every received message validated, is counted in the `grpc_server_*` metrics and passes your stream interceptors, which see a `grpc.ServerStream` with the incoming metadata, the peer and a working `SetHeader`/`SendHeader`/`SetTrailer`. What connect-go cannot offer is the gRPC transport itself: an interceptor that needs transport internals (the TLS `AuthInfo` of the gRPC port, `grpc.SetSendCompressor`) finds none. Auth, access logging and rate limiting are left to HTTP middleware there: the HTTP middleware already authenticates and logs every request, and HTTP traffic is rate-limited by `routerlimit.NewHandler(limiter)` in `WithHTTPMiddleWares` (pass the same `Limiter` as `WithLimiter`). Running the interceptor as well would count each Connect call twice under the same key. `WithRateLimit` needs none of that wiring: it installs its own middleware on the HTTP port and its own interceptors on the gRPC port. Opt out with `WithoutHTTPInterceptors()` (or `disableHTTPInterceptors: true`).

grpc-gateway's generated `RegisterXxxHandlerServer` calls the implementation directly and cannot run interceptors; use `RegisterXxxHandlerClient` with `gs.Conn()` when REST routes must go through the chain.

Handlers on the bridges can use `grpc.SetHeader`, `grpc.SetTrailer`, `grpc.Method` and `peer.FromContext` as on native gRPC (also in unary and streaming handlers registered on `gs.ConnectServer()`).

## Auth

`WithAuthFunc(func(ctx context.Context) (context.Context, error))` authenticates both gRPC and HTTP requests. Read credentials from incoming metadata (for example with `auth.AuthFromMD(ctx, "bearer")` from go-grpc-middleware), and return a context that carries the identity. If you store a `mux.AuthInfo` with `mux.NewAuthInfoContext`, its project/client/user/device/organization IDs are added to access logs, and handlers read it with `mux.AuthInfoFromContext`.

Two options exempt routes from auth, on both ports:

| Option | Matches | Use for |
|---|---|---|
| `WithNoAuthMethods("/pkg.Service/Method", "/pkg.Service/*")` (or `noAuthMethods:` in the config) | The exact gRPC full method or HTTP path; with a trailing `/*`, everything below that segment | Single RPC methods and whole services. **Use this one for RPCs.** |
| `WithNoAuthPrefixes("/v1/public/")` | Any path that starts with the string (`strings.HasPrefix`) | HTTP path trees. It does not know about segments: `/pkg.Service/Token` also exempts `/pkg.Service/TokenAdmin`, so do not use it for single methods |

Both lists apply together. `WithNoAuthMethods` adds to its list on every call; `WithNoAuthPrefixes` replaces its list. gRPC health, `/healthz` and `/readyz` are always exempt.

On HTTP, `?access_token=` is accepted as a bearer token when there is no `Authorization` header (see above for turning that off).

An auth failure is logged with the status the client received (`401` for `UNAUTHENTICATED`, `403` for `PERMISSION_DENIED`) plus `code` and `error`.

## Errors

gRPC status errors are mapped to HTTP for REST and Connect. REST error bodies look like:

```json
{"error": "not_found", "error_code": 5, "error_description": "user 42 not found", "details": [...]}
```

(camelCase keys with `useCamelCase`). Connect uses the Connect error format ([connectrpc.md](connectrpc.md)).

Requests the HTTP middleware rejects itself (an auth failure, a recovered panic) get that JSON body with the HTTP status, whatever the client speaks. A Connect stream, gRPC or gRPC-Web client can only map the HTTP status back to a code and loses the message. `WithProtocolErrors()` / `protocolErrors: true` answers those clients in their own protocol instead: HTTP 200 with the error in the Connect end-of-stream envelope or in the `grpc-status` trailers, exactly as a handler error is delivered. It applies to requests with `Content-Type: application/connect+*`, `application/grpc*` and `application/grpc-web*`; REST, Connect unary and plain HTTP requests keep the JSON body, so existing clients are not affected. Prefer standard codes with `google.rpc.ErrorInfo` details for machine-readable reasons. For legacy numeric codes, `mux.RegisterErrorCodes(pb.ErrorCode_name)` registers names for custom codes: codes 200-598 map to that HTTP status, codes starting with 2 (e.g. 2001) to 200, with 4 to 400, others to 500.

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
| `WithAuthFunc`, `WithNoAuthMethods`, `WithNoAuthPrefixes` | | Auth and exemptions, see above |
| `WithProtocolErrors()` | off | Middleware rejections in the client's RPC protocol, see [Errors](#errors) |
| `WithoutQueryAccessToken()`, `WithoutMethodOverride()` | both accepted | Ignore `?access_token=` / `X-HTTP-Method-Override` |
| `WithReflection()` | off | gRPC server reflection, see [Reflection](#reflection) |
| `WithDrainStreams(grace)` | off | End open streams when the server drains, see [Graceful shutdown](#graceful-shutdown) |
| `WithDebugHandlers(bool)` | off | `/debug/**` on the HTTP port, see [Debug handlers](#debug-handlers) |
| `WithAuthzPolicy(json)` | | gRPC authorization policy |
| `WithREST()` | off | REST routes from `google.api.http` annotations, see [rest.md](rest.md) |
| `WithTypedErrorDetails()` | off; on with `WithREST` | `"@type"` on the `details` of JSON error bodies, see [rest.md](rest.md#errors) |
| `WithRateLimit(ratelimit.Config)`, `WithRateLimitStore(store)`, `WithRateLimitSubject(fn)` | off, in memory | Declarative rate limiting on every protocol, see [rate-limiting.md](rate-limiting.md) |
| `WithTimeouts(TimeoutConfig)` | none | Server-side deadlines for unary calls, see [rate-limiting.md](rate-limiting.md#server-side-deadlines) |
| `WithLimiter(*routerlimit.Limiter)` | | The older header-keyed limiter (with `routerlimit.NewHandler` for the HTTP port) |
| `WithGRPCUnaryServerInterceptor`, `WithGRPCStreamServerInterceptors` | | Your interceptors |
| `WithoutHTTPInterceptors()` | chain on | Do not run the chain on HTTP bridges |
| `WithPreHTTPMiddleWares`, `WithHTTPMiddleWares` | | HTTP middleware before / after the default middleware |
| `WithCORS(CORSConfig)` | allow all | CORS; `CORSConfig{Disabled: true}` when a proxy handles it |
| `WithUseCamelCase()` | snake_case | JSON naming ([json-format.md](json-format.md)) |
| `WithAutoHTTP()` | off | JSON POST route per unary method |
| `WithTracing()` | off | OpenTelemetry server spans on gRPC and HTTP (not for health probes) and `trace_id`/`span_id` on request logs; export them with [`tracing.Init`](tracing.md) |
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

gRPC health follows the [health checking protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md): the empty service name is the server's overall status, every service registered on the server shares it, and a name that is not registered gets `NOT_FOUND` from `Check` and `SERVICE_UNKNOWN` from `Watch`. A Kubernetes `grpc` probe without `service:` asks for the empty name. A `Watch` stream ends (with `UNAVAILABLE`, after a final `NOT_SERVING`) when the server drains.

## Reflection

`WithReflection()` (or `reflection: true`) serves gRPC server reflection, `grpc.reflection.v1` and `v1alpha`, for every service registered on the gRPC server: on the gRPC port, and over gRPC, gRPC-Web and Connect on the HTTP port. Tools then need no proto files:

```bash
grpcurl -plaintext localhost:8081 list
grpcurl -plaintext localhost:8080 describe example.user.v1.UserService   # through the HTTP port (h2c)
buf curl --protocol grpc --http2-prior-knowledge --reflect -d '{}' http://localhost:8080/example.user.v1.UserService/ListUsers
```

It is off by default because it publishes the complete API schema. **Reflection is authenticated like any other method**: with `WithAuthFunc`, pass credentials (`grpcurl -H 'authorization: Bearer ...'`). To serve it without credentials, which is reasonable on an internal or development server, exempt it explicitly:

```go
grpcmux.WithNoAuthMethods(grpcmux.ReflectionMethods...)
```

Services that exist only as connect-go handlers on `gs.ConnectServer()` (never registered on the gRPC server) are not listed.

## Debug handlers

`GET /debug/**` on the HTTP port serves `http.DefaultServeMux`, which is where `net/http/pprof` and `expvar` register when a program imports them. Profiles and variables must not be public, so the route is **off unless you turn it on** with `WithDebugHandlers(true)` or `debugHandlers: true`. When it is on and an `AuthFunc` is configured, it is authenticated like every other route. Check that the AuthFunc really rejects strangers before enabling it: one that admits everyone, such as the AuthFunc of a nil `oidc.Provider` (guest mode), protects nothing. Without an AuthFunc, enable it only on a server that is reachable inside the cluster only.

## In-memory connection

`gs.Conn()` is a `*grpc.ClientConn` to this server's own services without a network hop, through the full gRPC chain (including auth). Use it for `RegisterXxxHandlerClient` or to call one service from another in the same process.

## Graceful shutdown

`gs.Run()` runs the gRPC, HTTP and metrics servers through `graceful.Run` and blocks. It returns an error when a server could not start or stopped unexpectedly (a port in use, a missing TLS file), and nil after a requested shutdown, so `main` can exit non-zero:

```go
if err := gs.Run(); err != nil {
	log.Action("Server").Fatal(err.Error()) // exit status 1
}
```

`gs.Start()` is the same without the result: after a failed start it logs the error and returns like after a signal, so a `main` that ends with it exits 0. Prefer `Run` (and `graceful.Run` for processes without a server).

Both return after a shutdown, which starts on the first of: SIGINT/SIGTERM, cancellation, `graceful.Close()`, or a server failing (for example, a port already in use). A second signal kills the process immediately.

Shutdown (`gs.Close`, then every other closer registered with `graceful.AddCloser`, newest first):

1. health switches to NOT_SERVING (`/readyz` and gRPC health), so readiness probes take the pod out of rotation;
2. wait `PRE_STOP` while load balancers notice; requests and streams are served as usual;
3. **draining begins**: the `Draining` signal fires and, with `WithDrainStreams`, open streams are ended (below);
4. HTTP `Shutdown` and gRPC `GracefulStop` run in parallel and wait for in-flight requests and streams;
5. the metrics and in-memory servers stop;
6. dependency closers run (database pools, gRPC client connections, ...).

`PRE_STOP`: unset, `0` or `0s` means no wait; a duration means that wait, capped at `5m`; an unparsable value means `1m`. All of shutdown shares one deadline, `GRACEFUL_TIMEOUT` (default `6m`); when it expires, remaining connections are closed forcibly.

### Long-lived streams and the drain

Unary calls in flight simply finish in step 4. A long-lived stream (a watch, server-sent events, a chat) does not end by itself: without help it holds step 4 until `GRACEFUL_TIMEOUT` and is then cut in the middle, and the client sees a broken connection instead of an end of stream. There are two ways to end streams when draining begins. They combine.

**`WithDrainStreams(grace)`** (or `drainStreams: true`, `drainStreamsGrace: 5s`) needs no handler changes. `grace` after draining begins (zero: at once) the context of every streaming call still open is canceled, with the cause `grpcmux.ErrServerDraining`, and a handler blocked in `Recv` is woken. A handler that returns because of it ends the call with `UNAVAILABLE "server is draining"`, delivered as a regular end of stream on every protocol: gRPC status trailers, the Connect end-of-stream envelope, the gRPC-Web trailer frame. Clients treat `UNAVAILABLE` as retryable and reconnect, to a pod that is still in rotation. It covers streams on the gRPC port, `gs.Conn()`, `RegisterConnectStreams` and connect-go handlers on `gs.ConnectServer()`. Unary calls are never canceled.

**`grpcmux.Draining(ctx)`** is the signal itself, for handlers that want to end their own way (a last message, status OK) and for everything that is not an RPC stream:

```go
func (s *Service) WatchNotes(req *pb.WatchNotesRequest, stream grpc.ServerStreamingServer[pb.WatchNotesResponse]) error {
	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case <-grpcmux.Draining(ctx): // closed when this server begins to drain
			return nil                // a clean end, status OK; the client re-subscribes
		case ev := <-events:
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}
```

`Draining(ctx)` works with the context of any gRPC handler and of any request on the HTTP port (Connect, REST, custom `Handle` routes); for a context that does not come from a grpcmux server it returns a nil channel, which never fires. Code without a request context (background loops, WebSocket handlers) uses `gs.Draining()`.

What a client observes, with `PRE_STOP=10s` and `WithDrainStreams(2*time.Second)`:

| Time after SIGTERM | Server | Unary call in flight | Open stream |
|---|---|---|---|
| 0s | `/readyz` 503, gRPC health NOT_SERVING | served | served; new streams still accepted |
| 10s (`PRE_STOP` over) | `Draining` fires; listeners close; HTTP/2 GOAWAY | completes normally | handlers that watch `Draining` end it (OK, or whatever they return) |
| 12s (grace over) | remaining stream contexts canceled | completes normally | ends with `UNAVAILABLE "server is draining"` |
| when the last call is done (HTTP/2 connections are closed up to a second later) | closers run, process exits | | |
| `GRACEFUL_TIMEOUT` | whatever is left is cut | cut | cut (a handler blocked in `Send` to a client that does not read) |

The gRPC health `Watch` stream is ended when draining begins in any case, so health-checking clients never hold a shutdown.

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
