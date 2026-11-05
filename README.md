# common-go

`github.com/ti/common-go` is the foundation for proto-first Go services:

- **One implementation, every protocol.** A gRPC service implementation is served as native gRPC, REST (grpc-gateway), ConnectRPC (no extra code generation) and plain HTTP from one `grpcmux.Server`. Validation, metrics, recovery and the rest of the interceptor chain behave the same on every protocol.
- **URI-driven dependencies.** Databases (MySQL, PostgreSQL, MongoDB, in-memory mock), Redis, Kafka, HTTP and gRPC clients are declared as struct fields and configured with one URI each, such as `mongodb://host/db` or `dns://svc:8081?timeout=5s`.
- **Batteries included.** JSON logs with request context, Prometheus metrics, optional OpenTelemetry tracing, auth hooks, CORS, health and readiness endpoints, and graceful shutdown that drains in-flight requests.
- **Small concurrency helpers.** `async.Future` for typed fan-out/await, and `graceful` for process lifecycle.

Requires **Go 1.27**.

```bash
go get github.com/ti/common-go@latest
```

## Hello world

Define the API in proto (with `google.api.http` annotations for REST and `buf.validate` rules for validation), generate Go code with buf, then:

```go
package main

import (
	"context"

	"github.com/ti/common-go/grpcmux"
	pb "example.com/hello/gen/go/hello/v1"
)

type greeter struct{ pb.UnimplementedGreeterServiceServer }

func (greeter) SayHello(_ context.Context, req *pb.SayHelloRequest) (*pb.SayHelloResponse, error) {
	return &pb.SayHelloResponse{Message: "Hello, " + req.GetName()}, nil
}

func main() {
	gs := grpcmux.NewServer() // HTTP :8080, gRPC :8081, metrics :9090
	impl := greeter{}

	pb.RegisterGreeterServiceServer(gs, impl)                                  // native gRPC
	grpcmux.RegisterConnectService(gs, &pb.GreeterService_ServiceDesc, impl)   // ConnectRPC on :8080
	_ = pb.RegisterGreeterServiceHandlerServer(context.Background(), gs.ServeMux(), impl) // REST on :8080

	gs.Start() // blocks; drains and exits cleanly on SIGTERM
}
```

```bash
curl -X POST localhost:8080/hello.v1.GreeterService/SayHello -H 'Content-Type: application/json' -d '{"name":"World"}'  # Connect
curl localhost:8080/v1/hello/World                                                                                 # REST
grpcurl -plaintext -d '{"name":"World"}' localhost:8081 hello.v1.GreeterService/SayHello                         # gRPC
```

`RegisterConnectService` registers the service on the gRPC server as well, so call either it or `pb.RegisterXxxServer`, not both, for the same service.

## Configuration and dependencies

```go
type Dependencies struct {
	dependencies.Dependency                      // marks the struct for config.Init
	DB    database.Database `required:"false"`  // mysql://, postgres://, mongodb://, mock://
	Users *dephttp.HTTP                          // http(s)://host?timeout=5s&try=2
}

type Config struct {
	Apis         grpcmux.Config
	Dependencies Dependencies
}

func main() {
	var cfg Config
	// Loads $CONFIG_PATH or -c (default configs/config.yaml), live-reloads it,
	// and initializes every dependency field from its URI.
	if err := config.Init(context.Background(), "", &cfg, dependencies.WithNewFns(database.New)); err != nil {
		log.Action("InitConfig").Fatal(err.Error())
	}
	gs := grpcmux.NewServer(grpcmux.WithConfig(&cfg.Apis))
	// ... register services that use cfg.Dependencies.DB ...
	gs.Start()
}
```

```yaml
apis:
  httpAddr: :8080
  grpcAddr: :8081
  metricsAddr: :9090
dependencies:
  db: mongodb://localhost:27017/app
  users: http://users.svc:8080?timeout=3s&try=2
```

Blank-import the database drivers you use (`_ "github.com/ti/common-go/dependencies/mongo"`, `.../sql`, `.../database/mock`).

## Packages

| Package | Purpose | Guide |
|---|---|---|
| `grpcmux` | The server: gRPC, REST, ConnectRPC, HTTP, health, shutdown; gRPC clients | [server](docs/guides/server.md), [connectrpc](docs/guides/connectrpc.md), [grpc-client](docs/guides/grpc-client.md) |
| `grpcmux/mux` | HTTP middleware, error format, auth info, error codes | [server](docs/guides/server.md), [json-format](docs/guides/json-format.md) |
| `config` | File/folder config binding with live reload | [configuration](docs/guides/configuration.md) |
| `dependencies` | URI-driven dependency injection | [dependencies](docs/guides/dependencies.md) |
| `dependencies/database`, `sql`, `mongo`, `database/mock` | One CRUD/page/stream interface over SQL, MongoDB and an in-memory store | [database](docs/guides/database.md) |
| `dependencies/http` | HTTP client with pooling, retries, proxies, tracing | [http-client](docs/guides/http-client.md) |
| `dependencies/redis`, `broker` (+`kafka`), `mqlru` | Redis, message queue, cache synchronized over a queue | [dependencies](docs/guides/dependencies.md) |
| `log` | Structured JSON logging with request context | [logging](docs/guides/logging.md) |
| `async`, `graceful` | Fan-out/await, process lifecycle | [server](docs/guides/server.md#graceful-shutdown) |
| `tools/*` | Rate limiting, client IP, snowflake IDs, stack traces | |

## More

- [Getting started](docs/guides/getting-started.md) and the runnable [examples](examples/) module: one user service on every protocol, with protovalidate, streaming, a Connect web client and Kubernetes manifests.
- [Upgrading from earlier versions](docs/upgrades/2026-Q4.md).
- [Documentation index](docs/README.md).
- [Maintenance playbook](docs/maintenance/playbook.md): how this library is kept current every quarter.

## License

MIT
