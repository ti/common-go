// Package app wires the example service: configuration, dependencies and one
// grpcmux server that serves UserService over every protocol. cmd/server runs
// it; the integration test drives it.
package app

import (
	"context"
	"fmt"

	"github.com/ti/common-go/background"
	"github.com/ti/common-go/dependencies"
	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/redis"
	reportv1 "github.com/ti/common-go/examples/gen/go/example/report/v1"
	userv1 "github.com/ti/common-go/examples/gen/go/example/user/v1"
	"github.com/ti/common-go/examples/gen/go/example/user/v1/userv1connect"
	"github.com/ti/common-go/examples/internal/reportservice"
	"github.com/ti/common-go/examples/internal/userservice"
	"github.com/ti/common-go/grpcmux"

	// Database drivers register their URI schemes (mock://, mongodb://,
	// mysql://, postgres://). Import the ones your deployment uses.
	_ "github.com/ti/common-go/dependencies/database/mock"
	_ "github.com/ti/common-go/dependencies/mongo"
	_ "github.com/ti/common-go/dependencies/sql"
)

// Config is the file configuration (configs/config.yaml).
type Config struct {
	Apis grpcmux.Config `yaml:"apis"`
	Log  struct {
		Level string `yaml:"level"`
	} `yaml:"log"`
	// Dependencies are URIs, initialized by config.Init / dependencies.Init.
	Dependencies Dependencies `yaml:"dependencies"`
}

// Dependencies are resolved from URIs by field name (case-insensitive).
type Dependencies struct {
	dependencies.Dependency
	DB database.Database
	// Redis is optional (dependencies.redis: redis://host:6379). When it is
	// set, the rate-limit buckets of apis.rateLimit live there and the limits
	// hold across replicas; without it they are kept in process memory.
	Redis *redis.Redis `required:"false"`
}

// NewDependencyOptions returns the creators for dependency types that do not
// implement Init themselves.
func NewDependencyOptions() []dependencies.Option {
	return []dependencies.Option{dependencies.WithNewFns(database.New)}
}

// New builds the server. Register everything before Start.
func New(_ context.Context, cfg *Config, opts ...grpcmux.Option) (*grpcmux.Server, error) {
	if cfg.Dependencies.DB == nil {
		return nil, fmt.Errorf("dependencies.db is required")
	}
	svc := userservice.New(cfg.Dependencies.DB)
	gs := grpcmux.NewServer(append([]grpcmux.Option{
		grpcmux.WithConfig(&cfg.Apis),
		// REST for every method with a google.api.http annotation, built from
		// the descriptors at registration: no generated gateway code and no
		// per-service call. `apis.rest: true` in the config does the same.
		grpcmux.WithREST(),
		// apis.rateLimit buckets go to Redis when it is configured (nil
		// otherwise: in memory).
		grpcmux.WithRateLimitStore(cfg.Dependencies.Redis.RateLimiter()),
	}, opts...)...)

	// 1. Native gRPC, and with WithREST the REST routes: POST /v1/users,
	//    GET /v1/users/{user_id}, ..., GET /v1/users:export (server streaming
	//    as newline-delimited JSON).
	userv1.RegisterUserServiceServer(gs, svc)

	// 2. Connect, gRPC-Web and gRPC on the HTTP port, including the streaming
	//    method, through connect-go v2 generated handlers. For unary-only
	//    services without connect-go codegen, use instead:
	//    grpcmux.RegisterConnectService(gs, &userv1.UserService_ServiceDesc, svc)
	userv1connect.RegisterUserServiceHandler(gs.ConnectServer(), userservice.ConnectHandler{Service: svc})

	// 3. Background work with observable status (docs/guides/background-work.md):
	//    StartReport submits a job to an in-process queue and returns,
	//    WatchReport streams its progress. At shutdown the queue is closed
	//    after the server has stopped serving and before the dependencies,
	//    wherever it is created. No connect-go codegen here: the gRPC
	//    implementation serves Connect unary calls and the streams as well.
	reports := reportservice.New(background.QueueOptions{})
	grpcmux.RegisterConnectService(gs, &reportv1.ReportService_ServiceDesc, reports)
	grpcmux.RegisterConnectStreams(gs, &reportv1.ReportService_ServiceDesc, reports)

	// Readiness (/readyz and gRPC health) also reflects the database.
	gs.AddHealthChecker(func(ctx context.Context) error {
		_, err := cfg.Dependencies.DB.Count(ctx, "users", database.C{})
		return err
	})
	return gs, nil
}
