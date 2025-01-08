package logging

import (
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"google.golang.org/grpc"
	pref "google.golang.org/protobuf/reflect/protoreflect"
)

var defaultOptions = &options{
	shouldLog: DefaultLoggingDecider,
	bodyEncoder: &bodyEncoder{
		maskFields: map[pref.Name]bool{},
	},
}

func evaluateOpt(opts []Option) *options {
	optCopy := &options{}
	*optCopy = *defaultOptions
	for _, o := range opts {
		o(optCopy)
	}
	return optCopy
}

// UnaryClientInterceptor returns a new unary client interceptor that optionally
// logs the execution of external gRPC calls.
func UnaryClientInterceptor(logger logging.Logger, opts ...Option) grpc.UnaryClientInterceptor {
	o := evaluateOpt(opts)
	return interceptors.UnaryClientInterceptor(reportable(logger, o))
}

// StreamClientInterceptor returns a new streaming client interceptor that optionally logs the
// execution of external gRPC calls.
func StreamClientInterceptor(logger logging.Logger, opts ...Option) grpc.StreamClientInterceptor {
	o := evaluateOpt(opts)
	return interceptors.StreamClientInterceptor(reportable(logger, o))
}

// UnaryServerInterceptor returns a new unary server interceptors that optionally logs endpoint handling.
func UnaryServerInterceptor(logger logging.Logger, opts ...Option) grpc.UnaryServerInterceptor {
	o := evaluateOpt(opts)
	return interceptors.UnaryServerInterceptor(reportable(logger, o))
}

// StreamServerInterceptor returns a new stream server interceptors that optionally logs endpoint handling.
func StreamServerInterceptor(logger logging.Logger, opts ...Option) grpc.StreamServerInterceptor {
	o := evaluateOpt(opts)
	return interceptors.StreamServerInterceptor(reportable(logger, o))
}
