package mux

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var defaultOptions = &options{
	recovery:         true,
	queryAccessToken: true,
	methodOverride:   true,
	runTimeOpts:      nil,
	middleWares:      nil,
	bodyReWriter:     nil,
	cors: CORSConfig{
		AllowedOrigins: []string{"*"},
	},
	newErrorBody: func(grpcStatus *status.Status, statusCodeStr string) proto.Message {
		statusError := &Error{
			Error:            statusCodeStr,
			ErrorCode:        int32(grpcStatus.Code()),
			ErrorDescription: grpcStatus.Message(),
		}
		return statusError
	},
	marshalOptions: defaultMarshalOptions,
	bodyMarshaler:  defaultMarshaler,
	errorMarshaler: defaultMarshaler,
}

var defaultMarshaler = &runtime.HTTPBodyMarshaler{
	Marshaler: &runtime.JSONPb{
		MarshalOptions: defaultMarshalOptions,
		DiscardUnknown: true,
	},
}

var defaultMarshalOptions = protojson.MarshalOptions{
	Multiline:       false,
	Indent:          "",
	AllowPartial:    false,
	UseProtoNames:   true,
	UseEnumNumbers:  false,
	EmitUnpopulated: false,
}

// CORSConfig holds CORS (Cross-Origin Resource Sharing) configuration. It can
// be bound from a config file (the cors key of grpcmux.Config).
type CORSConfig struct {
	// Disabled completely disables CORS header injection.
	// Use this when CORS is handled by a reverse proxy (e.g. Envoy, Nginx).
	Disabled bool `yaml:"disabled"`
	// AllowedOrigins is the list of allowed origins.
	// Use ["*"] to allow all origins (default).
	// When set to ["*"], the response mirrors the request Origin.
	// Otherwise only origins in this list are allowed.
	AllowedOrigins []string `yaml:"allowedOrigins"`
	// AllowedHeaders is the list of extra allowed request headers
	// appended to the built-in set (Authorization, Content-Type, the
	// Connect, gRPC and gRPC-Web protocol headers, X-Request-Id, ...).
	AllowedHeaders []string `yaml:"allowedHeaders"`
	// ExposeHeaders is the list of extra response headers the browser is
	// allowed to access from JavaScript, appended to the built-in set
	// (Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin, the encoding
	// headers and X-Request-Id).
	ExposeHeaders []string `yaml:"exposeHeaders"`
}

type options struct {
	runTimeOpts       []runtime.ServeMuxOption
	middleWares       []func(http.Handler) http.Handler
	preMiddleWares    []func(http.Handler) http.Handler
	logBody           bool
	noLog             bool
	recovery          bool
	cors              CORSConfig
	authFunc          func(context.Context) (context.Context, error)
	noAuthPrefix      []string
	noAuthMethods     []string
	queryAccessToken  bool
	methodOverride    bool
	protocolErrors    bool
	routeResolver     func(*http.Request) (*http.Request, string)  // see WithRouteResolver
	preAuth           func(http.ResponseWriter, *http.Request) int // see WithPreAuth
	notFoundStatus    bool                                         // see WithNotFoundStatus
	typedErrorDetails bool                                         // see WithTypedErrorDetails
	clientIP          func(*http.Request) string                   // see WithClientIP
	marshalOptions    protojson.MarshalOptions
	bodyMarshaler     runtime.Marshaler
	errorMarshaler    runtime.Marshaler
	newErrorBody      func(grpcStatus *status.Status, statusCodeStr string) proto.Message
	bodyReWriter      func(contentType, requestID string, orgErrorBody []byte) (body []byte)
	httpAuthFunc      func(context.Context, *http.Request) (context.Context, error)
	withoutHTTPStatus bool
	useCamelCase      bool
}

// Option the Options for this module
type Option func(*options)

// WithAuthFunc pluggable function, the http auth function, you can add auth info in http header or context.
func WithAuthFunc(fn func(context.Context) (context.Context, error)) Option {
	return func(o *options) {
		o.authFunc = fn
	}
}

// WithNoAuthPrefixes skips authentication for every path that starts with one
// of the prefixes (strings.HasPrefix). A prefix is not aware of path
// segments: "/pkg.Service/Token" also exempts "/pkg.Service/TokenAdmin".
// Use it for path trees ("/public/"); for single methods and whole services
// use [WithNoAuthMethods]. Calling it again replaces the list.
func WithNoAuthPrefixes(prefix ...string) Option {
	return func(o *options) {
		o.noAuthPrefix = prefix
	}
}

// WithNoAuthMethods skips authentication for paths that match a pattern
// exactly or by whole segments (see [MatchPath]):
//
//	/pkg.Service/Method   that method (or that exact HTTP path) only
//	/pkg.Service/*        every method of the service, and any deeper path
//
// Calling it again adds to the list.
func WithNoAuthMethods(patterns ...string) Option {
	return func(o *options) {
		o.noAuthMethods = append(slices.Clip(o.noAuthMethods), patterns...)
	}
}

// MatchPath reports whether path matches one of the patterns. A pattern is an
// exact path ("/pkg.Service/Method", "/healthz"), or ends in "/*" and matches
// everything below that segment ("/pkg.Service/*" matches
// "/pkg.Service/Method" and "/pkg.Service/Method/42", but not
// "/pkg.ServiceAdmin/Method"). gRPC full methods and HTTP paths have the same
// shape, so one list serves both.
func MatchPath(patterns []string, path string) bool {
	for _, p := range patterns {
		if p == path {
			return true
		}
		if tree, ok := strings.CutSuffix(p, "*"); ok && strings.HasSuffix(tree, "/") &&
			len(path) > len(tree) && strings.HasPrefix(path, tree) {
			return true
		}
	}
	return false
}

// noAuth reports whether path is exempt from authentication.
func (o *options) noAuth(path string) bool {
	for _, v := range o.noAuthPrefix {
		if strings.HasPrefix(path, v) {
			return true
		}
	}
	return MatchPath(o.noAuthMethods, path)
}

// WithoutQueryAccessToken stops the middleware from accepting a bearer token
// in the URL (?access_token=...). By default a request without an
// Authorization header may carry it there, which browsers need for requests
// that cannot set headers (EventSource, <img>, downloads); the price is that
// URLs end up in proxy logs, browser history and Referer headers. Turn it off
// when every client can send the header.
func WithoutQueryAccessToken() Option {
	return func(o *options) {
		o.queryAccessToken = false
	}
}

// WithoutMethodOverride stops the middleware from honoring the
// X-HTTP-Method-Override request header (by default a POST may be turned into
// GET, PUT, PATCH or DELETE with it).
func WithoutMethodOverride() Option {
	return func(o *options) {
		o.methodOverride = false
	}
}

// WithProtocolErrors makes the middleware answer the requests it rejects
// itself (authentication failures, recovered panics) in the wire format of
// the RPC protocol the client speaks, when the request is unambiguously an
// RPC stream or gRPC call:
//
//	Content-Type application/connect+*   HTTP 200 + Connect end-of-stream error
//	Content-Type application/grpc*       HTTP 200 + grpc-status trailers
//	Content-Type application/grpc-web*   HTTP 200 + grpc-status headers
//
// Every other request (REST, Connect unary, plain HTTP) keeps the JSON error
// body with its HTTP status, so existing clients are not affected. Without
// this option all rejections use that JSON body, which protocol clients can
// only map back to a code through the HTTP status.
func WithProtocolErrors() Option {
	return func(o *options) {
		o.protocolErrors = true
	}
}

// WithOutLog disable log body.
func WithOutLog() Option {
	return func(o *options) {
		o.logBody = false
		o.noLog = true
	}
}

// WithOutCORS disable cors.
// Deprecated: Use WithCORS(CORSConfig{Disabled: true}) instead.
func WithOutCORS() Option {
	return func(o *options) {
		o.cors.Disabled = true
	}
}

// WithCORS sets the CORS configuration.
func WithCORS(c CORSConfig) Option {
	return func(o *options) {
		o.cors = c
	}
}

// WithLogBody log with body.
func WithLogBody() Option {
	return func(o *options) {
		o.logBody = true
	}
}

// WithErrorBodyBuilder pluggable function that performs response error body
func WithErrorBodyBuilder(fn func(grpcStatus *status.Status, statusCodeStr string) proto.Message) Option {
	return func(o *options) {
		o.newErrorBody = fn
	}
}

// WithBodyReWriter pluggable function that performs body writer
func WithBodyReWriter(fn func(contentType, requestID string, orgErrorBody []byte) (body []byte)) Option {
	return func(o *options) {
		o.bodyReWriter = fn
	}
}

// WithMarshalOptions pluggable function that performs marshal.
func WithMarshalOptions(marshalOptions protojson.MarshalOptions) Option {
	return func(o *options) {
		o.marshalOptions = marshalOptions
		o.bodyMarshaler = &runtime.HTTPBodyMarshaler{
			Marshaler: &runtime.JSONPb{
				MarshalOptions: o.marshalOptions,
				DiscardUnknown: true,
			},
		}
		marshalOptions.EmitUnpopulated = false
		o.errorMarshaler = &runtime.HTTPBodyMarshaler{
			Marshaler: &runtime.JSONPb{
				MarshalOptions: marshalOptions,
				DiscardUnknown: true,
			},
		}
	}
}

// WithoutHTTPStatus pluggable function, this function determines whether to return the http status code.
func WithoutHTTPStatus() Option {
	return func(o *options) {
		o.withoutHTTPStatus = true
	}
}

// WithoutRecovery no recovery panic.
func WithoutRecovery() Option {
	return func(o *options) {
		o.recovery = false
	}
}

// WithHTTPAuthFunc pluggable function, this function determines whether to return the http status code.
func WithHTTPAuthFunc(fn func(context.Context, *http.Request) (context.Context, error)) Option {
	return func(o *options) {
		o.httpAuthFunc = fn
	}
}

// WithPreMiddleWares registers HTTP middlewares that run OUTSIDE the default
// interceptor chain (i.e. before authFunc/logging/recovery). Use this when a
// middleware must rewrite the request (e.g. translate a Cookie into an
// Authorization header) before the auth check happens — the middlewares
// registered via WithMiddleWares run AFTER authFunc and therefore cannot
// influence its result.
func WithPreMiddleWares(middleWares ...func(http.Handler) http.Handler) Option {
	return func(o *options) {
		o.preMiddleWares = middleWares
	}
}

// WithMiddleWares pluggable function that performs middle wares.
func WithMiddleWares(middleWares ...func(http.Handler) http.Handler) Option {
	return func(o *options) {
		o.middleWares = middleWares
	}
}

// WithRunTimeOpts with runtime options
func WithRunTimeOpts(opts ...runtime.ServeMuxOption) Option {
	return func(o *options) {
		o.runTimeOpts = opts
	}
}

// WithUseCamelCase enable camelCase format for JSON response (default is snake_case)
func WithUseCamelCase() Option {
	return func(o *options) {
		o.useCamelCase = true
		// Update marshal options to use camelCase
		o.marshalOptions = protojson.MarshalOptions{
			Multiline:       false,
			Indent:          "",
			AllowPartial:    false,
			UseProtoNames:   false, // false means use JSON names (camelCase)
			UseEnumNumbers:  false,
			EmitUnpopulated: false,
		}
		// Update body marshaler
		o.bodyMarshaler = &runtime.HTTPBodyMarshaler{
			Marshaler: &runtime.JSONPb{
				MarshalOptions: o.marshalOptions,
				DiscardUnknown: true,
			},
		}
		// Update error marshaler (for consistent error response format)
		o.errorMarshaler = &runtime.HTTPBodyMarshaler{
			Marshaler: &runtime.JSONPb{
				MarshalOptions: o.marshalOptions,
				DiscardUnknown: true,
			},
		}
	}
}

func evaluateOptions(opts []Option) *options {
	optCopy := &options{}
	*optCopy = *defaultOptions
	for _, o := range opts {
		o(optCopy)
	}
	return optCopy
}

// SetDefaultOptions set global default options
func SetDefaultOptions(opts ...Option) {
	for _, o := range opts {
		o(defaultOptions)
	}
}
