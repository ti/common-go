package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
)

const maxTokenResponse = 1 << 20

var (
	// ErrNoEndSession is returned by LogoutURL when the issuer advertises no
	// end_session_endpoint.
	ErrNoEndSession = errors.New("end_session_endpoint not available")
	// ErrNoTokenEndpoint is returned by Exchange when the issuer advertises no
	// token_endpoint.
	ErrNoTokenEndpoint = errors.New("token endpoint not discovered")
	// ErrTokenResponse is matched by Exchange errors when the token endpoint
	// answered 200 with an unreadable body.
	ErrTokenResponse = errors.New("decode token response")
)

// AuthRequest is the input of [Provider.AuthorizationURL].
type AuthRequest struct {
	RedirectURI string
	State       string
	// CodeChallenge is the PKCE S256 challenge; empty sends none.
	CodeChallenge string
	// Scope overrides Config.Scope when set.
	Scope string
	// Extra adds parameters such as prompt, login_hint or nonce.
	Extra url.Values
}

// AuthorizationURL builds the authorization-code (+PKCE) redirect URL.
func (p *Provider) AuthorizationURL(r AuthRequest) string {
	params := url.Values{
		"response_type": {"code"},
		"client_id":     {p.cfg.ClientID},
		"redirect_uri":  {r.RedirectURI},
		"state":         {r.State},
		"scope":         {p.cfg.Scope},
	}
	if r.Scope != "" {
		params.Set("scope", r.Scope)
	}

	if r.CodeChallenge != "" {
		params.Set("code_challenge", r.CodeChallenge)
		params.Set("code_challenge_method", "S256")
	}

	maps.Copy(params, r.Extra)

	return withQuery(p.discovery.AuthorizationEndpoint, params.Encode())
}

// LogoutURL builds the issuer logout redirect: OIDC RP-initiated logout
// (post_logout_redirect_uri + id_token_hint) or, with oidc_logout=false, the
// Cognito-style logout_uri form.
func (p *Provider) LogoutURL(redirectURI, idTokenHint string) (string, error) {
	endpoint := p.discovery.EndSessionEndpoint
	if endpoint == "" {
		return "", ErrNoEndSession
	}

	// Parameter order is fixed (not url.Values' sorted Encode) because the
	// URL shape is shared with the Python and Rust backends.
	query := "client_id=" + url.QueryEscape(p.cfg.ClientID)
	if !p.cfg.RPInitiatedLogout {
		return withQuery(endpoint, query+"&logout_uri="+url.QueryEscape(redirectURI)), nil
	}

	query += "&post_logout_redirect_uri=" + url.QueryEscape(redirectURI)
	if idTokenHint != "" {
		query += "&id_token_hint=" + url.QueryEscape(idTokenHint)
	}

	return withQuery(endpoint, query), nil
}

func withQuery(endpoint, query string) string {
	if strings.Contains(endpoint, "?") {
		return endpoint + "&" + query
	}

	return endpoint + "?" + query
}

// Grant is a token-endpoint grant: [AuthorizationCode], [RefreshToken] or
// [ClientCredentials].
type Grant interface {
	// GrantType is the grant_type value.
	GrantType() string
	form(f url.Values)
}

// AuthorizationCode redeems an authorization code (with its PKCE verifier).
type AuthorizationCode struct {
	Code         string
	RedirectURI  string
	CodeVerifier string
}

// GrantType implements [Grant].
func (AuthorizationCode) GrantType() string { return "authorization_code" }

func (g AuthorizationCode) form(f url.Values) {
	f.Set("code", g.Code)
	f.Set("redirect_uri", g.RedirectURI)
	f.Set("code_verifier", g.CodeVerifier)
}

// RefreshToken redeems a refresh token.
type RefreshToken string

// GrantType implements [Grant].
func (RefreshToken) GrantType() string { return "refresh_token" }

func (g RefreshToken) form(f url.Values) { f.Set("refresh_token", string(g)) }

// ClientCredentials requests a service token for this client itself.
type ClientCredentials struct {
	// Scope overrides Config.Scope when set.
	Scope string
}

// GrantType implements [Grant].
func (ClientCredentials) GrantType() string { return "client_credentials" }

func (g ClientCredentials) form(f url.Values) {
	if g.Scope != "" {
		f.Set("scope", g.Scope)
	}
}

// Token is a token-endpoint response.
type Token struct {
	TokenType        string `json:"token_type"`
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	ExpiresIn        int32  `json:"expires_in"`
	RefreshExpiresIn int32  `json:"refresh_expires_in"`
	// Sub is a non-standard extension some issuers return.
	Sub string `json:"sub"`
}

// TokenError is a non-200 answer from the token endpoint, e.g. invalid_grant.
// Its text, "token endpoint: <body>", is relied on by the frontend to show
// the issuer's own error_description.
type TokenError struct {
	StatusCode int
	// Code and Description are the RFC 6749 §5.2 error fields, when present.
	Code        string
	Description string
	Body        string
}

func (e *TokenError) Error() string { return "token endpoint: " + e.Body }

// Exchange posts grant to the token endpoint with this client's credentials.
// A non-200 answer is a *[TokenError] (errors.AsType[*oidc.TokenError]); an
// unreadable 200 matches ErrTokenResponse.
func (p *Provider) Exchange(ctx context.Context, grant Grant) (*Token, error) {
	endpoint := p.discovery.TokenEndpoint
	if endpoint == "" {
		return nil, ErrNoTokenEndpoint
	}

	form := url.Values{"grant_type": {grant.GrantType()}, "client_id": {p.cfg.ClientID}}
	if p.cfg.ClientSecret != "" {
		form.Set("client_secret", p.cfg.ClientSecret)
	}

	grant.form(form)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.opts.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse))
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		tokenErr := &TokenError{StatusCode: resp.StatusCode, Body: string(body)}

		var fields struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		if json.Unmarshal(body, &fields) == nil {
			tokenErr.Code, tokenErr.Description = fields.Error, fields.Description
		}

		return nil, tokenErr
	}

	var token Token

	err = json.Unmarshal(body, &token)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenResponse, err)
	}

	return &token, nil
}
