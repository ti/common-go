package database_test

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/ti/common-go/dependencies/database"
)

// recorder is a TracerProvider that remembers the spans that were started.
type recorder struct {
	noop.TracerProvider
	mu    sync.Mutex
	spans []startedSpan
}

type startedSpan struct {
	name  string
	kind  trace.SpanKind
	attrs map[string]string
}

func (r *recorder) Tracer(string, ...trace.TracerOption) trace.Tracer {
	return recordingTracer{r: r}
}

type recordingTracer struct {
	noop.Tracer
	r *recorder
}

func (t recordingTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	cfg := trace.NewSpanStartConfig(opts...)
	s := startedSpan{name: name, kind: cfg.SpanKind(), attrs: map[string]string{}}
	for _, kv := range cfg.Attributes() {
		s.attrs[string(kv.Key)] = kv.Value.AsString()
	}
	t.r.mu.Lock()
	t.r.spans = append(t.r.spans, s)
	t.r.mu.Unlock()
	return t.Tracer.Start(ctx, name, opts...)
}

func TestOperationSpan(t *testing.T) {
	rec := &recorder{}
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(rec)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	docs := database.MustCollection[doc](newDB(t), database.Schema{Table: "span_docs"}).Unscoped()
	_ = docs.Create(t.Context(), &doc{ID: "a"})
	_, _ = docs.List(t.Context(), database.ListRequest{})

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.spans) != 2 || rec.spans[0].name != "create span_docs" || rec.spans[1].name != "list span_docs" {
		t.Fatalf("spans = %+v", rec.spans)
	}
	s := rec.spans[0]
	if s.kind != trace.SpanKindClient || s.attrs["db.system.name"] != "mock" ||
		s.attrs["db.operation.name"] != "create" || s.attrs["db.collection.name"] != "span_docs" {
		t.Fatalf("span = %+v", s)
	}
}
