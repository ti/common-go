package oidc

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// URIScheme is the scheme of an auth configuration URI.
const URIScheme = "oidc"

// DefaultScope is requested when the URI has no scope parameter.
const DefaultScope = "openid profile email"

// Defaults for [Option]s.
const (
	// DefaultClockSkew is tolerated for nbf / iat (contract: 60 seconds).
	DefaultClockSkew = 60 * time.Second
	// DefaultHTTPTimeout bounds every call to the issuer.
	DefaultHTTPTimeout = 10 * time.Second
	// DefaultJWKSRefreshInterval is the minimum gap between JWKS refreshes
	// triggered by an unknown kid, so random kids cannot turn every request
	// into a JWKS fetch.
	DefaultJWKSRefreshInterval = 10 * time.Second
)

// ErrInvalidURI reports an auth URI that cannot configure a provider.
var ErrInvalidURI = errors.New("oidc: invalid auth uri")

// Config is everything an auth URI says. Build it with [ParseURI] /
// [ParseURL], or by hand for an issuer that is not reachable over https
// (tests, local IdPs).
type Config struct {
	// Issuer is the issuer URL, e.g. https://account.nx.run.
	Issuer       string
	ClientID     string
	ClientSecret string
	// Scope is the space-separated scope requested at authorization.
	Scope string
	// RPInitiatedLogout selects OIDC RP-initiated logout
	// (post_logout_redirect_uri + id_token_hint). False selects the
	// Cognito-style logout_uri flow. URI: oidc_logout=false turns it off.
	RPInitiatedLogout bool
	// Access holds the claim rules; empty admits every valid token.
	Access AccessPolicy
}

// ParseURI parses an auth URI, see the package documentation.
func ParseURI(raw string) (Config, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalidURI, err)
	}

	return ParseURL(u)
}

// ParseURL is [ParseURI] for an already parsed URL.
func ParseURL(u *url.URL) (Config, error) {
	if u.Scheme != URIScheme {
		return Config{}, fmt.Errorf("%w: scheme %q, want %q", ErrInvalidURI, u.Scheme, URIScheme)
	}

	if u.Host == "" {
		return Config{}, fmt.Errorf("%w: missing issuer host", ErrInvalidURI)
	}

	if u.User.Username() == "" {
		return Config{}, fmt.Errorf("%w: missing client id", ErrInvalidURI)
	}

	// Strict: u.Query() would silently drop a malformed pair (and with it,
	// say, an access rule), so a typo must fail at startup instead.
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Config{}, fmt.Errorf("%w: query: %w", ErrInvalidURI, err)
	}

	// Query().Get already decodes once; the second unescape keeps parity with
	// the Python and Rust backends, which decode scope twice.
	scope, err := url.QueryUnescape(query.Get(queryScope))
	if err != nil {
		return Config{}, fmt.Errorf("%w: scope: %w", ErrInvalidURI, err)
	}

	if scope == "" {
		scope = DefaultScope
	}

	access, err := ParseAccessPolicy(query)
	if err != nil {
		return Config{}, err
	}

	secret, _ := u.User.Password()

	return Config{
		Issuer:            "https://" + u.Host + u.Path,
		ClientID:          u.User.Username(),
		ClientSecret:      secret,
		Scope:             scope,
		RPInitiatedLogout: query.Get(queryOIDCLogout) != "false",
		Access:            access,
	}, nil
}

// Scopes returns Scope split into its values.
func (c Config) Scopes() []string {
	return strings.Fields(c.Scope)
}

// Redacted is the configuration as a URI-like string safe for logs.
func (c Config) Redacted() string {
	secret := ""
	if c.ClientSecret != "" {
		secret = "xxxxx"
	}

	rules := make([]string, 0, len(c.Access.Rules))
	for _, rule := range c.Access.Rules {
		rules = append(rules, rule.Path+"="+strings.Join(rule.Values, " "))
	}

	return fmt.Sprintf("issuer=%s client_id=%s secret=%s scope=%q rp_logout=%t rules=%q",
		c.Issuer, c.ClientID, secret, c.Scope, c.RPInitiatedLogout, rules)
}

func (c Config) validate() error {
	switch {
	case c.Issuer == "":
		return fmt.Errorf("%w: missing issuer", ErrInvalidURI)
	case c.ClientID == "":
		return fmt.Errorf("%w: missing client id", ErrInvalidURI)
	default:
		return nil
	}
}

// Option tunes a [Provider].
type Option func(*options)

type options struct {
	httpClient      *http.Client
	now             func() time.Time
	clockSkew       time.Duration
	refreshInterval time.Duration
	keys            keySet
}

func defaultOptions() options {
	return options{
		httpClient:      &http.Client{Timeout: DefaultHTTPTimeout},
		now:             time.Now,
		clockSkew:       DefaultClockSkew,
		refreshInterval: DefaultJWKSRefreshInterval,
	}
}

// WithHTTPClient sets the client used for discovery, JWKS and the token
// endpoint. It should carry a timeout.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.httpClient = c }
}

// WithClock replaces time.Now for token time checks.
func WithClock(now func() time.Time) Option {
	return func(o *options) { o.now = now }
}

// WithClockSkew sets the tolerance for nbf / iat.
func WithClockSkew(d time.Duration) Option {
	return func(o *options) { o.clockSkew = d }
}

// WithJWKSRefreshInterval sets the minimum gap between unknown-kid refreshes.
func WithJWKSRefreshInterval(d time.Duration) Option {
	return func(o *options) { o.refreshInterval = d }
}

// WithKeys installs verification keys by kid, e.g. for [NewFromDiscovery]
// with no reachable JWKS endpoint.
func WithKeys(keys map[string]*rsa.PublicKey) Option {
	return func(o *options) { o.keys = keySet{keys: keys} }
}
