package grpcmux

import (
	"context"
	"crypto/tls"
	"encoding/json/v2"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
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
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/ti/common-go/graceful"
	"github.com/ti/common-go/grpcmux/logging"
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

// NewClientConnWithURI creates a gRPC client connection from a URI such as
// dns://svc.ns.svc:8081?timeout=5s&try=3. The host (with an optional scheme
// such as dns://) is the gRPC target; a path ending in "/" is prepended to
// every method. Query parameters:
//
//	secure=true             TLS (also implied by a scheme ending in "s")
//	timeout=5s              per-call timeout
//	try=N                   retry failed calls up to N times
//	retryPolicy=native      retry with gRPC's built-in retry policy (UNAVAILABLE only,
//	                        exponential backoff, at most 4 retries) instead of the interceptor
//	backoff=true            reconnect with gRPC's default connection backoff
//	keepalive=30s           send keepalive pings on idle connections (keeps NAT/LB
//	                        mappings alive; the server must allow the interval —
//	                        grpcmux servers accept 10s or more)
//	waitForReady=true       calls wait for the connection to be ready instead of failing fast
//	block=true              NewClientConnWithURI waits until the connection is ready
//	loadBalancingPolicy=…   load-balancing policy (default round_robin)
//	log=true, logBody=true  client access log (with bodies)
//	metrics=false           disable Prometheus client metrics
//	tracing=true            OpenTelemetry client spans
//
// nolint:funlen
func NewClientConnWithURI(ctx context.Context, uri *url.URL, customOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
	query := uri.Query()
	falseStr := "false"
	trueStr := "true"
	logeEnable := query.Get("log") == trueStr
	secure := useTLS(uri)
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
	nativeRetry := query.Get("retryPolicy") == "native" && try > 0
	opts = append(opts, grpc.WithDefaultServiceConfig(serviceConfig(loadBalancingPolicy, nativeRetry, try)))
	if d, err := time.ParseDuration(query.Get("keepalive")); err == nil && d > 0 {
		opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                d,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}))
	}
	if query.Get("waitForReady") == trueStr {
		opts = append(opts, grpc.WithDefaultCallOptions(grpc.WaitForReady(true)))
	}

	// grpc.WithBlock is not supported by grpc.NewClient; the block behaviour is
	// emulated below by explicitly connecting and waiting for a ready state.
	block := query.Get("block") == trueStr

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
	if try > 0 && !nativeRetry {
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
	target, prefix := grpcTarget(uri)
	if prefix != "" {
		unaryInterceptor = append(unaryInterceptor, prefixUnaryClientInterceptor(prefix))
		streamInterceptor = append(streamInterceptor, prefixStreamClientInterceptor(prefix))
	}

	if len(unaryInterceptor) > 0 {
		opts = append(opts, grpc.WithChainUnaryInterceptor(unaryInterceptor...))
	}
	if len(streamInterceptor) > 0 {
		opts = append(opts, grpc.WithChainStreamInterceptor(streamInterceptor...))
	}
	if enableTracing {
		opts = append(opts, grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	}
	opts = append(opts, customOpts...)
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, err
	}
	// Emulate the deprecated grpc.WithBlock behaviour: trigger connection and
	// wait until the channel becomes ready or the context is done.
	if block {
		conn.Connect()
		for {
			state := conn.GetState()
			if state == connectivity.Ready {
				break
			}
			if !conn.WaitForStateChange(ctx, state) {
				// ctx expired or was cancelled.
				_ = conn.Close()
				return nil, ctx.Err()
			}
		}
	}
	return conn, nil
}

// useTLS reports whether the connection uses TLS: secure=true/false decides
// explicitly; otherwise https:// and grpcs:// mean TLS and every other scheme
// (dns, unix, http, custom resolvers) means plaintext.
func useTLS(uri *url.URL) bool {
	switch uri.Query().Get("secure") {
	case "true":
		return true
	case "false":
		return false
	}
	return uri.Scheme == "https" || uri.Scheme == "grpcs"
}

// grpcTarget converts a dependency URI into a gRPC target and an optional
// method prefix:
//
//	dns://svc.ns:8081           → "svc.ns:8081" (default dns resolver)
//	dns://svc.ns:8081/prefix/   → "svc.ns:8081", methods prefixed with "/prefix"
//	dns:///svc.ns:8081          → "dns:///svc.ns:8081"
//	unix:///run/app.sock        → "unix:///run/app.sock"
//	polaris:///Prod/svc:8081    → "polaris:///Prod/svc:8081" (custom resolver)
func grpcTarget(uri *url.URL) (target, prefix string) {
	if uri.Host == "" {
		return uri.Scheme + "://" + uri.Path, ""
	}
	if len(uri.Path) > 1 && strings.HasSuffix(uri.Path, "/") {
		prefix = strings.TrimSuffix(uri.Path, "/")
	}
	return uri.Host, prefix
}

func prefixUnaryClientInterceptor(prefix string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker,
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

// clientMetrics is process-wide so any number of clients can share it.
var clientMetrics = sync.OnceValue(func() *grpcprom.ClientMetrics {
	m := grpcprom.NewClientMetrics(
		grpcprom.WithClientHandlingTimeHistogram(
			grpcprom.WithHistogramBuckets([]float64{0.001, 0.01, 0.1, 0.3, 0.6, 1, 3, 6, 9, 20, 30, 60, 90, 120}),
		),
	)
	prometheus.DefaultRegisterer.MustRegister(m)
	return m
})

func addMetrics(enableTracing bool, logMeta []string) (grpc.UnaryClientInterceptor, grpc.StreamClientInterceptor) {
	clMetrics := clientMetrics()
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
		if len(labels) == 0 {
			return nil
		}
		return labels
	}
	return clMetrics.UnaryClientInterceptor(grpcprom.WithExemplarFromContext(labelsFromContext)),
		clMetrics.StreamClientInterceptor(grpcprom.WithExemplarFromContext(labelsFromContext))
}

// serviceConfig builds the default service config: the load-balancing
// policy (round_robin unless set) and, for retryPolicy=native, a retry
// policy for every method. gRPC caps maxAttempts at 5.
func serviceConfig(lbPolicy string, nativeRetry bool, try uint64) string {
	if lbPolicy == "" {
		lbPolicy = roundrobin.Name
	}
	type retryPolicy struct {
		MaxAttempts          uint64   `json:"maxAttempts"`
		InitialBackoff       string   `json:"initialBackoff"`
		MaxBackoff           string   `json:"maxBackoff"`
		BackoffMultiplier    float64  `json:"backoffMultiplier"`
		RetryableStatusCodes []string `json:"retryableStatusCodes"`
	}
	type methodConfig struct {
		Name        []struct{}  `json:"name"`
		RetryPolicy retryPolicy `json:"retryPolicy"`
	}
	cfg := struct {
		LoadBalancingConfig []map[string]struct{} `json:"loadBalancingConfig"`
		MethodConfig        []methodConfig        `json:"methodConfig,omitempty"`
	}{
		LoadBalancingConfig: []map[string]struct{}{{lbPolicy: {}}},
	}
	if nativeRetry {
		cfg.MethodConfig = []methodConfig{{
			Name: []struct{}{{}}, // all methods
			RetryPolicy: retryPolicy{
				MaxAttempts:          min(try+1, 5),
				InitialBackoff:       "0.1s",
				MaxBackoff:           "2s",
				BackoffMultiplier:    2,
				RetryableStatusCodes: []string{"UNAVAILABLE"},
			},
		}}
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		panic(err) // static shape; cannot fail
	}
	return string(b)
}
