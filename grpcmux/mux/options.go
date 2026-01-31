package mux

import (
	"context"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var defaultOptions = &options{
	recovery:     true,
	runTimeOpts:  nil,
	middleWares:  nil,
	bodyReWriter: nil,
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
		UnmarshalOptions: protojson.UnmarshalOptions{
			DiscardUnknown: true,
		},
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

type options struct {
	runTimeOpts       []runtime.ServeMuxOption
	middleWares       []func(http.Handler) http.Handler
	logBody           bool
	noLog             bool
	recovery          bool
	noCors            bool
	authFunc          func(context.Context) (context.Context, error)
	noAuthPrefix      []string
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

// WithNoAuthPrefixes pluggable function that performs no authentication.
// WithNoAuthPrefixes pluggable function that performs no authentication.
func WithNoAuthPrefixes(prefix ...string) Option {
	return func(o *options) {
		o.noAuthPrefix = prefix
	}
}

// WithOutLog disable log body.
func WithOutLog() Option {
	return func(o *options) {
		o.logBody = false
		o.noLog = true
	}
}

// WithOutCORS disable cors
func WithOutCORS() Option {
	return func(o *options) {
		o.noCors = true
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
				UnmarshalOptions: protojson.UnmarshalOptions{
					DiscardUnknown: true,
				},
			},
		}
		marshalOptions.EmitUnpopulated = false
		o.errorMarshaler = &runtime.HTTPBodyMarshaler{
			Marshaler: &runtime.JSONPb{
				MarshalOptions: marshalOptions,
				UnmarshalOptions: protojson.UnmarshalOptions{
					DiscardUnknown: true,
				},
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
				UnmarshalOptions: protojson.UnmarshalOptions{
					DiscardUnknown: true,
				},
			},
		}
		// Update error marshaler (for consistent error response format)
		o.errorMarshaler = &runtime.HTTPBodyMarshaler{
			Marshaler: &runtime.JSONPb{
				MarshalOptions: o.marshalOptions,
				UnmarshalOptions: protojson.UnmarshalOptions{
					DiscardUnknown: true,
				},
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
