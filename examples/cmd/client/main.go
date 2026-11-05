// Command client calls the example server over native gRPC (grpcmux.NewClient)
// and over Connect (connect-go v2), with the same generated messages.
//
// Start the server first: go run ./cmd/server
package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	userv1 "github.com/ti/common-go/examples/gen/go/example/user/v1"
	"github.com/ti/common-go/examples/gen/go/example/user/v1/userv1connect"
	"github.com/ti/common-go/grpcmux"
	"github.com/ti/common-go/log"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// gRPC. URI parameters configure the connection: timeout, retries with
	// gRPC's native retry policy, client keepalive and access logging.
	grpcClient, err := grpcmux.NewClient(ctx,
		"dns://127.0.0.1:8081?timeout=3s&try=2&retryPolicy=native&keepalive=30s&log=true",
		userv1.NewUserServiceClient)
	if err != nil {
		log.Action("Dial").Fatal(err.Error())
	}
	created, err := grpcClient.CreateUser(ctx, &userv1.CreateUserRequest{User: &userv1.User{
		Name:  "Ada",
		Email: fmt.Sprintf("ada+%d@example.com", time.Now().UnixNano()),
	}})
	if err != nil {
		log.Action("CreateUser").Fatal(err.Error())
	}
	log.With(map[string]any{"action": "CreateUser", "user_id": created.GetUser().GetUserId()}).Info("created over gRPC")

	// Connect over HTTP/1.1 with JSON, as a browser would.
	connectClient := userv1connect.NewUserServiceClient(connect.NewClient(
		connecthttp.NewTransport(http.DefaultClient, "http://127.0.0.1:8080", connecthttp.WithProtoJSON())))
	got, err := connectClient.GetUser(ctx, &userv1.GetUserRequest{UserId: created.GetUser().GetUserId()})
	if err != nil {
		log.Action("GetUser").Fatal(err.Error())
	}
	log.With(map[string]any{"action": "GetUser", "name": got.GetUser().GetName()}).Info("read over Connect")
}
