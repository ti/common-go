package oidc

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mustPolicy(t *testing.T, rawQuery string) AccessPolicy {
	t.Helper()

	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}

	p, err := ParseAccessPolicy(q)
	if err != nil {
		t.Fatalf("ParseAccessPolicy(%q): %v", rawQuery, err)
	}

	return p
}

// TestValidateThenAuthorize exercises the full path: a signed token is
// validated (401 layer) and then checked against the URI rules (403 layer).
func TestValidateThenAuthorize(t *testing.T) {
	kp := generateTestKeyPair(t)
	p := NewFromDiscovery(
		Config{Issuer: testIssuer, ClientID: "test-client", Access: mustPolicy(t, "groups=labs%20admin")},
		Discovery{Issuer: testIssuer},
		WithKeys(map[string]*rsa.PublicKey{kp.kid: kp.public}),
	)

	base := func(groups any) map[string]any {
		return map[string]any{
			"iss":    "https://test-issuer.example.com",
			"sub":    "user-1",
			"aud":    "test-client",
			"exp":    time.Now().Add(time.Hour).Unix(),
			"groups": groups,
		}
	}

	claims, err := p.ValidateToken(t.Context(), signJWT(t, kp, base([]string{"admin"})))
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	err = p.Authorize(claims)
	if err != nil {
		t.Errorf("Authorize(admin) = %v, want nil", err)
	}
	if got := claims.Groups; len(got) != 1 || got[0] != "admin" {
		t.Errorf("Groups = %v", got)
	}

	claims, err = p.ValidateToken(t.Context(), signJWT(t, kp, base("users")))
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	err = p.Authorize(claims)
	if !errors.Is(err, ErrAccessDenied) {
		t.Errorf("Authorize(users) = %v, want ErrAccessDenied", err)
	}
}

func decodeRawForTest(t *testing.T, s string) map[string]any {
	t.Helper()

	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()

	var raw map[string]any
	err := dec.Decode(&raw)
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}

	return raw
}
