# Configuration

## `config.Init`

```go
func Init(ctx context.Context, configURI string, configPtr any, opts ...dependencies.Option) error
```

`config.Init` binds a configuration source to a struct using [`github.com/ti/objectbind`](https://github.com/ti/objectbind) and keeps watching it, so later edits to the file are applied to the struct while the process runs.

Where the configuration comes from, in order:

1. `configURI`, if not empty (for example `file://configs/config.yaml` or a folder);
2. the `CONFIG_PATH` environment variable;
3. the `-c` command-line flag, default `configs/config.yaml`.

`Init` waits at most 5 seconds for the first load, unless `ctx` already has a deadline.

It also does two things for you:

- If the struct has a `Log` field with a `Level` string, `Log.Level` is applied with `log.SetLevel` (`debug`, `info`, `warn`, `error`) at startup and on every change. The key is optional: an absent or empty value keeps the current level (`debug` unless the program set another). An unknown name makes `Init` return an error; on a reload it is logged and the previous level stays. `Log` may also be a pointer to such a struct (it stays nil when the source has no `log` key), and `Level` a named string type. A `Log` field without a string `Level` is ignored.
- Every top-level struct field whose **first field is an embedded `dependencies.Dependency`** is initialized from its section of the config with `dependencies.Init`, using `opts` (for example `dependencies.WithNewFns(database.New)`). If that struct's second field is itself a struct, the section is treated as groups and initialized with `dependencies.InitMulti`. See [dependencies.md](dependencies.md).

**Live reload and concurrency.** The watcher works on a private copy of your struct. It does not touch your struct before `Init` has returned, and it never reads or writes the dependency fields: those are set once by `Init`, so handlers use `cfg.Dependencies.DB` without synchronization, and editing the `dependencies:` block of a running process has no effect (restart to change a dependency). When the source changes, only the top-level fields whose value changed are assigned, each as a fresh value, while a lock is held. Read a field that you expect to edit at runtime under that lock, or react to it with a `Binder` hook:

```go
config.RLock()
greeting := cfg.Service.Greeting
config.RUnlock()
```

Fields that never change in the source need no lock. Values set in the struct before `Init` are kept for keys the source does not have.

Use `config.Binder()` after `Init` to add your own hooks on fields (the returned `*objectbind.Binder` has `BindField(path, func(value, old any))`).

```go
type Config struct {
	Apis         grpcmux.Config
	Log          struct{ Level string }
	Dependencies Dependencies // embeds dependencies.Dependency
	Service      ServiceConfig
}
```

```yaml
apis:
  httpAddr: :8080
log:
  level: info
dependencies:
  db: mongodb://localhost:27017/app
service:
  greeting: hello
```

## `grpcmux.Config`

The file form of the most common server options, applied with `grpcmux.WithConfig(&cfg.Apis)`. Zero values keep the defaults.

| Field (YAML key) | Default | Meaning |
|---|---|---|
| `grpcAddr` | `:8081` | Native gRPC listener |
| `httpAddr` | `:8080` | HTTP listener (REST, Connect, custom handlers, health) |
| `metricsAddr` | `:9090` | Prometheus `/metrics`; set `WithMetricsAddr("")` in code to disable |
| `logBody` | `false` | Include request/response bodies in access logs (`password` fields masked in gRPC logs). Only the log entry is limited in size; handlers always receive the whole body |
| `tracing` | `false` | OpenTelemetry server spans (gRPC stats handler and `otelhttp`, health probes excluded) and `trace_id`/`span_id` on request logs; [`tracing.Init`](tracing.md) installs the exporter |
| `useCamelCase` | `false` | JSON field names in camelCase instead of snake_case ([json-format.md](json-format.md)) |
| `tlsCertFile`, `tlsKeyFile` | | Serve HTTPS (h2) instead of h2c on the HTTP port |
| `cors` | allow all origins | A `mux.CORSConfig`: `disabled`, `allowedOrigins`, `allowedHeaders`, `exposeHeaders` (the last two add to the built-in lists) |
| `maxRequestBodyBytes` | 32 MiB | Body limit on the ConnectRPC and auto-HTTP bridges |
| `disableHTTPInterceptors` | `false` | Do not run the gRPC interceptor chain on HTTP bridges ([server.md](server.md#interceptors)) |
| `rest` | `false` | Serve methods annotated with `google.api.http` as REST routes ([rest.md](rest.md)) |
| `typedErrorDetails` | `false` (`true` with `rest`) | Add `"@type"` to the `details` of JSON error bodies ([rest.md](rest.md#errors)) |
| `rateLimit` | | Rate-limit rules: `default`, `rules[]`, `trustedProxies`, `keyPrefix`, `disabled` ([rate-limiting.md](rate-limiting.md)) |
| `timeouts` | | Server-side deadlines for unary calls: `default`, `rules[]` ([rate-limiting.md](rate-limiting.md#server-side-deadlines)) |
| `noAuthMethods` | | Auth exemptions, exact (`/pkg.Service/Method`) or per service (`/pkg.Service/*`) ([server.md](server.md#auth)) |
| `reflection` | `false` | gRPC server reflection on both ports ([server.md](server.md#reflection)) |
| `drainStreams`, `drainStreamsGrace` | `false`, `0s` | End open streams when the server drains, after the grace (a duration such as `5s`) ([server.md](server.md#long-lived-streams-and-the-drain)) |
| `protocolErrors` | `false` | Answer rejected Connect-stream, gRPC and gRPC-Web requests in their protocol ([server.md](server.md#errors)) |
| `disableQueryAccessToken` | `false` | Do not accept `?access_token=` as the bearer token |
| `disableMethodOverride` | `false` | Ignore `X-HTTP-Method-Override` |
| `debugHandlers` | `false` | Serve `/debug/**` (pprof, expvar of `http.DefaultServeMux`) on the HTTP port ([server.md](server.md#debug-handlers)) |

```yaml
apis:
  cors:
    allowedOrigins: ["https://app.example.com"]
    exposeHeaders: ["X-Total-Count"]
  noAuthMethods:
    - /auth.v1.AuthService/Token
  reflection: true
  drainStreams: true
  drainStreamsGrace: 5s
```

Options passed after `WithConfig` override it, and options before it are overridden by non-zero config values.

## Environment variables

| Variable | Package | Meaning |
|---|---|---|
| `CONFIG_PATH` | `config` | Config URI when none is passed to `Init` |
| `PRE_STOP` | `grpcmux` | Wait after turning unready, before stopping servers ([server.md](server.md#graceful-shutdown)) |
| `GRACEFUL_TIMEOUT` | `graceful` | Overall shutdown deadline (default `6m`) |
| `PANIC_RECOVERY=false` | `grpcmux` | Disable panic recovery (let panics crash; useful in development) |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `OTEL_TRACES_SAMPLER`, ... | `tracing` | Trace export ([tracing.md](tracing.md)) |
