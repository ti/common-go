# Upgrade note: REST from annotations, declarative rate limiting, deadlines

Applies to the release that adds `grpcmux.WithREST`, `grpcmux.WithRateLimit` / `WithRateLimitStore` and `grpcmux.WithTimeouts` (branch `feat/platform-capabilities`). Everything new is opt-in. Guides: [rest.md](../guides/rest.md), [rate-limiting.md](../guides/rate-limiting.md).

## 1. Behavior changes for existing code

These apply without opting in to anything.

| # | Area | Change | What to do |
|---|---|---|---|
| R2 | `tools/routerlimit` | The default in-memory store (no `PersistenceFn`) counts exactly under concurrency (it could admit more than the quota) and drops expired windows (it kept every key it had ever seen). A limited HTTP request now also carries `Retry-After`. Rules, keys and fixed-window semantics are unchanged. | Nothing. |
| R3 | `redis.RateLimitN` while Redis is down | The in-process fallback now enforces the configured limit (`limit` per `period`, bucket of `limit`). It used to refill `period`-in-seconds tokens per second, which for a rule like 1000 per 10 minutes allowed 600 requests per second. It also had a data race. | Nothing; a Redis outage no longer lifts the limits in practice. |
| R4 | `redisrate.Limiter` | `AllowN` / `Reset` with a period below one second no longer panic (division by zero); the window is one second. | Nothing. |
| R5 | `ratelimit.RateLimiter` | `Allow` is atomic (concurrent callers could exceed the limit). New `Stop()` ends its eviction goroutine. | Call `Stop()` on limiters that are created and dropped at run time. |

Unchanged on purpose: the JSON error body (`mux.WriteHTTPErrorResponse`, the gateway error handler, middleware rejections) is byte for byte what it was, including `details` as bare messages without `"@type"` and a detail with no set fields being left out (pinned by `TestErrorBodyDefaultDetailsAreUnchanged`); without `WithREST` a request for an unrouted path still answers HTTP **200** with the `not_found` JSON body (R6 below changes that only together with REST); the routerlimit limiter keeps its header-keyed model and is not deprecated; `WithLimiter` and the new limiter are independent.

With `WithREST()` / `rest: true`:

| # | Change | What to do |
|---|---|---|
| R6 | A path no route matches answers **HTTP 404** with `Content-Type: application/json` (same body). This is planned to become the default in a later release. | Check health checks, uptime monitors and smoke tests that hit an unrouted path such as `/` and expect 200: point them at `/healthz` / `/readyz`. |
| R1 | **Typed error details** on the whole HTTP port of that server: each `details` entry of a JSON error body carries its type, `{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"...","domain":"..."}` instead of `{"reason":"...","domain":"..."}`, and a detail with no set fields is listed by its type instead of dropped. This is also available on its own: `grpcmux.WithTypedErrorDetails()` / `typedErrorDetails: true` (and `mux.SetDefaultOptions(mux.WithTypedErrorDetails())` for bodies handlers write with `mux.WriteHTTPErrorResponse`, which `WithREST` does not change). | A service with published REST clients that decode `details` strictly must not add `WithREST` to that server without telling them: the option is per server, so its existing gateway routes and its authentication errors change too. Clients that read `details[].reason` are unaffected. |
| R8 | An unrouted `HEAD` answers `404` like the `GET` it mirrors. (That `HEAD` is routed, authenticated and logged as its `GET` is a server-wide change of this release, with or without REST; see the server upgrade note.) | Monitors that send `HEAD` to an unrouted path and expect 200 need a routed, public path (`/healthz`). |
| R7 | The access log's `action` of a REST call is the RPC's full method (`/pkg.Service/Method`) instead of the first two path segments. | Dashboards that group REST traffic by path prefix now group by method, like gRPC and Connect. |

## 2. New

| API | Config key | Purpose |
|---|---|---|
| `grpcmux.WithTypedErrorDetails()`, `mux.WithTypedErrorDetails()` | `typedErrorDetails` | `"@type"` on error `details`; implied by `WithREST` |
| `grpcmux.WithREST()` | `rest` | REST routes from `google.api.http` annotations at registration, no generated gateway code |
| `grpcmux.WithRateLimit(ratelimit.Config)` | `rateLimit` | Declarative limits: default, per-method / per-service rules, burst, subject or IP keys |
| `grpcmux.WithRateLimitStore(ratelimit.Store)`, `(*redis.Redis).RateLimiter()` | `dependencies.redis` | Shared buckets (limits across replicas) |
| `grpcmux.WithRateLimitSubject(func(ctx) string)` | | Custom subject (for example per tenant) |
| `grpcmux.WithTimeouts(grpcmux.TimeoutConfig)` | `timeouts` | Server-side deadlines for unary calls |
| `tools/ratelimit`: `Config`, `Rule`, `Limiter`, `Store`, `MemoryStore`, `Limit`, `ParseLimit`, `GCRA` | | The limiter, usable without grpcmux |
| `tools/ip`: `TrustedProxies`, `ParseTrustedProxies` | `rateLimit.trustedProxies` | Client IP that a direct caller cannot forge |
| `mux.WithRouteResolver`, `mux.WithPreAuth`, `mux.WithNotFoundStatus` | | The hooks grpcmux uses for the above |
| Metrics `ratelimit_requests_total{rule,key,result}`, `ratelimit_store_errors_total` | | Registered when a limiter is configured |

No new dependency in the library module. The `examples` module gains `github.com/alicebob/miniredis/v2` for a test that runs two replicas against one Redis.

## 3. Adopting

### 3.1 REST

Before (generated gateway, registered through the in-memory connection so that interceptors run):

```yaml
# buf.gen.yaml
  - remote: buf.build/grpc-ecosystem/gateway
    out: gen/go
    opt: paths=source_relative
```

```go
userv1.RegisterUserServiceServer(gs, svc)
if err := userv1.RegisterUserServiceHandlerClient(ctx, gs.ServeMux(),
    userv1.NewUserServiceClient(gs.Conn())); err != nil {
    return nil, err
}
```

After:

```go
gs := grpcmux.NewServer(grpcmux.WithConfig(&cfg.Apis), grpcmux.WithREST()) // or apis.rest: true
userv1.RegisterUserServiceServer(gs, svc)
```

Remove the gateway plugin from `buf.gen.yaml`, delete the `*.pb.gw.go` files and the `RegisterXxxHandler...` calls. What changes for callers of such a service:

- Authentication, access logging and rate limiting run once (the HTTP middleware), not a second time on the in-memory gRPC hop; `WithNoAuthMethods("/pkg.Service/Method")` now also exempts the REST route.
- A server-streaming error **before the first message** is a normal error response (status + error body) instead of a `{"error":...}` line with the status of the code. Later errors are still an error line.
- R6 and R7 above.

A service that has no annotations yet: add them (see [rest.md](../guides/rest.md#mapping)), add `buf.build/googleapis/googleapis` to `deps` in `buf.yaml`, regenerate the Go code, enable `rest`. If an ingress only forwards `/package.Service/Method` paths, add the REST prefix to it ([rest.md](../guides/rest.md#routing-through-a-gateway)).

Pin the OpenAPI plugin (and the others) to exact versions as in [`examples/buf.gen.yaml`](../../examples/buf.gen.yaml), with `json_names_for_fields=false` unless the server runs with `useCamelCase`. The previously committed `examples/gen/openapi/api.swagger.json` documented camelCase names (`userId`) for a server that sends `user_id`.

### 3.2 Rate limiting

Before (`routerlimit`, wired in three places, keyed by header values):

```go
limiter := &routerlimit.Limiter{PersistenceFn: dep.Redis.RateLimitN, Config: &cfg.RouterLimit}
gs := grpcmux.NewServer(
    grpcmux.WithLimiter(limiter),
    grpcmux.WithHTTPMiddleWares(routerlimit.NewHandler(limiter)),
)
```

or a hand-written pre-auth middleware with its own bucket map and prefix list.

After:

```yaml
apis:
  rateLimit:
    default: {rate: 100/m, burst: 200}
    rules:
      - {match: "/auth.v1.AuthService/*", rate: 10/s, burst: 30, key: ip}
```

```go
gs := grpcmux.NewServer(
    grpcmux.WithConfig(&cfg.Apis),
    grpcmux.WithRateLimitStore(dep.Redis.RateLimiter()), // omit, or nil, for in-memory buckets
)
```

Differences to plan for when moving from `routerlimit`: buckets are token buckets (rate + burst) instead of fixed windows; the key is the authenticated subject or the client IP instead of header values you name (use `WithRateLimitSubject` for another identity); rules match exact methods or whole services, not arbitrary string prefixes; the client IP is the connection peer unless `trustedProxies` is configured (routerlimit's `ip` key believes the first `X-Forwarded-For` entry from anyone), so set `trustedProxies` when the service is behind an L7 proxy or all clients share one bucket; rejections are `RESOURCE_EXHAUSTED` with `RetryInfo` / `ErrorInfo` on every protocol (routerlimit's blacklist answered `ABORTED`; there is no blacklist); there are no `X-RateLimit-*` response headers. There is no automatic translation; keep `routerlimit` until the new rules have been checked against real traffic, and do not run both on the same traffic.

### 3.3 Deadlines

Before: an HTTP middleware that wraps the request context and a list of path substrings to exempt streams. After:

```yaml
apis:
  timeouts:
    default: 10s
    rules:
      - {match: "/agent.v1.AgentService/Generate", timeout: none}
```

Streams are exempt by construction (the deadline is part of the unary interceptor chains), it also covers the gRPC port, and expiry is `DEADLINE_EXCEEDED` instead of a cut connection. A plain HTTP route (`gs.Handle`) is not covered: it is not an RPC.

## 4. Observable contract

For services in other languages that must behave the same, and for contract tests. Everything below is what a client can observe; it is covered by `examples/internal/app/rest_test.go`, `ratelimit_test.go` and `grpcmux/{rest,ratelimit}_test.go`.

**REST**

- Path grammar: literal segments; `{field}` and nested `{a.b}` (one segment); `{field=<segments>}` where segments are literals, `*` (one segment) and a trailing `**` (the rest, slashes included), the variable's value being the matched segments joined with `/`; an optional `:verb` suffix on the last segment. Matching is on the URL-decoded path, method and path must both match, and among several matching routes the one registered last wins.
- Routes: exactly the `google.api.http` bindings (including `additional_bindings`) of unary and server-streaming methods; path variables URL-unescaped; `body: "*"`, `body: "<field>"`, no body; query parameters for the remaining fields by proto or JSON name; unknown query parameters ignored; the same field twice, or a value of the wrong type, is `400 invalid_argument`.
- Binding order: body first, then path variables (they overwrite what the body set), then query parameters (never for a field bound by the path or the body; none at all with `body: "*"`). Query keys: proto or JSON field name, dots for nested fields, a repeated key for repeated fields, `map[key]=value` for maps, a comma-separated list for a `FieldMask`, RFC 3339 for a `Timestamp`. An empty body with `body: "<field>"` leaves the field unset.
- Unknown JSON members (not declared by the message, at any depth) are ignored when decoding and never cause an error, also on `PATCH` and also with an explicit `update_mask`.
- Derived mask, exactly: walk the JSON object of the body against the body message; a member that is not a field (by proto name or JSON name) is skipped; a singular message field whose value is a non-empty object is descended into (`address.city`), and contributes nothing itself, so an object holding only unknown members yields no path; every other member yields the proto-name path of its field (scalars, `null`, `{}`, repeated and map fields, well-known types written as scalars, `google.protobuf.Any`); `google.protobuf.Struct` / `Value` objects yield one path per leaf using their keys as they are; paths are de-duplicated and sorted. A body with no declared member yields an empty mask, passed to the service as such.
- `PATCH` with a message body and an empty single `FieldMask` field: the mask is the set of fields present in the JSON body; `?update_mask=a,b` wins.
- Success: HTTP 200, `Content-Type: application/json`, protobuf JSON of the response (or of `response_body`), an empty message is `{}`.
- Failure: HTTP status by code (table in [rest.md](../guides/rest.md#errors)), `Content-Type: application/json`, body `{"error":"<lower_snake code name>","error_code":<int>,"error_description":"<message>","details":[<Any JSON with "@type">...]}`; `details` is absent when there are none (the `@type` form is the one a server with REST enabled writes; see R1); keys are camelCase (`errorCode`, `errorDescription`) when the server runs in camelCase mode.
- Unrouted path: 404, `error: "not_found"`, `error_description: "API NOT FOUND"`. A REST path with an HTTP method no route of the server handles: 501, `error: "unimplemented"`, `error_description: "Method Not Allowed"`; other routes for that method and path (custom handlers, catch-alls) are served, REST does not shadow them. `HEAD` behaves as `GET` without a body.
- Server streaming: HTTP 200, one JSON object per line, `{"result":<message>}`; an error after the first message is a last line `{"error":{"code":<int>,"message":"...","details":[...]}}`; an error before it is a normal failure response.
- Authentication: required unless the RPC's full method (or the path) is exempt; a missing or invalid credential is 401 with `error: "unauthenticated"` and nothing of the request is evaluated.

**Rate limiting**

- Configuration keys: `rateLimit.default`, `rateLimit.rules[]` with `match`, `rate` (`<n>/<s|m|h|d|duration>`), `burst` (default `n`), `key` (`subject` default, `ip`), `perMethod`, `unlimited`; `rateLimit.disabled`, `rateLimit.trustedProxies` (default: none), `rateLimit.ipv6Prefix` (default 64), `rateLimit.keyPrefix`.
- Client IP: the connection peer; `X-Forwarded-For` (rightmost entry that is not a trusted proxy) only when the peer is in `trustedProxies` (CIDRs, addresses, `private`). An IPv4 key value is the address; an IPv6 key value is its network, e.g. `2001:db8:1:2::/64`. Rates above one per microsecond and buckets needing more than ten years to refill are configuration errors.
- Matching: exact full method beats `/pkg.Service/*`, longer beats shorter, order irrelevant; `default` applies when no rule of its key matches; `ip` rules run before authentication, `subject` rules after; anonymous requests fall back to the client IP at the `subject` stage.
- Algorithm: GCRA, defined by `tools/ratelimit/testdata/gcra_cases.json` (pure function with a fake clock: `take(tat, now, interval, burst, n) -> allowed, new tat, remaining, retry_after, reset_after`, microseconds). The Redis form is the single file `tools/ratelimit/gcra.lua`, to be loaded verbatim by every backend: `KEYS[1]` the bucket key, `ARGV` = interval in microseconds (`period / n`, at least 1), burst, tokens to take; result `{allowed 0|1, remaining, retry_after µs, reset_after µs}`. A stored value further ahead than `now + interval*burst` (the clock stepped back) is corrected to that and written, also on a denied request. On a store error: check against an in-process bucket of the same rule (neither fail open nor fail closed).
- Bucket: token bucket of size `burst`, one token every `period / n`; a denied request does not consume anything. Shared-store key: `<keyPrefix>:<rule>|<subject|ip>|<value>`, plus `|<method>` with `perMethod`, plus `#<interval µs>:<burst>` (for example `ratelimit:default|subject|u:42#20000:100`), where `<rule>` is the `match` or `default` and a subject value is `u:<user id>` or `c:<client id>`. The limit is part of the key on purpose: a key must only ever be used with one interval and burst, so a changed limit starts fresh buckets (two versions of a service, or two services, with different limits for the same rule never read or rewrite each other's buckets). The stored value is the time (microseconds since the epoch, Redis server clock) at which the bucket is full again, with a matching expiry.
- Limited response: code `RESOURCE_EXHAUSTED`, message `rate limit exceeded`, details `google.rpc.RetryInfo{retry_delay}` then `google.rpc.ErrorInfo{reason: "RATE_LIMIT_EXCEEDED", domain: "ratelimit", metadata: {rule, key}}`; `Retry-After` in whole seconds (rounded up, minimum 1) as an HTTP response header on the HTTP port and as `retry-after` header metadata on the gRPC port; HTTP status 429 for REST and Connect unary, 200 with in-protocol status for Connect streams, gRPC and gRPC-Web.
- Never limited: `/healthz`, `/readyz`, `/favicon.ico`, `grpc.health.v1.Health/*`, `OPTIONS`.

**Deadlines**: `timeouts.default`, `timeouts.rules[]` with `match`, `timeout` (duration, or `0` / `none`); unary calls only; expiry is `DEADLINE_EXCEEDED` (HTTP 504).

## 5. Verification

```bash
go build ./... && go vet ./... && go test -race ./... && golangci-lint run ./... && go fix -diff ./...
(cd examples && go build ./... && go test ./...)
(cd examples && buf lint && buf generate && git diff --exit-code gen web/connect/src/gen)   # generated code is reproducible
```
