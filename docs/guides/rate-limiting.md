# Rate limiting and deadlines

grpcmux limits how often a caller may call the server, on every protocol, from configuration alone:

```yaml
apis:
  rateLimit:
    default: {rate: 50/s, burst: 100}                 # per caller: subject, or IP when anonymous
    rules:
      - {match: "/auth.v1.AuthService/*", rate: 5/s, burst: 10, key: ip}   # sign-in: per IP, before authentication
      - {match: "/note.v1.NoteService/WatchNotes", rate: 1/s, burst: 5}    # opening streams
      - {match: "/internal.v1.SyncService/*", unlimited: true}             # trusted callers
dependencies:
  redis: redis://redis:6379                           # optional: share the limits across replicas
```

```go
gs := grpcmux.NewServer(
    grpcmux.WithConfig(&cfg.Apis),                                    // apis.rateLimit
    grpcmux.WithRateLimitStore(cfg.Dependencies.Redis.RateLimiter()), // nil Redis: in memory
)
```

With the `rateLimit` block absent nothing is limited. In code, `grpcmux.WithRateLimit(ratelimit.Config{...})` takes the same structure.

## Rules

| Key | Meaning |
|---|---|
| `default` | The limit for every method that no rule **of the same `key`** matches. Optional |
| `rules[].match` | A full method or HTTP path (`/pkg.Service/Method`, `/auth/v1/token`), or a whole service or path tree (`/pkg.Service/*`, `/*`). `/pkg.Service/*` does not match `/pkg.ServiceAdmin/...`. The most specific match wins (an exact method over its service, a longer tree over a shorter one), whatever the order |
| `rate` | Sustained rate, `<count>/<period>`: `10/s`, `600/m`, `1000/h`, `5/10s`, `1/d` |
| `burst` | Bucket size: how many requests an idle caller may send at once. Default: the count of `rate` (`100/m` allows 100 at once, then one every 600 ms) |
| `key` | `subject` (default) or `ip`, see below |
| `perMethod` | `true`: every matched method has its own bucket. By default the methods under one rule share the caller's bucket |
| `unlimited` | `true`: the matched methods are exempt from limits of either key |
| `disabled` | (top level) turn the limiter off without deleting the rules |
| `trustedProxies` | (top level) whose `X-Forwarded-For` is believed; default nobody, see [Client IP](#client-ip) |
| `ipv6Prefix` | (top level) prefix length an IPv6 client is limited by, default 64 |
| `keyPrefix` | (top level) namespace of the keys in a shared store, default `ratelimit`. Services that share a Redis need different prefixes unless they are meant to share budgets |

What a rule matches is the **RPC's full method**: `/pkg.Service/Method` for gRPC, Connect and gRPC-Web, and for a REST route ([rest.md](rest.md)) the method it is bound to, so one rule covers every protocol. Other HTTP routes (`gs.Handle`, gateway routes on `gs.ServeMux()`) are matched by URL path. `/healthz`, `/readyz`, `/favicon.ico`, `grpc.health.v1.Health` and CORS preflights are never limited. WebSocket upgrades (`gs.HandleWebSocket`) bypass the HTTP middleware and are not limited.

The algorithm is a token bucket in its GCRA form: `rate` refills the bucket, `burst` is its size, and the whole state of a bucket is one timestamp. It was chosen over the fixed-window counter of the older limiter (`redis.RateLimitN`, unchanged) because a window allows twice the limit across a window edge, cannot express a burst separately from a rate, and cannot say exactly when to retry. The arithmetic is one pure function, `ratelimit.GCRA(tat, now, interval, burst, n)`, pinned by language-neutral cases with a fake clock in [`tools/ratelimit/testdata/gcra_cases.json`](../../tools/ratelimit/testdata/gcra_cases.json). An invalid configuration (unparsable rate, unknown key, a rate above one request per microsecond, a bucket that would take more than ten years to refill) stops the process at startup: a limiter that silently does not limit would be worse. The in-memory store measures time with the monotonic clock; in Redis, where the server's wall clock is used, a clock that steps backwards (a failover) empties the affected buckets instead of locking their keys out for the size of the step.

### Keys: who a bucket belongs to

- **`key: subject`** is checked **after authentication**. The subject is the user ID of the request's `mux.AuthInfo`, or its client ID when there is no user (service accounts); `dependencies/oidc`'s `AuthFunc` sets both. A request without identity (a method exempt from authentication, a server without AuthFunc, guest mode) is limited by client IP instead. `grpcmux.WithRateLimitSubject(func(ctx) string)` replaces how the subject is derived, for example to limit per tenant.
- **`key: ip`** is checked **before authentication**. It therefore also counts requests whose credentials are then rejected, and a limited request never reaches the credential check: use it for sign-in, token, password-reset and other public methods, and for anything where guessing must be slow.

Both kinds can apply to one request: with the example above, a call to `auth.v1.AuthService/Token` is charged to the per-IP rule before authentication and to the `default` rule after it. Each stage picks its most specific rule of its own key. To add a global per-IP ceiling in front of everything, add `{match: "/*", rate: 200/s, key: ip}`.

### Client IP

**By default the client IP is the peer address of the connection and `X-Forwarded-For` is ignored.** A forwarded header is only as trustworthy as whoever wrote it, and the server cannot know that by itself: behind an L4 load balancer or kube-proxy SNAT the peer has a private address and the header is the client's own, so believing it would let any caller pick a fresh bucket per request. Trust therefore has to be configured:

```yaml
rateLimit:
  trustedProxies: [10.42.0.0/16]     # the pod CIDR of the gateway; or [private]
```

| `trustedProxies` | Meaning |
|---|---|
| absent, empty, or `[none]` | Trust nobody. Right for a server that clients reach directly or through L4 only |
| CIDRs / addresses | Believe `X-Forwarded-For` from these peers. The precise choice behind an L7 proxy |
| `[private]` | Believe it from loopback, RFC 1918, 100.64.0.0/10, link-local and IPv6 unique-local peers. Only correct when every private peer is a proxy that sets the header |

**Behind an L7 proxy (Envoy Gateway, an ALB, nginx) you must set it.** Otherwise every client is seen as the proxy and all of them share the proxy's bucket: IP-keyed rules then throttle everybody together instead of each client. The effective trust model is logged once at startup (`action: ratelimit`, "trusted proxies: ...").

From a trusted peer the header is read from the right, skipping trusted proxies; the first address that is not one is the client. A client cannot change that entry by prepending others, because the proxy appends the real address after them. On the gRPC port the header is the `x-forwarded-for` metadata, under the same rule.

An **IPv6** client is limited by its /64, not by its single address: a subscriber controls the whole /64 and could use a new address for every request. `ipv6Prefix` changes the length (128 = per address; 56 or 48 for providers that delegate larger prefixes). IPv4 is always per address.

The same address is what the server logs and hands to handlers, whether or not any rate-limit rule is configured: the `ip` field of the access log (both ports), the `ip` incoming metadata (the key of `routerlimit` rules on `ip`) and `grpcmux.ClientIP(ctx)`. A handler that stores or logs the caller's address uses `grpcmux.ClientIP(ctx)` and never reads `X-Forwarded-For`, `X-Real-IP`, `Forwarded`, `True-Client-IP` or `CloudFront-Viewer-Address` itself: without `trustedProxies` those are whatever the caller sent. `tools/ip.TrustedProxies` is the same logic for code outside a request. `ip.GetIPFromHTTPRequest` (first `X-Forwarded-For` entry from anyone) is deprecated and no longer used by the server.

## Counting

| Traffic | Where it is counted |
|---|---|
| gRPC port | interceptors: `ip` rules first in the chain, `subject` rules after authentication (inside metrics and logging) |
| HTTP port (Connect, gRPC-Web, gRPC, REST, other routes) | HTTP middleware: `ip` rules before authentication, `subject` rules after it and before `WithHTTPMiddleWares` |
| `gs.Conn()` (in-memory connection) | never: a gateway route calling back through it has been counted by the HTTP middleware |
| Streams | once, when the stream is opened; messages are not counted |

Every request costs one bucket operation per stage that has a matching rule, and is never counted twice. Both ports share the buckets: a caller's budget is the same whichever protocol it uses.

## Where the buckets live

- **In memory** (default): limits hold per process. Behind a load balancer with N replicas a caller can get up to N times the limit. No goroutine, no dependency; idle buckets are dropped lazily.
- **Redis**: `grpcmux.WithRateLimitStore(redis.RateLimiter())` with a `*redis.Redis` dependency ([dependencies.md](dependencies.md)). Limits hold across replicas. One check is one `EVALSHA` round trip that reads and writes a single key (it works on Redis Cluster), timed on the Redis server's clock, with a 200 ms timeout; keys expire by themselves when the bucket is full again. `RateLimiter()` on a nil `*redis.Redis` returns nil and `WithRateLimitStore(nil)` keeps the buckets in memory, so an optional Redis dependency needs no `if`.

  The script is one file, [`tools/ratelimit/gcra.lua`](../../tools/ratelimit/gcra.lua) (`ratelimit.RedisScript`); its header specifies the key format, the three arguments and the four results. A backend in another language that shares the Redis loads the same file verbatim. It is tested against the same JSON cases as the Go function.

  **On a Redis error the limiter neither fails open nor fails closed**: the request is checked against an in-memory bucket of the same rule in this process (and a warning is logged at most every ten seconds, `ratelimit_store_errors_total` counts). An outage degrades to per-replica limits; it does not lift them and it does not reject requests that are within them.

The limit itself is part of a bucket's key (`ratelimit:default|subject|u:42#20000:100`: interval in microseconds and burst). Changing a rule's rate or burst therefore starts fresh buckets, and during a rolling deploy the old and the new replicas each enforce their own limit on their own buckets instead of reinterpreting each other's; the old keys expire by themselves. For the length of the rollout a caller can get the old and the new allowance once each.

Any type with `Take(ctx, key, limit, n) (ratelimit.Result, error)` is a `ratelimit.Store`. A store is always called with the same limit for a given key.

## What the client sees

A limited call fails with `RESOURCE_EXHAUSTED`, message `rate limit exceeded`, and two details: `google.rpc.RetryInfo` (`retry_delay`, exact) and `google.rpc.ErrorInfo` with reason `RATE_LIMIT_EXCEEDED`, domain `ratelimit` and metadata `rule` (the matched rule, `default` for the default) and `key` (`subject` or `ip`, what the bucket belongs to).

| Protocol | Response |
|---|---|
| REST, other HTTP routes | HTTP `429`, `Retry-After: <seconds>`, the JSON error body: `{"error":"resource_exhausted","error_code":8,"error_description":"rate limit exceeded","details":[{"@type":".../google.rpc.RetryInfo","retry_delay":"2s"},{"@type":".../google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED","domain":"ratelimit","metadata":{"rule":"default","key":"ip"}}]}` |
| Connect unary | HTTP `429`, `Retry-After`, the Connect error: `{"code":"resource_exhausted","message":"rate limit exceeded","details":[...]}` |
| Connect streams | HTTP `200`, `Retry-After`, the error in the end-of-stream message |
| gRPC and gRPC-Web on the HTTP port | HTTP `200`, `Retry-After`, `grpc-status: 8` with `grpc-status-details-bin` |
| gRPC port | status `RESOURCE_EXHAUSTED` with the details, response header metadata `retry-after: <seconds>` |

The `@type` keys in the REST body are there when the server renders typed error details (`WithREST` or `WithTypedErrorDetails()`, see [rest.md](rest.md#errors)); otherwise the two details are bare: `{"retry_delay":"2s"},{"reason":"RATE_LIMIT_EXCEEDED",...}`.

`Retry-After` is whole seconds, rounded up, at least 1. A client should wait that long (or `retry_delay`) before retrying; retrying earlier is counted and refused again. No `X-RateLimit-*` headers are sent on allowed responses.

## Metrics and logs

| Metric | Labels | Meaning |
|---|---|---|
| `ratelimit_requests_total` | `rule` (the `match`, or `default`), `key` (`subject`, `ip`: the rule's key), `result` (`allowed`, `limited`) | Checks by outcome. Label values come from the configuration, so cardinality is bounded |
| `ratelimit_store_errors_total` | | Checks that fell back to memory because the shared store failed |

Limited calls on the HTTP port are logged by the access log with `code: 8`, `rate_limit_rule` and the HTTP status that was sent (`429`, or `200` for the protocols that carry the status in-band). On the gRPC port a call refused by a `subject` rule is logged and counted in `grpc_server_handled_total` like any failed call; one refused by an `ip` rule is stopped before the logging interceptor and appears in `ratelimit_requests_total` only.

## Limiting at the gateway instead

Envoy Gateway can limit before a request reaches any pod (`BackendTrafficPolicy.rateLimit`, local or global with its own Redis). It protects the cluster from volume the pods should never see, and it is the same for every backend language. What it cannot do: it does not know the authenticated subject unless the gateway also validates the token, and its rejection is a bare HTTP 429, which gRPC and Connect clients read as `unavailable` with no retry delay and no reason (a Connect client only reads `resource_exhausted` from a Connect error body).

Draw the line by what the limit knows. Coarse per-IP or per-route ceilings against floods belong at the gateway, set well above anything a legitimate client does. Limits that are part of the API's behavior — per user, per method, sign-in attempts, with an error the client can act on — belong in the service, with this limiter. Using both is normal; the in-process limits should be the ones a well-behaved client can actually hit.

## Tuning notes

- Start with a generous `default` keyed by subject and tight `ip` rules on the few public methods. A positive list of limited methods (rules only, no default) is also fine.
- Server-to-server callers inside the cluster share few source addresses: exempt their services (`unlimited: true`) or give them their own rule, or an IP rule will throttle them.
- Limits are a safety valve for the process, not billing-grade quotas: in memory they are per replica, and with Redis an outage falls back to per replica.

## Server-side deadlines

```yaml
apis:
  timeouts:
    default: 10s
    rules:
      - {match: "/report.v1.ReportService/*", timeout: 60s}
      - {match: "/agent.v1.AgentService/Generate", timeout: none}
```

`timeouts` (or `grpcmux.WithTimeouts(grpcmux.TimeoutConfig{...})`) gives **unary** calls a deadline on the server, on every protocol, so a client that sends none (or a very long one) cannot keep a handler running forever. The earlier of the client's deadline (`grpc-timeout`, `Connect-Timeout-Ms`) and the configured one applies. `match` follows the same rules as for rate limits; `0` or `none` lifts the default for the matched methods.

A call that runs into the deadline has its context canceled and fails with `DEADLINE_EXCEEDED` (HTTP 504), provided the handler honors its context, as database and RPC clients do. **Streaming calls are never bounded**: a deadline would cut a healthy stream. Bound those in the handler or with the client's deadline, and see [server.md](server.md#long-lived-streams-and-the-drain) for ending them at shutdown.

## The older limiter

`tools/routerlimit` (`grpcmux.WithLimiter` plus `routerlimit.NewHandler` in `WithHTTPMiddleWares`, backed by `redis.RateLimitN`) keeps working and is independent of the above. It matches path prefixes, keys buckets by request header or metadata values that you name (a request that has none of them is not limited), counts in fixed windows and has no notion of authentication. New services should use `rateLimit`; do not run both for the same traffic. `tools/ratelimit.RateLimiter` is a standalone fixed-window counter for application logic such as "3 emails per address per hour".
