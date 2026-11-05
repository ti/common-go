package oidc

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// testKeyPair holds an RSA key pair for test JWT signing.
type testKeyPair struct {
	private *rsa.PrivateKey
	public  *rsa.PublicKey
	kid     string
}

func generateTestKeyPair(t *testing.T) *testKeyPair {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return &testKeyPair{
		private: priv,
		public:  &priv.PublicKey,
		kid:     "test-key-1",
	}
}

// signJWT creates a signed JWT token with the given claims using RS256.
func signJWT(t *testing.T, kp *testKeyPair, claims map[string]any) string {
	t.Helper()

	header := map[string]string{
		"alg": "RS256",
		"kid": kp.kid,
		"typ": "JWT",
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	hashed := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, kp.private, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return signingInput + "." + sigB64
}

// testIssuer is the issuer every test token and provider uses.
const testIssuer = "https://test-issuer.example.com"

// newTestProviderWithKeys creates a Provider with test keys pre-loaded and no
// network access.
func newTestProviderWithKeys(kp *testKeyPair, opts ...Option) *Provider {
	return NewFromDiscovery(
		Config{Issuer: testIssuer, ClientID: "test-client", Scope: DefaultScope, RPInitiatedLogout: true},
		Discovery{
			Issuer:        testIssuer,
			TokenEndpoint: testIssuer + "/token",
		},
		append([]Option{WithKeys(map[string]*rsa.PublicKey{kp.kid: kp.public})}, opts...)...,
	)
}

func TestValidateToken_ValidFutureExp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kp := generateTestKeyPair(t)
		p := newTestProviderWithKeys(kp)

		// Create a token with exp set far in the future.
		// In synctest, time starts at midnight UTC 2000-01-01.
		claims := map[string]any{
			"iss":   "https://test-issuer.example.com",
			"sub":   "user-123",
			"aud":   "test-client",
			"exp":   time.Now().Add(1 * time.Hour).Unix(),
			"iat":   time.Now().Unix(),
			"email": "user@example.com",
		}
		token := signJWT(t, kp, claims)

		result, err := p.ValidateToken(t.Context(), token)
		if err != nil {
			t.Fatalf("expected valid token, got error: %v", err)
		}
		if result.Sub != "user-123" {
			t.Errorf("expected sub=user-123, got %q", result.Sub)
		}
		if result.Email != "user@example.com" {
			t.Errorf("expected email=user@example.com, got %q", result.Email)
		}
	})
}

func TestValidateToken_ExpiredToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kp := generateTestKeyPair(t)
		p := newTestProviderWithKeys(kp)

		// Create a token with exp in the past.
		claims := map[string]any{
			"iss": "https://test-issuer.example.com",
			"sub": "user-expired",
			"aud": "test-client",
			"exp": time.Now().Add(-1 * time.Hour).Unix(),
			"iat": time.Now().Add(-2 * time.Hour).Unix(),
		}
		token := signJWT(t, kp, claims)

		_, err := p.ValidateToken(t.Context(), token)
		if err == nil {
			t.Fatal("expected error for expired token, got nil")
		}
		if err.Error() != "token expired" {
			t.Errorf("expected 'token expired' error, got: %v", err)
		}
	})
}

func TestValidateToken_UnknownKid_TriggersJWKSRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Generate two key pairs: one initially known, one unknown.
		kp := generateTestKeyPair(t)
		kp.kid = "unknown-key-id"

		// Set up a JWKS server that returns the new key.
		var jwksRequestCount atomic.Int32
		jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jwksRequestCount.Add(1)
			// Return the key with kid="unknown-key-id"
			nBytes := kp.public.N.Bytes()
			eBytes := big.NewInt(int64(kp.public.E)).Bytes()

			jwks := JWKS{
				Keys: []JWK{
					{
						Kty: "RSA",
						Kid: "unknown-key-id",
						N:   base64.RawURLEncoding.EncodeToString(nBytes),
						E:   base64.RawURLEncoding.EncodeToString(eBytes),
						Alg: "RS256",
						Use: "sig",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jwks)
		}))
		defer jwksSrv.Close()

		// Only the JWKS refresh can resolve "unknown-key-id".
		p := NewFromDiscovery(
			Config{Issuer: testIssuer, ClientID: "test-client"},
			Discovery{Issuer: testIssuer, JwksURI: jwksSrv.URL + "/jwks"},
			WithHTTPClient(jwksSrv.Client()),
		)

		// Create a token signed with the unknown kid.
		claims := map[string]any{
			"iss": "https://test-issuer.example.com",
			"sub": "user-new-key",
			"aud": "test-client",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
			"iat": time.Now().Unix(),
		}
		token := signJWT(t, kp, claims)

		result, err := p.ValidateToken(t.Context(), token)
		if err != nil {
			t.Fatalf("expected valid token after JWKS refresh, got error: %v", err)
		}
		if result.Sub != "user-new-key" {
			t.Errorf("expected sub=user-new-key, got %q", result.Sub)
		}

		// Verify that JWKS was fetched (refresh happened).
		if count := jwksRequestCount.Load(); count != 1 {
			t.Errorf("expected 1 JWKS refresh request, got %d", count)
		}
	})
}

func TestValidateToken_InvalidSignature(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kp := generateTestKeyPair(t)
		// Generate a different key pair to sign with - signature won't match.
		wrongKP, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generate wrong key: %v", err)
		}
		signerKP := &testKeyPair{
			private: wrongKP,
			public:  &wrongKP.PublicKey,
			kid:     kp.kid, // same kid so it finds the key, but wrong signature
		}

		p := newTestProviderWithKeys(kp)

		// Create a token signed with the wrong key.
		claims := map[string]any{
			"iss": "https://test-issuer.example.com",
			"sub": "user-tampered",
			"aud": "test-client",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
			"iat": time.Now().Unix(),
		}
		token := signJWT(t, signerKP, claims)

		_, validateErr := p.ValidateToken(t.Context(), token)
		if validateErr == nil {
			t.Fatal("expected error for invalid signature, got nil")
		}
	})
}

func TestValidateToken_UnsupportedAlgorithm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kp := generateTestKeyPair(t)
		p := newTestProviderWithKeys(kp)

		// Create a token with an unsupported algorithm header.
		header := map[string]string{
			"alg": "HS256",
			"kid": kp.kid,
			"typ": "JWT",
		}
		claims := map[string]any{
			"sub": "user",
			"exp": time.Now().Add(1 * time.Hour).Unix(),
		}

		headerJSON, _ := json.Marshal(header)
		claimsJSON, _ := json.Marshal(claims)
		headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
		claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
		// Fake signature
		token := headerB64 + "." + claimsB64 + "." + base64.RawURLEncoding.EncodeToString([]byte("fake-sig"))

		_, err := p.ValidateToken(t.Context(), token)
		if err == nil {
			t.Fatal("expected error for unsupported algorithm, got nil")
		}
		if err.Error() != "unsupported alg: HS256" {
			t.Errorf("expected 'unsupported alg: HS256', got: %v", err)
		}
	})
}
