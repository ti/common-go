package mux

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A handler behind the interceptor chain must be able to bound its writes:
// http.ResponseController reaches the connection only through Unwrap.
func TestResponseControllerReachesTheConnectionThroughTheInterceptor(t *testing.T) {
	handler := NewServeMux().Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(*responseWriter); !ok {
			t.Errorf("handler got %T: this test no longer exercises the interceptor's writer", w)
		}

		rc := http.NewResponseController(w)

		if err := rc.SetWriteDeadline(time.Now().Add(time.Minute)); err != nil {
			t.Errorf("SetWriteDeadline through the interceptor: %v", err)
		}

		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			t.Errorf("clearing the write deadline: %v", err)
		}

		if err := rc.SetReadDeadline(time.Now().Add(time.Minute)); err != nil {
			t.Errorf("SetReadDeadline through the interceptor: %v", err)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))

		if err := rc.Flush(); err != nil {
			t.Errorf("Flush through the interceptor: %v", err)
		}
	}))

	h2c := new(http.Protocols)
	h2c.SetUnencryptedHTTP2(true)

	for name, protocols := range map[string]*http.Protocols{"http1": nil, "h2c": h2c} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewUnstartedServer(handler)
			client := &http.Client{}

			if protocols != nil {
				protocols.SetHTTP1(true)
				srv.Config.Protocols = protocols

				only := new(http.Protocols)
				only.SetUnencryptedHTTP2(true)
				client.Transport = &http.Transport{Protocols: only}
			}

			srv.Start()
			t.Cleanup(srv.Close)

			// POST: OPTIONS and the health paths bypass the interceptor's writer.
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/svc.v1.Service/Method", http.NoBody)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d", resp.StatusCode)
			}

			if protocols != nil && resp.ProtoMajor != 2 {
				t.Fatalf("served over %s, want HTTP/2", resp.Proto)
			}
		})
	}
}

// The deadline set through the interceptor is the connection's: a write to a
// peer that stopped reading fails when it passes, instead of blocking.
func TestWriteDeadlineThroughTheInterceptorCutsAStalledWrite(t *testing.T) {
	result := make(chan error, 1)

	srv := httptest.NewServer(NewServeMux().Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w)
		w.WriteHeader(http.StatusOK)

		if err := rc.SetWriteDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
			result <- err

			return
		}

		chunk := make([]byte, 1<<20)

		for range 256 { // far more than the socket buffers hold
			if _, err := w.Write(chunk); err != nil {
				result <- nil

				return
			}

			_ = rc.Flush()
		}

		result <- context.DeadlineExceeded // every write went through: nothing stalled
	})))
	t.Cleanup(srv.Close)

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// A request whose response is never read.
	if _, err := conn.Write([]byte("POST /svc.v1.Service/Method HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n")); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("the stalled write was not cut by the deadline: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the handler is still blocked writing to a peer that stopped reading")
	}
}
