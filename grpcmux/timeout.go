package grpcmux

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/log"
)

// TimeoutConfig sets server-side deadlines for unary calls (the timeouts key
// of [Config]):
//
//	apis:
//	  timeouts:
//	    default: 10s
//	    rules:
//	      - {match: "/report.v1.ReportService/*", timeout: 60s}
//	      - {match: "/agent.v1.AgentService/Generate", timeout: none}
//
// See [WithTimeouts].
type TimeoutConfig struct {
	// Default bounds every unary call no rule matches: a Go duration, or
	// empty, "0" or "none" for no bound.
	Default string `yaml:"default"`
	// Rules override Default; the most specific match wins.
	Rules []TimeoutRule `yaml:"rules"`
}

// TimeoutRule is the deadline of one method or service.
type TimeoutRule struct {
	// Match is a full method ("/pkg.Service/Method") or a whole service
	// ("/pkg.Service/*").
	Match string `yaml:"match"`
	// Timeout is a Go duration; "0" or "none" lifts the default.
	Timeout string `yaml:"timeout"`
}

// WithTimeouts gives unary calls a server-side deadline, on every protocol:
// a handler whose client sent no deadline (or a very long one) cannot run
// forever. The earlier of the client's deadline (grpc-timeout,
// Connect-Timeout-Ms) and the configured one applies; a call that exceeds it
// fails with DEADLINE_EXCEEDED, provided the handler honours its context.
//
// Streaming calls are never bounded by it: a deadline would cut a healthy
// stream. Bound those in the handler, or with the client's deadline.
func WithTimeouts(cfg TimeoutConfig) Option {
	return func(o *options) {
		o.platform.timeouts = &cfg
	}
}

type methodTimeouts struct {
	deflt time.Duration
	rules []timeoutRule
	cache sync.Map // full method -> time.Duration
}

type timeoutRule struct {
	exact, tree string
	d           time.Duration
}

func parseTimeout(v string) (time.Duration, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "none":
		return 0, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil || d < 0 {
		return 0, fmt.Errorf("timeout %q is not a duration", v)
	}
	return d, nil
}

func newMethodTimeouts(cfg *TimeoutConfig) (*methodTimeouts, error) {
	if cfg == nil {
		return nil, nil //nolint:nilnil // nil means "no deadlines"
	}
	t := &methodTimeouts{}
	var err error
	if t.deflt, err = parseTimeout(cfg.Default); err != nil {
		return nil, fmt.Errorf("timeouts.default: %w", err)
	}
	for i, r := range cfg.Rules {
		d, err := parseTimeout(r.Timeout)
		if err != nil {
			return nil, fmt.Errorf("timeouts.rules[%d]: %w", i, err)
		}
		rule := timeoutRule{d: d}
		switch tree, ok := strings.CutSuffix(r.Match, "*"); {
		case r.Match == "":
			return nil, fmt.Errorf("timeouts.rules[%d] has no match", i)
		case ok && !strings.HasSuffix(tree, "/"):
			return nil, fmt.Errorf("timeouts.rules[%d]: match %q: \"*\" must follow a \"/\"", i, r.Match)
		case ok:
			rule.tree = tree
		default:
			rule.exact = r.Match
		}
		t.rules = append(t.rules, rule)
	}
	if t.deflt == 0 && len(t.rules) == 0 {
		return nil, nil //nolint:nilnil // nothing to enforce
	}
	return t, nil
}

// forMethod returns the deadline of a method; 0 means none. Full methods are
// a fixed set, so the result is cached.
func (t *methodTimeouts) forMethod(fullMethod string) time.Duration {
	if d, ok := t.cache.Load(fullMethod); ok {
		return d.(time.Duration)
	}
	d, best := t.deflt, -1
	for _, r := range t.rules {
		score := -1
		switch {
		case r.exact != "":
			if r.exact == fullMethod {
				score = 2*len(r.exact) + 1
			}
		case len(fullMethod) > len(r.tree) && strings.HasPrefix(fullMethod, r.tree):
			score = 2 * len(r.tree)
		}
		if score > best {
			d, best = r.d, score
		}
	}
	t.cache.Store(fullMethod, d)
	return d
}

// methodTimeouts builds the deadline table once per option set.
func (o *options) methodTimeouts() *methodTimeouts {
	t, err := newMethodTimeouts(o.platform.timeouts)
	if err != nil {
		log.Action("grpcmux.Config").Fatal("%v", err)
	}
	return t
}

// timeoutUnaryInterceptor applies the deadlines. It is part of the unary
// chains only, which is what exempts streams.
func timeoutUnaryInterceptor(t *methodTimeouts) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		d := t.forMethod(info.FullMethod)
		if d <= 0 {
			return handler(ctx, req)
		}
		ctx, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		resp, err := handler(ctx, req)
		// A handler that returns the context's bare error would be reported
		// as UNKNOWN on the HTTP bridges; say what happened.
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) && status.Code(err) == codes.Unknown {
			return nil, status.Error(codes.DeadlineExceeded, "deadline exceeded: the call took longer than "+d.String())
		}
		return resp, err
	}
}
