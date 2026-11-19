// Package tracing sets up OpenTelemetry trace export for a service: one call
// in main installs the global TracerProvider and propagator, configured by
// the standard OTEL_* environment variables.
//
//	shutdown, err := tracing.Init(ctx)
//	if err != nil {
//		log.Action("InitTracing").Warn("tracing disabled: " + err.Error())
//	}
//	// shutdown is also registered with graceful, so a grpcmux server flushes
//	// the last spans on exit without further code.
//
// grpcmux creates the server spans when tracing is on (apis.tracing: true or
// grpcmux.WithTracing()) and puts trace_id and span_id on every request log
// line; the gRPC and HTTP clients of this library propagate the context. This
// package only decides where spans go.
//
// Environment (OpenTelemetry specification names):
//
//	OTEL_EXPORTER_OTLP_ENDPOINT          collector, e.g. http://alloy.base.svc.cluster.local:4317
//	OTEL_EXPORTER_OTLP_TRACES_ENDPOINT   the same for traces only
//	OTEL_EXPORTER_OTLP_PROTOCOL          grpc (default) or http/protobuf
//	OTEL_EXPORTER_OTLP_HEADERS, _TIMEOUT, _COMPRESSION, _INSECURE, _CERTIFICATE
//	OTEL_SERVICE_NAME                    service.name
//	OTEL_RESOURCE_ATTRIBUTES             more resource attributes (k=v,k=v)
//	OTEL_TRACES_SAMPLER, _SAMPLER_ARG    always_on, always_off, traceidratio,
//	                                     parentbased_always_on (default),
//	                                     parentbased_always_off, parentbased_traceidratio
//	OTEL_TRACES_EXPORTER                 otlp (default) or none
//	OTEL_SDK_DISABLED                    true turns everything off
//	OTEL_BSP_*                           batch span processor tuning
//
// Without an endpoint (and without [WithSpanExporter]) Init installs nothing
// and returns a no-op shutdown, so local runs need no collector.
package tracing

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/ti/common-go/graceful"
	"github.com/ti/common-go/log"
)

type options struct {
	serviceName string
	exporter    sdktrace.SpanExporter
	sampler     sdktrace.Sampler
	keepProbes  bool
	noCloser    bool
}

// Option configures [Init].
type Option func(*options)

// WithServiceName sets the service.name resource attribute used when neither
// OTEL_SERVICE_NAME nor OTEL_RESOURCE_ATTRIBUTES names the service.
func WithServiceName(name string) Option {
	return func(o *options) { o.serviceName = name }
}

// WithSpanExporter exports spans with exp instead of the OTLP exporter built
// from the environment (for tests, or an exporter this package does not
// build). Init then installs the provider even without an OTLP endpoint.
func WithSpanExporter(exp sdktrace.SpanExporter) Option {
	return func(o *options) { o.exporter = exp }
}

// WithSampler replaces the sampler read from OTEL_TRACES_SAMPLER. Health
// probe spans are still dropped (see [WithProbeSpans]).
func WithSampler(s sdktrace.Sampler) Option {
	return func(o *options) { o.sampler = s }
}

// WithProbeSpans keeps the spans of health probes, which are dropped by
// default.
func WithProbeSpans() Option {
	return func(o *options) { o.keepProbes = true }
}

// WithoutGracefulShutdown stops Init from registering the provider's
// shutdown with graceful.AddCloser; call the returned function yourself.
func WithoutGracefulShutdown() Option {
	return func(o *options) { o.noCloser = true }
}

// Init installs the global TracerProvider and the W3C trace-context and
// baggage propagator, and returns a function that flushes buffered spans and
// stops the exporter. The function is also registered with
// graceful.AddCloser (unless [WithoutGracefulShutdown]): closers run newest
// first, so with Init called before the server starts, spans of the last
// requests are flushed after the server has drained.
//
// When tracing is not configured (no OTLP endpoint, OTEL_TRACES_EXPORTER=none
// or OTEL_SDK_DISABLED=true) it installs nothing and returns a no-op
// function and a nil error. An error means the configuration is unusable;
// the returned function is still safe to call.
func Init(ctx context.Context, opts ...Option) (shutdown func(context.Context) error, err error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	noop := func(context.Context) error { return nil }
	if disabled, _ := strconv.ParseBool(os.Getenv("OTEL_SDK_DISABLED")); disabled {
		return noop, nil
	}
	exporter := o.exporter
	if exporter == nil {
		exporter, err = exporterFromEnv(ctx)
		if err != nil {
			return noop, err
		}
		if exporter == nil {
			return noop, nil
		}
	}
	sampler := o.sampler
	if sampler == nil {
		sampler, err = samplerFromEnv()
		if err != nil {
			_ = exporter.Shutdown(ctx)
			return noop, err
		}
	}
	if !o.keepProbes {
		sampler = probeSampler{inner: sampler}
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(newResource(ctx, o.serviceName)),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	if !o.noCloser {
		graceful.AddCloser(tp.Shutdown)
	}
	log.Action("tracing").Info("OpenTelemetry tracing enabled, sampler " + sampler.Description())
	return tp.Shutdown, nil
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// exporterFromEnv builds the OTLP span exporter the environment describes,
// or returns nil when no export is configured. The exporters read endpoint,
// headers, timeout, compression and TLS settings from the environment
// themselves.
func exporterFromEnv(ctx context.Context) (sdktrace.SpanExporter, error) {
	switch kind := strings.ToLower(firstEnv("OTEL_TRACES_EXPORTER")); kind {
	case "", "otlp":
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("tracing: OTEL_TRACES_EXPORTER=%q is not supported (otlp, none)", kind)
	}
	if firstEnv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return nil, nil
	}
	var (
		exp sdktrace.SpanExporter
		err error
	)
	switch protocol := strings.ToLower(firstEnv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL")); protocol {
	case "", "grpc":
		exp, err = otlptracegrpc.New(ctx)
	case "http/protobuf":
		exp, err = otlptracehttp.New(ctx)
	default:
		return nil, fmt.Errorf("tracing: OTEL_EXPORTER_OTLP_PROTOCOL=%q is not supported (grpc, http/protobuf)", protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("tracing: create OTLP exporter: %w", err)
	}
	return exp, nil
}

// samplerFromEnv implements OTEL_TRACES_SAMPLER and OTEL_TRACES_SAMPLER_ARG
// as the specification defines them (the SDK's own parser is not exported,
// and the sampler has to be wrapped to drop probe spans).
func samplerFromEnv() (sdktrace.Sampler, error) {
	ratio := 1.0
	if v := firstEnv("OTEL_TRACES_SAMPLER_ARG"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 1 {
			return nil, fmt.Errorf("tracing: OTEL_TRACES_SAMPLER_ARG=%q is not a ratio between 0 and 1", v)
		}
		ratio = f
	}
	switch name := strings.ToLower(firstEnv("OTEL_TRACES_SAMPLER")); name {
	case "", "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample()), nil
	case "always_on":
		return sdktrace.AlwaysSample(), nil
	case "always_off":
		return sdktrace.NeverSample(), nil
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(ratio), nil
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample()), nil
	case "parentbased_traceidratio":
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio)), nil
	default:
		return nil, fmt.Errorf("tracing: OTEL_TRACES_SAMPLER=%q is not supported", name)
	}
}

// newResource describes the service: SDK and environment attributes
// (OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES), with fallbackName as
// service.name when the environment does not name the service.
func newResource(ctx context.Context, fallbackName string) *resource.Resource {
	res, err := resource.New(ctx, resource.WithTelemetrySDK(), resource.WithFromEnv())
	if err != nil && res == nil {
		res = resource.Empty()
	}
	if _, named := res.Set().Value("service.name"); !named {
		name := fallbackName
		if name == "" {
			name = "unknown_service"
			if len(os.Args) > 0 && os.Args[0] != "" {
				name += ":" + baseName(os.Args[0])
			}
		}
		if merged, err := resource.Merge(res, resource.NewSchemaless(attribute.String("service.name", name))); err == nil {
			res = merged
		}
	}
	return res
}

func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// probeSampler drops the spans of health probes and delegates everything
// else. Probes arrive every few seconds per pod; traced, they would dominate
// the trace store. grpcmux already skips them (it creates no span for
// /healthz, /readyz or grpc.health.v1); this covers spans started by other
// instrumentation, for example a service mesh sidecar's parent context or an
// HTTP server not built on grpcmux.
type probeSampler struct {
	inner sdktrace.Sampler
}

// IsProbeSpan reports whether a span name denotes a health probe: an HTTP
// span named "<METHOD> <path>" (or just the path) for /healthz, /readyz,
// /livez or /favicon.ico, or a grpc.health.v1.Health call.
func IsProbeSpan(name string) bool {
	if strings.Contains(name, "grpc.health.v1.Health/") {
		return true
	}
	path := name
	if _, p, ok := strings.Cut(name, " "); ok {
		path = p
	}
	switch path {
	case "/healthz", "/readyz", "/livez", "/favicon.ico":
		return true
	}
	return false
}

func (s probeSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if IsProbeSpan(p.Name) {
		return sdktrace.SamplingResult{
			Decision:   sdktrace.Drop,
			Tracestate: trace.SpanContextFromContext(p.ParentContext).TraceState(),
		}
	}
	return s.inner.ShouldSample(p)
}

func (s probeSampler) Description() string {
	return "DropProbes{" + s.inner.Description() + "}"
}

// Enabled reports whether the environment asks for trace export, that is,
// whether [Init] without options would install a provider.
func Enabled() bool {
	if disabled, _ := strconv.ParseBool(os.Getenv("OTEL_SDK_DISABLED")); disabled {
		return false
	}
	if kind := strings.ToLower(firstEnv("OTEL_TRACES_EXPORTER")); kind != "" && kind != "otlp" {
		return false
	}
	return firstEnv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}
