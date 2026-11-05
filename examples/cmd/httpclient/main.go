// Command httpclient shows the dependencies/http client: URI-configured
// timeouts, retries and logging, request-scoped log fields, and forwarding
// the request ID downstream.
//
// Start the server first: go run ./cmd/server
package main

import (
	"context"
	"net/http"

	dephttp "github.com/ti/common-go/dependencies/http"
	"github.com/ti/common-go/log"
)

func main() {
	cli, err := dephttp.New(context.Background(),
		"http://127.0.0.1:8080?timeout=5s&try=2&backoff=200ms&log=true")
	if err != nil {
		log.Action("NewClient").Fatal(err.Error())
	}
	defer func() { _ = cli.Close(context.Background()) }()

	// Fields on the context logger are added to the client's access log.
	ctx := log.NewContext(context.Background(), map[string]any{"request_id": "example-request-1"})
	header := http.Header{}
	header.Set("X-Request-Id", "example-request-1")

	// POST is not retried unless it carries an Idempotency-Key.
	header.Set("Idempotency-Key", "create-grace-1")
	var created struct {
		User struct {
			UserID string `json:"user_id"`
		} `json:"user"`
	}
	if err := cli.Request(ctx, http.MethodPost, "/v1/users", header,
		map[string]any{"name": "Grace", "email": "grace@example.com"}, &created); err != nil {
		log.Action("CreateUser").Fatal(err.Error())
	}

	var listed map[string]any
	if err := cli.Request(ctx, http.MethodGet, "/v1/users?page_size=10", nil, nil, &listed); err != nil {
		log.Action("ListUsers").Fatal(err.Error())
	}
	log.With(map[string]any{"action": "ListUsers", "created": created.User.UserID}).Info("listed users over REST")
}
