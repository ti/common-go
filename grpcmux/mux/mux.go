// Package mux provider mux for gprc and http
package mux

import (
	"net/http"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
)

const (
	requestIDHeader = "x-request-id"
	requestIDTag    = "request_id"
	requestTag      = "request"
)

// ServeMux the custom serve mux that implement grpc ServeMux to simplify the http restful.
type ServeMux struct {
	serveMux *runtime.ServeMux
	opts     *options
	handler  http.Handler
}

// NewServeMux allocates and returns a new ServeMux.
func NewServeMux(opts ...Option) *ServeMux {
	o := evaluateOptions(opts)
	if o.noLog {
		o.logBody = false
	}
	mux := &ServeMux{
		opts: o,
	}
	muxOpts := []runtime.ServeMuxOption{
		runtime.WithIncomingHeaderMatcher(func(s string) (string, bool) {
			// it is already inject in interceptor
			return "", false
		}),

		runtime.WithOutgoingHeaderMatcher(func(s string) (string, bool) {
			if strings.HasPrefix(s, "x-") || isPermanentMetaKey(s) {
				return s, true
			}
			return "", false
		}),
		runtime.WithMetadata(medaGetter(o)),
		runtime.WithErrorHandler(httpErrorHandler(o)),
		runtime.WithRoutingErrorHandler(routingErrorHandler(o)),
		runtime.WithMarshalerOption(runtime.MIMEWildcard, o.bodyMarshaler),
		runtime.WithForwardResponseOption(forwardResponser(o)),
	}

	// register default error codes
	muxOpts = append(muxOpts, mux.opts.runTimeOpts...)
	mux.serveMux = runtime.NewServeMux(muxOpts...)
	// Chain order (outer to inner): preMiddleWares → defaultInterceptor → middleWares → serveMux.
	// preMiddleWares wrap OUTSIDE defaultInterceptor so they can rewrite the request
	// (e.g. translate Cookie → Authorization) before authFunc rejects it.
	middleWares := make([]func(http.Handler) http.Handler, 0, len(o.preMiddleWares)+1+len(o.middleWares))
	middleWares = append(middleWares, o.preMiddleWares...)
	middleWares = append(middleWares, defaultInterceptor(o))
	middleWares = append(middleWares, o.middleWares...)
	mux.handler = handlerWithMiddleWares(mux.serveMux, middleWares...)
	return mux
}

// Handle http path
func (srv *ServeMux) Handle(method, path string, h runtime.HandlerFunc) {
	err := srv.serveMux.HandlePath(method, path, h)
	if err != nil {
		panic(err)
	}
}

// ServeHTTP handle http path
func (srv *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	srv.handler.ServeHTTP(w, r)
}

// ServeMux return grpc gateway server mux
func (srv *ServeMux) ServeMux() *runtime.ServeMux {
	return srv.serveMux
}

// Middleware wraps the given handler with the same HTTP interceptor chain
// (logging, auth, recovery, request-id, metadata injection, etc.)
// that is applied to the gateway routes.
// Use this to ensure custom handlers registered via Server.Handle share
// the same observability and security behaviour as gRPC-Gateway routes.
func (srv *ServeMux) Middleware(h http.Handler) http.Handler {
	middleWares := make([]func(http.Handler) http.Handler, 0, len(srv.opts.preMiddleWares)+1+len(srv.opts.middleWares))
	middleWares = append(middleWares, srv.opts.preMiddleWares...)
	middleWares = append(middleWares, defaultInterceptor(srv.opts))
	middleWares = append(middleWares, srv.opts.middleWares...)
	return handlerWithMiddleWares(h, middleWares...)
}

// isPermanentHTTPHeader checks whether hdr belongs to the list of
// permanent request headers maintained by IANA.
// http://www.iana.org/assignments/message-headers/message-headers.xml
func isPermanentMetaKey(hdr string) bool {
	switch hdr {
	case
		"accept",
		"accept-charset",
		"accept-language",
		"accept-ranges",
		"authorization",
		"cache-control",
		"content-type",
		"cookie",
		"location",
		"date",
		"expect",
		"from",
		"host",
		"if-match",
		"if-modified-since",
		"if-none-match",
		"if-schedule-tag-match",
		"if-unmodified-since",
		"max-forwards",
		"origin",
		"pragma",
		"referer",
		"user-agent",
		"via",
		"warning":
		return true
	}
	return false
}
