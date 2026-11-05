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

- If the struct has a `Log` field with a `Level` string, changes to `Log.Level` call `log.SetLevel` (`debug`, `info`, `warn`, `error`).
- Every top-level struct field whose **first field is an embedded `dependencies.Dependency`** is initialized from its section of the config with `dependencies.Init`, using `opts` (for example `dependencies.WithNewFns(database.New)`). If that struct's second field is itself a struct, the section is treated as groups and initialized with `dependencies.InitMulti`. See [dependencies.md](dependencies.md).

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
| `logBody` | `false` | Include request/response bodies in access logs (`password` fields masked in gRPC logs) |
| `tracing` | `false` | OpenTelemetry server spans (gRPC stats handler and `otelhttp`); you install the TracerProvider |
| `useCamelCase` | `false` | JSON field names in camelCase instead of snake_case ([json-format.md](json-format.md)) |
| `tlsCertFile`, `tlsKeyFile` | | Serve HTTPS (h2) instead of h2c on the HTTP port |
| `cors` | allow all origins | A `mux.CORSConfig`: `Disabled`, `AllowedOrigins`, `AllowedHeaders`, `ExposeHeaders` (the struct has no YAML tags; check key spelling against your config loader, or set it in code with `grpcmux.WithCORS`) |
| `maxRequestBodyBytes` | 32 MiB | Body limit on the ConnectRPC and auto-HTTP bridges |
| `disableHTTPInterceptors` | `false` | Do not run the gRPC interceptor chain on HTTP bridges ([server.md](server.md#interceptors)) |

Options passed after `WithConfig` override it, and options before it are overridden by non-zero config values.

## Environment variables

| Variable | Package | Meaning |
|---|---|---|
| `CONFIG_PATH` | `config` | Config URI when none is passed to `Init` |
| `PRE_STOP` | `grpcmux` | Wait after turning unready, before stopping servers ([server.md](server.md#graceful-shutdown)) |
| `GRACEFUL_TIMEOUT` | `graceful` | Overall shutdown deadline (default `6m`) |
| `PANIC_RECOVERY=false` | `grpcmux` | Disable panic recovery (let panics crash; useful in development) |
