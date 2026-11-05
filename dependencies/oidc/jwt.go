package oidc

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	algRS256       = "RS256"
	typJWT         = "JWT"
	typAccessJWT   = "at+jwt"
	tokenUseID     = "id"
	tokenUseAccess = "access"
	jwtParts       = 3
	bearerScheme   = "bearer"
)

// Classification sentinels. Every error from [Provider.ValidateToken]
// matches ErrInvalidToken with errors.Is while keeping its own message (the
// messages are part of the cross-backend contract).
var (
	// ErrMissingToken: no Authorization header (401).
	ErrMissingToken = errors.New("missing authorization header")
	// ErrNotBearer: an Authorization header with another scheme (401).
	ErrNotBearer = errors.New("authorization header must use Bearer scheme")
	// ErrInvalidToken: the JWT failed validation (401).
	ErrInvalidToken = errors.New("invalid token")
)

var (
	errInvalidJWTFormat  = errors.New("invalid jwt format")
	errUnsupportedAlg    = errors.New("unsupported alg")
	errMissingKeyID      = errors.New("missing key id")
	errUnsupportedTyp    = errors.New("unsupported typ")
	errTokenExpired      = errors.New("token expired")
	errAudienceMismatch  = errors.New("audience mismatch")
	errTokenNotYetValid  = errors.New("token not yet valid")
	errTokenIssuedFuture = errors.New("token issued in the future")
	errInvalidTokenUse   = errors.New("invalid token_use")
)

// invalidTokenError marks a validation failure as ErrInvalidToken without
// changing its text.
type invalidTokenError struct{ err error }

func (e invalidTokenError) Error() string        { return e.err.Error() }
func (e invalidTokenError) Unwrap() error        { return e.err }
func (e invalidTokenError) Is(target error) bool { return target == ErrInvalidToken }

// StringSlice decodes a JSON array of strings or one space/comma-separated
// string ("admin users") — issuers emit group claims both ways.
type StringSlice []string

// UnmarshalJSON implements json.Unmarshaler.
func (s *StringSlice) UnmarshalJSON(data []byte) error {
	var arr []string

	err := json.Unmarshal(data, &arr)
	if err == nil {
		*s = arr

		return nil
	}

	var str string

	err = json.Unmarshal(data, &str)
	if err != nil {
		return fmt.Errorf("unmarshal groups claim: %w", err)
	}

	*s = splitValues(str)

	return nil
}

// Audience is the aud claim: one string or an array of strings.
type Audience []string

// UnmarshalJSON implements json.Unmarshaler.
func (a *Audience) UnmarshalJSON(data []byte) error {
	var value string

	err := json.Unmarshal(data, &value)
	if err == nil {
		*a = Audience{value}

		return nil
	}

	var values []string

	err = json.Unmarshal(data, &values)
	if err != nil {
		return fmt.Errorf("unmarshal audience claim: %w", err)
	}

	*a = values

	return nil
}

// Contains reports whether the audience includes value.
func (a Audience) Contains(value string) bool {
	return slices.Contains(a, value)
}

// Claims are the validated claims of an access or ID token.
type Claims struct {
	Iss       string      `json:"iss"`
	Sub       string      `json:"sub"`
	Aud       Audience    `json:"aud"`
	Exp       int64       `json:"exp"`
	Nbf       int64       `json:"nbf"`
	Iat       int64       `json:"iat"`
	TokenUse  string      `json:"token_use"`
	ProjectID string      `json:"project_id"`
	Name      string      `json:"name"`
	Email     string      `json:"email"`
	Picture   string      `json:"picture"`
	Groups    StringSlice `json:"groups"`

	// Raw holds every claim (numbers as json.Number), for access rules and
	// claims this struct does not name.
	Raw map[string]any `json:"-"`
}

// Expiry is the exp claim as a time.
func (c *Claims) Expiry() time.Time { return time.Unix(c.Exp, 0) }

// AllGroups is the union of the "groups" and "group" claims (Cognito uses
// groups, account.nx.run group). group is read leniently from Raw so an
// unusual shape never turns a valid token into a 401.
func (c *Claims) AllGroups() []string {
	out := slices.Clone([]string(c.Groups))
	for _, g := range claimValues(c.Raw["group"]) {
		if !slices.Contains(out, g) {
			out = append(out, g)
		}
	}

	return out
}

// Strings returns the claim at path flattened to strings, with the same
// rules as access rules: dotted paths reach nested objects, strings split
// on spaces / commas, numbers and booleans use their JSON text.
func (c *Claims) Strings(path string) []string {
	return claimValues(lookupClaim(c.Raw, path))
}

// Lookup returns the raw claim at path as T (string, bool, json.Number,
// []any, map[string]any), e.g. claims.Lookup[string]("user.locale").
func (c *Claims) Lookup[T any](path string) (T, bool) { //nolint:ireturn // T is the caller's concrete type
	value, ok := lookupClaim(c.Raw, path).(T)

	return value, ok
}

// ValidateToken checks a compact RS256 JWT — signature against the JWKS,
// header markers, issuer, aud = client id, exp / nbf / iat — and returns
// its claims. Errors match [ErrInvalidToken].
func (p *Provider) ValidateToken(ctx context.Context, token string) (*Claims, error) {
	claims, err := p.validate(ctx, token)
	if err != nil {
		return nil, invalidTokenError{err}
	}

	return claims, nil
}

// Authorize applies the URI's claim access rules to validated claims.
// Errors match [ErrAccessDenied]. Kept separate from ValidateToken so a
// caller can tell 401 (bad token) from 403 (valid token, not allowed).
func (p *Provider) Authorize(claims *Claims) error {
	return p.cfg.Access.Allow(claims.Raw)
}

// Authenticate is the whole check for one request: it takes the
// Authorization header value, validates the Bearer token and applies the
// access rules. Errors match ErrMissingToken, ErrNotBearer, ErrInvalidToken
// (all 401) or ErrAccessDenied (403); see [HTTPStatus]. With
// ErrAccessDenied the (valid) claims are returned too, so the caller can log
// who was denied.
func (p *Provider) Authenticate(ctx context.Context, authorization string) (*Claims, error) {
	token, err := BearerToken(authorization)
	if err != nil {
		return nil, err
	}

	claims, err := p.ValidateToken(ctx, token)
	if err != nil {
		return nil, err
	}

	return claims, p.Authorize(claims)
}

// BearerToken extracts the token from an Authorization header value. The
// scheme is case-insensitive (RFC 7235).
func BearerToken(authorization string) (string, error) {
	if authorization == "" {
		return "", ErrMissingToken
	}

	scheme, token, ok := strings.Cut(authorization, " ")
	if !ok || !strings.EqualFold(scheme, bearerScheme) || strings.TrimSpace(token) == "" {
		return "", ErrNotBearer
	}

	return strings.TrimSpace(token), nil
}

func (p *Provider) validate(ctx context.Context, token string) (*Claims, error) {
	parts := strings.SplitN(token, ".", jwtParts)
	if len(parts) != jwtParts {
		return nil, errInvalidJWTFormat
	}

	header, err := parseHeader(parts[0])
	if err != nil {
		return nil, err
	}

	key, err := p.lookupKey(ctx, header.Kid)
	if err != nil {
		return nil, err
	}

	err = verifySignature(key, parts)
	if err != nil {
		return nil, err
	}

	return parseClaims(parts[1], p.discovery.Issuer, p.cfg.ClientID, p.opts.now(), p.opts.clockSkew)
}

type jwtHeader struct {
	Alg string  `json:"alg"`
	Kid string  `json:"kid"`
	Typ *string `json:"typ"`
}

func parseHeader(encoded string) (*jwtHeader, error) {
	headerBytes, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}

	var header jwtHeader

	err = json.Unmarshal(headerBytes, &header)
	if err != nil {
		return nil, fmt.Errorf("unmarshal header: %w", err)
	}

	switch {
	case header.Alg != algRS256:
		return nil, fmt.Errorf("%w: %s", errUnsupportedAlg, header.Alg)
	case header.Kid == "":
		return nil, errMissingKeyID
	case header.Typ != nil && *header.Typ != typJWT && *header.Typ != typAccessJWT:
		return nil, fmt.Errorf("%w: %s", errUnsupportedTyp, *header.Typ)
	default:
		return &header, nil
	}
}

func verifySignature(key *rsa.PublicKey, parts []string) error {
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}

	hashed := sha256.Sum256([]byte(parts[0] + "." + parts[1]))

	err = rsa.VerifyPKCS1v15(key, crypto.SHA256, hashed[:], signature)
	if err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}

	return nil
}

func parseClaims(payload, issuer, audience string, now time.Time, skew time.Duration) (*Claims, error) {
	claimsBytes, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}

	var claims Claims

	err = json.Unmarshal(claimsBytes, &claims)
	if err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	err = validateClaims(&claims, issuer, audience, now, skew)
	if err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(claimsBytes))
	decoder.UseNumber()

	err = decoder.Decode(&claims.Raw)
	if err != nil {
		return nil, fmt.Errorf("unmarshal raw claims: %w", err)
	}

	return &claims, nil
}

func validateClaims(claims *Claims, issuer, audience string, now time.Time, skew time.Duration) error {
	err := validateTimes(claims, now, skew)
	if err != nil {
		return err
	}

	if actual := normalizeIssuer(claims.Iss); actual == "" || actual != normalizeIssuer(issuer) {
		return fmt.Errorf("%w: got %q", errIssuerMismatch, claims.Iss)
	}

	if audience == "" || !claims.Aud.Contains(audience) {
		return fmt.Errorf("%w: expected %q", errAudienceMismatch, audience)
	}

	switch claims.TokenUse {
	case "", tokenUseID, tokenUseAccess:
		return nil
	default:
		return fmt.Errorf("%w: %s", errInvalidTokenUse, claims.TokenUse)
	}
}

// validateTimes checks exp (no skew: an expired token is expired) and nbf /
// iat (skew tolerated for clocks that run ahead).
func validateTimes(claims *Claims, now time.Time, skew time.Duration) error {
	switch {
	case claims.Exp == 0 || now.Unix() >= claims.Exp:
		return errTokenExpired
	case claims.Nbf != 0 && claims.Nbf > now.Add(skew).Unix():
		return errTokenNotYetValid
	case claims.Iat != 0 && claims.Iat > now.Add(skew).Unix():
		return errTokenIssuedFuture
	default:
		return nil
	}
}

func normalizeIssuer(issuer string) string {
	return strings.TrimRight(strings.TrimSpace(issuer), "/")
}
