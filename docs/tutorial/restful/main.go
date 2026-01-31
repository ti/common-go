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
	// 1. Initial configuration and dependencies (optional)
	var cfg Config
	err := config.Init(context.Background(), "", &cfg, dependencies.WithNewFns(database.New))
	if err != nil {
		log.Action("InitConfig").Fatal(err.Error())
	}
	// 2. Initialize the service
	srv := service.New(&cfg.Dependencies, &cfg.Service)
	gs := grpcmux.NewServer(
		grpcmux.WithConfig(&cfg.Apis),
	)
	pb.RegisterSayServer(gs, srv)                                             // register grpc
	_ = pb.RegisterSayHandlerServer(context.Background(), gs.ServeMux(), srv) // register http (optional)

	// Register UserService
	userSrv := service.NewUserServiceServer(&cfg.Dependencies, &cfg.Service)
	pb.RegisterUserServiceServer(gs, userSrv)                                             // register grpc
	_ = pb.RegisterUserServiceHandlerServer(context.Background(), gs.ServeMux(), userSrv) // register http (optional)

	// 3. Stream in internal process
	gs.HandleFunc(http.MethodPost, "/v1/stream", srv.HelloStreamHTTP)
	gs.Start()
}

// Config defines the configuration structure (used to integrate multiple modules)
type Config struct {
	Dependencies service.Dependencies
	Service      service.Config
	Apis         grpcmux.Config
}
