package oidc

import (
	"context"
	"crypto/rsa"
	"maps"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	grpcmd "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/grpcmux/mux"
)

func incoming(authorization string) context.Context {
	md := grpcmd.MD{}
	if authorization != "" {
		md.Set("authorization", authorization)
	}

	return grpcmd.NewIncomingContext(context.Background(), md)
}

func validClaims(sub string, extra map[string]any) map[string]any {
	now := time.Now()
	claims := map[string]any{
		"iss": testIssuer, "aud": "test-client", "sub": sub,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "project_id": "p1",
	}
	maps.Copy(claims, extra)

	return claims
}

func TestAuthFuncNilProviderAdmitsEverything(t *testing.T) {
	var p *Provider

	ctx, err := p.AuthFunc()(incoming(""))
	if err != nil {
		t.Fatalf("nil provider: %v", err)
	}

	if _, ok := FromContext(ctx); ok {
		t.Fatal("nil provider must not invent claims")
	}
}

func TestAuthFunc(t *testing.T) {
	kp := generateTestKeyPair(t)
	p := NewFromDiscovery(
		Config{Issuer: testIssuer, ClientID: "test-client", Scope: DefaultScope, Access: mustPolicy(t, "groups=admin")},
		Discovery{Issuer: testIssuer},
		WithKeys(map[string]*rsa.PublicKey{kp.kid: kp.public}),
	)
	auth := p.AuthFunc()

	cases := []struct {
		name          string
		authorization string
		code          codes.Code
		msgPrefix     string
	}{
		{"missing header", "", codes.Unauthenticated, "missing authorization header"},
		{"not bearer", "Basic abc", codes.Unauthenticated, "authorization header must use Bearer scheme"},
		{"invalid token", "Bearer not.a.jwt", codes.Unauthenticated, "invalid token: "},
		{"valid token, rule fails", "Bearer " + signJWT(t, kp, validClaims("u1", map[string]any{"groups": []string{"dev"}})),
			codes.PermissionDenied, "access denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := auth(incoming(tc.authorization))
			if status.Code(err) != tc.code || !strings.HasPrefix(status.Convert(err).Message(), tc.msgPrefix) {
				t.Fatalf("err = %v, want %v %q...", err, tc.code, tc.msgPrefix)
			}
		})
	}

	t.Run("valid token", func(t *testing.T) {
		token := signJWT(t, kp, validClaims("u1", map[string]any{"groups": "admin"}))

		ctx, err := auth(incoming("Bearer " + token))
		if err != nil {
			t.Fatal(err)
		}

		claims, ok := FromContext(ctx)
		if !ok || claims.Sub != "u1" {
			t.Fatalf("claims = %+v", claims)
		}

		info, ok := mux.AuthInfoFromContext(ctx)
		if !ok || info.GetUserID() != "u1" || info.GetClientID() != "test-client" ||
			info.GetProjectID() != "p1" || info.AuthType() != "oidc" {
			t.Fatalf("auth info = %+v", info)
		}
	})
}
