# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`github.com/ti/common-go` is a Go **library** (no `main` of its own) for building proto-first microservices: a single server that serves gRPC, grpc-gateway REST, ConnectRPC and plain HTTP, plus URI-driven dependency injection for databases, Redis, Kafka, HTTP and gRPC clients. Requires **Go 1.27** (generic methods, `encoding/json/v2`, stdlib `uuid`, `math/rand/v2` are used — see `docs/GO_1_27_UPGRADE.md`).

## Commands

```bash
go build ./...
go vet ./...
go test ./... -race
go test ./dependencies/database/mock -run TestName -v   # single test
```

- `go.sum` is listed in `.gitignore` (and so is `go.mod`, though `go.mod` is tracked anyway). The local `go.sum` can go stale, and then builds fail with "missing go.sum entry". Run `go mod download` to fix it.
- No repo-level Makefile or golangci config. Commit history shows golangci-lint is used, so keep code lint-clean.
- Proto code generation (`buf generate`) is only used under `docs/tutorial/restful/` (it has its own Makefile and `buf.yaml`). The only proto in the library itself is `grpcmux/mux/error.proto`, and its generated `error.pb.go` is committed.
- `docs/examples/*.go` use `//go:build ignore`. `docs/tutorial/**` is compiled as part of `./...`, so API changes must keep the tutorials building.
- Commits follow Conventional Commits with a scope, e.g. `feat(grpcmux): ...`, `fix(grpcmux): ...`, `refactor(async): ...`.

## Architecture

### Startup flow used by consumers
`config.Init(ctx, uri, &cfg)` → `dependencies.Init(ctx, &deps, cfg.Dependencies)` → `grpcmux.NewServer(grpcmux.WithConfig(&cfg.Apis))` → register services → `server.Start()`.

- **`config`** binds a file/folder URI into a struct via `github.com/ti/objectbind`, which supports live reload. If the URI is empty, it falls back to the `CONFIG_PATH` env var and then the `-c` flag (default `configs/config.yaml`). A `Log.Level` field gets a hook that calls `log.SetLevel`. Dependency-typed fields in the config are initialized too (`initDeps`). `config.Binder()` exposes the binder so you can add field hooks.
- **`dependencies`** uses reflection. It walks struct fields and matches each field name (case-insensitive) to a key in a `map[string]string` of URIs. A field is resolved in this order:
  1. The pointer type implements `Init(ctx, *url.URL) error` or `Init(ctx, string) error`.
  2. A constructor was registered with `dependencies.WithNewFns(...)`. This can be a `pb.NewXxxClient` (a gRPC client is built via `grpcmux.NewClientConnWithURI`) or `func(ctx, *url.URL|string) (T, error)`.

  If the type also implements `Close(ctx) error`, it is registered with `graceful.AddCloser`. Fields are required unless tagged `required:"false"`. By default fields initialize concurrently via `async.Future`. `InitMulti` does the same thing for nested config groups. A field of type `dependencies.Dependency` is a placeholder that is skipped.
- **Scheme registries:** `database.DB` dispatches on the URI scheme to implementations that register themselves in `init()` with `database.RegisterImplements`: `mysql`/`postgres` (`dependencies/sql`), `mongodb` (`dependencies/mongo`) and `mock` (`dependencies/database/mock`, an in-memory store for tests). `broker` works the same way (`kafka`). The adapter package must be blank-imported for its scheme to exist. `database.Database` is the shared CRUD/stream/page interface across SQL and Mongo, so new backends must implement all of it.

### grpcmux (the server)
- `NewServer` builds one `grpc.Server` with chained interceptors from `NewFullMiddleWare` (`server_opts.go`: logging, metrics, recovery, auth, validator, rate limiting) and an HTTP `mux.ServeMux` that wraps grpc-gateway's `runtime.ServeMux`.
- **In-memory gRPC connection:** a second `bufServer` on an in-process listener lets HTTP handlers call gRPC services without going through the network. `Server.Conn()` returns it.
- `RegisterService` registers on gRPC. With `WithAutoHTTP()` it also exposes every method as `POST /{Service}/{Method}`. `connect.go` exposes services over the ConnectRPC protocol.
- **HTTP handler layering, in `startHTTP`:** custom `Handle` routes go into `httpServerMux`, with the gateway mux as the fallback at `/`, and the whole thing is wrapped once by `mux.Middleware`. After that comes the WebSocket upgrade dispatch, then `otelhttp` when tracing is on. Serving is h2c (HTTP/1.1 + cleartext HTTP/2) by default, or TLS with `WithTLS`. Read/Write timeouts are deliberately 0 so streaming RPCs aren't cut off. Don't add them.
- **`grpcmux/mux/interceptor.go` (HTTP path):** request-derived log fields (action, ip, request_id, …) are injected **before** auth so that 401s still produce a complete log line. Auth fields (user_id, …) are added after auth succeeds. `action` is the path cut to its first two segments (`rpcAction`) so logs stay low-cardinality.
- `Start()` runs gRPC, metrics and HTTP through `graceful.Start` and registers `Close`. `graceful` handles signals and runs every registered closer.
- Env vars: `PANIC_RECOVERY=false` disables recovery. `PRE_STOP` sets a delay before shutdown (see `Close`).

### log
`log` wraps `slog` and writes JSON. The request-scoped logger travels in the context: `log.NewContext`, `log.Inject(ctx, fields)` to add fields to the current request's logger, `log.Extract(ctx)`, and `.Action(name)`.

## Gotchas
- Use `encoding/json/v2` everywhere. The one exception is `grpcmux/logging/audit.go`, which has to use v1 `json.RawMessage` because a gRPC interface requires it. Use `jsontext.Value` where you would have used `json.RawMessage`.
- `async.Future.Async` is a generic method that takes exactly one input and one output. To pass several arguments, wrap them in a small struct (see `initItemArgs` in `dependencies/dependencies.go`).
- HTTP JSON uses snake_case by default. `WithUseCamelCase()` / `useCamelCase: true` switches it (see `docs/JSON_FORMAT.md`).
- Parts of `README.md` are out of date. It links to `docs/BUF_GUIDE.md` and `docs/OPTIMIZATION_SUMMARY.md`, which don't exist, and its examples use `dependencies.Database`/`dependencies.Redis`, which aren't defined in `dependencies/`. Check the code rather than the README.

## Maintenance cycle
Modernization runs quarterly following `docs/maintenance/playbook.md`. `scripts/modernize-scan.sh` is read-only: it writes a report to `docs/maintenance/scans/<date>.md` and also builds/vets/tests the reference consumer `~/Workspace/projects/auth/backend` against this checkout using a temporary `go.work`. Each cycle's plan is `docs/maintenance/cycles/<YYYY>-Q<n>.md`. API-compatibility, deprecation and behavior-change rules are in playbook §2.
