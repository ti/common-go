# Documentation

## Guides

| Guide | Covers |
|---|---|
| [Getting started](guides/getting-started.md) | A new service from proto to running server |
| [Configuration](guides/configuration.md) | `config.Init`, `grpcmux.Config`, environment variables |
| [Dependencies](guides/dependencies.md) | URI-driven dependency injection, URI reference for every client |
| [Server](guides/server.md) | `grpcmux`: protocols, routing, middleware, interceptors, auth, errors, health, reflection, graceful shutdown and the drain hook, options |
| [ConnectRPC](guides/connectrpc.md) | `RegisterConnectService`, `RegisterConnectStreams`, connect-go handlers, browser clients |
| [REST](guides/rest.md) | `WithREST`: REST routes from `google.api.http` annotations, mapping rules, errors, streaming, OpenAPI generation |
| [Rate limiting and deadlines](guides/rate-limiting.md) | `rateLimit` rules, subject and IP keys, trusted proxies, Redis-backed buckets, responses per protocol, `timeouts` |
| [JSON format](guides/json-format.md) | snake_case vs camelCase, what the setting covers |
| [Databases](guides/database.md) | `database.Database`, drivers, connection strings, paging |
| [HTTP client](guides/http-client.md) | `dependencies/http`: requests, retries, pooling, proxies |
| [OIDC authentication](guides/oidc.md) | `dependencies/oidc`: auth URI, token validation, claim access rules, grpcmux auth function, OAuth calls |
| [gRPC client](guides/grpc-client.md) | `grpcmux.NewClient`, targets, TLS, URI parameters |
| [Background work](guides/background-work.md) | `background`: supervised goroutines, periodic tasks, job queue with status and watch, shutdown, storage seam, when to use something else |
| [Logging](guides/logging.md) | JSON log schema and the `log` API |
| [Tracing](guides/tracing.md) | `tracing.Init`: OpenTelemetry export from the `OTEL_*` environment, probe spans, trace IDs in logs |
| [Deployment](guides/deployment.md) | Kubernetes probes, shutdown, load balancing, Envoy Gateway |

Runnable code: the [examples](../examples/) module.

## Upgrading

- [2026-Q4](upgrades/2026-Q4.md): migration guide for services on earlier versions
- [Go 1.27](upgrades/go-1.27.md): Go 1.27 features adopted by the library
- [Background work](upgrades/background-work.md): package `background`, and the `graceful`/`async` behavior changes
- [Server platform](upgrades/server-platform.md): drain hook, stream interceptors on the HTTP port, reflection, auth exemptions, protocol errors, package `tracing`, and the behavior changes that came with them

- [REST and rate limiting](upgrades/rest-and-rate-limiting.md): `WithREST`, `WithRateLimit`, `WithTimeouts`, the error-body and limiter behavior changes, and the observable contract for other-language backends

## Maintenance

- [Playbook](maintenance/playbook.md): the quarterly modernization process
- [Cycles](maintenance/cycles/): plan and results of each cycle
- [Scans](maintenance/scans/): output of `scripts/modernize-scan.sh`

## Package READMEs

- [config](../config/README.md)
- [dependencies/database](../dependencies/database/README.md)
- [dependencies/sql](../dependencies/sql/README.md)
- [dependencies/database/mock](../dependencies/database/mock/README.md)
