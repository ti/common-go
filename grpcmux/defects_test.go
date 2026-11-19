package grpcmux

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/log"
)

// captureLogs redirects the library's log output for the test and returns a
// function that reports the lines written so far, decoded.
func captureLogs(t *testing.T) func() []map[string]any {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	log.SetOutput(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}))
	t.Cleanup(func() { log.SetOutput(io.Discard) })
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		var lines []map[string]any
		for line := range strings.SplitSeq(buf.String(), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) == nil {
				lines = append(lines, m)
			}
		}
		return lines
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// findLog waits for a log line whose action is the given one.
func findLog(t *testing.T, lines func() []map[string]any, action string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, l := range lines() {
			if l["action"] == action {
				return l
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no log line with action %q in %v", action, lines())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// httpResult is what the tests look at in an HTTP response.
type httpResult struct {
	StatusCode int
	Header     http.Header
}

func post(t *testing.T, url string, header map[string]string, body string) (httpResult, string) {
	t.Helper()
	return do(t, http.MethodPost, url, header, body)
}

func do(t *testing.T, method, url string, header map[string]string, body string) (httpResult, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return httpResult{StatusCode: resp.StatusCode, Header: resp.Header}, string(b)
}

// WithNoAuthMethods exempts exactly the named method; the older prefix form
// also exempts every method whose name merely starts the same way.
func TestNoAuthMethodsAreExact(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const adminPath = testCheckPath + "Admin"
	call := func(s *Server, addr, path string) codes.Code {
		return status.Code(dial(t, addr).Invoke(ctx, path, &pbhealth.HealthCheckRequest{}, &pbhealth.HealthCheckResponse{}))
	}
	httpStatus := func(s *Server, path string) int {
		resp, _ := post(t, "http://"+s.opts.httpAddr+path, nil, "{}")
		return resp.StatusCode
	}

	exact, _ := connectServer(t, WithAuthFunc(denyAll), WithNoAuthMethods(testCheckPath))
	prefix, _ := connectServer(t, WithAuthFunc(denyAll), WithNoAuthPrefixes(testCheckPath))
	service, _ := connectServer(t, WithAuthFunc(denyAll), WithNoAuthMethods("/"+testServiceName+"/*"))

	for _, tc := range []struct {
		name      string
		s         *Server
		wantAdmin codes.Code
		wantHTTP  int
	}{
		{"exact method", exact, codes.Unauthenticated, http.StatusUnauthorized},
		{"prefix (old form)", prefix, codes.OK, http.StatusOK},
		{"whole service", service, codes.OK, http.StatusOK},
	} {
		if got := call(tc.s, tc.s.opts.grpcAddr, testCheckPath); got != codes.OK {
			t.Errorf("%s: gRPC Check = %v, want OK (exempt)", tc.name, got)
		}
		if got := call(tc.s, tc.s.opts.grpcAddr, adminPath); got != tc.wantAdmin {
			t.Errorf("%s: gRPC CheckAdmin = %v, want %v", tc.name, got, tc.wantAdmin)
		}
		if got := httpStatus(tc.s, testCheckPath); got != http.StatusOK {
			t.Errorf("%s: HTTP Check = %d, want 200 (exempt)", tc.name, got)
		}
		if got := httpStatus(tc.s, adminPath); got != tc.wantHTTP {
			t.Errorf("%s: HTTP CheckAdmin = %d, want %d", tc.name, got, tc.wantHTTP)
		}
		if got := call(tc.s, tc.s.opts.grpcAddr, testMutatePath); tc.name != "whole service" && got != codes.Unauthenticated {
			t.Errorf("%s: gRPC Mutate = %v, want UNAUTHENTICATED", tc.name, got)
		}
	}
}

// An auth rejection is logged with the status the client received: 403 for
// PERMISSION_DENIED (it used to be logged as 401), with code and error.
func TestAuthRejectionIsLoggedWithItsStatus(t *testing.T) {
	lines := captureLogs(t)
	s, _ := connectServer(t, WithAuthFunc(denyAll))
	url := "http://" + s.opts.httpAddr

	resp, _ := post(t, url+testCheckPath, map[string]string{"Authorization": "Bearer forbidden"}, "{}")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("response status = %d, want 403", resp.StatusCode)
	}
	l := findLog(t, lines, testCheckPath)
	if l["status"] != float64(http.StatusForbidden) || l["code"] != float64(codes.PermissionDenied) || l["error"] != "not allowed" {
		t.Errorf("403 log line: status=%v code=%v error=%v", l["status"], l["code"], l["error"])
	}

	resp, _ = post(t, url+testMutatePath, nil, "{}")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("response status = %d, want 401", resp.StatusCode)
	}
	if l := findLog(t, lines, testMutatePath); l["status"] != float64(http.StatusUnauthorized) {
		t.Errorf("401 log line: status=%v", l["status"])
	}
}

// On the gRPC port authentication runs before validation: an unauthenticated
// caller must not learn whether its request was valid.
func TestAuthRunsBeforeValidationOnGRPC(t *testing.T) {
	s, _ := connectServer(t, WithAuthFunc(denyAll))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cc := dial(t, s.opts.grpcAddr)
	invalid := &pbhealth.HealthCheckRequest{Service: "invalid"}
	if err := cc.Invoke(ctx, testCheckPath, invalid, &pbhealth.HealthCheckResponse{}); status.Code(err) != codes.Unauthenticated {
		t.Errorf("invalid request without credentials: %v, want UNAUTHENTICATED", err)
	}
	authed := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer good")
	if err := cc.Invoke(authed, testCheckPath, invalid, &pbhealth.HealthCheckResponse{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("invalid request with credentials: %v, want INVALID_ARGUMENT", err)
	}
	// The HTTP port already behaved this way.
	if resp, _ := post(t, "http://"+s.opts.httpAddr+testCheckPath, nil, `{"service":"invalid"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("HTTP: invalid request without credentials = %d, want 401", resp.StatusCode)
	}
}

// A cross-origin gRPC-Web or Connect browser client needs X-Grpc-Web and
// X-User-Agent allowed and the Grpc-* response headers exposed.
func TestCORSForBrowserRPCClients(t *testing.T) {
	s, _ := connectServer(t, WithCORS(CORSConfig{
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedHeaders: []string{"X-Custom"},
		ExposeHeaders:  []string{"X-Extra"},
	}))
	url := "http://" + s.opts.httpAddr + testCheckPath
	resp, _ := do(t, http.MethodOptions, url, map[string]string{
		"Origin":                         "https://app.example.com",
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "content-type,x-grpc-web,x-user-agent",
	}, "")
	allow := strings.ToLower(resp.Header.Get("Access-Control-Allow-Headers"))
	for _, h := range []string{"x-grpc-web", "x-user-agent", "connect-protocol-version", "grpc-timeout", "authorization", "x-custom"} {
		if !strings.Contains(allow, h) {
			t.Errorf("preflight Access-Control-Allow-Headers %q lacks %s", allow, h)
		}
	}
	resp, _ = post(t, url, map[string]string{"Origin": "https://app.example.com"}, "{}")
	expose := strings.ToLower(resp.Header.Get("Access-Control-Expose-Headers"))
	for _, h := range []string{"grpc-status", "grpc-message", "grpc-status-details-bin", "x-extra"} {
		if !strings.Contains(expose, h) {
			t.Errorf("Access-Control-Expose-Headers %q lacks %s", expose, h)
		}
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q", got)
	}
	resp, _ = post(t, url, map[string]string{"Origin": "https://evil.example.com"}, "{}")
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a foreign origin got Access-Control-Allow-Origin %q", got)
	}
}

// grpc.health.v1: the empty name and registered services share the server's
// status; a name that is not registered is NOT_FOUND (it used to be SERVING).
func TestHealthUnknownService(t *testing.T) {
	s, _ := connectServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hc := pbhealth.NewHealthClient(dial(t, s.opts.grpcAddr))
	for _, name := range []string{"", testServiceName, "grpc.health.v1.Health"} {
		resp, err := hc.Check(ctx, &pbhealth.HealthCheckRequest{Service: name})
		if err != nil || resp.GetStatus() != pbhealth.HealthCheckResponse_SERVING {
			t.Errorf("Check(%q) = %v, %v; want SERVING", name, resp, err)
		}
	}
	if _, err := hc.Check(ctx, &pbhealth.HealthCheckRequest{Service: "no.such.Service"}); status.Code(err) != codes.NotFound {
		t.Errorf("Check(unknown) = %v, want NOT_FOUND", err)
	}
	w, err := hc.Watch(ctx, &pbhealth.HealthCheckRequest{Service: "no.such.Service"})
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := w.Recv(); err != nil || resp.GetStatus() != pbhealth.HealthCheckResponse_SERVICE_UNKNOWN {
		t.Errorf("Watch(unknown) = %v, %v; want SERVICE_UNKNOWN", resp, err)
	}
	list, err := hc.List(ctx, &pbhealth.HealthListRequest{})
	if err != nil || list.GetStatuses()[testServiceName].GetStatus() != pbhealth.HealthCheckResponse_SERVING ||
		list.GetStatuses()[""].GetStatus() != pbhealth.HealthCheckResponse_SERVING {
		t.Errorf("List = %v, %v", list, err)
	}
}

// With WithProtocolErrors an auth failure on a Connect stream, a gRPC call
// or a gRPC-Web call on the HTTP port is answered in that protocol (HTTP 200
// and an end-of-stream error with the real message), while REST and Connect
// unary clients keep the JSON body with its HTTP status.
func TestProtocolErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	on, _ := streamServer(t, WithAuthFunc(denyAll), WithProtocolErrors())
	off, _ := streamServer(t, WithAuthFunc(denyAll))

	const want = "missing or invalid token"
	for _, p := range streamProtocols[1:] {
		st := p.open(t, on, ctx, streamWatchPath, false, &pbhealth.HealthCheckRequest{})
		_, err := st.Recv()
		if streamCode(err) != "Unauthenticated" || streamMessage(err) != want {
			t.Errorf("%s with protocol errors: %v, want UNAUTHENTICATED %q", p.name, err, want)
		}
		// Without the option the client can only map the HTTP 401 to the
		// code; it does not get the status the server meant.
		st = p.open(t, off, ctx, streamWatchPath, false, &pbhealth.HealthCheckRequest{})
		_, err = st.Recv()
		if streamCode(err) != "Unauthenticated" || streamMessage(err) == want {
			t.Errorf("%s without protocol errors: %v, want UNAUTHENTICATED derived from HTTP 401", p.name, err)
		}
	}

	// On the wire: a Connect stream gets HTTP 200 and an end-of-stream envelope.
	for _, tc := range []struct {
		s          *Server
		wantStatus int
		wantType   string
	}{{on, http.StatusOK, "application/connect+json"}, {off, http.StatusUnauthorized, "application/json"}} {
		resp, body := post(t, "http://"+tc.s.opts.httpAddr+streamWatchPath,
			map[string]string{"Content-Type": "application/connect+json"}, "")
		if resp.StatusCode != tc.wantStatus || resp.Header.Get("Content-Type") != tc.wantType {
			t.Errorf("connect stream: HTTP %d %s, want %d %s", resp.StatusCode, resp.Header.Get("Content-Type"), tc.wantStatus, tc.wantType)
		}
		if tc.wantStatus == http.StatusOK && (len(body) < 5 || body[0] != 0x02 || !strings.Contains(body, `"code":"unauthenticated"`)) {
			t.Errorf("connect stream body is not an end-of-stream envelope: %q", body)
		}
	}

	// REST and Connect unary are untouched by the option.
	for _, s := range []*Server{on, off} {
		resp, body := post(t, "http://"+s.opts.httpAddr+testCheckPath, nil, "{}")
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, `"error":"unauthenticated"`) ||
			!strings.Contains(body, `"error_description":"missing or invalid token"`) {
			t.Errorf("unary JSON: HTTP %d %s", resp.StatusCode, body)
		}
	}

	// With credentials the stream works.
	authed := context.WithValue(ctx, bearerKey{}, "good")
	st := streamProtocols[2].open(t, on, authed, streamWatchPath, false, &pbhealth.HealthCheckRequest{Service: "header"})
	if _, err := st.Recv(); err != nil {
		t.Errorf("authenticated stream: %v", err)
	}
}

// ?access_token= and X-HTTP-Method-Override can be turned off, and the
// override never turns a GET into something else.
func TestQueryTokenAndMethodOverride(t *testing.T) {
	seen := func(s *Server) *string {
		var method string
		s.Handle("/echo-method", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method = r.Method
			_, _ = io.WriteString(w, r.Method)
		}))
		return &method
	}
	def := newTestServer(t, WithAuthFunc(denyAll))
	defMethod := seen(def)
	runTestServer(t, def)
	strict := newTestServer(t, WithAuthFunc(denyAll), WithoutQueryAccessToken(), WithoutMethodOverride())
	strictMethod := seen(strict)
	runTestServer(t, strict)
	good := map[string]string{"Authorization": "Bearer good"}

	// Default: the query token authenticates, as before.
	if resp, _ := post(t, "http://"+def.opts.httpAddr+"/echo-method?access_token=good", nil, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("default: ?access_token= = %d, want 200", resp.StatusCode)
	}
	if resp, _ := post(t, "http://"+strict.opts.httpAddr+"/echo-method?access_token=good", nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("WithoutQueryAccessToken: ?access_token= = %d, want 401", resp.StatusCode)
	}
	if resp, _ := post(t, "http://"+strict.opts.httpAddr+"/echo-method", good, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("WithoutQueryAccessToken: Authorization header = %d, want 200", resp.StatusCode)
	}

	override := map[string]string{"Authorization": "Bearer good", "X-HTTP-Method-Override": "DELETE"}
	post(t, "http://"+def.opts.httpAddr+"/echo-method", override, "")
	if *defMethod != http.MethodDelete {
		t.Errorf("default: POST + override DELETE reached the handler as %s", *defMethod)
	}
	do(t, http.MethodGet, "http://"+def.opts.httpAddr+"/echo-method", override, "")
	if *defMethod != http.MethodGet {
		t.Errorf("a GET was overridden to %s; only POST may be", *defMethod)
	}
	post(t, "http://"+def.opts.httpAddr+"/echo-method", map[string]string{"Authorization": "Bearer good", "X-HTTP-Method-Override": "TRACE"}, "")
	if *defMethod != http.MethodPost {
		t.Errorf("override to TRACE reached the handler as %s", *defMethod)
	}
	post(t, "http://"+strict.opts.httpAddr+"/echo-method", override, "")
	if *strictMethod != http.MethodPost {
		t.Errorf("WithoutMethodOverride: handler saw %s, want POST", *strictMethod)
	}
}

func init() {
	// Something on http.DefaultServeMux, where net/http/pprof registers.
	http.HandleFunc("/debug/grpcmux-test", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "debug data")
	})
}

// /debug/** serves http.DefaultServeMux. It is off unless asked for, and
// when it is on and an AuthFunc is configured it is behind it.
func TestDebugHandlers(t *testing.T) {
	// served reports whether the debug handler answered (an unknown route is
	// answered by the gateway's "API NOT FOUND" body instead).
	get := func(s *Server, header map[string]string) (status int, served bool) {
		resp, body := do(t, http.MethodGet, "http://"+s.opts.httpAddr+"/debug/grpcmux-test", header, "")
		return resp.StatusCode, body == "debug data"
	}
	good := map[string]string{"Authorization": "Bearer good"}
	start := func(opts ...Option) *Server {
		s := newTestServer(t, opts...)
		runTestServer(t, s)
		return s
	}
	if _, served := get(start(), nil); served {
		t.Error("default, no auth: /debug is served")
	}
	if _, served := get(start(WithAuthFunc(denyAll)), good); served {
		t.Error("default, with auth: /debug is served without being asked for")
	}
	if _, served := get(start(WithDebugHandlers(true)), nil); !served {
		t.Error("WithDebugHandlers(true), no auth: /debug is not served")
	}
	authed := start(WithAuthFunc(denyAll), WithDebugHandlers(true))
	if code, served := get(authed, nil); served || code != http.StatusUnauthorized {
		t.Errorf("WithDebugHandlers(true), no credentials: /debug = %d (served: %v), want 401", code, served)
	}
	if _, served := get(authed, good); !served {
		t.Error("WithDebugHandlers(true), credentials: /debug is not served")
	}
	if _, served := get(start(WithAuthFunc(denyAll), WithDebugHandlers(false)), good); served {
		t.Error("WithDebugHandlers(false): /debug is served")
	}
}

// grpc.SetHeader and grpc.SetTrailer in a unary handler registered on
// ConnectServer() reach the client (they used to be dropped).
func TestConnectServerUnaryResponseMetadata(t *testing.T) {
	s := newTestServer(t)
	procedure := "/" + testServiceName + "/Check"
	s.ConnectServer().Register(connect.Method{
		Spec: connect.Spec{Procedure: procedure, StreamType: connect.StreamTypeUnary, Schema: methodDescriptor(testServiceName, "Check")},
		Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
			req := &pbhealth.HealthCheckRequest{}
			if err := stream.Receive(req); err != nil {
				return err
			}
			_ = grpc.SetHeader(ctx, metadata.Pairs("x-test-header", "h"))
			_ = grpc.SetTrailer(ctx, metadata.Pairs("x-test-trailer", "t"))
			return stream.Send(serving)
		},
	})
	runTestServer(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, info, err := connectClient(s, procedure).call(ctx, &pbhealth.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if h, tr := info.ResponseHeader().Get("x-test-header"), info.ResponseTrailer().Get("x-test-trailer"); h != "h" || tr != "t" {
		t.Errorf("header %q trailer %q, want h and t", h, tr)
	}
}

// A HEAD is served as the GET it mirrors, without a body: same status and
// headers as that GET on every kind of route, the auth error on a protected
// route, and never a 5xx merely because of the method. (Before, the CORS
// step answered every HEAD with an empty 200, whatever the path and without
// authentication.) RPC paths have no HEAD form and answer 405 with Allow.
func TestHEADMirrorsGET(t *testing.T) {
	page := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Page", body)
			_, _ = io.WriteString(w, body)
		}
	}
	good := map[string]string{"Authorization": "Bearer good"}
	for _, mode := range []struct {
		name string
		opts []Option
		auth bool
	}{
		{"default", nil, false},
		{"default+auth", []Option{WithAuthFunc(denyAll)}, true},
		{"rest", []Option{WithREST()}, false},
		{"rest+auth", []Option{WithREST(), WithAuthFunc(denyAll)}, true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			s := newTestServer(t, mode.opts...)
			s.HandleFunc(http.MethodGet, "/gw", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
				page("gw")(w, r)
			})
			s.Handle("/plain", page("plain"))
			s.Handle("GET /x", page("x"))
			RegisterConnectService(s, &testServiceDesc, &testService{})
			runTestServer(t, s)
			base := "http://" + s.opts.httpAddr

			for _, path := range []string{"/gw", "/plain", "/x", "/favicon.ico", "/no/such/path", "/healthz", "/readyz"} {
				probe := path == "/healthz" || path == "/readyz" || path == "/favicon.ico"
				for _, header := range []map[string]string{nil, good} {
					get, getBody := do(t, http.MethodGet, base+path, header, "")
					head, headBody := do(t, http.MethodHead, base+path, header, "")
					name := path
					if header != nil {
						name += " (credentials)"
					}
					if head.StatusCode != get.StatusCode {
						t.Errorf("%s: HEAD = %d, GET = %d", name, head.StatusCode, get.StatusCode)
					}
					if head.StatusCode >= 500 {
						t.Errorf("%s: HEAD = %d, a 5xx", name, head.StatusCode)
					}
					if headBody != "" {
						t.Errorf("%s: HEAD response has a body: %q", name, headBody)
					}
					if cl := head.Header.Get("Content-Length"); cl != "" && cl != strconv.Itoa(len(getBody)) {
						t.Errorf("%s: HEAD Content-Length = %s, the GET body has %d bytes", name, cl, len(getBody))
					}
					if head.Header.Get("X-Page") != get.Header.Get("X-Page") {
						t.Errorf("%s: HEAD X-Page = %q, GET X-Page = %q", name, head.Header.Get("X-Page"), get.Header.Get("X-Page"))
					}
					// What the status must be, not only that the two agree.
					want := http.StatusOK
					switch {
					case mode.auth && header == nil && !probe:
						want = http.StatusUnauthorized
					case path == "/no/such/path" && strings.HasPrefix(mode.name, "rest"):
						want = http.StatusNotFound // WithREST turns the 404 status on
					}
					if head.StatusCode != want {
						t.Errorf("%s: HEAD = %d, want %d", name, head.StatusCode, want)
					}
				}
			}

			// RPC paths: no HEAD form.
			for path, allow := range map[string]string{testCheckPath: "GET, POST", testMutatePath: "POST"} {
				head, body := do(t, http.MethodHead, base+path, good, "")
				if head.StatusCode != http.StatusMethodNotAllowed || head.Header.Get("Allow") != allow || body != "" {
					t.Errorf("HEAD %s = %d Allow %q body %q, want 405 Allow %q", path, head.StatusCode, head.Header.Get("Allow"), body, allow)
				}
				if mode.auth {
					if head, _ := do(t, http.MethodHead, base+path, nil, ""); head.StatusCode != http.StatusUnauthorized {
						t.Errorf("HEAD %s without credentials = %d, want 401", path, head.StatusCode)
					}
				}
			}
			// A CORS preflight is still answered by the middleware.
			if resp, _ := do(t, http.MethodOptions, base+"/x", map[string]string{"Origin": "https://x.example"}, ""); resp.StatusCode != http.StatusOK {
				t.Errorf("OPTIONS /x = %d, want 200", resp.StatusCode)
			}
		})
	}
}

// The access log names the method the client sent.
func TestHEADIsLoggedAsHEAD(t *testing.T) {
	lines := captureLogs(t)
	s := newTestServer(t)
	s.Handle("/logged-page", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	runTestServer(t, s)
	do(t, http.MethodHead, "http://"+s.opts.httpAddr+"/logged-page", nil, "")
	if l := findLog(t, lines, "/logged-page"); l["msg"] != http.MethodHead {
		t.Errorf("log msg = %v, want HEAD", l["msg"])
	}
}

// An AuthFunc that admits everyone (guest mode: the AuthFunc of a nil
// oidc.Provider) must not make /debug/** public.
func TestDebugHandlersOffWithPermissiveAuthFunc(t *testing.T) {
	guest := func(ctx context.Context) (context.Context, error) { return ctx, nil }
	s := newTestServer(t, WithAuthFunc(guest))
	runTestServer(t, s)
	if _, body := do(t, http.MethodGet, "http://"+s.opts.httpAddr+"/debug/grpcmux-test", nil, ""); body == "debug data" {
		t.Error("/debug is served by a server whose AuthFunc admits everyone")
	}
}
