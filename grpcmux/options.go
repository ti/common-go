package grpcmux

import (
	"net/http"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth"
	muxlogging "github.com/ti/common-go/grpcmux/logging"
	"github.com/ti/common-go/tools/routerlimit"
	"google.golang.org/grpc"
)

var defaultOptions = &options{
	httpAddr:    ":8080",
	grpcAddr:    ":8081",
	metricsAddr: ":9090",
	metaTags:    []string{"client_id", "user_id", "device_id", "request_id"},
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
	noAuthPrefix                 []string
	withOutKeepAliveOpts         bool
	autoHTTP                     bool
	tracing                      bool
	logBody                      bool
	useCamelCase                 bool
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

// WithoutKeepAliveParams do not auto add grpc.KeepaliveParams on server
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

// Config the config exporter.
type Config struct {
	GrpcAddr     string `yaml:"grpcAddr"`
	HTTPAddr     string `yaml:"httpAddr"`
	MetricsAddr  string `yaml:"metricsAddr"`
	LogBody      bool   `yaml:"logBody"`
	Tracing      bool   `yaml:"tracing"`
	UseCamelCase bool   `yaml:"useCamelCase"`
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

// WithUseCamelCase enable camelCase format for JSON response (default is snake_case)
func WithUseCamelCase() Option {
	return func(o *options) {
		o.useCamelCase = true
	}
}

// withOptions with the current options.
func withOptions(opt *options) Option {
	return func(o *options) {
		*o = *opt
	}
}

// SetDefaultOptions set global default options
func SetDefaultOptions(opts ...Option) {
	for _, o := range opts {
		o(defaultOptions)
	}
}
