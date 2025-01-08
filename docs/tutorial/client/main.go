package main

import (
	"context"
	"flag"
	"time"

	pb "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"
	"github.com/ti/common-go/grpcmux"
	"github.com/ti/common-go/log"
)

func main() {
	flag.Parse()
	// System initialization maximum timeout is 5 seconds
	ctx, cc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cc()
	cli, err := grpcmux.NewClient(ctx, "http://127.0.0.1:8081?log=true", pb.NewSayClient)
	if err != nil {
		panic(err)
	}
	resp, err := cli.Hello(ctx, &pb.Request{Name: "panic"})
	if err != nil {
		panic(err)
	}
	log.Action("grpc.Test").Info(resp.Msg)
}
