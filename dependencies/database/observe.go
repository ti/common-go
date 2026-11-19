package database

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const tracerName = "github.com/ti/common-go/dependencies/database"

// operationDuration is the Prometheus form of the OpenTelemetry metric
// db.client.operation.duration (stable semantic conventions), with the
// buckets the convention recommends. It is created once per process, like
// the grpcmux collectors.
var operationDuration = sync.OnceValue(func() *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "db_client_operation_duration_seconds",
		Help:    "Duration of repository operations (database.Repo) by engine, operation, table and outcome.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10},
	}, []string{"db_system_name", "db_operation_name", "db_collection_name", "error_type"})
	if err := prometheus.DefaultRegisterer.Register(h); err != nil {
		if are, ok := errors.AsType[prometheus.AlreadyRegisteredError](err); ok {
			if existing, ok := are.ExistingCollector.(*prometheus.HistogramVec); ok {
				return existing
			}
		}
	}
	return h
})

// observe runs one repository operation inside a client span named
// "<operation> <table>" and records its duration. The table name comes from
// a Schema, never from a request, so span names and labels stay low
// cardinality.
func observe(ctx context.Context, system, operation, table string, fn func(ctx context.Context) error) error {
	ctx, span := otel.Tracer(tracerName).Start(ctx, operation+" "+table,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system.name", system),
			attribute.String("db.operation.name", operation),
			attribute.String("db.collection.name", table),
		))
	start := time.Now()
	err := fn(ctx)
	errType := ""
	if err != nil {
		code := status.Code(err)
		errType = code.String()
		span.SetAttributes(attribute.String("error.type", errType))
		if !expectedOutcome(code) {
			span.SetStatus(otelcodes.Error, err.Error())
		}
	}
	span.End()
	operationDuration().WithLabelValues(system, operation, table, errType).Observe(time.Since(start).Seconds())
	return err
}

// expectedOutcome reports whether a code is an answer of the store rather
// than a failure of it: such spans are not marked as errors.
func expectedOutcome(code codes.Code) bool {
	switch code {
	case codes.NotFound, codes.AlreadyExists, codes.Aborted, codes.InvalidArgument, codes.Canceled:
		return true
	default:
		return false
	}
}
