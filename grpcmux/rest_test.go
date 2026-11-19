package grpcmux

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// A hand-written service with google.api.http rules (no protoc), on the
// health messages: the request has one string field "service", the response
// one enum field "status".
//
//	service grpcmux.resttest.v1.ThingService {
//	  rpc Get(HealthCheckRequest) returns (HealthCheckResponse) {
//	    option (google.api.http) = {
//	      get: "/rt/v1/things/{service}"
//	      additional_bindings {get: "/rt/v1/{service=shelves/*}/thing"}
//	    };
//	  }
//	  rpc Status(...)  {get: "/rt/v1/things/{service}/status", response_body: "status"}
//	  rpc Rename(...)  {post: "/rt/v1/things", body: "service"}          // scalar body
//	  rpc Act(...)     {post: "/rt/v1/things/{service}:act", body: "*"}   // custom verb
//	  rpc Watch(...) returns (stream ...) {get: "/rt/v1/things/{service}:watch"}
//	  rpc Upload(stream ...) returns (...) {post: "/rt/v1/upload", body: "*"} // no REST form
//	  rpc Plain(...)   // no rule: not a REST route
//	}
const restTestService = "grpcmux.resttest.v1.ThingService"

func httpOption(rule *annotations.HttpRule) *descriptorpb.MethodOptions {
	opts := &descriptorpb.MethodOptions{}
	proto.SetExtension(opts, annotations.E_Http, rule)
	return opts
}

func init() {
	const in, out = ".grpc.health.v1.HealthCheckRequest", ".grpc.health.v1.HealthCheckResponse"
	method := func(name string, rule *annotations.HttpRule) *descriptorpb.MethodDescriptorProto {
		m := &descriptorpb.MethodDescriptorProto{Name: new(name), InputType: new(in), OutputType: new(out)}
		if rule != nil {
			m.Options = httpOption(rule)
		}
		return m
	}
	watch := method("Watch", &annotations.HttpRule{Pattern: &annotations.HttpRule_Get{Get: "/rt/v1/things/{service}:watch"}})
	watch.ServerStreaming = new(true)
	upload := method("Upload", &annotations.HttpRule{Pattern: &annotations.HttpRule_Post{Post: "/rt/v1/upload"}, Body: "*"})
	upload.ClientStreaming = new(true)
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    new("grpcmux/resttest/v1/thing.proto"),
		Package: new("grpcmux.resttest.v1"),
		Syntax:  new("proto3"),
		Dependency: []string{
			pbhealth.File_grpc_health_v1_health_proto.Path(),
			annotations.File_google_api_annotations_proto.Path(),
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: new("ThingService"),
			Method: []*descriptorpb.MethodDescriptorProto{
				method("Get", &annotations.HttpRule{
					Pattern: &annotations.HttpRule_Get{Get: "/rt/v1/things/{service}"},
					AdditionalBindings: []*annotations.HttpRule{
						{Pattern: &annotations.HttpRule_Get{Get: "/rt/v1/{service=shelves/*}/thing"}},
					},
				}),
				method("Status", &annotations.HttpRule{
					Pattern:      &annotations.HttpRule_Get{Get: "/rt/v1/things/{service}/status"},
					ResponseBody: "status",
				}),
				method("Rename", &annotations.HttpRule{Pattern: &annotations.HttpRule_Post{Post: "/rt/v1/things"}, Body: "service"}),
				method("Act", &annotations.HttpRule{Pattern: &annotations.HttpRule_Post{Post: "/rt/v1/things/{service}:act"}, Body: "*"}),
				watch,
				upload,
				method("Plain", nil),
			},
		}},
	}
	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		panic(err)
	}
	if err := protoregistry.GlobalFiles.RegisterFile(fd); err != nil {
		panic(err)
	}
}

// thingService answers every unary method with the request's "service"
// echoed in a response header, and drives Watch by the same value:
//
//	"fail-first"  NOT_FOUND with an ErrorInfo before any message
//	"fail-later"  one message, then ABORTED
//	"forever"     messages until the client goes away
//	anything else two messages, then a clean end
type thingService struct {
	watchEnded atomic.Int32
}

type thingServer interface {
	unary(ctx context.Context, in *pbhealth.HealthCheckRequest) (*pbhealth.HealthCheckResponse, error)
	watch(in *pbhealth.HealthCheckRequest, stream grpc.ServerStream) error
}

func (s *thingService) unary(ctx context.Context, in *pbhealth.HealthCheckRequest) (*pbhealth.HealthCheckResponse, error) {
	if in.GetService() == "missing" {
		st, _ := status.New(codes.NotFound, "no such thing").WithDetails(&errdetails.ErrorInfo{Reason: "THING_MISSING", Domain: "test"})
		return nil, st.Err()
	}
	_ = grpc.SetHeader(ctx, metadata.Pairs("x-thing", in.GetService()))
	return &pbhealth.HealthCheckResponse{Status: pbhealth.HealthCheckResponse_SERVING}, nil
}

func (s *thingService) watch(in *pbhealth.HealthCheckRequest, stream grpc.ServerStream) error {
	defer s.watchEnded.Add(1)
	_ = stream.SetHeader(metadata.Pairs("x-thing", in.GetService()))
	msg := &pbhealth.HealthCheckResponse{Status: pbhealth.HealthCheckResponse_SERVING}
	switch in.GetService() {
	case "fail-first":
		st, _ := status.New(codes.NotFound, "no such thing").WithDetails(&errdetails.ErrorInfo{Reason: "THING_MISSING", Domain: "test"})
		return st.Err()
	case "fail-later":
		if err := stream.SendMsg(msg); err != nil {
			return err
		}
		return status.Error(codes.Aborted, "gave up")
	case "forever":
		for {
			if err := stream.SendMsg(msg); err != nil {
				return err
			}
			select {
			case <-stream.Context().Done():
				return stream.Context().Err()
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	for range 2 {
		if err := stream.SendMsg(msg); err != nil {
			return err
		}
	}
	return nil
}

func thingUnary(method string) grpc.MethodDesc {
	return grpc.MethodDesc{
		MethodName: method,
		Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			in := &pbhealth.HealthCheckRequest{}
			if err := dec(in); err != nil {
				return nil, err
			}
			if interceptor == nil {
				return srv.(thingServer).unary(ctx, in)
			}
			info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/" + restTestService + "/" + method}
			return interceptor(ctx, in, info, func(ctx context.Context, req any) (any, error) {
				return srv.(thingServer).unary(ctx, req.(*pbhealth.HealthCheckRequest))
			})
		},
	}
}

var thingServiceDesc = grpc.ServiceDesc{
	ServiceName: restTestService,
	HandlerType: (*thingServer)(nil),
	Methods:     []grpc.MethodDesc{thingUnary("Get"), thingUnary("Status"), thingUnary("Rename"), thingUnary("Act"), thingUnary("Plain")},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Watch",
			ServerStreams: true,
			Handler: func(srv any, stream grpc.ServerStream) error {
				in := &pbhealth.HealthCheckRequest{}
				if err := stream.RecvMsg(in); err != nil {
					return err
				}
				return srv.(thingServer).watch(in, stream)
			},
		},
		{StreamName: "Upload", ClientStreams: true, Handler: func(any, grpc.ServerStream) error { return nil }},
	},
	Metadata: "grpcmux/resttest/v1/thing.proto",
}

func restServer(t *testing.T, opts ...Option) (*Server, *thingService) {
	t.Helper()
	s := newTestServer(t, append([]Option{WithREST()}, opts...)...)
	svc := &thingService{}
	s.RegisterService(&thingServiceDesc, svc)
	runTestServer(t, s)
	return s, svc
}

// restResult is what the tests look at in a response.
type restResult struct {
	StatusCode int
	Header     http.Header
}

func restDo(t *testing.T, s *Server, method, path, body string) (restResult, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), method, "http://"+s.opts.httpAddr+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return restResult{StatusCode: resp.StatusCode, Header: resp.Header}, string(b)
}

func TestRESTBindings(t *testing.T) {
	s, _ := restServer(t)
	tests := []struct {
		name, method, path, body string
		status                   int
		wantBody, wantThing      string
	}{
		{"path variable", http.MethodGet, "/rt/v1/things/a", "", 200, `{"status":"SERVING"}`, "a"},
		{"escaped path variable", http.MethodGet, "/rt/v1/things/a%20b", "", 200, `{"status":"SERVING"}`, "a b"},
		{"additional binding with a multi-segment variable", http.MethodGet, "/rt/v1/shelves/7/thing", "", 200, `{"status":"SERVING"}`, "shelves/7"},
		{"response_body", http.MethodGet, "/rt/v1/things/a/status", "", 200, `"SERVING"`, "a"},
		{"scalar body field", http.MethodPost, "/rt/v1/things", `"renamed"`, 200, `{"status":"SERVING"}`, "renamed"},
		{"custom verb, body *, path wins over body", http.MethodPost, "/rt/v1/things/a:act", `{"service":"from-body"}`, 200, `{"status":"SERVING"}`, "a"},
		{"query cannot overwrite a path variable", http.MethodGet, "/rt/v1/things/a?service=b", "", 200, `{"status":"SERVING"}`, "a"},
		{"error status and body", http.MethodGet, "/rt/v1/things/missing", "", 404, `"error":"not_found"`, ""},
		{"method without a rule is not a REST route", http.MethodGet, "/rt/v1/plain", "", 404, `"error":"not_found"`, ""},
		{"client streaming has no REST form", http.MethodPost, "/rt/v1/upload", `{}`, 404, `"error":"not_found"`, ""},
		{"known path, other method", http.MethodDelete, "/rt/v1/things/a", "", 501, `"error":"unimplemented"`, ""},
		{"malformed body", http.MethodPost, "/rt/v1/things/a:act", `{`, 400, `"error":"invalid_argument"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := restDo(t, s, tt.method, tt.path, tt.body)
			if resp.StatusCode != tt.status || !strings.Contains(body, tt.wantBody) {
				t.Fatalf("%s %s: %d %s, want %d containing %s", tt.method, tt.path, resp.StatusCode, body, tt.status, tt.wantBody)
			}
			if got := resp.Header.Get("X-Thing"); got != tt.wantThing {
				t.Fatalf("handler saw service %q, want %q", got, tt.wantThing)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type %q", ct)
			}
		})
	}
	// The error carries its details with their type.
	_, body := restDo(t, s, http.MethodGet, "/rt/v1/things/missing", "")
	if !strings.Contains(body, `"@type":"type.googleapis.com/google.rpc.ErrorInfo"`) || !strings.Contains(body, `"reason":"THING_MISSING"`) {
		t.Fatalf("error body: %s", body)
	}
}

// Without WithREST nothing changes: no REST routes, and an unknown path
// keeps its historical HTTP 200 with a not_found body.
func TestRESTIsOptIn(t *testing.T) {
	s := newTestServer(t)
	s.RegisterService(&thingServiceDesc, &thingService{})
	runTestServer(t, s)
	resp, body := restDo(t, s, http.MethodGet, "/rt/v1/things/a", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"error":"not_found"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

// REST calls run the unary interceptor chain of the HTTP bridges, and the
// JSON naming option applies to bodies and errors.
func TestRESTInterceptorsAndCamelCase(t *testing.T) {
	var seen []string
	s, _ := restServer(t, WithUseCamelCase(), WithGRPCUnaryServerInterceptor(
		func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			seen = append(seen, info.FullMethod+" "+req.(*pbhealth.HealthCheckRequest).GetService())
			if m, _ := grpc.Method(ctx); m != info.FullMethod {
				t.Errorf("grpc.Method = %q", m)
			}
			return handler(ctx, req)
		}))
	if resp, body := restDo(t, s, http.MethodGet, "/rt/v1/things/a", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if want := []string{"/" + restTestService + "/Get a"}; !slices.Equal(seen, want) {
		t.Fatalf("interceptor saw %v, want %v", seen, want)
	}
	_, body := restDo(t, s, http.MethodGet, "/rt/v1/things/missing", "")
	if !strings.Contains(body, `"errorDescription":"no such thing"`) || !strings.Contains(body, `"errorCode":5`) {
		t.Fatalf("camelCase error body: %s", body)
	}
}

type streamLine struct {
	Result *struct {
		Status string `json:"status"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func readLines(t *testing.T, body string) []streamLine {
	t.Helper()
	var lines []streamLine
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		var l streamLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		lines = append(lines, l)
	}
	return lines
}

func TestRESTServerStream(t *testing.T) {
	var opened atomic.Int32
	s, svc := restServer(t, WithGRPCStreamServerInterceptors(
		func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if info.FullMethod == "/"+restTestService+"/Watch" && info.IsServerStream {
				opened.Add(1)
			}
			return handler(srv, ss)
		}))

	// Two messages and a clean end; headers set before the first message are
	// HTTP response headers.
	resp, body := restDo(t, s, http.MethodGet, "/rt/v1/things/a:watch", "")
	lines := readLines(t, body)
	if resp.StatusCode != http.StatusOK || len(lines) != 2 || lines[0].Result == nil || lines[0].Result.Status != "SERVING" || lines[1].Error != nil {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Thing") != "a" {
		t.Fatalf("stream header not forwarded: %v", resp.Header)
	}

	// An error before the first message is an ordinary error response.
	resp, body = restDo(t, s, http.MethodGet, "/rt/v1/things/fail-first:watch", "")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, `"error":"not_found"`) ||
		!strings.Contains(body, `"reason":"THING_MISSING"`) {
		t.Fatalf("early error: %d %s", resp.StatusCode, body)
	}

	// An error after the first message ends the stream with an error line.
	resp, body = restDo(t, s, http.MethodGet, "/rt/v1/things/fail-later:watch", "")
	lines = readLines(t, body)
	if resp.StatusCode != http.StatusOK || len(lines) != 2 || lines[0].Result == nil ||
		lines[1].Error == nil || lines[1].Error.Code != int(codes.Aborted) || lines[1].Error.Message != "gave up" {
		t.Fatalf("late error: %d %s", resp.StatusCode, body)
	}

	if opened.Load() != 3 {
		t.Fatalf("stream interceptor ran %d times, want 3", opened.Load())
	}

	// A client that goes away ends the handler.
	ended := svc.watchEnded.Load()
	ctx, cancel := context.WithCancel(t.Context())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+s.opts.httpAddr+"/rt/v1/things/forever:watch", nil)
	live, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(live.Body).ReadString('\n'); err != nil {
		t.Fatalf("first line of the endless stream: %v", err)
	}
	cancel()
	_ = live.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for svc.watchEnded.Load() == ended {
		if time.Now().After(deadline) {
			t.Fatal("the stream handler kept running after the client disconnected")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTemplateVariables(t *testing.T) {
	tests := map[string][]string{
		"/v1/users":                              nil,
		"/v1/users/{user_id}":                    {"user_id"},
		"/v1/{parent=shelves/*}/books/{book.id}": {"parent", "book.id"},
		"/v1/users/{user_id}:export":             {"user_id"},
		"/v1/{name=**}":                          {"name"},
	}
	for template, want := range tests {
		if got := templateVariables(template); !slices.Equal(got, want) {
			t.Errorf("templateVariables(%q) = %v, want %v", template, got, want)
		}
	}
}

// A rule that cannot be served is a programming error reported at
// registration, not a 500 at request time.
func TestRESTInvalidRulePanics(t *testing.T) {
	rt := restRoute{}
	md := mustMethodDescriptor(t, restTestService, "Get")
	if err := rt.bind(&annotations.HttpRule{Pattern: &annotations.HttpRule_Post{Post: "/x"}, Body: "nope"}, md); err == nil {
		t.Error("unknown body field: expected an error")
	}
	if err := rt.bind(&annotations.HttpRule{Pattern: &annotations.HttpRule_Get{Get: "/x"}, ResponseBody: "nope"}, md); err == nil {
		t.Error("unknown response_body field: expected an error")
	}
	if err := rt.bind(&annotations.HttpRule{}, md); err == nil {
		t.Error("rule without a pattern: expected an error")
	}
}

func mustMethodDescriptor(t *testing.T, service, method string) protoreflect.MethodDescriptor {
	t.Helper()
	md, ok := methodDescriptor(service, method).(protoreflect.MethodDescriptor)
	if !ok {
		t.Fatalf("no descriptor for %s.%s", service, method)
	}
	return md
}

// Typed error details are opt-in for a server without REST: its error bodies
// keep the published form unless WithTypedErrorDetails (or the config key)
// says otherwise.
func TestTypedErrorDetailsOption(t *testing.T) {
	fail := func(s *Server) {
		s.HandleFunc(http.MethodGet, "/fail", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
			st, _ := status.New(codes.NotFound, "no such thing").WithDetails(&errdetails.ErrorInfo{Reason: "THING_MISSING", Domain: "test"})
			_, outbound := runtime.MarshalerForRequest(s.ServeMux(), r)
			runtime.HTTPError(r.Context(), s.ServeMux(), outbound, w, r, st.Err())
		})
	}
	tests := []struct {
		name  string
		opts  []Option
		typed bool
	}{
		{"default", nil, false},
		{"WithTypedErrorDetails", []Option{WithTypedErrorDetails()}, true},
		{"config key", []Option{WithConfig(&Config{TypedErrorDetails: true})}, true},
		{"implied by WithREST", []Option{WithREST()}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, tt.opts...)
			fail(s)
			runTestServer(t, s)
			_, body := restDo(t, s, http.MethodGet, "/fail", "")
			if !strings.Contains(body, `"reason":"THING_MISSING"`) {
				t.Fatalf("body %s", body)
			}
			if got := strings.Contains(body, `"@type"`); got != tt.typed {
				t.Fatalf("typed = %v, want %v: %s", got, tt.typed, body)
			}
		})
	}
}

// A REST route must not take a path away from other routes: another HTTP
// method on the same path still reaches a HandleFunc route or a catch-all on
// the gateway mux, and is "method not allowed" only when nothing routes it.
func TestRESTDoesNotShadowOtherMethods(t *testing.T) {
	for _, rest := range []bool{false, true} {
		var opts []Option
		if rest {
			opts = append(opts, WithREST())
		}
		s := newTestServer(t, opts...)
		s.RegisterService(&thingServiceDesc, &thingService{})
		s.HandleFunc(http.MethodPut, "/rt/v1/things/{id}", func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
			_, _ = io.WriteString(w, "custom-put")
		})
		s.HandleFunc(http.MethodDelete, "/{path=**}", func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
			_, _ = io.WriteString(w, "catch-all-delete")
		})
		runTestServer(t, s)
		for _, tt := range []struct{ method, path, want string }{
			{http.MethodPut, "/rt/v1/things/a", "custom-put"},
			{http.MethodDelete, "/rt/v1/things/a", "catch-all-delete"},
			{http.MethodDelete, "/rt/v1/things/a:act", "catch-all-delete"},
		} {
			resp, body := restDo(t, s, tt.method, tt.path, "")
			if resp.StatusCode != http.StatusOK || body != tt.want {
				t.Errorf("rest=%v %s %s: %d %q, want 200 %q", rest, tt.method, tt.path, resp.StatusCode, body, tt.want)
			}
		}
		if !rest {
			continue
		}
		// Nothing routes PATCH on that path: method not allowed, as before.
		if resp, body := restDo(t, s, http.MethodPatch, "/rt/v1/things/a", ""); resp.StatusCode != http.StatusNotImplemented ||
			!strings.Contains(body, `"error":"unimplemented"`) {
			t.Errorf("PATCH on a GET/PUT/DELETE path: %d %s", resp.StatusCode, body)
		}
		// And the REST route itself still works.
		if resp, _ := restDo(t, s, http.MethodGet, "/rt/v1/things/a", ""); resp.StatusCode != http.StatusOK || resp.Header.Get("X-Thing") != "a" {
			t.Errorf("GET REST route: %d", resp.StatusCode)
		}
	}
}

// With REST, HEAD is routed like the GET it mirrors: 404 for an unrouted
// path, the GET's status (without a body) for a routed one, and behind
// authentication.
func TestRESTHead(t *testing.T) {
	deny := func(ctx context.Context) (context.Context, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if len(md.Get("authorization")) == 0 {
			return ctx, status.Error(codes.Unauthenticated, "no token")
		}
		return ctx, nil
	}
	s, _ := restServer(t, WithAuthFunc(deny), WithNoAuthMethods("/"+restTestService+"/Get"))
	head := func(path string) (int, string) {
		resp, body := restDo(t, s, http.MethodHead, path, "")
		return resp.StatusCode, body
	}
	// Authentication comes before routing, for HEAD as for GET.
	if code, _ := head("/no/such/route"); code != http.StatusUnauthorized {
		t.Errorf("HEAD unrouted without credentials: %d, want 401", code)
	}
	open, _ := restServer(t)
	if resp, body := restDo(t, open, http.MethodHead, "/no/such/route", ""); resp.StatusCode != http.StatusNotFound || body != "" {
		t.Errorf("HEAD unrouted: %d %q, want 404 and no body", resp.StatusCode, body)
	}
	if resp, _ := restDo(t, open, http.MethodGet, "/no/such/route", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET unrouted: %d, want 404", resp.StatusCode)
	}
	if code, body := head("/rt/v1/things/a"); code != http.StatusOK || body != "" {
		t.Errorf("HEAD on an exempt REST GET route: %d %q, want 200 and no body", code, body)
	}
	if code, _ := head("/rt/v1/things/a/status"); code != http.StatusUnauthorized {
		t.Errorf("HEAD on a protected REST GET route: %d, want 401", code)
	}
	if code, _ := head("/healthz"); code != http.StatusOK {
		t.Errorf("HEAD /healthz: %d, want 200", code)
	}
}

// A handler registered with Handle is served ahead of the REST routes. A path
// it owns must not be treated as the REST call a template would also match:
// no auth exemption and no rate-limit rule of that RPC.
func TestRESTResolutionAgreesWithDispatch(t *testing.T) {
	deny := func(ctx context.Context) (context.Context, error) {
		return ctx, status.Error(codes.Unauthenticated, "no token")
	}
	s := newTestServer(t, WithREST(), WithAuthFunc(deny), WithNoAuthMethods("/"+restTestService+"/Get"))
	s.RegisterService(&thingServiceDesc, &thingService{})
	var reached atomic.Bool
	s.Handle("/rt/v1/things/secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		_, _ = io.WriteString(w, "secret")
	}))
	runTestServer(t, s)

	// The exempt REST route answers without credentials ...
	if resp, body := restDo(t, s, http.MethodGet, "/rt/v1/things/a", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("exempt REST route: %d %s", resp.StatusCode, body)
	}
	// ... the custom handler on a path the template also matches does not.
	resp, body := restDo(t, s, http.MethodGet, "/rt/v1/things/secret", "")
	if resp.StatusCode != http.StatusUnauthorized || reached.Load() {
		t.Fatalf("custom handler under a REST template: %d %s (handler reached: %v), want 401", resp.StatusCode, body, reached.Load())
	}
}
