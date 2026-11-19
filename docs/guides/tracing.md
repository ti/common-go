# Tracing (OpenTelemetry)

Tracing has two halves, and the library provides both:

| Half | What | How |
|---|---|---|
| Instrumentation | Server spans for gRPC calls and HTTP requests, context propagation in the gRPC and HTTP clients, `trace_id`/`span_id` on request logs, trace exemplars on metrics | `apis.tracing: true` (or `grpcmux.WithTracing()`); `?tracing=true` on client URIs |
| Export | A TracerProvider with an OTLP exporter, a sampler and a resource, configured from the standard `OTEL_*` environment | `tracing.Init(ctx)` from `github.com/ti/common-go/tracing` |

```go
func main() {
	ctx := context.Background()
	var cfg Config
	if err := config.Init(ctx, "", &cfg); err != nil {
		log.Action("InitConfig").Fatal(err.Error())
	}
	// A no-op unless OTEL_EXPORTER_OTLP_ENDPOINT is set, so local runs need no collector.
	if _, err := tracing.Init(ctx, tracing.WithServiceName("user-service")); err != nil {
		log.Action("InitTracing").Warn("tracing disabled: " + err.Error())
	}
	gs := grpcmux.NewServer(grpcmux.WithConfig(&cfg.Apis)) // apis.tracing: true
	// ...
	gs.Start() // on shutdown, after the server has drained, buffered spans are flushed
}
```

`Init` installs the global TracerProvider and the W3C `traceparent` + `baggage` propagator, and returns a shutdown function. The function is also registered with `graceful.AddCloser`, so with `gs.Start()` (or `graceful.Start`) you do not call it yourself; pass `tracing.WithoutGracefulShutdown()` if you manage the lifecycle differently.

## Environment

The names are the OpenTelemetry specification's; a Helm chart sets them on the container.

| Variable | Meaning |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Collector, for example `http://alloy.base.svc.cluster.local:4317` (`http://` means plaintext). `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` sets it for traces only. **Without either, `Init` installs nothing** |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` (default) or `http/protobuf` |
| `OTEL_EXPORTER_OTLP_HEADERS`, `_TIMEOUT`, `_COMPRESSION`, `_INSECURE`, `_CERTIFICATE` | Read by the exporters themselves |
| `OTEL_SERVICE_NAME` | `service.name`; falls back to `tracing.WithServiceName`, then `unknown_service:<binary>` |
| `OTEL_RESOURCE_ATTRIBUTES` | More resource attributes, `k=v,k=v` |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | `always_on`, `always_off`, `traceidratio`, `parentbased_always_on` (default), `parentbased_always_off`, `parentbased_traceidratio`; the argument is the ratio, 0 to 1 |
| `OTEL_TRACES_EXPORTER` | `otlp` (default) or `none` |
| `OTEL_SDK_DISABLED` | `true` turns tracing off |
| `OTEL_BSP_*` | Batch span processor tuning |

`Init` returns an error for a configuration it cannot honor (an unknown sampler, exporter or protocol) rather than silently tracing nothing; the returned function is still safe to call.

## Health probes

Probes arrive every few seconds per pod and would dominate the trace store. They are excluded twice: grpcmux creates no span for `/healthz`, `/readyz`, `/favicon.ico` and `grpc.health.v1.Health` (as it writes no access log for them), and the sampler `Init` installs drops spans with those names whoever started them. `tracing.WithProbeSpans()` keeps them.

## Logs

With tracing on, the request log line of every HTTP request and gRPC call has `trace_id` and `span_id`, and so has everything logged through the request logger (`log.Extract(ctx)`), so a log line links to its trace and a trace to its logs. Nothing is added when there is no valid span (tracing off, or no provider installed). `mux.TraceFields(ctx)` returns the two fields for your own logging.

```json
{"time":"...","level":"INFO","msg":"POST","action":"/note.v1.NoteService/CreateNote","request_id":"...","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","status":200}
```

## Options

| Option | Purpose |
|---|---|
| `WithServiceName(name)` | `service.name` when the environment does not set it |
| `WithSampler(s)` | Replace the sampler from the environment (probe spans are still dropped) |
| `WithSpanExporter(exp)` | Export with `exp` instead of OTLP from the environment (tests, other exporters); installs the provider even without an endpoint |
| `WithProbeSpans()` | Keep health probe spans |
| `WithoutGracefulShutdown()` | Do not register the shutdown with `graceful` |

`tracing.Enabled()` reports whether the environment asks for export.
