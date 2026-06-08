package http

import (
	"context"
	"net/url"
)

type proxyContextKey struct{}

// WithProxy stores the proxy URL in the context for per-request proxy support.
// If the uri cannot be parsed, the original context is returned unchanged.
func WithProxy(ctx context.Context, uri string) context.Context {
	proxyURL, err := url.Parse(uri)
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, proxyContextKey{}, proxyURL)
}

// ProxyFromContext extracts the proxy URL from the context, if present.
func ProxyFromContext(ctx context.Context) (*url.URL, bool) {
	proxyURL, ok := ctx.Value(proxyContextKey{}).(*url.URL)
	return proxyURL, ok
}
