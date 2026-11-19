package mux

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// An error from a gateway backed by a gRPC connection carries the raw
// trailers (grpc-status-details-bin is binary). They must never be written
// as HTTP headers: HTTP/1.1 clients reject the response as malformed.
func TestErrorDoesNotLeakGRPCTrailers(t *testing.T) {
	m := NewServeMux()
	st, _ := status.New(codes.InvalidArgument, "bad input").WithDetails(&errdetails.ErrorInfo{Reason: "BAD"})
	details, _ := proto.Marshal(st.Proto())
	m.Handle(http.MethodGet, "/fail", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		ctx := runtime.NewServerMetadataContext(r.Context(), runtime.ServerMetadata{
			HeaderMD: metadata.Pairs("x-request-cost", "7", "grpc-internal", "no"),
			TrailerMD: metadata.MD{
				"grpc-status-details-bin": {string(details)},
				"grpc-status":             {"3"},
				"x-trace-note":            {"kept"},
				"x-binary-bin":            {"\x00\x01"},
			},
		})
		_, outbound := runtime.MarshalerForRequest(m.ServeMux(), r)
		runtime.HTTPError(ctx, m.ServeMux(), outbound, w, r, st.Err())
	})
	srv := httptest.NewServer(m)
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/fail", nil)
	resp, err := srv.Client().Do(req) // HTTP/1.1
	if err != nil {
		t.Fatalf("HTTP/1.1 client rejected the response: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "bad input") {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	for k := range resp.Header {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "grpc-") || strings.HasSuffix(lk, "-bin") {
			t.Errorf("leaked header %s", k)
		}
	}
	if resp.Header.Get("X-Request-Cost") != "7" {
		t.Errorf("header metadata not forwarded: %v", resp.Header)
	}
	if resp.Header.Get("X-Trace-Note") != "kept" {
		t.Errorf("x- trailer metadata not forwarded: %v", resp.Header)
	}
}

func errorWithDetails(t *testing.T) error {
	t.Helper()
	st, err := status.New(codes.AlreadyExists, "email taken").WithDetails(
		&errdetails.ErrorInfo{Reason: "EMAIL_ALREADY_IN_USE", Domain: "example.user.v1", Metadata: map[string]string{"email": "a@b.c"}},
		&errdetails.RetryInfo{}, // no set fields
		&errdetails.QuotaFailure{Violations: []*errdetails.QuotaFailure_Violation{{Subject: "user:1", Description: "3 left"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return st.Err()
}

// compact removes the insignificant whitespace protojson adds (deliberately
// not the same in every build), so bodies can be compared byte for byte.
func compact(t *testing.T, b []byte) string {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return out.String()
}

// The default error body is a published wire format: details are the bare
// JSON of each message, without "@type", and a detail with no set fields is
// left out. It must not change without an opt-in.
func TestErrorBodyDefaultDetailsAreUnchanged(t *testing.T) {
	const want = `{"error":"already_exists","error_code":6,"error_description":"email taken","details":[` +
		`{"reason":"EMAIL_ALREADY_IN_USE","domain":"example.user.v1","metadata":{"email":"a@b.c"}},` +
		`{"violations":[{"subject":"user:1","description":"3 left"}]}]}`

	// Written by a handler.
	rec := httptest.NewRecorder()
	WriteHTTPErrorResponse(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/users", nil), errorWithDetails(t))
	if got := compact(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || got != want {
		t.Fatalf("WriteHTTPErrorResponse: %d\n got %s\nwant %s", rec.Code, got, want)
	}

	// Written by the gateway error handler of a mux with default options.
	m := NewServeMux()
	m.Handle(http.MethodGet, "/fail", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		_, outbound := runtime.MarshalerForRequest(m.ServeMux(), r)
		runtime.HTTPError(r.Context(), m.ServeMux(), outbound, w, r, errorWithDetails(t))
	})
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/fail", nil))
	if got := compact(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || got != want {
		t.Fatalf("gateway error handler: %d\n got %s\nwant %s", rec.Code, got, want)
	}
}

// With WithTypedErrorDetails the body carries the same machine-readable
// information as the Connect error: every detail with its type, none left
// out for being empty.
func TestErrorBodyTypedDetails(t *testing.T) {
	const want = `{"error":"already_exists","error_code":6,"error_description":"email taken","details":[` +
		`{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"EMAIL_ALREADY_IN_USE","domain":"example.user.v1","metadata":{"email":"a@b.c"}},` +
		`{"@type":"type.googleapis.com/google.rpc.RetryInfo"},` +
		`{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"subject":"user:1","description":"3 left"}]}]}`
	m := NewServeMux(WithTypedErrorDetails())
	m.Handle(http.MethodGet, "/fail", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		_, outbound := runtime.MarshalerForRequest(m.ServeMux(), r)
		runtime.HTTPError(r.Context(), m.ServeMux(), outbound, w, r, errorWithDetails(t))
	})
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/fail", nil))
	if got := compact(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || got != want {
		t.Fatalf("typed: %d\n got %s\nwant %s", rec.Code, got, want)
	}
	// The middleware's own rejections (authentication) follow the option too.
	denied := NewServeMux(WithTypedErrorDetails(), WithAuthFunc(func(ctx context.Context) (context.Context, error) {
		return ctx, errorWithDetails(t)
	}))
	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/anything", nil))
	if got := compact(t, rec.Body.Bytes()); got != want {
		t.Fatalf("middleware rejection:\n got %s\nwant %s", got, want)
	}
}
