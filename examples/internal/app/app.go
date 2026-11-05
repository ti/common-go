// Package app wires the example service: configuration, dependencies and one
// grpcmux server that serves UserService over every protocol. cmd/server runs
// it; the integration test drives it.
package app

import (
	"context"
	"fmt"

	"github.com/ti/common-go/dependencies"
	"github.com/ti/common-go/dependencies/database"
	userv1 "github.com/ti/common-go/examples/gen/go/example/user/v1"
	"github.com/ti/common-go/examples/gen/go/example/user/v1/userv1connect"
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
}

// NewDependencyOptions returns the creators for dependency types that do not
// implement Init themselves.
func NewDependencyOptions() []dependencies.Option {
	return []dependencies.Option{dependencies.WithNewFns(database.New)}
}

// New builds the server. Register everything before Start.
func New(ctx context.Context, cfg *Config, opts ...grpcmux.Option) (*grpcmux.Server, error) {
	if cfg.Dependencies.DB == nil {
		return nil, fmt.Errorf("dependencies.db is required")
	}
	svc := userservice.New(cfg.Dependencies.DB)
	gs := grpcmux.NewServer(append([]grpcmux.Option{grpcmux.WithConfig(&cfg.Apis)}, opts...)...)

	// 1. Native gRPC (and the in-memory connection used below).
	userv1.RegisterUserServiceServer(gs, svc)

	// 2. REST from the google.api.http annotations. Registering the gateway
	//    against the in-memory connection (instead of RegisterXxxHandlerServer)
	//    gives REST the full gRPC interceptor chain and server streaming
	//    (GET /v1/users:export as newline-delimited JSON).
	if err := userv1.RegisterUserServiceHandlerClient(ctx, gs.ServeMux(),
		userv1.NewUserServiceClient(gs.Conn())); err != nil {
		return nil, err
	}

	// 3. Connect, gRPC-Web and gRPC on the HTTP port, including the streaming
	//    method, through connect-go v2 generated handlers. For unary-only
	//    services without connect-go codegen, use instead:
	//    grpcmux.RegisterConnectService(gs, &userv1.UserService_ServiceDesc, svc)
	userv1connect.RegisterUserServiceHandler(gs.ConnectServer(), userservice.ConnectHandler{Service: svc})

	// Readiness (/readyz and gRPC health) also reflects the database.
	gs.AddHealthChecker(func(ctx context.Context) error {
		_, err := cfg.Dependencies.DB.Count(ctx, "users", database.C{})
		return err
	})
	return gs, nil
}
