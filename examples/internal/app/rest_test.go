package app_test

import (
	"bufio"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	userv1 "github.com/ti/common-go/examples/gen/go/example/user/v1"
	"github.com/ti/common-go/grpcmux"
)

// REST in this file is what grpcmux.WithREST serves from the google.api.http
// annotations of user.proto: there is no generated gateway code and no route
// is written by hand.

type restUser struct {
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Age        *int32 `json:"age"`
	CreateTime string `json:"create_time"`
	UpdateTime string `json:"update_time"`
}

// restError is the REST error body.
type restError struct {
	Error            string `json:"error"`
	ErrorCode        int    `json:"error_code"`
	ErrorDescription string `json:"error_description"`
	Details          []struct {
		Type       string            `json:"@type"`
		Reason     string            `json:"reason"`
		Domain     string            `json:"domain"`
		Metadata   map[string]string `json:"metadata"`
		RetryDelay string            `json:"retry_delay"`
		Violations []struct {
			RuleID string `json:"rule_id"`
		} `json:"violations"`
	} `json:"details"`
}

// restResponse is what the tests look at besides the body.
type restResponse struct {
	StatusCode int
	Header     http.Header
}

// call sends a REST request and decodes the JSON response into out.
func (e *env) call(t *testing.T, method, path, body string, header http.Header, out any) restResponse {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, e.httpBase+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	maps.Copy(req.Header, header)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: status %d, body is not JSON: %v", method, path, resp.StatusCode, err)
		}
	}
	return restResponse{StatusCode: resp.StatusCode, Header: resp.Header}
}

func (e *env) mustFail(t *testing.T, method, path, body string, wantStatus int, wantError string) restError {
	t.Helper()
	var re restError
	resp := e.call(t, method, path, body, nil, &re)
	if resp.StatusCode != wantStatus || re.Error != wantError {
		t.Fatalf("%s %s: status %d error %q (%s), want %d %q",
			method, path, resp.StatusCode, re.Error, re.ErrorDescription, wantStatus, wantError)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("%s %s: error Content-Type %q", method, path, ct)
	}
	return re
}

// Create, read, update (PATCH with and without update_mask) and delete, all
// over REST.
func TestRESTCRUD(t *testing.T) {
	e := start(t)

	// POST /v1/users, body: "user" (the body is the User, not the request).
	var created struct{ User restUser }
	resp := e.call(t, http.MethodPost, "/v1/users", `{"name":"Ada","email":"ada@example.com","age":36}`, nil, &created)
	if resp.StatusCode != http.StatusOK || created.User.UserID == "" || created.User.Name != "Ada" ||
		created.User.Age == nil || *created.User.Age != 36 {
		t.Fatalf("create: %d %+v", resp.StatusCode, created)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type %q", ct)
	}
	// google.protobuf.Timestamp is an RFC 3339 string.
	if !strings.HasSuffix(created.User.CreateTime, "Z") || !strings.Contains(created.User.CreateTime, "T") {
		t.Fatalf("create_time %q is not RFC 3339", created.User.CreateTime)
	}
	id := created.User.UserID

	// GET /v1/users/{user_id}: path variable.
	var got struct{ User restUser }
	if resp := e.call(t, http.MethodGet, "/v1/users/"+id, "", nil, &got); resp.StatusCode != http.StatusOK ||
		got.User.Email != "ada@example.com" {
		t.Fatalf("get: %d %+v", resp.StatusCode, got)
	}

	// PATCH /v1/users/{user.user_id} without update_mask: the mask is the
	// set of fields present in the body, so email and age are untouched.
	var patched struct{ User restUser }
	resp = e.call(t, http.MethodPatch, "/v1/users/"+id, `{"name":"Ada Lovelace"}`, nil, &patched)
	if resp.StatusCode != http.StatusOK || patched.User.Name != "Ada Lovelace" ||
		patched.User.Email != "ada@example.com" || patched.User.Age == nil {
		t.Fatalf("patch without mask: %d %+v", resp.StatusCode, patched)
	}

	// PATCH with an explicit ?update_mask=: only the named field is written,
	// whatever else the body carries.
	resp = e.call(t, http.MethodPatch, "/v1/users/"+id+"?update_mask=email",
		`{"name":"ignored","email":"lovelace@example.com"}`, nil, &patched)
	if resp.StatusCode != http.StatusOK || patched.User.Name != "Ada Lovelace" ||
		patched.User.Email != "lovelace@example.com" {
		t.Fatalf("patch with mask: %d %+v", resp.StatusCode, patched)
	}
	// camelCase names are accepted on input too (updateMask, JSON names).
	resp = e.call(t, http.MethodPatch, "/v1/users/"+id+"?updateMask=name", `{"name":"Countess"}`, nil, &patched)
	if resp.StatusCode != http.StatusOK || patched.User.Name != "Countess" {
		t.Fatalf("patch with updateMask: %d %+v", resp.StatusCode, patched)
	}

	// The same state is visible on gRPC: one implementation, one store.
	viaGRPC, err := e.grpc.GetUser(t.Context(), &userv1.GetUserRequest{UserId: id})
	if err != nil || viaGRPC.GetUser().GetName() != "Countess" || viaGRPC.GetUser().GetEmail() != "lovelace@example.com" {
		t.Fatalf("gRPC view: %v %v", viaGRPC, err)
	}

	// DELETE /v1/users/{user_id}: an empty response message is {}.
	var empty map[string]any
	if resp := e.call(t, http.MethodDelete, "/v1/users/"+id, "", nil, &empty); resp.StatusCode != http.StatusOK || len(empty) != 0 {
		t.Fatalf("delete: %d %v", resp.StatusCode, empty)
	}
	e.mustFail(t, http.MethodGet, "/v1/users/"+id, "", http.StatusNotFound, "not_found")
}

// GET /v1/users pages with query parameters.
func TestRESTListPaging(t *testing.T) {
	e := start(t)
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		if resp := e.call(t, http.MethodPost, "/v1/users", `{"name":"`+n+`","email":"`+n+`@example.com"}`, nil, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("create %s: %d", n, resp.StatusCode)
		}
	}
	type page struct {
		Users         []restUser `json:"users"`
		NextPageToken string     `json:"next_page_token"`
	}
	var names []string
	token := ""
	for i := range 4 {
		q := url.Values{"page_size": {"2"}}
		if i == 1 {
			q = url.Values{"pageSize": {"2"}} // the JSON name works as well
		}
		if token != "" {
			q.Set("page_token", token)
		}
		// Unknown query parameters are ignored.
		q.Set("utm_source", "test")
		var p page
		if resp := e.call(t, http.MethodGet, "/v1/users?"+q.Encode(), "", nil, &p); resp.StatusCode != http.StatusOK {
			t.Fatalf("page %d: %d", i, resp.StatusCode)
		}
		if len(p.Users) > 2 {
			t.Fatalf("page %d has %d users, page_size is 2", i, len(p.Users))
		}
		for _, u := range p.Users {
			names = append(names, u.Name)
		}
		if token = p.NextPageToken; token == "" {
			break
		}
	}
	if strings.Join(names, "") != "edcba" {
		t.Fatalf("names = %v, want e d c b a", names)
	}
	// A query value of the wrong type is rejected, not ignored.
	e.mustFail(t, http.MethodGet, "/v1/users?page_size=many", "", http.StatusBadRequest, "invalid_argument")
}

// gRPC codes map to HTTP statuses, and the error body carries the
// google.rpc.ErrorInfo reason and domain exactly like the Connect error does.
func TestRESTErrors(t *testing.T) {
	e := start(t)
	if resp := e.call(t, http.MethodPost, "/v1/users", `{"name":"Ada","email":"ada@example.com"}`, nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	// ALREADY_EXISTS -> 409, with the reason.
	re := e.mustFail(t, http.MethodPost, "/v1/users", `{"name":"Ada 2","email":"ada@example.com"}`,
		http.StatusConflict, "already_exists")
	if re.ErrorCode != int(codes.AlreadyExists) || len(re.Details) != 1 ||
		re.Details[0].Type != "type.googleapis.com/google.rpc.ErrorInfo" ||
		re.Details[0].Reason != "EMAIL_ALREADY_IN_USE" || re.Details[0].Domain != "example.user.v1" ||
		re.Details[0].Metadata["email"] != "ada@example.com" {
		t.Fatalf("duplicate: %+v", re)
	}

	// NOT_FOUND -> 404, with the reason.
	re = e.mustFail(t, http.MethodGet, "/v1/users/42", "", http.StatusNotFound, "not_found")
	if len(re.Details) != 1 || re.Details[0].Reason != "USER_NOT_FOUND" {
		t.Fatalf("not found: %+v", re)
	}

	// Validation (buf.validate, enforced by the interceptor chain): 400 with
	// the violations.
	re = e.mustFail(t, http.MethodPost, "/v1/users", `{"name":"Bob","email":"not-an-email"}`,
		http.StatusBadRequest, "invalid_argument")
	if len(re.Details) != 1 || re.Details[0].Type != "type.googleapis.com/buf.validate.Violations" ||
		len(re.Details[0].Violations) != 1 || re.Details[0].Violations[0].RuleID != "string.email" {
		t.Fatalf("validation: %+v", re)
	}
	// A required body that is missing fails validation too.
	e.mustFail(t, http.MethodPost, "/v1/users", "", http.StatusBadRequest, "invalid_argument")
	// So does a rule on a query parameter.
	e.mustFail(t, http.MethodGet, "/v1/users?page_size=1000", "", http.StatusBadRequest, "invalid_argument")

	// Malformed JSON and unknown routes.
	e.mustFail(t, http.MethodPost, "/v1/users", `{"name":`, http.StatusBadRequest, "invalid_argument")
	e.mustFail(t, http.MethodGet, "/v1/nothing-here", "", http.StatusNotFound, "not_found")
	// A REST path with a method it is not bound to: the answer the gateway
	// mux gives for its own routes.
	e.mustFail(t, http.MethodPut, "/v1/users", `{}`, http.StatusNotImplemented, "unimplemented")
}

// Authentication applies to REST routes like to every other route, and an
// exemption is written once, by RPC name, for every protocol.
func TestRESTAuth(t *testing.T) {
	authFunc := func(ctx context.Context) (context.Context, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if v := md.Get("authorization"); len(v) == 0 || v[0] != "Bearer secret" {
			return ctx, status.Error(codes.Unauthenticated, "missing or invalid token")
		}
		return ctx, nil
	}
	e := startWith(t, nil,
		grpcmux.WithAuthFunc(authFunc),
		grpcmux.WithNoAuthMethods("/example.user.v1.UserService/ListUsers"),
	)
	token := http.Header{"Authorization": {"Bearer secret"}}

	// Exempt by RPC name: GET /v1/users is ListUsers.
	if resp := e.call(t, http.MethodGet, "/v1/users", "", nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("exempt list without token: %d", resp.StatusCode)
	}
	// Not exempt: POST on the same path is CreateUser, GET /v1/users/{id} is
	// GetUser, the stream is ExportUsers.
	e.mustFail(t, http.MethodPost, "/v1/users", `{"name":"Ada","email":"ada@example.com"}`,
		http.StatusUnauthorized, "unauthenticated")
	e.mustFail(t, http.MethodGet, "/v1/users/1", "", http.StatusUnauthorized, "unauthenticated")
	e.mustFail(t, http.MethodGet, "/v1/users:export", "", http.StatusUnauthorized, "unauthenticated")

	var created struct{ User restUser }
	if resp := e.call(t, http.MethodPost, "/v1/users", `{"name":"Ada","email":"ada@example.com"}`, token, &created); resp.StatusCode != http.StatusOK {
		t.Fatalf("create with token: %d", resp.StatusCode)
	}
	if resp := e.call(t, http.MethodGet, "/v1/users/"+created.User.UserID, "", token, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("get with token: %d", resp.StatusCode)
	}
}

// A server-streaming method over REST is newline-delimited JSON, one
// {"result": ...} object per message, flushed as it is sent.
func TestRESTServerStreaming(t *testing.T) {
	e := start(t)
	for _, n := range []string{"a", "b", "c"} {
		if resp := e.call(t, http.MethodPost, "/v1/users", `{"name":"`+n+`","email":"`+n+`@example.com"}`, nil, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("create %s: %d", n, resp.StatusCode)
		}
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, e.httpBase+"/v1/users:export", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d Content-Type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var names []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var line struct {
			Result struct{ User restUser } `json:"result"`
			Error  json.RawMessage         `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		if line.Error != nil {
			t.Fatalf("stream error: %s", line.Error)
		}
		names = append(names, line.Result.User.Name)
	}
	if len(names) != 3 {
		t.Fatalf("stream delivered %v, want 3 users", names)
	}
}

// The committed OpenAPI document (gen/openapi/api.swagger.json, generated by
// `buf generate` from the same annotations) describes exactly the routes the
// server serves: every documented operation is routed, with snake_case
// parameter names like the server's JSON.
func TestOpenAPIMatchesServedRoutes(t *testing.T) {
	raw, err := os.ReadFile("../../gen/openapi/api.swagger.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Swagger string                                `json:"swagger"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Swagger != "2.0" || len(doc.Paths) == 0 {
		t.Fatalf("unexpected document: swagger %q, %d paths", doc.Swagger, len(doc.Paths))
	}
	for _, want := range []string{"/v1/users", "/v1/users/{user_id}", "/v1/users/{user.user_id}", "/v1/users:export"} {
		if _, ok := doc.Paths[want]; !ok {
			t.Errorf("OpenAPI document has no path %s", want)
		}
	}
	e := start(t)
	variable := regexp.MustCompile(`\{[^}]+\}`)
	for path, operations := range doc.Paths {
		for method := range operations {
			var re restError
			target := variable.ReplaceAllString(path, "0")
			resp := e.call(t, strings.ToUpper(method), target, "", nil, nil)
			if resp.StatusCode == http.StatusNotFound {
				// Routed-but-missing resources say so; an unrouted path says
				// "API NOT FOUND".
				e.call(t, strings.ToUpper(method), target, "", nil, &re)
			}
			if resp.StatusCode == http.StatusNotImplemented || re.ErrorDescription == "API NOT FOUND" {
				t.Errorf("%s %s is documented but not served (status %d)", strings.ToUpper(method), path, resp.StatusCode)
			}
		}
	}
}
