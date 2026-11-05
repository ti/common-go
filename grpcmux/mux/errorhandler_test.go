package mux

import (
	"context"
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
