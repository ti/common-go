package tracing

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// clearEnv removes every variable Init reads, so the developer's shell does
// not leak into a test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"OTEL_SDK_DISABLED", "OTEL_TRACES_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
		"OTEL_TRACES_SAMPLER", "OTEL_TRACES_SAMPLER_ARG", "OTEL_SERVICE_NAME", "OTEL_RESOURCE_ATTRIBUTES",
	} {
		t.Setenv(k, "")
	}
	prev := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
}

func span(name string) {
	_, s := otel.Tracer("test").Start(context.Background(), name)
	s.End()
}

// Without an endpoint nothing is installed and nothing fails.
func TestInitWithoutEndpointIsANoOp(t *testing.T) {
	clearEnv(t)
	before := otel.GetTracerProvider()
	shutdown, err := Init(context.Background(), WithoutGracefulShutdown())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if otel.GetTracerProvider() != before {
		t.Error("Init installed a provider without an endpoint")
	}
	if Enabled() {
		t.Error("Enabled() = true without an endpoint")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("no-op shutdown: %v", err)
	}
	for _, env := range [][2]string{{"OTEL_SDK_DISABLED", "true"}, {"OTEL_TRACES_EXPORTER", "none"}} {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
		t.Setenv(env[0], env[1])
		if _, err := Init(context.Background(), WithoutGracefulShutdown()); err != nil || otel.GetTracerProvider() != before {
			t.Errorf("%s=%s: err=%v, provider installed=%v", env[0], env[1], err, otel.GetTracerProvider() != before)
		}
		t.Setenv(env[0], "")
	}
}

// Spans are exported with the service name; health probe spans are not.
func TestInitExportsAndDropsProbes(t *testing.T) {
	clearEnv(t)
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=test")
	exp := tracetest.NewInMemoryExporter()
	shutdown, err := Init(context.Background(), WithSpanExporter(exp), WithServiceName("svc-under-test"),
		WithoutGracefulShutdown())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, name := range []string{
		"POST /note.v1.NoteService/ListNotes", "GET /healthz", "GET /readyz", "GET /favicon.ico",
		"grpc.health.v1.Health/Check", "note.v1.NoteService/WatchNotes",
	} {
		span(name)
	}
	if err := otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range exp.GetSpans() {
		names = append(names, s.Name)
		attrs := map[string]string{}
		for _, kv := range s.Resource.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
		if attrs["service.name"] != "svc-under-test" || attrs["deployment.environment"] != "test" {
			t.Errorf("resource = %v", attrs)
		}
	}
	if got := strings.Join(names, "|"); got != "POST /note.v1.NoteService/ListNotes|note.v1.NoteService/WatchNotes" {
		t.Errorf("exported spans = %s; probe spans must be dropped", got)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("second shutdown: %v", err)
	}
}

// OTEL_SERVICE_NAME wins over the fallback name.
func TestServiceNameFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("OTEL_SERVICE_NAME", "from-env")
	res := newResource(context.Background(), "fallback")
	if v, _ := res.Set().Value("service.name"); v.AsString() != "from-env" {
		t.Errorf("service.name = %q, want from-env", v.AsString())
	}
}

func TestSamplerFromEnv(t *testing.T) {
	clearEnv(t)
	for _, tc := range []struct{ name, arg, want string }{
		{"", "", "ParentBased{root:AlwaysOnSampler"},
		{"always_on", "", "AlwaysOnSampler"},
		{"always_off", "", "AlwaysOffSampler"},
		{"traceidratio", "0.25", "TraceIDRatioBased{0.25}"},
		{"parentbased_always_on", "", "ParentBased{root:AlwaysOnSampler"},
		{"parentbased_always_off", "", "ParentBased{root:AlwaysOffSampler"},
		{"parentbased_traceidratio", "0.5", "ParentBased{root:TraceIDRatioBased{0.5}"},
	} {
		t.Setenv("OTEL_TRACES_SAMPLER", tc.name)
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", tc.arg)
		s, err := samplerFromEnv()
		if err != nil || !strings.HasPrefix(s.Description(), tc.want) {
			t.Errorf("%q/%q: %v, %v; want %s", tc.name, tc.arg, s, err, tc.want)
		}
	}
	for _, bad := range [][2]string{{"jaeger_remote", ""}, {"traceidratio", "2"}, {"traceidratio", "x"}} {
		t.Setenv("OTEL_TRACES_SAMPLER", bad[0])
		t.Setenv("OTEL_TRACES_SAMPLER_ARG", bad[1])
		if _, err := samplerFromEnv(); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
	// A dropped probe keeps the parent's trace state and never asks the
	// wrapped sampler.
	res := probeSampler{inner: sdktrace.AlwaysSample()}.ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(), Name: "GET /healthz", Kind: trace.SpanKindServer,
	})
	if res.Decision != sdktrace.Drop {
		t.Errorf("probe decision = %v", res.Decision)
	}
}

// The exporter is built from the standard environment: OTLP over HTTP...
func TestExportOverHTTPFromEnv(t *testing.T) {
	clearEnv(t)
	var posts atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/traces" {
			posts.Add(1)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	if !Enabled() {
		t.Fatal("Enabled() = false with an endpoint")
	}
	shutdown, err := Init(context.Background(), WithoutGracefulShutdown())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	span("GET /something")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil { // flushes
		t.Fatalf("shutdown: %v", err)
	}
	if posts.Load() == 0 {
		t.Error("the collector received no POST /v1/traces")
	}
}

// ...and over gRPC, with the http:// endpoint form the Helm charts use.
func TestExportOverGRPCFromEnv(t *testing.T) {
	clearEnv(t)
	var exports atomic.Int32
	lis, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	collector := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		if m, _ := grpc.MethodFromServerStream(stream); m == "/opentelemetry.proto.collector.trace.v1.TraceService/Export" {
			exports.Add(1)
		}
		// Enough for the test: the exporter treats this as a failed export.
		return status.Error(codes.Unimplemented, "test collector")
	}))
	go func() { _ = collector.Serve(lis) }()
	defer collector.Stop()

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+lis.Addr().String())
	shutdown, err := Init(context.Background(), WithoutGracefulShutdown())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	span("GET /something")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = shutdown(ctx)
	if exports.Load() == 0 {
		t.Error("the collector received no TraceService/Export call")
	}
}

// A configuration Init cannot honor is an error, not a silent no-op.
func TestInitRejectsUnsupportedConfiguration(t *testing.T) {
	clearEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
	for _, env := range [][2]string{
		{"OTEL_TRACES_EXPORTER", "zipkin"},
		{"OTEL_EXPORTER_OTLP_PROTOCOL", "http/json"},
		{"OTEL_TRACES_SAMPLER", "nonsense"},
	} {
		t.Setenv(env[0], env[1])
		shutdown, err := Init(context.Background(), WithoutGracefulShutdown())
		if err == nil {
			t.Errorf("%s=%s: no error", env[0], env[1])
		}
		if shutdown == nil || shutdown(context.Background()) != nil {
			t.Errorf("%s=%s: shutdown is not a safe no-op", env[0], env[1])
		}
		t.Setenv(env[0], "")
	}
}
