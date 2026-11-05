# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`github.com/ti/common-go` is a Go **library** (no `main` of its own) for building proto-first microservices: a single server that serves gRPC, grpc-gateway REST, ConnectRPC and plain HTTP, plus URI-driven dependency injection for databases, Redis, Kafka, HTTP and gRPC clients. Requires **Go 1.27** (generic methods, `encoding/json/v2`, stdlib `uuid`, `math/rand/v2`; see `docs/upgrades/go-1.27.md`). User docs live in `docs/guides/`; runnable samples in `examples/`.

## Commands

```bash
go build ./...
go vet ./...
go test -race ./...
go test ./grpcmux -run TestConnectConformance -v   # single test
golangci-lint run ./...                            # config: .golangci.yml (v2.15.0 pinned in CI)
go fix -diff ./...                                 # must be empty (modernizers)
(cd examples && go build ./... && go test ./...)   # examples are a separate module
```

- `go.mod` pins `toolchain go1.27.3`; `go.sum` is tracked.
- CI (`.github/workflows/ci.yml`): tidy, build, vet, race tests, lint, `go fix -diff`, govulncheck, examples, and a check that rejects non-English (CJK) text.
- `examples/` is its own module (`replace github.com/ti/common-go => ../`), so its dependencies (protovalidate generated code, connect-go, ...) don't leak into consumers. Its proto code is generated with buf and committed.
- The only proto in the library is `grpcmux/mux/error.proto` (generated `error.pb.go` committed).
- Commits follow Conventional Commits with a scope, e.g. `feat(grpcmux): ...`, `fix(graceful): ...`.

## Architecture

### Startup flow used by consumers
`config.Init(ctx, uri, &cfg, dependencies.WithNewFns(...))` (also inits dependency structs) → `grpcmux.NewServer(grpcmux.WithConfig(&cfg.Apis))` → register services → `gs.Start()` (blocks until shutdown).

- **`config`** binds a file/folder URI into a struct via `github.com/ti/objectbind` (live reload). Empty URI → `CONFIG_PATH` → `-c` flag (default `configs/config.yaml`). A `Log.Level` field calls `log.SetLevel`. Top-level struct fields whose first field embeds `dependencies.Dependency` are initialized with `dependencies.Init` (or `InitMulti` when the second field is a struct).
- **`dependencies`** matches struct field names (case-insensitive) to URI keys. Resolution: (1) the pointer type has `Init(ctx, *url.URL|string) error`; (2) a `WithNewFns` constructor (`pb.NewXxxClient` → gRPC conn via `grpcmux.NewClientConnWithURI`, or `func(ctx, *url.URL|string) (T, error)`). Types with `Close(ctx) error` are registered with `graceful.AddCloser`. Required unless `required:"false"`. Fields init concurrently via `async.Future.Go`.
- **Scheme registries:** `database.DB` dispatches on scheme to implementations registered in `init()` (`mysql`/`postgres` in `dependencies/sql`, `mongodb` in `dependencies/mongo`, `mock` in `dependencies/database/mock`); `broker` likewise (`kafka`). Adapters must be blank-imported. New `database.Database` backends must implement the whole interface.

### grpcmux (the server)
- `buildInterceptors` (`server_opts.go`) builds two chains: gRPC (protovalidate, PGV `Validate()`, auth, authz, metrics, logging, rate limit, custom, recovery) and `httpUnary` (same minus auth, logging and rate limiting, which HTTP middleware does; running the limiter twice would double-count Connect calls), which the HTTP bridges run. `WithoutHTTPInterceptors()` disables the latter.
- **HTTP bridges** (`bridge.go`): `RegisterConnectService` (`connect.go`, Connect unary protocol incl. gzip, timeouts, GET for NO_SIDE_EFFECTS, error details) and `WithAutoHTTP` routes dispatch through `grpc.MethodDesc.Handler` with a fake `ServerTransportStream` and peer. `gs.ConnectServer()` is a connect-go v2 `connect.Server` mounted with `connecthttp`; its interceptors run the unary chain and convert gRPC status errors to `*connect.Error` (v2 only sends `*connect.Error` messages).
- `RegisterService`/`registerGRPC` register on both the gRPC server and `bufServer` (in-memory; `Conn()`).
- **HTTP layering (`httpHandler`)**: custom `Handle` routes in `httpServerMux` with the gateway mux as fallback, wrapped once by `mux.Middleware`, then WebSocket dispatch, then `otelhttp`. Stdlib-only HTTP/2 (`http.Protocols`, `http.HTTP2Config`), h2c by default. Read/Write timeouts are deliberately 0 so streaming isn't cut off. Don't add them.
- **`grpcmux/mux/interceptor.go`**: request log fields are injected **before** auth so 401s log completely; `action` is cut to two path segments (`rpcAction`).
- Health: `/healthz` = liveness (always 200), `/readyz` and gRPC health = readiness (NOT_SERVING before serving, during drain, or when a checker fails).
- `Start()` → `graceful.Start` (signals registered first; shutdown on signal, `graceful.Close()`, ctx, or a server error). `Close`: NOT_SERVING → `PRE_STOP` → HTTP Shutdown ∥ GracefulStop → closers (LIFO, one `GRACEFUL_TIMEOUT` deadline). `PANIC_RECOVERY=false` disables recovery.
- Prometheus collectors are created once per process (`sync.OnceValue`), so `NewServer`/`NewClientConn` may be called repeatedly.

### log
`log` wraps `slog` and writes JSON. Request-scoped logger in the context: `log.NewContext`, `log.Inject(ctx, fields)`, `log.Extract(ctx)`, `.Action(name)`. Messages are format strings only when args are given; use `With` for fields.

## Gotchas
- JSON: `encoding/json/v2` only for library-owned types with explicit tags (e.g. Connect error bodies, service config). Caller-owned types (dephttp request/response bodies, mqlru values, SQL params, Mongo page results, `mongo/codecs`) use `encoding/json` (v1): v2 is case-sensitive and encodes nil slices as `[]`, which silently breaks application structs and third-party APIs. `grpcmux/logging/audit.go` uses v1 `json.RawMessage` because a gRPC interface requires it.
- `async`: use `Future.Go(func(ctx) error)` for fan-out without results; `Future.Async[I, O]` takes exactly one input and returns one output.
- gRPC client TLS: only `secure=true`, `https://`, `grpcs://`. `dns://` is plaintext.
- HTTP JSON is snake_case by default; `WithUseCamelCase()` / `useCamelCase: true` switches it (`docs/guides/json-format.md`).
- MySQL URIs must be `mysql://user:pass@host:3306/db`; the driver-style `tcp(host:3306)` does not parse as a URL.

## Maintenance cycle
Modernization runs quarterly following `docs/maintenance/playbook.md`. `scripts/modernize-scan.sh` is read-only: it writes a report to `docs/maintenance/scans/<date>.md` and also builds/vets/tests the reference consumer `~/Workspace/projects/auth/backend` against this checkout through a temporary copy of auth's go.mod (`go -modfile` with a `replace`). Don't use `go.work` for this; it reports false `ambiguous import` errors. Each cycle's plan is `docs/maintenance/cycles/<YYYY>-Q<n>.md`. API-compatibility, deprecation and behavior-change rules are in playbook §2. Consumers upgrade by following `docs/upgrades/<cycle>.md`.
