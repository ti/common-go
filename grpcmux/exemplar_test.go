package grpcmux

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func runes(l prometheus.Labels) int {
	n := 0
	for k, v := range l {
		n += utf8.RuneCountInString(k) + utf8.RuneCountInString(v)
	}
	return n
}

func TestExemplarLabels(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	fields := []string{"client_id", "user_id", "device_id", "request_id"}
	values := func(m map[string]string) func(string) string {
		return func(f string) string { return m[f] }
	}

	t.Run("a signed-in request with a trace id stays inside the limit", func(t *testing.T) {
		// The shape that panicked in production: four ordinary identifiers plus a
		// trace id add up to more than 128 runes.
		in := map[string]string{
			"client_id":  "u5HewIf9_gccM3O5Q-nwAA",
			"user_id":    "2013628762215391232",
			"device_id":  "0d8f4c2e-7b1a-4c3d-9e5f-6a7b8c9d0e1f",
			"request_id": "5f2c9a1e-3b4d-4e6f-8a7b-9c0d1e2f3a4b",
		}
		raw := len("trace_id") + len(traceID)
		for k, v := range in {
			raw += len(k) + len(v)
		}
		if raw <= prometheus.ExemplarMaxRunes {
			t.Fatalf("test data is too small to show the defect: %d runes", raw)
		}
		got := exemplarLabels(traceID, fields, values(in))
		if got["trace_id"] != traceID {
			t.Fatalf("trace_id must be kept first, got %v", got)
		}
		if n := runes(got); n > prometheus.ExemplarMaxRunes {
			t.Fatalf("%d runes, over the limit: %v", n, got)
		}
		// Whole values or nothing: no truncated identifiers.
		for k, v := range got {
			if k != "trace_id" && v != in[k] {
				t.Fatalf("%s was altered: %q", k, v)
			}
		}
		if _, ok := got["client_id"]; !ok {
			t.Fatalf("fields are taken in order while they fit, got %v", got)
		}
	})

	t.Run("an oversized caller-supplied value is left out, later fields still fit", func(t *testing.T) {
		in := map[string]string{"device_id": strings.Repeat("d", 500), "request_id": "r-1"}
		got := exemplarLabels("", fields, values(in))
		if _, ok := got["device_id"]; ok {
			t.Fatalf("device_id of 500 runes must be dropped: %v", got)
		}
		if got["request_id"] != "r-1" {
			t.Fatalf("request_id must still be recorded: %v", got)
		}
	})

	t.Run("runes are counted, not bytes", func(t *testing.T) {
		// 60 two-byte runes: 120 bytes but 60 runes, which fits next to its name.
		v := strings.Repeat("é", 60)
		got := exemplarLabels("", []string{"user_id"}, values(map[string]string{"user_id": v}))
		if got["user_id"] != v {
			t.Fatalf("a value within the rune budget was dropped: %v", got)
		}
	})

	t.Run("exactly at the limit is accepted, one over is not", func(t *testing.T) {
		name := "k"
		fit := strings.Repeat("v", prometheus.ExemplarMaxRunes-len(name))
		if got := exemplarLabels("", []string{name}, values(map[string]string{name: fit})); got[name] != fit {
			t.Fatalf("a value that fills the budget exactly was dropped")
		}
		if got := exemplarLabels("", []string{name}, values(map[string]string{name: fit + "v"})); got != nil {
			t.Fatalf("a value one rune over was kept: %v", got)
		}
	})

	t.Run("invalid UTF-8 is left out", func(t *testing.T) {
		got := exemplarLabels("", []string{"device_id", "user_id"},
			values(map[string]string{"device_id": "bad\xff\xfe", "user_id": "u1"}))
		if _, ok := got["device_id"]; ok {
			t.Fatalf("invalid UTF-8 must not reach the exemplar: %v", got)
		}
		if got["user_id"] != "u1" {
			t.Fatalf("user_id lost: %v", got)
		}
	})

	t.Run("nothing to record is nil", func(t *testing.T) {
		if got := exemplarLabels("", fields, values(nil)); got != nil {
			t.Fatalf("want nil, got %v", got)
		}
	})

	t.Run("a field named trace_id cannot override the span's", func(t *testing.T) {
		got := exemplarLabels(traceID, []string{"trace_id"}, values(map[string]string{"trace_id": "forged"}))
		if got["trace_id"] != traceID {
			t.Fatalf("got %v", got)
		}
	})
}

// TestMetricsInterceptorSurvivesHostileMetadata drives the real metrics
// interceptor, which records into real Prometheus collectors: before the
// exemplar was bounded each of these calls panicked inside AddWithExemplar.
func TestMetricsInterceptorSurvivesHostileMetadata(t *testing.T) {
	unary, _ := metricsInterceptors(true, []string{"client_id", "user_id", "device_id", "request_id"})
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36},
		SpanID:     trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7},
		TraceFlags: trace.FlagsSampled,
	})
	cases := map[string]metadata.MD{
		"signed-in caller with a trace": metadata.Pairs(
			"client_id", "u5HewIf9_gccM3O5Q-nwAA", "user_id", "2013628762215391232",
			"device_id", "0d8f4c2e-7b1a-4c3d-9e5f-6a7b8c9d0e1f", "request_id", "5f2c9a1e-3b4d-4e6f-8a7b-9c0d1e2f3a4b"),
		"long device id":    metadata.Pairs("device_id", strings.Repeat("d", 4096)),
		"long request id":   metadata.Pairs("request_id", strings.Repeat("r", 4096)),
		"invalid utf-8":     metadata.Pairs("device_id", "bad\xff\xfe"),
		"no metadata":       {},
		"many long at once": metadata.Pairs("client_id", strings.Repeat("c", 100), "user_id", strings.Repeat("u", 100)),
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/exemplar.v1.Test/Call"}
	for name, md := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(trace.ContextWithSpanContext(context.Background(), sc), md)
			called := false
			_, err := unary(ctx, nil, info, func(context.Context, any) (any, error) {
				called = true
				return "ok", nil
			})
			if err != nil || !called {
				t.Fatalf("handler not reached cleanly: called=%v err=%v", called, err)
			}
		})
	}
}
