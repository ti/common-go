package http

import (
	"net/http"
)

type options struct {
	client *http.Client
}

// Option the Option of http
type Option func(*options)

func evaluateOptions(opts []Option) *options {
	opt := &options{}
	for _, o := range opts {
		o(opt)
	}
	return opt
}

// WithHTTPClient with custom httpclient
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) {
		o.client = client
	}
}
