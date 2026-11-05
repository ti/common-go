package oidc

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchDiscoveryRejectsNonSuccessHTTPStatus(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"issuer":"` + server.URL + `","jwks_uri":"` + server.URL + `/jwks"}`))
	}))
	t.Cleanup(server.Close)

	_, err := New(t.Context(), Config{Issuer: server.URL, ClientID: "c"}, WithHTTPClient(server.Client()))
	if !errors.Is(err, errDiscoveryHTTPStatus) {
		t.Fatalf("New error = %v, want wrapped errDiscoveryHTTPStatus", err)
	}
	if !strings.Contains(err.Error(), "503 Service Unavailable") {
		t.Errorf("New error = %q, want HTTP status", err)
	}
}

func TestFetchJWKSRejectsNonSuccessHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	t.Cleanup(server.Close)

	p := NewFromDiscovery(
		Config{Issuer: testIssuer, ClientID: "c"},
		Discovery{Issuer: testIssuer, JwksURI: server.URL + "/jwks"},
		WithHTTPClient(server.Client()),
	)
	before := p.keys.Load()

	_, err := p.fetchJWKS(t.Context())
	if !errors.Is(err, errJWKSHTTPStatus) {
		t.Fatalf("fetchJWKS error = %v, want wrapped errJWKSHTTPStatus", err)
	}
	if !strings.Contains(err.Error(), "502 Bad Gateway") {
		t.Errorf("fetchJWKS error = %q, want HTTP status", err)
	}
	if p.keys.Load() != before {
		t.Fatal("JWKS keys were replaced from non-2xx response")
	}
}
