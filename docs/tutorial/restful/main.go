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

	// Database Drivers - Import the database driver you need:
	//
	// For Mock Database (testing):
	_ "github.com/ti/common-go/dependencies/database/mock"
	//
	// For MongoDB (uncomment when using MongoDB):
	// _ "github.com/ti/common-go/dependencies/mongodb"
	//
	// For MySQL/PostgreSQL (uncomment when using SQL databases):
	// _ "github.com/ti/common-go/dependencies/sql"
	//
	// Note: You can import multiple drivers if your application needs to
	// connect to different database types. The database.New() function
	// will automatically use the correct driver based on the connection
	// string scheme (mock://, mongodb://, mysql://, postgres://)
)

func main() {
	// 1. Initial configuration and dependencies
	var cfg Config
	err := config.Init(context.Background(), "", &cfg, dependencies.WithNewFns(database.New))
	if err != nil {
		log.Action("InitConfig").Fatal(err.Error())
	}

	// 2. Create gRPC-Gateway server
	gs := grpcmux.NewServer(
		grpcmux.WithConfig(&cfg.Apis),
	)

	// 3. Register UserService (CRUD operations)
	userSrv := service.NewUserServiceServer(&cfg.Dependencies, &cfg.Service)
	pb.RegisterUserServiceServer(gs, userSrv)                                             // register grpc
	_ = pb.RegisterUserServiceHandlerServer(context.Background(), gs.ServeMux(), userSrv) // register http

	// 4. Start server
	gs.Start()
}

// Config defines the configuration structure (used to integrate multiple modules)
type Config struct {
	Dependencies service.Dependencies
	Service      service.Config
	Apis         grpcmux.Config
}
