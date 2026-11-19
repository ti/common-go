# common-go examples

A complete service built on common-go: one `UserService` implementation served as native gRPC, REST, Connect and gRPC-Web, including a server-streaming method, with protovalidate rules, cursor pagination, AIP-193 errors, health and graceful shutdown. Copy this layout to start a new service.

This is a separate Go module (`github.com/ti/common-go/examples`, with `replace github.com/ti/common-go => ../`), so its dependencies don't become dependencies of the library.

```
proto/example/user/v1/user.proto   API, data model and validation rules (the source of truth)
buf.yaml, buf.gen.yaml             code generation with pinned plugins (Go, gRPC, connect-go v2, OpenAPI, TypeScript)
gen/                               generated code (committed, so `go run` works without buf)
internal/userservice               the implementation: business logic only
internal/reportservice             background work: StartReport submits a job, WatchReport streams its progress
internal/app                       wiring: config, dependencies, protocols, health; integration test
internal/notestore                 repository pattern: a domain Store interface on database.Database (PostgreSQL, MySQL, MongoDB, mock)
cmd/server                         main: config.Init → app.New → Start
cmd/client                         calls the server over gRPC and Connect
cmd/httpclient                     dependencies/http client usage
configs/config.yaml                ports, JSON style, CORS, dependency URIs
web/connect                        browser client (Vite + @connectrpc/connect-web)
deploy/k8s                         Deployment/Service with probes and drain timing; Gateway API routes
deploy/envoy                       minimal Envoy edge proxy
logging/python                     JSON logging in the same format for Python services
```

## Run

```bash
cd examples
go run ./cmd/server          # HTTP :8080, gRPC :8081, metrics :9090; in-memory database
```

In another terminal:

```bash
# REST (grpcmux.WithREST, from the google.api.http annotations)
curl -X POST localhost:8080/v1/users -H 'Content-Type: application/json' \
  -d '{"name":"Ada","email":"ada@example.com","age":36}'
curl 'localhost:8080/v1/users?page_size=10'
curl 'localhost:8080/v1/users:export'                     # server streaming, newline-delimited JSON

# Connect (POST with JSON, or GET for side-effect-free methods)
curl -X POST localhost:8080/example.user.v1.UserService/ListUsers \
  -H 'Content-Type: application/json' -d '{"page_size":10}'

# Validation and errors: 400 with violations, 409 with an ErrorInfo reason
curl -X POST localhost:8080/v1/users -H 'Content-Type: application/json' -d '{"name":"Bob","email":"nope"}'

# Health: liveness and readiness
curl localhost:8080/healthz; curl localhost:8080/readyz

# Reflection (apis.reflection: true): no proto files needed, on either port
grpcurl -plaintext localhost:8081 list
buf curl --protocol grpc --http2-prior-knowledge --reflect -d '{}' \
  http://localhost:8080/example.user.v1.UserService/ListUsers

# gRPC and Connect from Go
go run ./cmd/client
```

The browser client:

```bash
cd web/connect && npm install && npm run dev               # http://localhost:5173
```

To use a real database, change `dependencies.db` in `configs/config.yaml`, for example to `mongodb://localhost:27017/app` or `postgres://user:pass@localhost:5432/app?sslmode=disable`. All three drivers are imported in `internal/app`.

## How the protocols are wired

`internal/app/app.go` registers the one implementation twice, on a server created with `grpcmux.WithREST()`:

```go
userv1.RegisterUserServiceServer(gs, svc)                                          // native gRPC, and REST from the annotations
userv1connect.RegisterUserServiceHandler(gs.ConnectServer(), userservice.ConnectHandler{Service: svc}) // Connect, gRPC-Web
```

- REST needs no generated gateway code and no call of its own: with `WithREST()` (or `apis.rest: true`) registering a service also adds a route for each `google.api.http` annotation. REST requests run the same interceptor chain as the Connect path, including validation, and the streaming `ExportUsers` method is newline-delimited JSON ([REST guide](../docs/guides/rest.md)).
- Connect uses connect-go v2 generated handlers so streaming works. For a unary-only service you can skip connect-go codegen and use `grpcmux.RegisterConnectService(gs, &userv1.UserService_ServiceDesc, svc)` instead of the first and third lines. Use one approach or the other, not both.
- Methods return gRPC status errors everywhere. grpcmux converts them for Connect and maps them to HTTP statuses and a JSON error body for REST; the `google.rpc.ErrorInfo` reason reaches the client on every protocol.
- Rate limits and deadlines come from the `rateLimit` and `timeouts` blocks of `configs/config.yaml` ([guide](../docs/guides/rate-limiting.md)). With `dependencies.redis` set, the buckets live in Redis and the limits hold across replicas.

`configs/config.yaml` also turns on what a production service wants from the server ([server guide](../docs/guides/server.md)): `drainStreams` (open streams end with `UNAVAILABLE` right after `PRE_STOP` instead of holding the shutdown), `protocolErrors` (auth failures on streams in the client's protocol), `reflection`, and `tracing` (spans are exported by `tracing.Init` in `cmd/server` when `OTEL_EXPORTER_OTLP_ENDPOINT` is set; request logs then carry `trace_id`).

See [docs/guides](../docs/guides/) for each building block.

## Regenerate code

```bash
cd examples
buf lint && buf generate    # buf >= 1.50; remote plugins from buf.build, each pinned to an exact version
git diff --exit-code gen web/connect/src/gen   # an unchanged tree regenerates to identical files
```

`buf generate` also writes the OpenAPI document of the REST API, `gen/openapi/api.swagger.json`, with the `buf.build/grpc-ecosystem/openapiv2` plugin pinned to the grpc-gateway version in `go.mod` and with snake_case field names, like the server's JSON. `TestOpenAPIMatchesServedRoutes` checks that every documented operation is served. To upgrade a generator, change its version in `buf.gen.yaml` (and the matching module in `go.mod`), regenerate and commit the diff.

## Test

```bash
cd examples && go test ./...
```

`internal/app/app_test.go` starts the real server on free ports and checks the following on every protocol: CRUD, validation, error reasons, pagination and streaming.
