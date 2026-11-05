package http

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func newTestClient(t *testing.T, uri string, opts ...Option) *HTTP {
	t.Helper()
	h, err := New(context.Background(), uri, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return h
}

func defaultProxyFn() uintptr {
	return reflect.ValueOf(http.DefaultTransport.(*http.Transport).Proxy).Pointer()
}

// ?proxy= must only affect this client, never the process-global
// http.DefaultTransport shared by every other client.
func TestProxyDoesNotMutateDefaultTransport(t *testing.T) {
	before := defaultProxyFn()
	_ = newTestClient(t, "http://example.invalid?proxy=http://127.0.0.1:1")
	if after := defaultProxyFn(); after != before {
		t.Fatal("http.DefaultTransport.Proxy was modified")
	}
}

// A user-supplied client is not mutated either.
func TestProxyDoesNotMutateUserClient(t *testing.T) {
	tr := &http.Transport{}
	user := &http.Client{Transport: tr}
	_ = newTestClient(t, "http://example.invalid?proxy=http://127.0.0.1:1", WithHTTPClient(user))
	if tr.Proxy != nil || user.Transport != tr {
		t.Fatal("user-supplied client or transport was modified")
	}
}

// ?tracing=true wraps the transport with otelhttp.
func TestTracingWrapsTransport(t *testing.T) {
	h := newTestClient(t, "http://example.invalid?tracing=true")
	if _, ok := h.client.Transport.(*otelhttp.Transport); !ok {
		t.Fatalf("transport = %T, want *otelhttp.Transport", h.client.Transport)
	}
}

// The default transport is tuned for service-to-service traffic, and the
// pool size can be set from the URI.
func TestTransportTuning(t *testing.T) {
	h := newTestClient(t, "http://example.invalid")
	tr, ok := h.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", h.client.Transport)
	}
	if tr == http.DefaultTransport {
		t.Fatal("client shares http.DefaultTransport")
	}
	if tr.MaxIdleConnsPerHost < 64 {
		t.Fatalf("MaxIdleConnsPerHost = %d", tr.MaxIdleConnsPerHost)
	}
	h = newTestClient(t, "http://example.invalid?maxIdlePerHost=7&idleTimeout=3s")
	tr = h.client.Transport.(*http.Transport)
	if tr.MaxIdleConnsPerHost != 7 || tr.IdleConnTimeout != 3*time.Second {
		t.Fatalf("MaxIdleConnsPerHost=%d IdleConnTimeout=%v", tr.MaxIdleConnsPerHost, tr.IdleConnTimeout)
	}
}

// countingServer counts requests and new TCP connections.
func countingServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, &conns
}

// Per-request proxies (WithProxy) reuse pooled connections instead of building
// a new transport for every request.
func TestPerRequestProxyReusesConnections(t *testing.T) {
	// The "proxy" answers forwarded requests itself.
	proxy, conns := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	h := newTestClient(t, "http://upstream.invalid")
	ctx := WithProxy(context.Background(), proxy.URL)
	for range 5 {
		var body string
		if err := h.Request(ctx, http.MethodGet, "/", nil, nil, &body); err != nil {
			t.Fatal(err)
		}
		if body != "ok" {
			t.Fatalf("body = %q", body)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("opened %d connections to the proxy, want 1", n)
	}
}

// Idempotent requests retry 502/503/504 with backoff.
func TestRetryOnUnavailable(t *testing.T) {
	var calls atomic.Int32
	srv, _ := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	h := newTestClient(t, srv.URL+"?try=3")
	var body string
	if err := h.Request(context.Background(), http.MethodGet, "/", nil, nil, &body); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || body != "ok" {
		t.Fatalf("calls=%d body=%q", calls.Load(), body)
	}
}

// Non-idempotent requests are not retried after the request may have reached
// the server, unless explicitly allowed.
func TestNoRetryForPOSTByDefault(t *testing.T) {
	var calls atomic.Int32
	srv, _ := countingServer(t, func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
		panic(http.ErrAbortHandler) // drop the connection mid-request
	})
	h := newTestClient(t, srv.URL+"?try=2")
	err := h.Request(context.Background(), http.MethodPost, "/", nil, "x", nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("POST attempted %d times, want 1", n)
	}

	calls.Store(0)
	h = newTestClient(t, srv.URL+"?try=2&retryNonIdempotent=true")
	_ = h.Request(context.Background(), http.MethodPost, "/", nil, "x", nil)
	if n := calls.Load(); n != 3 {
		t.Fatalf("POST with retryNonIdempotent attempted %d times, want 3", n)
	}
}

// An Idempotency-Key header makes a POST safe to retry.
func TestRetryPOSTWithIdempotencyKey(t *testing.T) {
	var calls atomic.Int32
	srv, _ := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	h := newTestClient(t, srv.URL+"?try=2")
	hdr := http.Header{}
	hdr.Set("Idempotency-Key", "k1")
	var body string
	if err := h.Request(context.Background(), http.MethodPost, "/", hdr, "x", &body); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

// Retries stop as soon as the context is done.
func TestRetryHonorsContext(t *testing.T) {
	srv, _ := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	h := newTestClient(t, srv.URL+"?try=50&backoff=1s")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := h.Request(ctx, http.MethodGet, "/", nil, nil, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("retries ignored the context deadline (%v)", time.Since(start))
	}
	if !errors.Is(err, context.DeadlineExceeded) && err.Error() == "" {
		t.Fatalf("err = %v", err)
	}
}

// Response decoding keeps encoding/json v1 semantics for caller-owned types:
// third-party APIs are matched case-insensitively and nulls are accepted.
// (A switch to json/v2 defaults silently left such fields empty.)
func TestResponseDecodingIsCaseInsensitive(t *testing.T) {
	srv, _ := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"AccessToken":"tok","expires_in":60,"scopes":null}`))
	})
	h := newTestClient(t, srv.URL)
	var out struct {
		AccessToken string   `json:"accessToken"`
		ExpiresIn   int      `json:"expires_in"`
		Scopes      []string `json:"scopes"`
	}
	if err := h.Request(context.Background(), http.MethodGet, "/", nil, nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.AccessToken != "tok" || out.ExpiresIn != 60 {
		t.Fatalf("decoded %+v", out)
	}
}

// Request bodies encode nil slices as null, as encoding/json v1 does.
func TestRequestEncodingNilSliceIsNull(t *testing.T) {
	var got string
	srv, _ := countingServer(t, func(_ http.ResponseWriter, r *http.Request) {
		b := make([]byte, 128)
		n, _ := r.Body.Read(b)
		got = string(b[:n])
	})
	h := newTestClient(t, srv.URL)
	body := struct {
		IDs []string `json:"ids"`
	}{}
	if err := h.Request(context.Background(), http.MethodPost, "/", nil, body, nil); err != nil {
		t.Fatal(err)
	}
	if got != `{"ids":null}` {
		t.Fatalf("body = %s", got)
	}
}
