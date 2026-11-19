package mux

import (
	"context"
	"net/http"
)

// WithRouteResolver lets the server tell the middleware which RPC a request
// is for when the path is not the RPC's own "/pkg.Service/Method" (a REST
// route). fn is called once per request, after CORS and
// X-HTTP-Method-Override; it returns the request to continue with (it may
// carry the resolved route in its context) and the RPC's full method, or the
// request unchanged and "".
//
// For a resolved request the access log's action is the full method, and
// the request is exempt from authentication when either its path or its full
// method matches [WithNoAuthMethods] / [WithNoAuthPrefixes].
func WithRouteResolver(fn func(r *http.Request) (*http.Request, string)) Option {
	return func(o *options) {
		o.routeResolver = fn
	}
}

// WithPreAuth registers a check that runs for every request before
// authentication, after CORS and the request's log fields (so its rejections
// carry CORS headers and are logged like any other response). fn returns 0
// to let the request continue, or the HTTP status of the response it wrote.
// grpcmux uses it for rate limits keyed by client IP.
func WithPreAuth(fn func(w http.ResponseWriter, r *http.Request) int) Option {
	return func(o *options) {
		o.preAuth = fn
	}
}

// WithNotFoundStatus makes a request for a path no route matches answer with
// HTTP 404 and Content-Type application/json.
//
// Without it the same not_found JSON body is sent with HTTP 200, which is
// what this mux has always done and what a health check pointed at an
// unrouted path (such as "/") may silently depend on. grpcmux turns it on
// together with REST routes (grpcmux.WithREST); it is planned to become the
// default.
func WithNotFoundStatus() Option {
	return func(o *options) {
		o.notFoundStatus = true
	}
}

// WithTypedErrorDetails renders the details of a JSON error body with their
// type, as google.protobuf.Any JSON:
//
//	"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"...","domain":"..."}]
//
// so a client can tell which detail is which, as it can in a Connect error
// or a gRPC status. A detail with no set fields is then listed by its type.
//
// Without it (the default) each detail is the bare JSON of its message,
// {"reason":"...","domain":"..."}, and a detail with no set fields is left
// out: the format this package has always written, which clients of existing
// APIs may decode strictly.
//
// On a ServeMux it covers the errors of its routes and of its middleware.
// [WriteHTTPErrorResponse], which handlers call directly, follows the package
// defaults: mux.SetDefaultOptions(mux.WithTypedErrorDetails()).
func WithTypedErrorDetails() Option {
	return func(o *options) {
		o.typedErrorDetails = true
	}
}

type methodNotAllowedKey struct{}

// MarkMethodNotAllowed returns a context that tells the gateway mux: if no
// route matches this request, answer "method not allowed" instead of "not
// found", because the path is known under another HTTP method to a router in
// front of it (the REST routes of grpcmux.WithREST).
func MarkMethodNotAllowed(ctx context.Context) context.Context {
	return context.WithValue(ctx, methodNotAllowedKey{}, true)
}
