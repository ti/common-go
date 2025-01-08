// Package service implements grpc proto interface.
package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	pb "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"
	"github.com/ti/common-go/grpcmux/mux"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// New service implement hello
func New(dep *Dependencies, cfg *Config) *Server {
	mux.RegisterErrorCodes(pb.ErrorCode_name)
	return &Server{
		dep: dep,
		cfg: cfg,
	}
}

// Server the instance for grpc proto.
type Server struct {
	pb.UnimplementedSayServer
	dep *Dependencies
	cfg *Config
}

// Hello implements grpc proto Hello Method interface.
func (s *Server) Hello(_ context.Context, req *pb.Request) (*pb.Response, error) {
	if err := req.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	switch req.Name {
	case "error":
		return nil, status.Error(codes.Code(pb.ErrorCode_CustomNotFound), "the error example for CustomNotFound")
	case "error1":
		return nil, status.Error(codes.InvalidArgument, "the error example for 4xx")
	case "404":
		return nil, status.Error(codes.NotFound, "the error example for 404")
	case "panic":
		panic("the error example for panic")
	case "metrics":
		demoCounter.Add(1)
	case "host":
		host, _ := os.Hostname()
		return &pb.Response{
			Msg: fmt.Sprintf("hello %s form %s", req.Name, host),
		}, nil
	}
	// return result
	return &pb.Response{
		Msg: fmt.Sprintf("hello %s", req.Name),
	}, nil
}

// HelloStream hello stream
func (s *Server) HelloStream(in *pb.Request, srv pb.Say_HelloStreamServer) error {
	for i := 0; i < 5; i++ {
		if err := srv.Send(&pb.Response{
			Msg: fmt.Sprintf("hello %s for %d", in.Name, i),
		}); err != nil {
			log.Printf("send error %v", err)
		}
		time.Sleep(time.Second)
	}
	return nil
}
