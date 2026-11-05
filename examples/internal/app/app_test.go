package app_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/ti/common-go/dependencies/database"
	userv1 "github.com/ti/common-go/examples/gen/go/example/user/v1"
	"github.com/ti/common-go/examples/gen/go/example/user/v1/userv1connect"
	"github.com/ti/common-go/examples/internal/app"
	"github.com/ti/common-go/graceful"
	"github.com/ti/common-go/grpcmux"
)

type env struct {
	httpBase string
	grpc     userv1.UserServiceClient
	connect  userv1connect.UserServiceClient
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func waitHTTP(t *testing.T, url string) {
	t.Helper()
	for range 100 {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never became ready", url)
}

// start runs the real app (all protocols) on free ports against an
// in-memory database.
func start(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/"+strings.ReplaceAll(t.Name(), "/", "_"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &app.Config{Apis: grpcmux.Config{HTTPAddr: freeAddr(t), GrpcAddr: freeAddr(t)}}
	cfg.Dependencies.DB = db
	gs, err := app.New(ctx, cfg, grpcmux.WithMetricsAddr(""))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { gs.Start(); close(done) }()
	t.Cleanup(func() {
		graceful.Close()
		<-done
	})
	base := "http://" + cfg.Apis.HTTPAddr
	waitHTTP(t, base+"/readyz")

	grpcClient, err := grpcmux.NewClient(ctx, "dns://"+cfg.Apis.GrpcAddr+"?metrics=false", userv1.NewUserServiceClient)
	if err != nil {
		t.Fatal(err)
	}
	connectClient := userv1connect.NewUserServiceClient(connect.NewClient(
		connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithProtoJSON())))
	return &env{httpBase: base, grpc: grpcClient, connect: connectClient}
}

func (e *env) rest(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), method, e.httpBase+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func newUser(name, email string) *userv1.CreateUserRequest {
	return &userv1.CreateUserRequest{User: &userv1.User{Name: name, Email: email}}
}

// One implementation answers gRPC, Connect and REST identically.
func TestCRUDAcrossProtocols(t *testing.T) {
	e := start(t)
	ctx := t.Context()

	created, err := e.grpc.CreateUser(ctx, newUser("Ada", "ada@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetUser().GetUserId()

	got, err := e.connect.GetUser(ctx, &userv1.GetUserRequest{UserId: id})
	if err != nil || got.GetUser().GetEmail() != "ada@example.com" {
		t.Fatalf("Connect GetUser: %v %v", got, err)
	}

	code, body := e.rest(t, http.MethodGet, "/v1/users/"+id, "")
	if code != http.StatusOK || !strings.Contains(body, `"user_id":"`+id+`"`) {
		t.Fatalf("REST GetUser: %d %s", code, body)
	}

	upd, err := e.grpc.UpdateUser(ctx, &userv1.UpdateUserRequest{
		User:       &userv1.User{UserId: id, Name: "Ada Lovelace"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	if err != nil || upd.GetUser().GetName() != "Ada Lovelace" || upd.GetUser().GetEmail() != "ada@example.com" {
		t.Fatalf("UpdateUser with mask: %v %v", upd, err)
	}

	if _, err := e.connect.DeleteUser(ctx, &userv1.DeleteUserRequest{UserId: id}); err != nil {
		t.Fatal(err)
	}
	if code, body := e.rest(t, http.MethodGet, "/v1/users/"+id, ""); code != http.StatusNotFound {
		t.Fatalf("REST GetUser after delete: %d %s", code, body)
	}
}

// buf.validate rules are enforced before the method runs, on every protocol.
func TestValidation(t *testing.T) {
	e := start(t)
	ctx := t.Context()
	bad := newUser("Bob", "not-an-email")

	if _, err := e.grpc.CreateUser(ctx, bad); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("gRPC: %v", err)
	}
	if _, err := e.connect.CreateUser(ctx, bad); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("Connect: %v", err)
	}
	// Required on create (message-level CEL rule), optional on update.
	if _, err := e.grpc.CreateUser(ctx, newUser("Bob", "")); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("create without email: %v", err)
	}
	code, body := e.rest(t, http.MethodPost, "/v1/users", `{"name":"Bob","email":"not-an-email"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "string.email") {
		t.Fatalf("REST: %d %s", code, body)
	}
}

// Domain errors carry a canonical code plus a google.rpc.ErrorInfo reason.
func TestErrorReason(t *testing.T) {
	e := start(t)
	ctx := t.Context()
	if _, err := e.grpc.CreateUser(ctx, newUser("Ada", "ada@example.com")); err != nil {
		t.Fatal(err)
	}
	_, err := e.connect.CreateUser(ctx, newUser("Ada 2", "ada@example.com"))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeAlreadyExists {
		t.Fatalf("duplicate: %v", err)
	}
	var reason string
	for _, d := range cerr.Details() {
		if msg, err := connectproto.UnmarshalErrorDetail(d); err == nil {
			if info, ok := msg.(*errdetails.ErrorInfo); ok {
				reason = info.GetReason()
			}
		}
	}
	if reason != "EMAIL_ALREADY_IN_USE" {
		t.Fatalf("reason = %q, details = %v", reason, cerr.Details())
	}
}

// Cursor pagination walks every user exactly once, newest first.
func TestListPagination(t *testing.T) {
	e := start(t)
	ctx := t.Context()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		if _, err := e.grpc.CreateUser(ctx, newUser(n, n+"@example.com")); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	token := ""
	for range 10 {
		resp, err := e.connect.ListUsers(ctx, &userv1.ListUsersRequest{PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range resp.GetUsers() {
			names = append(names, u.GetName())
		}
		if token = resp.GetNextPageToken(); token == "" {
			break
		}
	}
	if strings.Join(names, "") != "edcba" {
		t.Fatalf("names = %v", names)
	}
}

// The server-streaming method works over gRPC, Connect and REST (NDJSON).
func TestExportStreaming(t *testing.T) {
	e := start(t)
	ctx := t.Context()
	for _, n := range []string{"a", "b", "c"} {
		if _, err := e.grpc.CreateUser(ctx, newUser(n, n+"@example.com")); err != nil {
			t.Fatal(err)
		}
	}

	gs, err := e.grpc.ExportUsers(ctx, &userv1.ExportUsersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for {
		if _, err := gs.Recv(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 3 {
		t.Fatalf("gRPC stream: %d users", n)
	}

	cs, err := e.connect.ExportUsers(ctx, &userv1.ExportUsersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	n = 0
	for {
		if _, err := cs.Receive(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 3 {
		t.Fatalf("Connect stream: %d users", n)
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.httpBase+"/v1/users:export", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	n = 0
	for sc.Scan() {
		if strings.Contains(sc.Text(), `"result"`) {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("REST stream: %d users", n)
	}
}
