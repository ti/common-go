package oidc

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"
)

var (
	errUnknownKeyID   = errors.New("unknown key id")
	errJWKSHTTPStatus = errors.New("jwks endpoint returned non-success status")
)

// JWK is one JSON Web Key (only RSA signing keys are used).
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

// JWKS is a JSON Web Key Set document.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// keySet is an immutable snapshot, swapped atomically: reads on the request
// path take no lock.
type keySet struct {
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

// lookupKey returns the key for kid. An unknown kid triggers at most one
// JWKS refresh per refresh interval, shared by every concurrent caller.
func (p *Provider) lookupKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	current := p.keys.Load()
	if key, ok := current.keys[kid]; ok {
		return key, nil
	}

	if p.discovery.JwksURI == "" || p.opts.now().Sub(current.fetched) < p.opts.refreshInterval {
		return nil, fmt.Errorf("%w: %s", errUnknownKeyID, kid)
	}

	// The refresh outlives the first caller's cancellation: other requests
	// wait on the same flight, and a client hang-up must not fail them.
	flightCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.opts.httpClient.Timeout+time.Second)
	defer cancel()

	_, err, _ := p.refresh.Do("jwks", func() (any, error) {
		fresh, err := p.fetchJWKS(flightCtx)
		if err != nil {
			return nil, err
		}

		p.keys.Store(fresh)

		return nil, nil //nolint:nilnil // singleflight carries only the error
	})
	if err != nil {
		return nil, fmt.Errorf("refresh jwks: %w", err)
	}

	if key, ok := p.keys.Load().keys[kid]; ok {
		return key, nil
	}

	return nil, fmt.Errorf("%w: %s", errUnknownKeyID, kid)
}

func (p *Provider) fetchJWKS(ctx context.Context) (*keySet, error) {
	if p.discovery.JwksURI == "" {
		return nil, fmt.Errorf("%w: discovery has no jwks_uri", errUnknownKeyID)
	}

	var jwks JWKS

	err := getJSON(ctx, p.opts.httpClient, p.discovery.JwksURI, errJWKSHTTPStatus, &jwks)
	if err != nil {
		return nil, err
	}

	return &keySet{keys: parseJWKS(jwks), fetched: p.opts.now()}, nil
}

// parseJWKS keeps the usable RSA signing keys; malformed entries are skipped
// so one bad key cannot take the whole set down.
func parseJWKS(jwks JWKS) map[string]*rsa.PublicKey {
	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))

	for _, key := range jwks.Keys {
		if key.Kty != "RSA" || key.Kid == "" || (key.Use != "" && key.Use != "sig") {
			continue
		}

		publicKey, err := parseRSAPublicKey(key.N, key.E)
		if err != nil {
			continue
		}

		keys[key.Kid] = publicKey
	}

	return keys
}

var errBadRSAKey = errors.New("invalid rsa key")

func parseRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}

	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}

	exponent := new(big.Int).SetBytes(eBytes)
	if len(nBytes) == 0 || !exponent.IsInt64() || exponent.Int64() < 3 || exponent.Int64() > math.MaxInt32 {
		return nil, errBadRSAKey
	}

	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(exponent.Int64())}, nil
}
