// Package ratelimit limits how often a caller may call a method.
//
// [Limiter] is the declarative limiter grpcmux installs from configuration
// ([Config]): a default limit plus per-method and per-service rules, keyed by
// the authenticated subject or the client IP, on token buckets kept in memory
// ([MemoryStore]) or in a shared [Store] such as Redis.
//
// [RateLimiter] is an older, standalone fixed-window counter for ad-hoc use
// (for example "3 emails per address per hour").
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ti/common-go/log"
	"github.com/ti/common-go/tools/ip"
)

// Config is the file form of a [Limiter]. In a grpcmux configuration it is
// the rateLimit key:
//
//	apis:
//	  rateLimit:
//	    default: {rate: 50/s, burst: 100}        # per subject, per IP when anonymous
//	    rules:
//	      - {match: "/auth.v1.AuthService/*", rate: 5/s, burst: 10, key: ip}
//	      - {match: "/note.v1.NoteService/WatchNotes", rate: 1/s, burst: 5}
//	      - {match: "/internal.v1.SyncService/*", unlimited: true}
//
// A Config without a default and without rules limits nothing.
type Config struct {
	// Disabled turns the limiter off without removing the rules.
	Disabled bool `yaml:"disabled"`
	// Default applies to every method no rule of the same key matches.
	Default *Rule `yaml:"default"`
	// Rules override Default for the methods they match. The most specific
	// match wins (an exact method over its service), not the first.
	Rules []Rule `yaml:"rules"`
	// TrustedProxies lists the peers whose X-Forwarded-For is believed when
	// working out the client IP: CIDRs, addresses, or "private" (loopback and
	// private networks). Empty (the default) or "none": nobody, the client IP
	// is the connection's peer. Behind a proxy this MUST be set, or every
	// client shares the proxy's bucket; it must not name peers that are not
	// proxies, or callers choose their own bucket. See ip.ParseTrustedProxies.
	TrustedProxies []string `yaml:"trustedProxies"`
	// IPv6Prefix is the prefix length a bucket keyed by an IPv6 client
	// address belongs to. A subscriber typically controls a whole /64 and can
	// use a new address for every request, so the default is 64; 128 limits
	// each address on its own. IPv4 addresses are always limited singly.
	IPv6Prefix int `yaml:"ipv6Prefix"`
	// KeyPrefix namespaces the bucket keys in a shared store. Services that
	// share one Redis must use different prefixes unless they are meant to
	// share budgets. Default "ratelimit".
	KeyPrefix string `yaml:"keyPrefix"`
}

// Rule is one limit.
type Rule struct {
	// Match selects the methods the rule applies to: a full method or an
	// HTTP path ("/pkg.Service/Method", "/auth/v1/token"), or a whole
	// service or path tree ("/pkg.Service/*", "/*"). Not used on Default.
	Match string `yaml:"match"`
	// Rate is the sustained rate, "<count>/<period>": "10/s", "600/m",
	// "1000/h", "5/10s".
	Rate string `yaml:"rate"`
	// Burst is the bucket size: how many requests an idle caller may send at
	// once. 0 means the count of Rate.
	Burst int `yaml:"burst"`
	// Key is what a bucket belongs to:
	//
	//	subject  (default) the authenticated caller; the client IP for
	//	         requests that carry no identity. Checked after authentication.
	//	ip       the client IP. Checked before authentication, so it also
	//	         bounds requests whose credentials are then rejected: use it
	//	         for sign-in, token and other public methods.
	Key string `yaml:"key"`
	// PerMethod gives every method the rule matches its own bucket. By
	// default all methods under one rule share the caller's bucket.
	PerMethod bool `yaml:"perMethod"`
	// Unlimited exempts the matched methods from limits of either key.
	Unlimited bool `yaml:"unlimited"`
}

// Key values of a Rule.
const (
	KeySubject = "subject"
	KeyIP      = "ip"
)

// Stage is the point of a request at which a limit is checked.
type Stage int

const (
	// PreAuth checks the rules keyed by client IP, before authentication.
	PreAuth Stage = iota
	// PostAuth checks the rules keyed by subject, after authentication.
	PostAuth
)

// Reason and Domain identify a rate-limit rejection in a google.rpc.ErrorInfo
// detail on every protocol.
const (
	Reason = "RATE_LIMIT_EXCEEDED"
	Domain = "ratelimit"
)

// Decision is the outcome of [Limiter.Allow].
type Decision struct {
	// Limited is true when the request must be rejected.
	Limited bool
	// Rule is the Match of the rule that was applied ("default" for the
	// default rule, empty when no rule applied).
	Rule string
	// Key is the kind of bucket that was charged: "subject" or "ip".
	Key string
	// Limit is the applied limit.
	Limit Limit
	// RetryAfter is how long the caller should wait. Zero unless Limited.
	RetryAfter time.Duration
	// Remaining is the number of requests left in the bucket.
	Remaining int
}

// Limiter applies a Config. It is safe for concurrent use.
type Limiter struct {
	rules   []*rule // user rules, then built-in exemptions
	deflt   *rule
	store   Store
	memory  *MemoryStore // the default store, and the fallback when store fails
	proxies *ip.TrustedProxies
	prefix  string
	v6bits  int

	storeErrors prometheus.Counter
	lastWarn    atomic.Int64
}

type rule struct {
	name      string // Match, or "default"
	exact     string // exact path, or ""
	tree      string // path prefix ending in "/", or ""
	limit     Limit
	limitTag  string // "#<interval µs>:<burst>", the end of every bucket key of the rule
	key       string
	perMethod bool
	unlimited bool

	allowed, limited prometheus.Counter
}

// Option configures a Limiter.
type Option func(*Limiter)

// WithStore keeps the buckets in store instead of process memory, so that a
// limit holds across replicas. A nil store is ignored. When the store returns
// an error the request is checked against an in-memory bucket instead: an
// outage degrades to per-replica limits, it does not lift them.
func WithStore(store Store) Option {
	return func(l *Limiter) {
		if store != nil {
			l.store = store
		}
	}
}

// exemptPaths are never limited: probes must not fail because of traffic.
var exemptPaths = []string{"/grpc.health.v1.Health/*", "/healthz", "/readyz", "/favicon.ico"}

// New builds a Limiter. It returns (nil, nil) when cfg limits nothing
// (disabled, or no default and no rules), and a nil *Limiter allows
// everything.
func New(cfg Config, opts ...Option) (*Limiter, error) {
	if cfg.Disabled || (cfg.Default == nil && len(cfg.Rules) == 0) {
		return nil, nil //nolint:nilnil // a nil Limiter is the documented "off" value
	}
	proxies, err := ip.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("ratelimit: %w", err)
	}
	l := &Limiter{
		memory:      NewMemoryStore(),
		proxies:     proxies,
		prefix:      cfg.KeyPrefix,
		storeErrors: storeErrors(),
	}
	if l.prefix == "" {
		l.prefix = "ratelimit"
	}
	switch {
	case cfg.IPv6Prefix == 0:
		l.v6bits = 64
	case cfg.IPv6Prefix < 16 || cfg.IPv6Prefix > 128:
		return nil, fmt.Errorf("ratelimit: ipv6Prefix %d must be between 16 and 128", cfg.IPv6Prefix)
	default:
		l.v6bits = cfg.IPv6Prefix
	}
	l.store = l.memory
	if cfg.Default != nil {
		if cfg.Default.Match != "" {
			return nil, errors.New("ratelimit: default must not set match")
		}
		l.deflt, err = compile(*cfg.Default, "default")
		if err != nil {
			return nil, err
		}
		if l.deflt.unlimited {
			l.deflt = nil
		}
	}
	for i, r := range cfg.Rules {
		if r.Match == "" {
			return nil, fmt.Errorf("ratelimit: rules[%d] has no match", i)
		}
		c, err := compile(r, r.Match)
		if err != nil {
			return nil, err
		}
		l.rules = append(l.rules, c)
	}
	for _, p := range exemptPaths {
		c, _ := compile(Rule{Match: p, Unlimited: true}, p)
		l.rules = append(l.rules, c)
	}
	for _, o := range opts {
		o(l)
	}
	// Said once, at startup: which addresses the limits are keyed by is the
	// part of this configuration that is easiest to get wrong.
	log.Action("ratelimit").Info(fmt.Sprintf("rate limiting on: %d rule(s), default %v; trusted proxies: %s; IPv6 clients keyed by /%d",
		len(cfg.Rules), cfg.Default != nil, l.proxies, l.v6bits))
	return l, nil
}

// ipKey is the bucket identity of a client address: the address itself for
// IPv4, its network of the configured prefix length for IPv6.
func (l *Limiter) ipKey(clientIP string) string {
	addr, err := netip.ParseAddr(clientIP)
	if err != nil || !addr.Unmap().Is6() || l.v6bits >= 128 {
		return clientIP
	}
	p, err := addr.WithZone("").Prefix(l.v6bits)
	if err != nil {
		return clientIP
	}
	return p.String()
}

func compile(r Rule, name string) (*rule, error) {
	c := &rule{name: name, perMethod: r.PerMethod, unlimited: r.Unlimited}
	if tree, ok := strings.CutSuffix(r.Match, "*"); ok {
		if !strings.HasSuffix(tree, "/") {
			return nil, fmt.Errorf("ratelimit: match %q: \"*\" must follow a \"/\" (for example /pkg.Service/*)", r.Match)
		}
		c.tree = tree
	} else {
		c.exact = r.Match
	}
	switch strings.ToLower(r.Key) {
	case "", KeySubject:
		c.key = KeySubject
	case KeyIP:
		c.key = KeyIP
	default:
		return nil, fmt.Errorf("ratelimit: rule %q: key %q is not \"subject\" or \"ip\"", name, r.Key)
	}
	if c.unlimited {
		return c, nil
	}
	if r.Rate == "" {
		return nil, fmt.Errorf("ratelimit: rule %q needs a rate or unlimited: true", name)
	}
	limit, err := ParseLimit(r.Rate, r.Burst)
	if err != nil {
		return nil, fmt.Errorf("ratelimit: rule %q: %w", name, err)
	}
	c.limit = limit
	// The limit is part of the bucket key. A bucket's stored time only means
	// something together with the interval and burst it was written under:
	// when a rule's limit changes (a rolling deploy, two services on one
	// prefix) the new limit starts fresh buckets instead of reinterpreting,
	// and rewriting, the old ones.
	c.limitTag = "#" + strconv.FormatInt(limit.Interval().Microseconds(), 10) + ":" + strconv.Itoa(limit.Burst)
	c.allowed = requestsTotal().WithLabelValues(name, c.key, "allowed")
	c.limited = requestsTotal().WithLabelValues(name, c.key, "limited")
	return c, nil
}

// specificity ranks a match: -1 no match, otherwise the longer pattern wins
// and an exact path beats a tree of the same length.
func (r *rule) specificity(path string) int {
	switch {
	case r.exact != "":
		if r.exact == path {
			return 2*len(r.exact) + 1
		}
	case len(path) > len(r.tree) && strings.HasPrefix(path, r.tree):
		return 2 * len(r.tree)
	}
	return -1
}

// match returns the rule to apply to method at stage, or nil for no limit.
func (l *Limiter) match(stage Stage, method string) *rule {
	key := KeySubject
	if stage == PreAuth {
		key = KeyIP
	}
	var best *rule
	bestScore := -1
	if l.deflt != nil && l.deflt.key == key {
		best, bestScore = l.deflt, 0
	}
	for _, r := range l.rules {
		if !r.unlimited && r.key != key {
			continue
		}
		if s := r.specificity(method); s > bestScore {
			best, bestScore = r, s
		}
	}
	if best == nil || best.unlimited {
		return nil
	}
	return best
}

// ClientIP returns the client address for a connection from remoteAddr with
// the given X-Forwarded-For values, honouring the configured trusted proxies.
func (l *Limiter) ClientIP(remoteAddr string, forwardedFor []string) string {
	if l == nil {
		return (*ip.TrustedProxies)(nil).ClientIP(remoteAddr, forwardedFor)
	}
	return l.proxies.ClientIP(remoteAddr, forwardedFor)
}

// Allow charges one request for method at the given stage. clientIP is the
// caller's address (see [Limiter.ClientIP]); subject identifies an
// authenticated caller and is only used at PostAuth, where an empty subject
// falls back to the client IP. With neither, the request is not limited.
func (l *Limiter) Allow(ctx context.Context, stage Stage, method, clientIP, subject string) Decision {
	if l == nil {
		return Decision{}
	}
	r := l.match(stage, method)
	if r == nil {
		return Decision{}
	}
	kind, value := KeyIP, clientIP
	if stage == PostAuth && subject != "" {
		kind, value = KeySubject, subject
	}
	if value == "" {
		return Decision{}
	}
	if kind == KeyIP {
		value = l.ipKey(value)
	}
	var key strings.Builder
	key.Grow(len(l.prefix) + len(r.name) + len(kind) + len(value) + len(method) + len(r.limitTag) + 4)
	key.WriteString(l.prefix)
	key.WriteByte(':')
	key.WriteString(r.name)
	key.WriteByte('|')
	key.WriteString(kind)
	key.WriteByte('|')
	key.WriteString(value)
	if r.perMethod {
		key.WriteByte('|')
		key.WriteString(method)
	}
	key.WriteString(r.limitTag)
	res, err := l.store.Take(ctx, key.String(), r.limit, 1)
	if err != nil {
		l.storeFailed(ctx, err)
		res, _ = l.memory.Take(ctx, key.String(), r.limit, 1)
	}
	d := Decision{Rule: r.name, Key: kind, Limit: r.limit, Remaining: res.Remaining}
	if res.Allowed {
		r.allowed.Inc()
		return d
	}
	r.limited.Inc()
	d.Limited = true
	d.RetryAfter = res.RetryAfter
	return d
}

// storeFailed counts a store error and logs it at most every ten seconds.
func (l *Limiter) storeFailed(ctx context.Context, err error) {
	l.storeErrors.Inc()
	now := time.Now().UnixNano()
	last := l.lastWarn.Load()
	if now-last < int64(10*time.Second) || !l.lastWarn.CompareAndSwap(last, now) {
		return
	}
	log.Extract(ctx).Action("ratelimit").Warn("rate limit store failed, limiting in memory: " + err.Error())
}

// Prometheus collectors are process-wide and created once, so a process may
// build more than one Limiter.
var (
	requestsTotal = sync.OnceValue(func() *prometheus.CounterVec {
		c := prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ratelimit_requests_total",
			Help: "Requests checked by the rate limiter, by rule, bucket key kind (subject or ip) and result (allowed or limited).",
		}, []string{"rule", "key", "result"})
		prometheus.DefaultRegisterer.MustRegister(c)
		return c
	})
	storeErrors = sync.OnceValue(func() prometheus.Counter {
		c := prometheus.NewCounter(prometheus.CounterOpts{
			Name: "ratelimit_store_errors_total",
			Help: "Rate limit checks that fell back to the in-memory store because the shared store failed.",
		})
		prometheus.DefaultRegisterer.MustRegister(c)
		return c
	})
)
