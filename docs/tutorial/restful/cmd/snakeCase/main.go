package main

import (
	"context"
	"net/http"

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

	// 2. Initialize the Say service
	srv := service.New(&cfg.Dependencies, &cfg.Service)

	// 3. Initialize the UserService
	userSrv := service.NewUserServiceServer(&cfg.Dependencies, &cfg.Service)

	// 4. Create server with default snake_case format
	gs := grpcmux.NewServer(
		grpcmux.WithHTTPAddr(":8082"), // Different port to avoid conflict
		grpcmux.WithGrpcAddr(":8083"),
		grpcmux.WithMetricsAddr(":9091"),
		// No WithUseCamelCase() - uses default snake_case
	)

	// 5. Register Say service
	pb.RegisterSayServer(gs, srv)
	_ = pb.RegisterSayHandlerServer(context.Background(), gs.ServeMux(), srv)

	// 6. Register UserService (CRUD operations)
	pb.RegisterUserServiceServer(gs, userSrv)
	_ = pb.RegisterUserServiceHandlerServer(context.Background(), gs.ServeMux(), userSrv)

	// 7. Stream in internal process
	gs.HandleFunc(http.MethodPost, "/v1/stream", srv.HelloStreamHTTP)

	log.Action("Start").Info("Server ready with snake_case JSON format (default)",
		"httpAddr", ":8082",
		"grpcAddr", ":8083",
		"format", "snake_case",
		"services", "Say, UserService")

	gs.Start()
}

// Config defines the configuration structure (used to integrate multiple modules)
type Config struct {
	Dependencies service.Dependencies
	Service      service.Config
	Apis         grpcmux.Config
}
