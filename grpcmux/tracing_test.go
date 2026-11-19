package grpcmux

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
)

// With tracing on, every request log line carries the trace_id and span_id of
// its server span, on the HTTP port and on the gRPC port, and health probes
// produce neither a span nor a log line.
func TestTraceIDsOnRequestLogLines(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	lines := captureLogs(t)
	s, _ := connectServer(t, WithTracing())
	base := "http://" + s.opts.httpAddr

	// HTTP port.
	if resp, _ := post(t, base+testCheckPath, nil, "{}"); resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP call: %d", resp.StatusCode)
	}
	// gRPC port.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dial(t, s.opts.grpcAddr).Invoke(ctx, testMutatePath, &pbhealth.HealthCheckRequest{}, &pbhealth.HealthCheckResponse{}); err != nil {
		t.Fatal(err)
	}
	// Probes.
	httpGetStatus(t, base+"/healthz")
	httpGetStatus(t, base+"/readyz")
	if _, err := pbhealth.NewHealthClient(dial(t, s.opts.grpcAddr)).Check(ctx, &pbhealth.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}

	spans := map[string]string{} // trace id -> span name
	spanIDs := map[string]bool{}
	deadline := time.Now().Add(3 * time.Second)
	for len(exp.GetSpans()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	for _, sp := range exp.GetSpans() {
		spans[sp.SpanContext.TraceID().String()] = sp.Name
		spanIDs[sp.SpanContext.SpanID().String()] = true
	}
	if len(spans) != 2 {
		t.Errorf("spans = %v, want one per call and none for the probes", spans)
	}
	for _, action := range []string{testCheckPath, testMutatePath} {
		l := findLog(t, lines, action)
		traceID, _ := l["trace_id"].(string)
		spanID, _ := l["span_id"].(string)
		if spans[traceID] == "" || !spanIDs[spanID] {
			t.Errorf("%s: log line trace_id=%q span_id=%q do not match an exported span %v", action, traceID, spanID, spans)
		}
	}
}

// Without tracing no trace fields are logged.
func TestNoTraceFieldsWithoutTracing(t *testing.T) {
	lines := captureLogs(t)
	s, _ := connectServer(t)
	post(t, "http://"+s.opts.httpAddr+testCheckPath, nil, "{}")
	if l := findLog(t, lines, testCheckPath); l["trace_id"] != nil || l["span_id"] != nil {
		t.Errorf("log line has trace fields without tracing: %v", l)
	}
}
