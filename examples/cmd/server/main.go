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
	"github.com/ti/common-go/tracing"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	var cfg app.Config
	if err := config.Init(ctx, "", &cfg, app.NewDependencyOptions()...); err != nil {
		log.Action("InitConfig").Fatal(err.Error())
	}
	// Trace export, configured by the standard OTEL_* environment (a no-op
	// without OTEL_EXPORTER_OTLP_ENDPOINT). The server creates the spans when
	// apis.tracing is on and logs trace_id/span_id with every request; the
	// last spans are flushed on shutdown.
	if _, err := tracing.Init(ctx, tracing.WithServiceName("example-user-service")); err != nil {
		log.Action("InitTracing").Warn("tracing disabled: " + err.Error())
	}
	gs, err := app.New(ctx, &cfg)
	cancel()
	if err != nil {
		log.Action("InitServer").Fatal(err.Error())
	}
	// Blocks until SIGTERM/SIGINT, then drains (see docs/guides/server.md).
	// A server that could not start (port in use) is an error: exit 1.
	if err := gs.Run(); err != nil {
		log.Action("Server").Fatal(err.Error())
	}
}
