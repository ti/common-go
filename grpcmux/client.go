package grpcmux

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

	"google.golang.org/grpc/credentials"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/metadata"

	"google.golang.org/grpc/balancer/roundrobin"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/timeout"
	"github.com/ti/common-go/graceful"
	"github.com/ti/common-go/grpcmux/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
)

// NewClient new grpc client.
func NewClient[T any](ctx context.Context, uri string, pbNewXxxClient func(cc grpc.ClientConnInterface) T,
	opts ...grpc.DialOption,
) (client T, err error) {
	var u *url.URL
	u, err = url.Parse(uri)
	if err != nil {
		err = fmt.Errorf("parse grpc uri %s error for %w", uri, err)
		return
	}
	return NewClientWithURL(ctx, u, pbNewXxxClient, opts...)
}

// NewClientWithURL new grpc client with uri.
func NewClientWithURL[T any](ctx context.Context, uri *url.URL, pbNewXxxClient func(cc grpc.ClientConnInterface) T,
	opts ...grpc.DialOption,
) (client T, err error) {
	var conn *grpc.ClientConn
	conn, err = NewClientConnWithURI(ctx, uri, opts...)
	if err != nil {
		return
	}
	// add global closer hook
	graceful.AddCloser(func(ctx context.Context) error {
		return conn.Close()
	})
	return pbNewXxxClient(conn), nil
}

// NewClientConn new grpc conn with uri.
func NewClientConn(ctx context.Context, uri string, opts ...grpc.DialOption) (grpc.ClientConnInterface, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("parse grpc uri %s error for %w", uri, err)
	}
	return NewClientConnWithURI(ctx, u, opts...)
}

// NewClientConnWithURI new client conn with uri
// nolint:funlen
func NewClientConnWithURI(ctx context.Context, uri *url.URL, customOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
	query := uri.Query()
	falseStr := "false"
	trueStr := "true"
	logeEnable := query.Get("log") == trueStr
	secure := query.Get("secure") == trueStr || strings.HasSuffix(uri.Scheme, "s")
	noMetrics := query.Get("metrics") == falseStr
	callTimeout, _ := time.ParseDuration(query.Get("timeout"))
	loadBalancingPolicy := query.Get("loadBalancingPolicy")
	try, _ := strconv.ParseUint(query.Get("try"), 10, 32)
	var unaryInterceptor []grpc.UnaryClientInterceptor
	var streamInterceptor []grpc.StreamClientInterceptor
	var opts []grpc.DialOption
	// secure
	if !secure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
		})))
	}
	// base
	if loadBalancingPolicy == "" {
		loadBalancingPolicy = roundrobin.Name
	}
	opts = append(opts, grpc.WithDefaultServiceConfig(fmt.Sprintf(`{"LoadBalancingPolicy":"%s"}`,
		loadBalancingPolicy)))

	if query.Get("block") == trueStr {
		opts = append(opts, grpc.WithBlock())
	}

	metaTags := []string{"client_id", "user_id", "device_id", "request_id"}
	// log
	if logeEnable {
		logBody := query.Get("logBody") == trueStr
		var logOpts []logging.Option
		if logBody {
			logOpts = []logging.Option{
				logging.WithDecider(logging.DefaultLoggingBodyDecider),
				logging.WithBodyMaskField("password"),
			}
		}
		unaryInterceptor = append(unaryInterceptor,
			logging.UnaryClientInterceptor(interceptorLogger(), logOpts...))
		streamInterceptor = append(streamInterceptor,
			logging.StreamClientInterceptor(interceptorLogger(), logOpts...))
	}
	if callTimeout > 0 {
		unaryInterceptor = append(unaryInterceptor, timeout.UnaryClientInterceptor(callTimeout))
	}
	if try > 0 {
		unaryInterceptor = append(unaryInterceptor, retry.UnaryClientInterceptor(retry.WithMax(uint(try))))
		streamInterceptor = append(streamInterceptor, retry.StreamClientInterceptor(retry.WithMax(uint(try))))
	}
	if query.Get("backoff") == trueStr {
		opts = append(opts, grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.DefaultConfig,
			MinConnectTimeout: 2 * time.Second,
		}))
	}
	// tracing
	enableTracing := query.Get("tracing") == trueStr
	// metrics
	if !noMetrics {
		unary, stream := addMetrics(enableTracing, metaTags)
		unaryInterceptor = append(unaryInterceptor, unary)
		streamInterceptor = append(streamInterceptor, stream)
	}
	// prefix
	if uri.Path != "" && strings.HasSuffix(uri.Path, "/") {
		prefix := uri.Path[0 : len(uri.Path)-1]
		unaryInterceptor = append(unaryInterceptor, prefixUnaryClientInterceptor(prefix))
		streamInterceptor = append(streamInterceptor, prefixStreamClientInterceptor(prefix))
	}

	if len(unaryInterceptor) > 0 {
		opts = append(opts, grpc.WithChainUnaryInterceptor(unaryInterceptor...))
	}
	if len(streamInterceptor) > 0 {
		opts = append(opts, grpc.WithChainStreamInterceptor(streamInterceptor...))
	}
	uri.RawQuery = ""
	target := uri.Host
	if enableTracing {
		opts = append(opts, grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	}
	opts = append(opts, customOpts...)
	return grpc.DialContext(ctx, target, opts...)
}

func prefixUnaryClientInterceptor(prefix string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		return invoker(ctx, prefix+method, req, reply, cc, opts...)
	}
}

func prefixStreamClientInterceptor(prefix string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		return streamer(ctx, desc, cc, prefix+method, opts...)
	}
}

func addMetrics(enableTracing bool, logMeta []string) (grpc.UnaryClientInterceptor, grpc.StreamClientInterceptor) {
	clMetrics := grpcprom.NewClientMetrics(
		grpcprom.WithClientHandlingTimeHistogram(
			grpcprom.WithHistogramBuckets([]float64{0.001, 0.01, 0.1, 0.3, 0.6, 1, 3, 6, 9, 20, 30, 60, 90, 120}),
		),
	)
	reg := prometheus.DefaultRegisterer
	reg.MustRegister(clMetrics)
	labelsFromContext := func(ctx context.Context) prometheus.Labels {
		metaData := metadata.ExtractIncoming(ctx)
		labels := make(prometheus.Labels)
		for _, f := range logMeta {
			if data := metaData.Get(f); data != "" {
				labels[f] = data
			}
		}
		if enableTracing {
			if span := trace.SpanContextFromContext(ctx); span.IsSampled() {
				labels["trace_id"] = span.TraceID().String()
			}
		}
		return nil
	}
	return clMetrics.UnaryClientInterceptor(grpcprom.WithExemplarFromContext(labelsFromContext)),
		clMetrics.StreamClientInterceptor(grpcprom.WithExemplarFromContext(labelsFromContext))
}
