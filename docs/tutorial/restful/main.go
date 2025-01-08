package main

import (
	"context"
	"net/http"

	"github.com/ti/common-go/config"
	pb "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"
	"github.com/ti/common-go/docs/tutorial/restful/service"
	"github.com/ti/common-go/grpcmux"
	"github.com/ti/common-go/log"
)

func main() {
	// 1. Initial configuration and dependencies (optional)
	var cfg Config
	err := config.Init(context.Background(), "", &cfg)
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
