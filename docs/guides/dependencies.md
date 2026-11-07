# Dependencies

`dependencies` turns a map of URIs into ready-to-use clients. You declare *what* you need as struct fields; configuration says *where* each one is.

```go
type Dependencies struct {
	dependencies.Dependency                         // optional marker (needed for config.Init)
	DB      database.Database                       // required
	Cache   *redis.Redis      `required:"false"`    // skipped when not configured
	Users   *dephttp.HTTP
	Billing pbbilling.BillingServiceClient          // gRPC client
	Events  *broker.Broker
}

var deps Dependencies
err := dependencies.Init(ctx, &deps, map[string]string{
	"db":      "postgres://app:secret@db:5432/app?sslmode=disable",
	"cache":   "redis://:secret@redis:6379?db=0",
	"users":   "http://users.svc:8080?timeout=3s&try=2",
	"billing": "dns://billing.svc:8081?timeout=5s",
	"events":  "kafka://kafka-0:9092,kafka-1:9092/app-events",
}, dependencies.WithNewFns(database.New, pbbilling.NewBillingServiceClient))
```

With `config.Init`, the map is the struct's section in the config file and `Init` is called for you ([configuration.md](configuration.md)).

## How a field is resolved

Field names are matched to map keys case-insensitively (`DB` ← `db`). Fields must be pointers or interfaces. For each field with a URI:

1. If the pointer type has `Init(ctx, *url.URL) error` (or `Init(ctx, string) error`), a new value is allocated and `Init` is called. All clients in this repository work this way: `*dephttp.HTTP`, `*redis.Redis`, `*broker.Broker`, `*mongo.Mongo`, `*mqlru.Lru`, `*database.DB`.
2. Otherwise a constructor registered with `dependencies.WithNewFns` is used, matched by its return type:
   - `func(context.Context, *url.URL) (T, error)` or `func(context.Context, string) (T, error)`, such as `database.New` for `database.Database` fields;
   - a generated gRPC constructor `pb.NewXxxClient(grpc.ClientConnInterface) pb.XxxClient`. The connection is created with [`grpcmux.NewClientConnWithURI`](grpc-client.md), so all its URI parameters apply.
3. If neither applies, `Init` returns an error that says which `WithNewFns` constructor to add.

Fields are **required** unless tagged `required:"false"`; a missing URI for a required field is an error. A field that is already non-nil is left alone, and a field of type `dependencies.Dependency` is skipped.

Fields are initialized **concurrently** (with `async.Future`); the first error is returned.

Every dependency whose type has `Close(ctx) error`, and every gRPC connection created this way, is registered with `graceful.AddCloser`, so it is closed when the process shuts down, after the servers have drained ([server.md](server.md#graceful-shutdown)).

## Groups: `InitMulti`

For larger services, group dependencies by area:

```go
type Deps struct {
	Storage struct {
		DB    database.Database
		Cache *redis.Redis `required:"false"`
	}
	Upstream struct {
		Users *dephttp.HTTP
	}
}
err := dependencies.InitMulti(ctx, &deps, map[string]map[string]string{
	"storage":  {"db": "mongodb://mongo:27017/app"},
	"upstream": {"users": "http://users.svc:8080"},
})
```

Group names are matched case-insensitively; groups without configuration are skipped.

## URI reference

| Field type | Scheme | Example | Notes |
|---|---|---|---|
| `database.Database` (+ `WithNewFns(database.New)`) or `*database.DB` | `mysql` | `mysql://user:pass@host:3306/db?charset=utf8mb4&parseTime=true` | blank-import `dependencies/sql`; see [database.md](database.md) |
| same | `postgres` | `postgres://user:pass@host:5432/db?sslmode=disable` | blank-import `dependencies/sql` |
| same, or `*mongo.Mongo` | `mongodb` | `mongodb://user:pass@host:27017/db?authSource=admin` | blank-import `dependencies/mongo`; the path (database) is required |
| same | `mock` | `mock://local/db` | in-memory; blank-import `dependencies/database/mock` |
| `*redis.Redis` | `redis`, `rediss` (TLS) | `redis://user:pass@h1:6379,h2:6379?db=0&client=app` | `master=` (Sentinel), `cache=true` (client-side cache), `shuffle=true` |
| `*broker.Broker` | `kafka` | `kafka://user:pass@b1:9092,b2:9092/default-topic?v=2.8.0&tls=true` | blank-import `dependencies/broker/kafka`; Kafka protocol version defaults to 2.0.0 |
| `*mqlru.Lru` | `cache` or a broker scheme | `cache://memory?ttl=5m&capacity=10000` | in-process TTL cache; with a broker URI (`kafka://...?ttl=5m`) invalidations are shared across instances; `touch=false`, `mq=false` |
| `*dephttp.HTTP` | `http`, `https`, custom resolvers | `http://host:8080/base?timeout=3s&try=2` | see [http-client.md](http-client.md) |
| `*oidc.Provider` | `oidc` | `oidc://client:secret@account.example.com?groups=admin` | OIDC token validation and access rules; see [oidc.md](oidc.md) |
| `pb.XxxClient` (+ `WithNewFns(pb.NewXxxClient)`) | `dns` and other gRPC resolvers | `dns://svc.ns.svc:8081?timeout=5s&try=3` | see [grpc-client.md](grpc-client.md) |

## Options

- `dependencies.WithNewFns(fns...)`: constructors, as above. Panics at startup if a function has an unsupported signature.
- `dependencies.WithGRPCDialOptions(opts...)`: extra `grpc.DialOption`s for every gRPC client created from a URI.
- `dependencies.WithSync()`: initialize fields one after another instead of concurrently (useful when debugging start-up order).

## Writing your own dependency

Give the type an `Init(ctx context.Context, u *url.URL) error` method (and optionally `Close(ctx context.Context) error`), and use a pointer field:

```go
type Search struct{ client *search.Client }

func (s *Search) Init(ctx context.Context, u *url.URL) error {
	c, err := search.Dial(ctx, u.Host, u.Query().Get("index"))
	s.client = c
	return err
}

func (s *Search) Close(ctx context.Context) error { return s.client.Close() }
```
