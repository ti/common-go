package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"golang.org/x/sync/singleflight"
)

var (
	errDiscoveryHTTPStatus = errors.New("discovery endpoint returned non-success status")
	errIssuerMismatch      = errors.New("issuer mismatch")
)

// Discovery holds the OpenID Connect discovery document fields in use.
type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	JwksURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

// Provider validates tokens for one OIDC client and performs its OAuth
// calls. Safe for concurrent use once constructed.
type Provider struct {
	cfg       Config
	opts      options
	discovery Discovery

	keys    atomic.Pointer[keySet]
	refresh singleflight.Group
}

// Open parses an auth URI and connects to its issuer (discovery + JWKS).
func Open(ctx context.Context, uri string, opts ...Option) (*Provider, error) {
	cfg, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}

	return New(ctx, cfg, opts...)
}

// New connects to cfg.Issuer: it fetches discovery and the JWKS, and fails
// when either is unavailable (auth must not fail open).
func New(ctx context.Context, cfg Config, opts ...Option) (*Provider, error) {
	p := new(Provider)

	err := p.open(ctx, cfg, opts)
	if err != nil {
		return nil, err
	}

	return p, nil
}

// NewFromDiscovery builds a provider from a known discovery document without
// any network call. Keys come from [WithKeys]; when discovery has a jwks_uri
// an unknown kid still triggers a refresh. Meant for tests and offline use.
func NewFromDiscovery(cfg Config, discovery Discovery, opts ...Option) *Provider {
	p := new(Provider)
	p.setup(cfg, discovery, opts)

	return p
}

// Init lets common-go's config.Init build the provider from a URI field
// (dependencies.Dependency); see the package documentation for the format.
func (p *Provider) Init(ctx context.Context, u *url.URL) error {
	cfg, err := ParseURL(u)
	if err != nil {
		return err
	}

	return p.open(ctx, cfg, nil)
}

// Config returns the configuration the provider was built from.
func (p *Provider) Config() Config { return p.cfg }

// ClientID is the OAuth client id; access tokens must carry it in aud.
func (p *Provider) ClientID() string { return p.cfg.ClientID }

// Discovery returns the discovery document read at startup.
func (p *Provider) Discovery() Discovery { return p.discovery }

func (p *Provider) open(ctx context.Context, cfg Config, opts []Option) error {
	err := cfg.validate()
	if err != nil {
		return err
	}

	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}

	discovery, err := fetchDiscovery(ctx, o.httpClient, cfg.Issuer)
	if err != nil {
		return fmt.Errorf("fetch discovery: %w", err)
	}

	p.setup(cfg, discovery, opts)

	keys, err := p.fetchJWKS(ctx)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}

	p.keys.Store(keys)

	return nil
}

func (p *Provider) setup(cfg Config, discovery Discovery, opts []Option) {
	p.cfg = cfg
	p.discovery = discovery
	p.opts = defaultOptions()

	for _, opt := range opts {
		opt(&p.opts)
	}

	p.keys.Store(new(p.opts.keys))
}

func fetchDiscovery(ctx context.Context, client *http.Client, issuer string) (Discovery, error) {
	var discovery Discovery

	err := getJSON(ctx, client, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration",
		errDiscoveryHTTPStatus, &discovery)
	if err != nil {
		return Discovery{}, err
	}

	if normalizeIssuer(discovery.Issuer) == "" || normalizeIssuer(discovery.Issuer) != normalizeIssuer(issuer) {
		return Discovery{}, fmt.Errorf("%w: configured %q, discovered %q", errIssuerMismatch, issuer, discovery.Issuer)
	}

	return discovery, nil
}

// getJSON GETs url and decodes a 2xx JSON body into v; other statuses wrap
// statusErr with the status text.
func getJSON(ctx context.Context, client *http.Client, url string, statusErr error, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: %s", statusErr, resp.Status)
	}

	err = json.NewDecoder(resp.Body).Decode(v)
	if err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}

	return nil
}
