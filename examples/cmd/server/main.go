// Command server runs the example UserService on every protocol:
//
//	HTTP :8080 — REST (/v1/users...), Connect and gRPC-Web (/example.user.v1.UserService/*), /healthz, /readyz
//	gRPC :8081 — native gRPC
//	     :9090 — Prometheus /metrics
//
// Run from the examples directory: go run ./cmd/server
// The configuration file defaults to configs/config.yaml (override with -c or CONFIG_PATH).
package main

import (
	"context"
	"time"

	"github.com/ti/common-go/config"
	"github.com/ti/common-go/examples/internal/app"
	"github.com/ti/common-go/log"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	var cfg app.Config
	if err := config.Init(ctx, "", &cfg, app.NewDependencyOptions()...); err != nil {
		log.Action("InitConfig").Fatal(err.Error())
	}
	gs, err := app.New(ctx, &cfg)
	cancel()
	if err != nil {
		log.Action("InitServer").Fatal(err.Error())
	}
	// Blocks until SIGTERM/SIGINT, then drains (see docs/guides/server.md).
	gs.Start()
}
