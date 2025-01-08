package grpcmux

import (
	"context"
	"fmt"
	"github.com/ti/common-go/tools/stacktrace"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/metadata"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/validator"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	muxlogging "github.com/ti/common-go/grpcmux/logging"
	"github.com/ti/common-go/log"
	"github.com/ti/common-go/tools/routerlimit"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/authz"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// NewFullMiddleWare new full middleWare grpc server options.
// nolint: funlen
func NewFullMiddleWare(opts ...Option) (unaryServerInterceptors []grpc.UnaryServerInterceptor,
	streamInterceptors []grpc.StreamServerInterceptor,
) {
	o := evaluateOptions(opts)
	// tags and validate
	unaryServerInterceptors = []grpc.UnaryServerInterceptor{
		validator.UnaryServerInterceptor(validator.WithFailFast()),
	}
	streamInterceptors = []grpc.StreamServerInterceptor{
		validator.StreamServerInterceptor(validator.WithFailFast()),
	}
	// auth
	if o.authFunction != nil {
		unary, stream := authInterceptors(o.authFunction, o.noAuthPrefix)
		unaryServerInterceptors = append(unaryServerInterceptors, unary)
		streamInterceptors = append(streamInterceptors, stream)
	}
	// audit
	if o.authzPolicy != "" {
		muxlogging.RegisterAuditLogger(o.logger)
		interceptor, err := authz.NewStatic(o.authzPolicy)
		if err != nil {
			log.Action("authzPolicy").Fatal("failed to create interceptor: %v", err)
		}
		unaryServerInterceptors = append(unaryServerInterceptors, interceptor.UnaryInterceptor)
		streamInterceptors = append(streamInterceptors, interceptor.StreamInterceptor)
	}
	// metrics
	var panicsTotal prometheus.Counter
	if o.metricsAddr != "" {
		panicsTotal = promauto.With(prometheus.DefaultRegisterer).NewCounter(prometheus.CounterOpts{
			Name: "grpc_req_panics_recovered_total",
			Help: "Total number of gRPC requests recovered from internal panic.",
		})
		unary, stream := metricsInterceptors(o.tracing, o.metaTags)
		unaryServerInterceptors = append(unaryServerInterceptors, unary)
		streamInterceptors = append(streamInterceptors, stream)
	}
	// logging
	unaryServerInterceptors = append(unaryServerInterceptors,
		muxlogging.UnaryServerInterceptor(interceptorLogger(),
			o.loggingOpts...))
	streamInterceptors = append(streamInterceptors,
		muxlogging.StreamServerInterceptor(interceptorLogger(),
			o.loggingOpts...))

	// ratelimit
	if o.limiter != nil {
		unaryServerInterceptors = append(unaryServerInterceptors,
			routerlimit.UnaryServerInterceptor(o.limiter))
		streamInterceptors = append(streamInterceptors,
			routerlimit.StreamServerInterceptor(o.limiter))
	}
	if len(o.grpcUnaryServerInterceptors) > 0 {
		unaryServerInterceptors = append(unaryServerInterceptors, o.grpcUnaryServerInterceptors...)
	}
	if len(o.grpcStreamServerInterceptors) > 0 {
		streamInterceptors = append(streamInterceptors, o.grpcStreamServerInterceptors...)
	}
	// recovery
	grpcPanicRecoveryHandler := func(ctx context.Context, p any) (err error) {
		if panicsTotal != nil {
			panicsTotal.Inc()
		}
		stackInfo := stacktrace.Callers(5)
		log.Inject(ctx, map[string]any{
			"stack": stackInfo,
		})
		return status.Errorf(codes.Internal, "%s", p)
	}
	if !noRecovery {
		unaryServerInterceptors = append(unaryServerInterceptors,
			recovery.UnaryServerInterceptor(recovery.WithRecoveryHandlerContext(grpcPanicRecoveryHandler)))
		streamInterceptors = append(streamInterceptors,
			recovery.StreamServerInterceptor(recovery.WithRecoveryHandlerContext(grpcPanicRecoveryHandler)))
	}
	return
}

func metricsInterceptors(tracing bool, logMeta []string) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	srvMetrics := grpcprom.NewServerMetrics(
		grpcprom.WithServerHandlingTimeHistogram(
			grpcprom.WithHistogramBuckets([]float64{
				0.001, 0.01, 0.1,
				0.3, 0.6, 1, 3, 6, 9, 20, 30, 60, 90, 120,
			}),
		),
	)
	samplerFromContext := func(ctx context.Context) prometheus.Labels {
		metaData := metadata.ExtractIncoming(ctx)
		labels := make(prometheus.Labels)
		for _, f := range logMeta {
			if data := metaData.Get(f); data != "" {
				labels[f] = data
			}
		}
		if tracing {
			if span := trace.SpanContextFromContext(ctx); span.IsSampled() {
				labels["trace_id"] = span.TraceID().String()
			}
		}
		return labels
	}
	prometheus.DefaultRegisterer.MustRegister(srvMetrics)
	return srvMetrics.UnaryServerInterceptor(grpcprom.WithExemplarFromContext(samplerFromContext)),
		srvMetrics.StreamServerInterceptor(grpcprom.WithExemplarFromContext(samplerFromContext))
}

func authInterceptors(grpcAuthFunction auth.AuthFunc, noAuthPrefix []string) (grpc.UnaryServerInterceptor,
	grpc.StreamServerInterceptor,
) {
	allButHealthZ := func(ctx context.Context, callMeta interceptors.CallMeta) bool {
		if callMeta.Service == healthpb.Health_ServiceDesc.ServiceName {
			return false
		}
		fullMethod := callMeta.FullMethod()
		for _, v := range noAuthPrefix {
			if strings.HasPrefix(fullMethod, v) {
				return false
			}
		}
		return true
	}
	return selector.UnaryServerInterceptor(auth.UnaryServerInterceptor(grpcAuthFunction),
			selector.MatchFunc(allButHealthZ)),
		selector.StreamServerInterceptor(auth.StreamServerInterceptor(grpcAuthFunction),
			selector.MatchFunc(allButHealthZ))
}

// This code is simple enough to be copied and not imported.
func interceptorLogger() logging.Logger {
	return logging.LoggerFunc(func(ctx context.Context, lvl logging.Level, msg string, fields ...any) {
		logTags := map[string]any{}
		for i := 0; i < len(fields); i += 2 {
			logTags[fields[i].(string)] = fields[i+1]
		}
		logger := log.Extract(ctx).With(logTags)
		switch lvl {
		case logging.LevelDebug:
			logger.Debug(msg)
		case logging.LevelInfo:
			logger.Info(msg)
		case logging.LevelWarn:
			logger.Warn(msg)
		case logging.LevelError:
			logger.Error(msg)
		default:
			panic(fmt.Sprintf("unknown level %v", lvl))
		}
	})
}
