# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Releases are identified by commit until semver tags are introduced.

## 2026-Q4 modernization (unreleased)

Upgrade guide: [docs/upgrades/2026-Q4.md](docs/upgrades/2026-Q4.md).

### Security
- Fixed 10 vulnerabilities reachable from library code by upgrading grpc (GO-2026-6061, -6348, -6443), x/net (GO-2026-6603, -6610, -6611, -6612, -6617), x/text (GO-2026-5970) and the toolchain to go1.27.3 (crypto/x509, GO-2026-6809). mongo-driver v2.9.3 also includes fixes for CVE-2026-81521, -88031, -107324 and -107325.

### Fixed
- **Graceful shutdown never ran**: `graceful.Start` installed its signal handler only after every server had returned, so SIGTERM killed the process with no health flip, no `PRE_STOP` wait, no drain and no closers.
- Data race in `graceful.AddCloser` during concurrent dependency initialization.
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
- `log.Fatal` dropped the logger's fields (it used the standard library logger).
- `dependencies/sql`: `log=` was ignored, and package options were left in the driver DSN unless another option was set.

### Added
- Interceptor parity: the unary interceptor chain (protovalidate, PGV, authz, metrics, custom interceptors, recovery) runs on the ConnectRPC bridge, auto-HTTP routes and connect-go v2 handlers; `WithoutHTTPInterceptors()` / `disableHTTPInterceptors` opts out.
- protovalidate (`buf.validate`) enforcement on every protocol.
- Connect bridge: gzip, `Connect-Timeout-Ms`, protocol-version check, GET for `NO_SIDE_EFFECTS` methods, error details, header/trailer metadata, 415 for streaming content types, `WithMaxRequestBodyBytes`.
- `Server.ConnectServer()`: connect-go v2 `connect.Server` mounted on the HTTP port (Connect, gRPC-Web, gRPC), with the interceptor chain and gRPC-status-to-`connect.Error` conversion.
- `/readyz` (readiness, follows drain and health checkers); `/healthz` stays liveness.
- Server options: `WithHTTPTimeouts`, `WithMaxConcurrentStreams`, `WithKeepalive` (default enforcement accepts 10s pings).
- gRPC client URI options: `keepalive=`, `waitForReady=`, `retryPolicy=native`; `loadBalancingConfig` service config.
- HTTP client: tuned per-client transport, `maxIdlePerHost=`, `idleTimeout=`, `h2c=`, `backoff=`, `retryNonIdempotent=`, idempotency-aware retries of 502/503/504.
- `async.Future.Go`; `database.IsNotFound` / `IsAlreadyExists`; `graceful` `GRACEFUL_TIMEOUT`.
- `examples/` module (UserService on all protocols with streaming, protovalidate, AIP-193 errors, Kubernetes and Envoy manifests, browser client) and `docs/guides/`.
- CI (tidy, build, vet, race tests, golangci-lint v2.15.0, `go fix -diff`, govulncheck, examples, English-only check), tracked `go.sum`, quarterly maintenance playbook and scan script.

### Changed
- **Breaking:** `WithConnectOptions` (connect-go v1) removed; use `gs.ConnectServer()` with connect-go v2 generated code. connect-go v1 is no longer a dependency.
- HTTP/2 on the HTTP port uses only the standard library (`http.Protocols`, `http.HTTP2Config`); `golang.org/x/net/http2` is no longer used by the server.
- `PRE_STOP=0` means no wait (was 1 minute); values over 5 minutes are capped (were reset to 1 minute).
- `graceful.Start` handles signals in any goroutine, returns after shutdown, and shuts down when a server fails to start; closers share one deadline.
- HTTP client metrics drop the query string from the path label.
- All dependencies upgraded (grpc v1.85, grpc-gateway v2.33, OpenTelemetry v1.47, mongo-driver v2.9.3, sarama v1.61, prometheus client v1.25, ...); `go fix` modernizers applied.

### Removed
- `docs/tutorial` and `docs/examples` (replaced by `examples/` and `docs/guides/`).
- `dependencies/proxy` (a `package main` forward proxy and a Cloudflare VLESS script) and `dependencies/http/example` (now `examples/cmd/httpclient`).
