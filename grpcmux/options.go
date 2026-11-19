package grpcmux

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	muxlogging "github.com/ti/common-go/grpcmux/logging"
	"github.com/ti/common-go/grpcmux/mux"
	"github.com/ti/common-go/log"
	"github.com/ti/common-go/tools/ratelimit"
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
	noAuthMethods                []string
	reflection                   bool
	drainStreams                 bool
	drainStreamsGrace            time.Duration
	protocolErrors               bool
	withoutQueryAccessToken      bool
	withoutMethodOverride        bool
	debugHandlers                *bool
	drain                        *drainer // set by NewServer
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
	platform                     platformOptions // REST, rate limit, deadlines
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

// WithTracing creates OpenTelemetry server spans for gRPC calls and HTTP
// requests (health probes excluded) and adds trace_id and span_id to the
// request log line. Spans go to the global TracerProvider: install one with
// tracing.Init (package github.com/ti/common-go/tracing), otherwise they are
// dropped.
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

// WithNoAuthPrefixes skips authentication for gRPC full methods and HTTP
// paths that start with one of the prefixes (strings.HasPrefix). A prefix is
// not aware of path segments: "/pkg.Service/Token" also exempts
// "/pkg.Service/TokenAdmin" and "/pkg.Service/Token/anything". Use it for
// HTTP path trees such as "/public/"; to exempt single methods or whole
// services use [WithNoAuthMethods]. Calling it again replaces the list.
func WithNoAuthPrefixes(prefix ...string) Option {
	return func(o *options) {
		o.noAuthPrefix = prefix
	}
}

// WithNoAuthMethods skips authentication for gRPC full methods and HTTP paths
// that match a pattern exactly or by whole segments:
//
//	/pkg.Service/Method   that method (or that exact HTTP path) and nothing else
//	/pkg.Service/*        every method of the service (and any deeper HTTP path)
//
// Unlike [WithNoAuthPrefixes], exempting "/pkg.Service/Token" does not exempt
// "/pkg.Service/TokenAdmin". Both options can be used together; calling this
// one again adds to the list.
func WithNoAuthMethods(patterns ...string) Option {
	return func(o *options) {
		o.noAuthMethods = append(slices.Clip(o.noAuthMethods), patterns...)
	}
}

// WithReflection serves gRPC server reflection (grpc.reflection.v1 and
// v1alpha) for every registered service, on the gRPC port and, over the gRPC,
// gRPC-Web and Connect protocols, on the HTTP port, so grpcurl, buf curl
// --reflect and similar tools work without local proto files. It is off by
// default because it publishes the complete API schema.
//
// Reflection is authenticated like any other method: with [WithAuthFunc] a
// caller needs valid credentials (grpcurl -H "authorization: Bearer ...").
// To serve it without credentials, exempt it:
//
//	grpcmux.WithNoAuthMethods(grpcmux.ReflectionMethods...)
func WithReflection() Option {
	return func(o *options) {
		o.reflection = true
	}
}

// WithDrainStreams ends streaming RPCs that are still open when the server
// drains, instead of letting them hold shutdown until GRACEFUL_TIMEOUT.
//
// grace after draining begins (see [Server.Draining]; that is after the
// PRE_STOP wait) the context of every open streaming call is canceled with
// the cause [ErrServerDraining]; a zero grace cancels at once. A handler that
// returns because of it ends the call with UNAVAILABLE "server is draining",
// delivered as a normal end of stream on every protocol (gRPC status
// trailers, Connect end-of-stream envelope, gRPC-Web trailer frame), so the
// client sees a clean, retryable end instead of a broken connection. Unary
// calls are never canceled; they finish within GRACEFUL_TIMEOUT as before.
//
// Handlers that want to end a stream their own way (a last message, an OK
// status) watch [Draining] and return before the grace expires.
func WithDrainStreams(grace time.Duration) Option {
	return func(o *options) {
		o.drainStreams = true
		o.drainStreamsGrace = max(grace, 0)
	}
}

// WithProtocolErrors answers requests the HTTP middleware rejects itself
// (authentication failures, recovered panics) in the client's RPC wire format
// when the request is a Connect stream, gRPC or gRPC-Web call: HTTP 200 with
// the error in the end-of-stream envelope or the grpc-status trailers, as the
// handlers themselves answer. Other requests (REST, Connect unary, plain
// HTTP) keep the JSON error body and HTTP status. See mux.WithProtocolErrors.
func WithProtocolErrors() Option {
	return func(o *options) {
		o.protocolErrors = true
	}
}

// WithoutQueryAccessToken stops the HTTP port from accepting a bearer token
// in the URL (?access_token=...) when a request has no Authorization header.
// See mux.WithoutQueryAccessToken.
func WithoutQueryAccessToken() Option {
	return func(o *options) {
		o.withoutQueryAccessToken = true
	}
}

// WithoutMethodOverride stops the HTTP port from honoring the
// X-HTTP-Method-Override request header.
func WithoutMethodOverride() Option {
	return func(o *options) {
		o.withoutMethodOverride = true
	}
}

// WithDebugHandlers controls GET /debug/** on the HTTP port, which serves
// http.DefaultServeMux (net/http/pprof and expvar register there when a
// program imports them).
//
// The route is off unless WithDebugHandlers(true) (or debugHandlers: true in
// the config) turns it on, because profiles and variables must not be
// public. When it is on and an AuthFunc is configured, it is authenticated
// like every other route; make sure that AuthFunc really rejects strangers
// (the AuthFunc of a nil oidc.Provider, guest mode, admits everyone).
func WithDebugHandlers(enabled bool) Option {
	return func(o *options) {
		o.debugHandlers = &enabled
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
	// REST serves methods annotated with google.api.http as REST routes; see
	// [WithREST].
	REST bool `yaml:"rest"`
	// TypedErrorDetails adds "@type" to the details of JSON error bodies;
	// see [WithTypedErrorDetails]. Implied by REST.
	TypedErrorDetails bool `yaml:"typedErrorDetails"`
	// RateLimit are the rate-limit rules; see [WithRateLimit].
	RateLimit *ratelimit.Config `yaml:"rateLimit"`
	// Timeouts are server-side deadlines for unary calls; see [WithTimeouts].
	Timeouts *TimeoutConfig `yaml:"timeouts"`
	// NoAuthMethods are exact or whole-segment auth exemptions; see
	// [WithNoAuthMethods].
	NoAuthMethods []string `yaml:"noAuthMethods"`
	// Reflection serves gRPC server reflection; see [WithReflection].
	Reflection bool `yaml:"reflection"`
	// DrainStreams ends open streaming RPCs when the server drains; see
	// [WithDrainStreams]. DrainStreamsGrace is the time handlers get to end
	// on their own first, as a Go duration ("5s"); empty means none.
	DrainStreams      bool   `yaml:"drainStreams"`
	DrainStreamsGrace string `yaml:"drainStreamsGrace"`
	// ProtocolErrors: see [WithProtocolErrors].
	ProtocolErrors bool `yaml:"protocolErrors"`
	// DisableQueryAccessToken: see [WithoutQueryAccessToken].
	DisableQueryAccessToken bool `yaml:"disableQueryAccessToken"`
	// DisableMethodOverride: see [WithoutMethodOverride].
	DisableMethodOverride bool `yaml:"disableMethodOverride"`
	// DebugHandlers: "true" serves GET /debug/** on the HTTP port; empty or
	// "false" does not. See [WithDebugHandlers].
	DebugHandlers string `yaml:"debugHandlers"`
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
		applyPlatformConfig(o, c)
		if len(c.NoAuthMethods) > 0 {
			WithNoAuthMethods(c.NoAuthMethods...)(o)
		}
		if c.Reflection {
			o.reflection = true
		}
		if c.DrainStreams {
			grace, err := parseOptionalDuration(c.DrainStreamsGrace)
			if err != nil {
				// Zero is the safe reading: streams end as soon as draining begins.
				log.Action("grpcmux.Config").Error("drainStreamsGrace: " + err.Error() + "; using 0s")
			}
			WithDrainStreams(grace)(o)
		}
		if c.ProtocolErrors {
			o.protocolErrors = true
		}
		if c.DisableQueryAccessToken {
			o.withoutQueryAccessToken = true
		}
		if c.DisableMethodOverride {
			o.withoutMethodOverride = true
		}
		if c.DebugHandlers != "" {
			enabled, err := strconv.ParseBool(c.DebugHandlers)
			if err != nil {
				// Off is the safe reading of a value that is not a boolean.
				log.Action("grpcmux.Config").Error("debugHandlers: " + err.Error() + "; using false")
			}
			o.debugHandlers = &enabled
		}
	}
}

func parseOptionalDuration(v string) (time.Duration, error) {
	if v == "" {
		return 0, nil
	}
	return time.ParseDuration(v)
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
// [WithAutoHTTP] routes, and the unary and streaming handlers registered on
// [Server.ConnectServer] or with [RegisterConnectStreams]) from running the
// gRPC interceptor chain.
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
