package oidc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// countingIssuer wraps a fakeIssuer, counting discovery requests and able to
// go down.
type countingIssuer struct {
	*fakeIssuer

	discoveryHits atomic.Int32
	down          atomic.Bool
}

func newCountingIssuer(t *testing.T) *countingIssuer {
	t.Helper()

	c := &countingIssuer{fakeIssuer: newFakeIssuer(t)}
	inner := c.Config.Handler
	c.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)

			return
		}

		if r.URL.Path == "/.well-known/openid-configuration" {
			c.discoveryHits.Add(1)
		}

		inner.ServeHTTP(w, r)
	})

	return c
}

func lazyProvider(t *testing.T, c *countingIssuer, ttl time.Duration, now func() time.Time) *Provider {
	t.Helper()

	cfg, err := ParseURI(c.uri("scope=openid"))
	if err != nil {
		t.Fatal(err)
	}

	opts := []Option{WithHTTPClient(c.Client()), WithLazyDiscovery(ttl)}
	if now != nil {
		opts = append(opts, WithClock(now))
	}

	p, err := New(context.Background(), cfg, opts...)
	if err != nil {
		t.Fatalf("New (lazy) must not touch the network: %v", err)
	}

	return p
}

// A lazy provider starts while the issuer is unreachable (for example, the
// issuer is this process and its port is not listening yet).
func TestLazyDiscoveryStartsWithIssuerDown(t *testing.T) {
	c := newCountingIssuer(t)
	c.down.Store(true)

	p := lazyProvider(t, c, time.Hour, nil)
	if c.discoveryHits.Load() != 0 || c.jwksHits.Load() != 0 {
		t.Fatal("lazy provider fetched at construction")
	}

	if (p.Discovery() != Discovery{}) {
		t.Fatalf("Discovery before first load = %+v", p.Discovery())
	}

	if _, err := p.Discover(t.Context()); err == nil {
		t.Fatal("Discover with the issuer down and nothing cached must fail")
	}

	c.down.Store(false)

	d, err := p.Discover(t.Context())
	if err != nil || d.TokenEndpoint != c.URL+"/token" {
		t.Fatalf("Discover = %+v, %v", d, err)
	}

	if _, err := p.Discover(t.Context()); err != nil || c.discoveryHits.Load() != 1 {
		t.Fatalf("second Discover refetched (hits %d) or failed: %v", c.discoveryHits.Load(), err)
	}
}

// After the TTL discovery is refetched; a failed refetch serves the stale copy.
func TestLazyDiscoveryTTLAndStaleFallback(t *testing.T) {
	c := newCountingIssuer(t)
	now := time.Now()
	p := lazyProvider(t, c, time.Hour, func() time.Time { return now })

	if _, err := p.Discover(t.Context()); err != nil {
		t.Fatal(err)
	}

	now = now.Add(2 * time.Hour)

	if _, err := p.Discover(t.Context()); err != nil || c.discoveryHits.Load() != 2 {
		t.Fatalf("expected a refetch after the TTL (hits %d): %v", c.discoveryHits.Load(), err)
	}

	now = now.Add(2 * time.Hour)
	c.down.Store(true)

	d, err := p.Discover(t.Context())
	if err != nil || d.TokenEndpoint == "" {
		t.Fatalf("stale fallback: %+v, %v", d, err)
	}
}

// ValidateToken and Exchange load discovery (and keys) themselves.
func TestLazyDiscoveryValidateAndExchange(t *testing.T) {
	c := newCountingIssuer(t)
	p := lazyProvider(t, c, 0, nil)

	claims, err := p.ValidateToken(t.Context(), c.token(t, nil))
	if err != nil || claims.Sub != "u1" {
		t.Fatalf("ValidateToken (lazy) = %+v, %v", claims, err)
	}

	tok, err := p.Exchange(t.Context(), RefreshToken("rt"))
	if err != nil || tok.AccessToken != "at" {
		t.Fatalf("Exchange (lazy) = %+v, %v", tok, err)
	}

	if c.discoveryHits.Load() != 1 {
		t.Fatalf("discovery fetched %d times, want 1", c.discoveryHits.Load())
	}
}

// Eager mode still fails at startup when the issuer is down (fail closed).
func TestEagerDiscoveryFailsAtStartup(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{Issuer: srv.URL, ClientID: "cid"}

	_, err := New(t.Context(), cfg, WithHTTPClient(srv.Client()))
	if err == nil || !errors.Is(err, errDiscoveryHTTPStatus) {
		t.Fatalf("eager New with issuer down: %v", err)
	}
}
