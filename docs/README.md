# Documentation

## Guides

| Guide | Covers |
|---|---|
| [Getting started](guides/getting-started.md) | A new service from proto to running server |
| [Configuration](guides/configuration.md) | `config.Init`, `grpcmux.Config`, environment variables |
| [Dependencies](guides/dependencies.md) | URI-driven dependency injection, URI reference for every client |
| [Server](guides/server.md) | `grpcmux`: protocols, routing, middleware, interceptors, auth, errors, health, graceful shutdown, options |
| [ConnectRPC](guides/connectrpc.md) | `RegisterConnectService`, connect-go handlers, browser clients |
| [JSON format](guides/json-format.md) | snake_case vs camelCase, what the setting covers |
| [Databases](guides/database.md) | `database.Database`, drivers, connection strings, paging |
| [HTTP client](guides/http-client.md) | `dependencies/http`: requests, retries, pooling, proxies |
| [gRPC client](guides/grpc-client.md) | `grpcmux.NewClient`, targets, TLS, URI parameters |
| [Logging](guides/logging.md) | JSON log schema and the `log` API |
| [Deployment](guides/deployment.md) | Kubernetes probes, shutdown, load balancing, Envoy Gateway |

Runnable code: the [examples](../examples/) module.

## Upgrading

- [2026-Q4](upgrades/2026-Q4.md): migration guide for services on earlier versions
- [Go 1.27](upgrades/go-1.27.md): Go 1.27 features adopted by the library

## Maintenance

- [Playbook](maintenance/playbook.md): the quarterly modernization process
- [Cycles](maintenance/cycles/): plan and results of each cycle
- [Scans](maintenance/scans/): output of `scripts/modernize-scan.sh`

## Package READMEs

- [config](../config/README.md)
- [dependencies/database](../dependencies/database/README.md)
- [dependencies/sql](../dependencies/sql/README.md)
- [dependencies/database/mock](../dependencies/database/mock/README.md)
