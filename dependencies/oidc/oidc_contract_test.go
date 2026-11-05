package oidc

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"os"
	"strings"
	"testing"
	"time"
)

type validationContract struct {
	ClockSkewSeconds int64                    `json:"clock_skew_seconds"`
	Issuer           string                   `json:"issuer"`
	ClientID         string                   `json:"client_id"`
	Cases            []validationContractCase `json:"cases"`
}

type validationContractCase struct {
	Name          string                   `json:"name"`
	Header        map[string]string        `json:"header"`
	Claims        validationContractClaims `json:"claims"`
	Valid         bool                     `json:"valid"`
	ErrorContains string                   `json:"error_contains"`
}

type validationContractClaims struct {
	Issuer                 string `json:"issuer"`
	Audience               any    `json:"audience"`
	ExpiresInSeconds       int64  `json:"expires_in_seconds"`
	IssuedAtOffsetSeconds  int64  `json:"issued_at_offset_seconds"`
	NotBeforeOffsetSeconds *int64 `json:"not_before_offset_seconds"`
	TokenUse               string `json:"token_use"`
	Groups                 any    `json:"groups"`
}

func TestValidateToken_SharedContract(t *testing.T) {
	contractBytes, err := os.ReadFile("testdata/oidc-jwt-validation-cases.json")
	if err != nil {
		t.Fatalf("read shared validation contract: %v", err)
	}

	var contract validationContract

	err = json.Unmarshal(contractBytes, &contract)
	if err != nil {
		t.Fatalf("decode shared validation contract: %v", err)
	}
	if got := int64(DefaultClockSkew / time.Second); got != contract.ClockSkewSeconds {
		t.Fatalf("clock skew = %d seconds, contract requires %d", got, contract.ClockSkewSeconds)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	const kid = "shared-contract-key"

	provider := NewFromDiscovery(
		Config{Issuer: contract.Issuer, ClientID: contract.ClientID},
		Discovery{Issuer: contract.Issuer},
		WithKeys(map[string]*rsa.PublicKey{kid: &privateKey.PublicKey}),
	)

	for _, testCase := range contract.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			runValidationContractCase(t, provider, privateKey, kid, contract.ClientID, testCase)
		})
	}
}

func runValidationContractCase(
	t *testing.T,
	provider *Provider,
	privateKey *rsa.PrivateKey,
	kid string,
	clientID string,
	testCase validationContractCase,
) {
	t.Helper()

	now := time.Now()
	header := map[string]string{"alg": algRS256, "kid": kid}
	maps.Copy(header, testCase.Header)

	claims := map[string]any{
		"iss": testCase.Claims.Issuer,
		"sub": "shared-contract-user",
		"aud": testCase.Claims.Audience,
		"exp": now.Add(time.Duration(testCase.Claims.ExpiresInSeconds) * time.Second).Unix(),
		"iat": now.Add(time.Duration(testCase.Claims.IssuedAtOffsetSeconds) * time.Second).Unix(),
	}
	if testCase.Claims.NotBeforeOffsetSeconds != nil {
		claims["nbf"] = now.Add(time.Duration(*testCase.Claims.NotBeforeOffsetSeconds) * time.Second).Unix()
	}
	if testCase.Claims.TokenUse != "" {
		claims["token_use"] = testCase.Claims.TokenUse
	}
	if testCase.Claims.Groups != nil {
		claims["groups"] = testCase.Claims.Groups
	}

	token := signContractJWT(t, privateKey, header, claims)
	result, validateErr := provider.ValidateToken(t.Context(), token)
	if testCase.Valid {
		if validateErr != nil {
			t.Fatalf("expected valid token, got %v", validateErr)
		}
		if !result.Aud.Contains(clientID) {
			t.Fatalf("audience %v does not contain client ID", result.Aud)
		}

		return
	}

	if validateErr == nil {
		t.Fatal("expected token rejection")
	}
	if !strings.Contains(validateErr.Error(), testCase.ErrorContains) {
		t.Fatalf("error %q does not contain %q", validateErr, testCase.ErrorContains)
	}
}

func signContractJWT(
	t *testing.T,
	privateKey *rsa.PrivateKey,
	header map[string]string,
	claims map[string]any,
) string {
	t.Helper()

	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	headerPart := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsPart := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerPart + "." + claimsPart
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}
