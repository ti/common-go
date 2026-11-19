package grpcmux

import (
	"context"

	"github.com/ti/common-go/tools/ip"
	"github.com/ti/common-go/tools/ratelimit"
)

// platformOptions are the options of the REST, rate-limit and deadline
// features (rest.go, ratelimit.go, timeout.go).
type platformOptions struct {
	rest        bool
	typedErrors bool
	rateLimit   *ratelimit.Config
	rateStore   ratelimit.Store
	rateSubject func(ctx context.Context) string
	timeouts    *TimeoutConfig

	limiter      *ratelimit.Limiter // built on first use, see options.rateLimiter
	limiterBuilt bool

	proxies      *ip.TrustedProxies // built on first use, see options.trustedProxies
	proxiesBuilt bool
}

// applyPlatformConfig is the WithConfig part of these features.
func applyPlatformConfig(o *options, c *Config) {
	if c.REST {
		o.platform.rest = true
	}
	if c.TypedErrorDetails {
		o.platform.typedErrors = true
	}
	if c.RateLimit != nil {
		o.platform.rateLimit = c.RateLimit
	}
	if c.Timeouts != nil {
		o.platform.timeouts = c.Timeouts
	}
}

// WithTypedErrorDetails makes the JSON error bodies of the HTTP port (REST
// and gateway routes, and the errors the HTTP middleware answers itself)
// list their details with "@type", so that a client can find the
// google.rpc.ErrorInfo among them; see mux.WithTypedErrorDetails. It is off
// by default because it changes a published wire format; [WithREST] turns it
// on. Errors a handler writes with mux.WriteHTTPErrorResponse follow
// mux.SetDefaultOptions(mux.WithTypedErrorDetails()).
func WithTypedErrorDetails() Option {
	return func(o *options) {
		o.platform.typedErrors = true
	}
}
