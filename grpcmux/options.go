package grpcmux

import (
	"net/http"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	muxlogging "github.com/ti/common-go/grpcmux/logging"
	"github.com/ti/common-go/grpcmux/mux"
	"github.com/ti/common-go/tools/routerlimit"
)

var defaultOptions = &options{
	httpAddr:    ":8080",
	grpcAddr:    ":8081",
	metricsAddr: ":9090",
	metaTags:    []string{"client_id", "user_id", "device_id", "request_id"},

	keepaliveParams: keepalive.ServerParameters{
		// Recycle connections so clients re-resolve and rebalance across pods.
		// MaxConnectionAgeGrace stays at gRPC's default (infinite), so streams
		// in progress are never cut by this.
		MaxConnectionAge: time.Minute,
	},
	keepalivePolicy: keepalive.EnforcementPolicy{
		// gRPC's default MinTime is 5 minutes: a client that pings more often
		// gets GOAWAY "too_many_pings". Accept the 10s+ intervals that client
		// keepalive (and most proxies) use, also on idle connections.
		MinTime:             10 * time.Second,
		PermitWithoutStream: true,
	},
	readHeaderTimeout:    10 * time.Second,
	idleTimeout:          5 * time.Minute,
	maxHeaderBytes:       1 << 20,
	maxConcurrentStreams: 1000,
	maxRequestBodyBytes:  32 << 20, // matches the largest message the HTTP bridges accept
}

type options struct {
	loggingOpts                  []muxlogging.Option
	logger                       logging.Logger
	authFunction                 auth.AuthFunc
	limiter                      *routerlimit.Limiter
	grpcAddr                     string
	httpAddr                     string
	metricsAddr                  string
	metaTags                     []string
	authzPolicy                  string
	grpcServerOpts               []grpc.ServerOption
	grpcUnaryServerInterceptors  []grpc.UnaryServerInterceptor
	grpcStreamServerInterceptors []grpc.StreamServerInterceptor
	httpMiddleWares              []func(http.Handler) http.Handler
	preHTTPMiddleWares           []func(http.Handler) http.Handler
	noAuthPrefix                 []string
	withOutKeepAliveOpts         bool
	autoHTTP                     bool
	tracing                      bool
	logBody                      bool
	useCamelCase                 bool
	healthCheckers               []HealthChecker
	tlsCertFile                  string
	tlsKeyFile                   string
	cors                         *mux.CORSConfig
	disableClientPriority        bool
	withoutHTTPInterceptors      bool
	keepaliveParams              keepalive.ServerParameters
	keepalivePolicy              keepalive.EnforcementPolicy
	readHeaderTimeout            time.Duration
	idleTimeout                  time.Duration
	maxHeaderBytes               int
	maxConcurrentStreams         int
	maxRequestBodyBytes          int64
}

func evaluateOptions(opts []Option) *options {
	optCopy := &options{}
	*optCopy = *defaultOptions
	for _, o := range opts {
		o(optCopy)
	}
	return optCopy
}

// Option the option for this module
type Option func(*options)

// WithoutKeepAliveParams leaves the gRPC server's keepalive parameters and
// enforcement policy at gRPC's own defaults instead of grpcmux's (see
// [WithKeepalive]).
func WithoutKeepAliveParams() Option {
	return func(o *options) {
		o.withOutKeepAliveOpts = true
	}
}

// WithTracing do not auto add otel tracing
func WithTracing() Option {
	return func(o *options) {
		o.tracing = true
	}
}

// WithAuthFunc pluggable function that performs authentication.
func WithAuthFunc(f auth.AuthFunc) Option {
	return func(o *options) {
		o.authFunction = f
	}
}

// WithNoAuthPrefixes pluggable function that performs no authentication.
func WithNoAuthPrefixes(prefix ...string) Option {
	return func(o *options) {
		o.noAuthPrefix = prefix
	}
}

// WithLoggingOptions Decider how log output.
func WithLoggingOptions(opts ...muxlogging.Option) Option {
	return func(o *options) {
		o.loggingOpts = opts
	}
}

// WithAuthzPolicy add authzPolicy for router.
// refer: [google.golang.org/grpc/authz.authorizationPolicy]
func WithAuthzPolicy(authzPolicy string) Option {
	return func(o *options) {
		o.authzPolicy = authzPolicy
	}
}

// WithMetaTags add tags from metadata
func WithMetaTags(tags []string) Option {
	return func(o *options) {
		o.metaTags = tags
	}
}

// CORSConfig is an alias for mux.CORSConfig exposed at the grpcmux package level.
type CORSConfig = mux.CORSConfig

// Config is the file/YAML form of the most common options. Zero values keep
// the defaults.
type Config struct {
	GrpcAddr     string      `yaml:"grpcAddr"`
	HTTPAddr     string      `yaml:"httpAddr"`
	MetricsAddr  string      `yaml:"metricsAddr"`
	LogBody      bool        `yaml:"logBody"`
	Tracing      bool        `yaml:"tracing"`
	UseCamelCase bool        `yaml:"useCamelCase"`
	TLSCertFile  string      `yaml:"tlsCertFile"`
	TLSKeyFile   string      `yaml:"tlsKeyFile"`
	CORS         *CORSConfig `yaml:"cors"`
	// MaxRequestBodyBytes limits request bodies on the ConnectRPC and
	// auto-HTTP bridges (default 32 MiB).
	MaxRequestBodyBytes int64 `yaml:"maxRequestBodyBytes"`
	// DisableHTTPInterceptors turns off the gRPC interceptor chain on the
	// HTTP bridges; see [WithoutHTTPInterceptors].
	DisableHTTPInterceptors bool `yaml:"disableHTTPInterceptors"`
}

// WithConfig init with config
func WithConfig(c *Config) Option {
	return func(o *options) {
		if c == nil {
			return
		}
		if c.GrpcAddr != "" {
			o.grpcAddr = c.GrpcAddr
		}
		if c.HTTPAddr != "" {
			o.httpAddr = c.HTTPAddr
		}
		if c.MetricsAddr != "" {
			o.metricsAddr = c.MetricsAddr
		}
		if c.LogBody {
			o.logBody = true
			o.loggingOpts = []muxlogging.Option{
				muxlogging.WithDecider(muxlogging.DefaultLoggingBodyDecider),
				muxlogging.WithBodyMaskField("password"),
			}
		}
		if c.Tracing {
			o.tracing = c.Tracing
		}
		if c.UseCamelCase {
			o.useCamelCase = true
		}
		if c.TLSCertFile != "" && c.TLSKeyFile != "" {
			o.tlsCertFile = c.TLSCertFile
			o.tlsKeyFile = c.TLSKeyFile
		}
		if c.CORS != nil {
			o.cors = c.CORS
		}
		if c.MaxRequestBodyBytes > 0 {
			o.maxRequestBodyBytes = c.MaxRequestBodyBytes
		}
		if c.DisableHTTPInterceptors {
			o.withoutHTTPInterceptors = true
		}
	}
}

// WithCORS sets the CORS configuration for the HTTP server.
//
// Example — restrict to specific origins:
//
//	gs := grpcmux.NewServer(grpcmux.WithCORS(grpcmux.CORSConfig{
//	    AllowedOrigins: []string{"https://example.com", "https://app.example.com"},
//	    ExposeHeaders:  []string{"X-Request-Id"},
//	}))
//
// Example — disable CORS (handled by reverse proxy):
//
//	gs := grpcmux.NewServer(grpcmux.WithCORS(grpcmux.CORSConfig{Disabled: true}))
func WithCORS(c CORSConfig) Option {
	return func(o *options) {
		o.cors = &c
	}
}

// WithAutoHTTP auto set http handler for grpc service
func WithAutoHTTP() Option {
	return func(o *options) {
		o.autoHTTP = true
	}
}

// WithLimiter Decider how log output.
func WithLimiter(l *routerlimit.Limiter) Option {
	return func(o *options) {
		o.limiter = l
	}
}

// WithGrpcAddr set grpc addr.
func WithGrpcAddr(s string) Option {
	return func(o *options) {
		o.grpcAddr = s
	}
}

// WithHTTPAddr set http addr.
func WithHTTPAddr(s string) Option {
	return func(o *options) {
		o.httpAddr = s
	}
}

// WithMetricsAddr set metrics addr.
func WithMetricsAddr(s string) Option {
	return func(o *options) {
		o.metricsAddr = s
	}
}

// WithGRPCServerOption with other grpc options
func WithGRPCServerOption(opts ...grpc.ServerOption) Option {
	return func(o *options) {
		o.grpcServerOpts = opts
	}
}

// WithGRPCUnaryServerInterceptor with other unaryServer interceptor
func WithGRPCUnaryServerInterceptor(i ...grpc.UnaryServerInterceptor) Option {
	return func(o *options) {
		o.grpcUnaryServerInterceptors = i
	}
}

// WithGRPCStreamServerInterceptors with other stream interceptor
func WithGRPCStreamServerInterceptors(i ...grpc.StreamServerInterceptor) Option {
	return func(o *options) {
		o.grpcStreamServerInterceptors = i
	}
}

// WithHTTPMiddleWares with http middleware
func WithHTTPMiddleWares(middleWares ...func(http.Handler) http.Handler) Option {
	return func(o *options) {
		o.httpMiddleWares = middleWares
	}
}

// WithPreHTTPMiddleWares registers HTTP middlewares that run OUTSIDE the
// default interceptor chain (before authFunc / logging / recovery). Use this
// when a middleware must rewrite the request before the auth check — e.g.
// translate a HttpOnly cookie into an Authorization: Bearer header so the
// downstream authFunc sees a valid token. Middlewares registered via
// WithHTTPMiddleWares run AFTER authFunc and therefore cannot influence its
// result.
func WithPreHTTPMiddleWares(middleWares ...func(http.Handler) http.Handler) Option {
	return func(o *options) {
		o.preHTTPMiddleWares = middleWares
	}
}

// WithUseCamelCase enable camelCase format for JSON response (default is snake_case)
func WithUseCamelCase() Option {
	return func(o *options) {
		o.useCamelCase = true
	}
}

// WithTLS enables HTTPS/TLS mode for the HTTP server.
// When set, the server listens with TLS instead of h2c (cleartext HTTP/2).
// Use this when TLS terminates at the Go server rather than at a reverse proxy.
// If not set (default), the server uses h2c, which is suitable for deployments
// behind a TLS-terminating proxy (e.g., Envoy Gateway, Nginx, AWS ALB).
//
// Example:
//
//	// TLS mode: browser connects directly with HTTPS
//	gs := grpcmux.NewServer(grpcmux.WithTLS("/path/to/cert.pem", "/path/to/key.pem"))
//
//	// h2c mode (default): TLS terminated at Envoy/proxy
//	gs := grpcmux.NewServer()
func WithTLS(certFile, keyFile string) Option {
	return func(o *options) {
		o.tlsCertFile = certFile
		o.tlsKeyFile = keyFile
	}
}

// WithDisableClientPriority disables RFC 9218 HTTP/2 client-priority
// handling (added in Go 1.27, https://go.dev/issue/75500). By default the
// server prioritizes streams per the client's declared priority; setting
// this serves requests round-robin instead, matching pre-Go-1.27 behavior.
func WithDisableClientPriority() Option {
	return func(o *options) {
		o.disableClientPriority = true
	}
}

// WithoutHTTPInterceptors stops the HTTP bridges ([RegisterConnectService],
// [WithAutoHTTP] routes and [WithConnectOptions]) from running the gRPC unary
// interceptor chain.
//
// By default those bridges run the same chain as native gRPC — validation,
// authz policy, metrics, interceptors added with
// [WithGRPCUnaryServerInterceptor], and panic recovery — so one service
// implementation behaves the same on every protocol. Authentication, access
// logging and rate limiting are the exceptions: HTTP middleware does them for
// HTTP requests (rate-limit HTTP traffic with routerlimit.NewHandler in
// WithHTTPMiddleWares).
//
// grpc-gateway's generated RegisterXxxHandlerServer functions call the
// service directly and cannot run interceptors. To get them on REST routes,
// register the gateway against the in-memory connection instead:
//
//	pb.RegisterXxxHandlerClient(ctx, gs.ServeMux(), pb.NewXxxClient(gs.Conn()))
func WithoutHTTPInterceptors() Option {
	return func(o *options) {
		o.withoutHTTPInterceptors = true
	}
}

// WithKeepalive sets the gRPC server's keepalive parameters and enforcement
// policy. The defaults recycle connections every minute (without cutting
// in-flight streams) and accept client pings every 10s or slower.
func WithKeepalive(params keepalive.ServerParameters, policy keepalive.EnforcementPolicy) Option {
	return func(o *options) {
		o.keepaliveParams = params
		o.keepalivePolicy = policy
	}
}

// WithHTTPTimeouts sets the HTTP server's ReadHeaderTimeout (default 10s) and
// IdleTimeout (default 5m). Zero keeps a value unchanged.
//
// There is deliberately no read/write timeout: those would cut streaming RPCs
// (server streaming, Connect streams, WebSockets). Bound individual requests
// with deadlines instead.
func WithHTTPTimeouts(readHeader, idle time.Duration) Option {
	return func(o *options) {
		if readHeader > 0 {
			o.readHeaderTimeout = readHeader
		}
		if idle > 0 {
			o.idleTimeout = idle
		}
	}
}

// WithMaxConcurrentStreams sets the HTTP/2 concurrent-stream limit per
// connection on the HTTP port (default 1000).
func WithMaxConcurrentStreams(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.maxConcurrentStreams = n
		}
	}
}

// WithMaxRequestBodyBytes limits request bodies accepted by the ConnectRPC
// and auto-HTTP bridges (default 32 MiB). Larger requests fail with
// RESOURCE_EXHAUSTED.
func WithMaxRequestBodyBytes(n int64) Option {
	return func(o *options) {
		if n > 0 {
			o.maxRequestBodyBytes = n
		}
	}
}

// SetDefaultOptions set global default options
func SetDefaultOptions(opts ...Option) {
	for _, o := range opts {
		o(defaultOptions)
	}
}
