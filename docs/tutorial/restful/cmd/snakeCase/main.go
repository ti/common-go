package main

import (
	"context"
	"fmt"

	"github.com/ti/common-go/config"
	"github.com/ti/common-go/dependencies"
	"github.com/ti/common-go/dependencies/database"
	pb "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"
	"github.com/ti/common-go/docs/tutorial/restful/service"
	"github.com/ti/common-go/grpcmux"
	"github.com/ti/common-go/log"

	// Database driver - Mock Database for testing
	_ "github.com/ti/common-go/dependencies/database/mock"
)

func main() {
	// 1. Initial configuration and dependencies with database support
	var cfg Config
	err := config.Init(context.Background(), "", &cfg, dependencies.WithNewFns(database.New))
	if err != nil {
		log.Action("InitConfig").Fatal(err.Error())
	}

	log.Action("Start").Info("Starting server with snake_case JSON format (default)")

	// 2. Initialize the UserService
	userSrv := service.NewUserServiceServer(&cfg.Dependencies, &cfg.Service)

	// 3. Create server with default snake_case format
	gs := grpcmux.NewServer(
		grpcmux.WithHTTPAddr(":8082"), // Different port to avoid conflict
		grpcmux.WithGrpcAddr(":8083"),
		grpcmux.WithMetricsAddr(":9091"),
		// No WithUseCamelCase() - uses default snake_case
	)

	// 4. Register custom health checkers.
	// Each checker is called on every gRPC/HTTP health check request.
	// If any checker returns a non-nil error, the service reports NOT_SERVING.

	// Example: check database connectivity by running a lightweight query
	gs.AddHealthChecker(func(ctx context.Context) error {
		if cfg.Dependencies.DB == nil {
			return nil
		}
		_, err := cfg.Dependencies.DB.Count(ctx, "_health", nil)
		if err != nil {
			return fmt.Errorf("database unhealthy: %w", err)
		}
		return nil
	})

	// Example: check that a required downstream HTTP dependency was initialised
	gs.AddHealthChecker(func(ctx context.Context) error {
		if cfg.Dependencies.DemoHTTP == nil {
			return fmt.Errorf("demo http dependency not initialised")
		}
		return nil
	})

	// 5. Register UserService (CRUD operations)
	pb.RegisterUserServiceServer(gs, userSrv)
	_ = pb.RegisterUserServiceHandlerServer(context.Background(), gs.ServeMux(), userSrv)

	log.Action("Start").Info("Server ready with snake_case JSON format (default)",
		"httpAddr", ":8082",
		"grpcAddr", ":8083",
		"format", "snake_case",
		"service", "UserService")

	// 6. Start server
	gs.Start()
}

// Config defines the configuration structure (used to integrate multiple modules)
type Config struct {
	Dependencies service.Dependencies
	Service      service.Config
	Apis         grpcmux.Config
}
