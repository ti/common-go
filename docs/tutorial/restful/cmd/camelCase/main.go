package main

import (
	"context"

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

	log.Action("Start").Info("Starting server with camelCase JSON format")

	// 2. Initialize the UserService
	userSrv := service.NewUserServiceServer(&cfg.Dependencies, &cfg.Service)

	// 3. Create server with camelCase enabled
	gs := grpcmux.NewServer(
		grpcmux.WithHTTPAddr(":8080"),
		grpcmux.WithGrpcAddr(":8081"),
		grpcmux.WithMetricsAddr(":9090"),
		grpcmux.WithUseCamelCase(), // Enable camelCase format
	)

	// 4. Register UserService (CRUD operations)
	pb.RegisterUserServiceServer(gs, userSrv)
	_ = pb.RegisterUserServiceHandlerServer(context.Background(), gs.ServeMux(), userSrv)

	log.Action("Start").Info("Server ready with camelCase JSON format",
		"httpAddr", ":8080",
		"grpcAddr", ":8081",
		"format", "camelCase",
		"service", "UserService")

	// 5. Start server
	gs.Start()
}

// Config defines the configuration structure (used to integrate multiple modules)
type Config struct {
	Dependencies service.Dependencies
	Service      service.Config
	Apis         grpcmux.Config
}
