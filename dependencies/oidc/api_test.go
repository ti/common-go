package oidc

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseURI(t *testing.T) {
	cfg, err := ParseURI("oidc://cid:s3cret@account.nx.run/tenant?scope=offline%20openid&oidc_logout=false&group=labs%20admin")
	if err != nil {
		t.Fatalf("ParseURI: %v", err)
	}

	if cfg.Issuer != "https://account.nx.run/tenant" || cfg.ClientID != "cid" || cfg.ClientSecret != "s3cret" {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.Scope != "offline openid" || cfg.RPInitiatedLogout {
		t.Errorf("scope / logout = %q / %t", cfg.Scope, cfg.RPInitiatedLogout)
	}
	if len(cfg.Access.Rules) != 1 || cfg.Access.Rules[0].Path != "group" {
		t.Errorf("rules = %+v", cfg.Access.Rules)
	}
	if red := cfg.Redacted(); strings.Contains(red, "s3cret") || !strings.Contains(red, "secret=xxxxx") {
		t.Errorf("Redacted leaks or hides wrong: %s", red)
	}

	public, err := ParseURI("oidc://cid:@account.nx.run")
	if err != nil || public.Scope != DefaultScope || !public.RPInitiatedLogout || public.ClientSecret != "" {
		t.Errorf("defaults = %+v, %v", public, err)
	}

	for _, bad := range []string{
		"https://cid:@account.nx.run",       // wrong scheme
		"oidc://cid:@",                      // no issuer host
		"oidc://account.nx.run",             // no client id
		"oidc://cid:@account.nx.run?group=", // empty access rule
		"oidc://cid:@account.nx.run?scope=%zz",
	} {
		_, err := ParseURI(bad)
		if !errors.Is(err, ErrInvalidURI) {
			t.Errorf("ParseURI(%q) = %v, want ErrInvalidURI", bad, err)
		}
	}
}

// fakeIssuer is a TLS issuer serving discovery, JWKS and a token endpoint.
type fakeIssuer struct {
	*httptest.Server

	kp         *testKeyPair
	jwksHits   atomic.Int32
	tokenForms chan url.Values
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()

	f := &fakeIssuer{kp: generateTestKeyPair(t), tokenForms: make(chan url.Values, 4)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Discovery{
			Issuer:                f.URL,
			AuthorizationEndpoint: f.URL + "/authorize",
			TokenEndpoint:         f.URL + "/token",
			JwksURI:               f.URL + "/jwks",
			EndSessionEndpoint:    f.URL + "/logout?tenant=t1",
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		f.jwksHits.Add(1)
		_ = json.NewEncoder(w).Encode(JWKS{Keys: []JWK{jwkOf(f.kp.kid, f.kp.public)}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.tokenForms <- r.PostForm

		w.Header().Set("Content-Type", "application/json")
		switch r.PostForm.Get("code") {
		case "bad":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code expired"}`))
		case "garbled":
			_, _ = w.Write([]byte(`{"access_token":`))
		default:
			_, _ = w.Write([]byte(`{"token_type":"Bearer","access_token":"at","refresh_token":"rt","expires_in":3600}`))
		}
	})
	f.Server = httptest.NewTLSServer(mux)
	t.Cleanup(f.Close)

	return f
}

func jwkOf(kid string, key *rsa.PublicKey) JWK {
	return JWK{
		Kty: "RSA", Kid: kid, Use: "sig", Alg: algRS256,
		N: base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

func (f *fakeIssuer) uri(query string) string {
	return "oidc://cid:s3cret@" + strings.TrimPrefix(f.URL, "https://") + "?" + query
}

func (f *fakeIssuer) token(t *testing.T, extra map[string]any) string {
	t.Helper()

	claims := map[string]any{"iss": f.URL, "sub": "u1", "aud": "cid", "exp": time.Now().Add(time.Hour).Unix()}
	maps.Copy(claims, extra)

	return signJWT(t, f.kp, claims)
}

func TestOpenFromURIAndAuthenticate(t *testing.T) {
	issuer := newFakeIssuer(t)

	p, err := Open(t.Context(), issuer.uri("group=labs%20admin"), WithHTTPClient(issuer.Client()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if p.Discovery().TokenEndpoint != issuer.URL+"/token" || issuer.jwksHits.Load() != 1 {
		t.Fatalf("discovery = %+v, jwks hits = %d", p.Discovery(), issuer.jwksHits.Load())
	}

	allowed := issuer.token(t, map[string]any{"groups": []string{"admin", "test"}})
	claims, err := p.Authenticate(t.Context(), "bearer "+allowed)
	if err != nil || claims.Sub != "u1" {
		t.Fatalf("Authenticate(allowed) = %+v, %v", claims, err)
	}

	denied := issuer.token(t, map[string]any{"user": map[string]any{"role": "guest"}})
	claims, err = p.Authenticate(t.Context(), "Bearer "+denied)
	if !errors.Is(err, ErrAccessDenied) || HTTPStatus(err) != http.StatusForbidden || claims == nil || claims.Sub != "u1" {
		t.Fatalf("Authenticate(denied) = %+v, %v", claims, err)
	}
	if role, ok := claims.Lookup[map[string]any]("user"); !ok || role["role"] != "guest" {
		t.Errorf("Lookup[map] = %v, %t", role, ok)
	}
	if got := claims.Strings("user.role"); len(got) != 1 || got[0] != "guest" {
		t.Errorf("Strings = %v", got)
	}

	expired := issuer.token(t, map[string]any{"exp": time.Now().Add(-time.Minute).Unix(), "group": "admin"})
	_, err = p.Authenticate(t.Context(), "Bearer "+expired)
	if !errors.Is(err, ErrInvalidToken) || err.Error() != "token expired" || HTTPStatus(err) != http.StatusUnauthorized {
		t.Fatalf("Authenticate(expired) = %v", err)
	}

	for header, want := range map[string]error{"": ErrMissingToken, "Basic abc": ErrNotBearer, "Bearer ": ErrNotBearer} {
		_, err := p.Authenticate(t.Context(), header)
		if !errors.Is(err, want) || HTTPStatus(err) != http.StatusUnauthorized {
			t.Errorf("Authenticate(%q) = %v, want %v", header, err, want)
		}
	}
}

func TestOpenFailsClosed(t *testing.T) {
	issuer := newFakeIssuer(t)

	// An unreachable issuer and a missing discovery document are both fatal:
	// auth must not start in a fail-open state.
	_, err := Open(t.Context(), "oidc://cid:@localhost:1", WithHTTPClient(issuer.Client()))
	if err == nil {
		t.Fatal("Open succeeded against an unreachable issuer")
	}

	_, err = New(t.Context(), Config{Issuer: issuer.URL + "/other", ClientID: "cid"}, WithHTTPClient(issuer.Client()))
	if err == nil {
		t.Fatal("New succeeded without a discovery document")
	}
}

func TestClientCalls(t *testing.T) {
	issuer := newFakeIssuer(t)

	p, err := Open(t.Context(), issuer.uri("scope=openid%20email"), WithHTTPClient(issuer.Client()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	authURL, _ := url.Parse(p.AuthorizationURL(AuthRequest{
		RedirectURI: "https://app/cb", State: "st", CodeChallenge: "ch",
		Extra: url.Values{"prompt": {"login"}},
	}))
	q := authURL.Query()
	if authURL.Path != "/authorize" || q.Get("client_id") != "cid" || q.Get("scope") != "openid email" ||
		q.Get("code_challenge_method") != "S256" || q.Get("prompt") != "login" || q.Get("response_type") != "code" {
		t.Errorf("AuthorizationURL = %s", authURL)
	}

	logout, err := p.LogoutURL("https://app/bye", "idt")
	if err != nil || logout != issuer.URL+"/logout?tenant=t1&client_id=cid&post_logout_redirect_uri=https%3A%2F%2Fapp%2Fbye&id_token_hint=idt" {
		t.Errorf("LogoutURL = %s, %v", logout, err)
	}

	token, err := p.Exchange(t.Context(), AuthorizationCode{Code: "ok", RedirectURI: "https://app/cb", CodeVerifier: "v"})
	if err != nil || token.AccessToken != "at" || token.ExpiresIn != 3600 {
		t.Fatalf("Exchange(code) = %+v, %v", token, err)
	}
	form := <-issuer.tokenForms
	if form.Get("grant_type") != "authorization_code" || form.Get("client_secret") != "s3cret" || form.Get("code_verifier") != "v" {
		t.Errorf("token form = %v", form)
	}

	_, err = p.Exchange(t.Context(), AuthorizationCode{Code: "bad"})
	<-issuer.tokenForms
	tokenErr, ok := errors.AsType[*TokenError](err)
	if !ok || tokenErr.StatusCode != http.StatusBadRequest || tokenErr.Code != "invalid_grant" ||
		tokenErr.Description != "code expired" || !strings.HasPrefix(err.Error(), "token endpoint: {") {
		t.Errorf("Exchange(bad) = %#v", err)
	}

	_, err = p.Exchange(t.Context(), AuthorizationCode{Code: "garbled"})
	<-issuer.tokenForms
	if !errors.Is(err, ErrTokenResponse) {
		t.Errorf("Exchange(garbled) = %v, want ErrTokenResponse", err)
	}

	_, _ = p.Exchange(t.Context(), RefreshToken("r1"))
	if form := <-issuer.tokenForms; form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "r1" {
		t.Errorf("refresh form = %v", form)
	}

	_, _ = p.Exchange(t.Context(), ClientCredentials{Scope: "svc"})
	if form := <-issuer.tokenForms; form.Get("grant_type") != "client_credentials" || form.Get("scope") != "svc" {
		t.Errorf("client_credentials form = %v", form)
	}
}

func TestLogoutURLVariants(t *testing.T) {
	cognito := NewFromDiscovery(Config{Issuer: testIssuer, ClientID: "c i"},
		Discovery{Issuer: testIssuer, EndSessionEndpoint: "https://idp/logout"})
	got, err := cognito.LogoutURL("https://app/", "ignored")
	if err != nil || got != "https://idp/logout?client_id=c+i&logout_uri=https%3A%2F%2Fapp%2F" {
		t.Errorf("cognito LogoutURL = %s, %v", got, err)
	}

	none := NewFromDiscovery(Config{Issuer: testIssuer, ClientID: "c"}, Discovery{Issuer: testIssuer})
	_, err = none.LogoutURL("https://app/", "")
	if !errors.Is(err, ErrNoEndSession) {
		t.Errorf("LogoutURL without endpoint = %v", err)
	}
	_, err = none.Exchange(t.Context(), RefreshToken("r"))
	if !errors.Is(err, ErrNoTokenEndpoint) {
		t.Errorf("Exchange without endpoint = %v", err)
	}
}

func TestUnknownKidRefreshIsRateLimited(t *testing.T) {
	issuer := newFakeIssuer(t)
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }

	p, err := Open(t.Context(), issuer.uri(""), WithHTTPClient(issuer.Client()), WithClock(clock))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	stranger := generateTestKeyPair(t)
	stranger.kid = "rotated-away"
	token := signJWT(t, stranger, map[string]any{"iss": issuer.URL, "aud": "cid", "exp": now.Add(time.Hour).Unix()})

	for range 5 {
		_, err := p.ValidateToken(t.Context(), token)
		if !errors.Is(err, errUnknownKeyID) {
			t.Fatalf("ValidateToken = %v, want unknown key id", err)
		}
	}
	if hits := issuer.jwksHits.Load(); hits != 1 {
		t.Fatalf("jwks hits within the interval = %d, want 1 (startup only)", hits)
	}

	now = now.Add(DefaultJWKSRefreshInterval)
	_, _ = p.ValidateToken(t.Context(), token)
	_, _ = p.ValidateToken(t.Context(), token)
	if hits := issuer.jwksHits.Load(); hits != 2 {
		t.Fatalf("jwks hits after the interval = %d, want 2", hits)
	}
}

func TestMiddleware(t *testing.T) {
	issuer := newFakeIssuer(t)

	p, err := Open(t.Context(), issuer.uri("group=admin"), WithHTTPClient(issuer.Client()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	handler := p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := FromContext(r.Context())
		if !ok {
			t.Error("no claims in context")
		}

		_, _ = w.Write([]byte(claims.Sub))
	}))

	call := func(authorization string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		return rec
	}

	if rec := call("Bearer " + issuer.token(t, map[string]any{"group": "admin"})); rec.Code != http.StatusOK || rec.Body.String() != "u1" {
		t.Errorf("allowed = %d %q", rec.Code, rec.Body)
	}

	rec := call("Bearer " + issuer.token(t, map[string]any{"group": "users"}))
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusForbidden || body["error"] != "permission_denied" || !strings.Contains(body["error_description"], `claim "group"`) {
		t.Errorf("denied = %d %v", rec.Code, body)
	}

	rec = call("")
	if rec.Code != http.StatusUnauthorized || !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("missing = %d %v", rec.Code, rec.Header())
	}

	if _, ok := FromContext(t.Context()); ok {
		t.Error("FromContext on a bare context reported claims")
	}
}
