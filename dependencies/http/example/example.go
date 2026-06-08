package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	dephttp "github.com/ti/common-go/dependencies/http"
	"github.com/ti/common-go/log"
)

func main() {
	// new http client
	cli, err := dephttp.New(context.Background(), "http://127.0.0.1:8080?try=3&timeout=10s&log=true")
	if err != nil {
		panic(err)
	}

	// 日志增加user_id和request_id
	ctx := log.NewContext(context.Background(), map[string]any{
		"user_id":    "the_user_id",
		"request_id": "uuid_from_logger_request",
	})
	req := map[string]string{
		"test": "go",
	}

	var resp RespInfo

	// 下游调用增加x-request-id http头
	err = cli.Request(ctx, http.MethodPost, "/_info/test", map[string][]string{
		"x-request-id": {"uuid_from_logger_request"},
	}, req, &resp)
	if err != nil {
		return
	}

	b, _ := json.Marshal(resp.Header)
	log.Action("test").Info(string(b))
}

// RespInfo the response info
type RespInfo struct {
	Time   time.Time
	Body   string
	Header http.Header
}
