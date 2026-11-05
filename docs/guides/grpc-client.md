# gRPC clients

```go
users, err := grpcmux.NewClient(ctx, "dns://users.svc:8081?timeout=5s&try=3", pb.NewUserServiceClient)
resp, err := users.GetUser(ctx, &pb.GetUserRequest{UserId: "42"})
```

`grpcmux.NewClient[T]` (and `NewClientWithURL`) builds a connection with `NewClientConnWithURI` and registers its `Close` with `graceful`. `NewClientConn` returns just the connection. As a dependency field, use the generated client interface plus `dependencies.WithNewFns(pb.NewUserServiceClient)` ([dependencies.md](dependencies.md)).

## Targets

| URI | gRPC target |
|---|---|
| `dns://svc.ns.svc:8081` | `svc.ns.svc:8081` (DNS resolver; with the default `round_robin` policy every resolved address is used) |
| `dns://svc.ns.svc:8081/prefix/` | as above; every method is prefixed with `/prefix` |
| `dns:///svc.ns.svc:8081` | `dns:///svc.ns.svc:8081` |
| `unix:///run/app.sock` | `unix:///run/app.sock` |
| `polaris:///Production/svc:8081` | passed to a custom registered resolver |

**TLS** is used when `secure=true`, or the scheme is `https` or `grpcs`; `secure=false` forces plaintext. Every other scheme (including `dns`) is plaintext. TLS uses the system roots and TLS 1.2 or newer.

For Kubernetes services, use a headless Service (or rely on connection recycling: grpcmux servers close connections after a minute, so clients re-resolve and rebalance).

## URI parameters

| Parameter | Meaning |
|---|---|
| `timeout=5s` | Per-call timeout |
| `try=N` | Retry failed calls up to N times (go-grpc-middleware retry interceptor; UNAVAILABLE and RESOURCE_EXHAUSTED) |
| `retryPolicy=native` | With `try`, use gRPC's built-in retry policy instead: UNAVAILABLE only, exponential backoff 0.1s to 2s, at most 5 attempts |
| `backoff=true` | Reconnect with gRPC's default connection backoff (2s minimum connect timeout) |
| `keepalive=30s` | Ping idle connections at this interval (keeps NAT and load-balancer mappings alive). The server must allow the interval; grpcmux servers accept 10s or more, gRPC's default servers only 5m or more |
| `waitForReady=true` | Calls wait for the connection to become ready instead of failing fast |
| `block=true` | `NewClientConnWithURI` waits until the connection is ready (bounded by ctx) |
| `loadBalancingPolicy=pick_first` | Load-balancing policy (default `round_robin`; names are case-sensitive) |
| `log=true`, `logBody=true` | Client access log (with bodies, `password` masked) |
| `metrics=false` | Disable Prometheus client metrics |
| `tracing=true` | OpenTelemetry client spans |
| `secure=true` / `secure=false` | Force TLS on or off |

Extra dial options: pass them to `NewClient(ctx, uri, newFn, opts...)`, or `dependencies.WithGRPCDialOptions(...)` for clients created by `dependencies.Init`.
