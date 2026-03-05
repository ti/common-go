package main

// ConnectRPC handler adapter for UserService.
//
// Since the existing proto files were generated without protoc-gen-connect-go,
// we construct ConnectRPC handlers manually using connect.NewUnaryHandler.
// This adapter bridges the existing *service.UserServiceServer (which implements
// the standard gRPC pb.UserServiceServer interface) with the ConnectRPC HTTP handler
// model, so both protocols are served by the same business logic.
//
// Routing:
//   - ConnectRPC paths  → /pb.UserService/<MethodName>  (mounted via gs.Handle)
//   - gRPC-Gateway REST → /v1/users, /v1/users/:id, …   (mounted on gs.ServeMux())
//   - Native gRPC       → port 8081 (TCP, binary framing)
//
// Protocol support per handler (automatic, zero config):
//   - Connect protocol (JSON or binary, HTTP/1.1 or HTTP/2)  ← browser-friendly
//   - gRPC protocol (binary, HTTP/2)
//   - gRPC-Web protocol (binary, HTTP/1.1 or HTTP/2)
//
// See https://connectrpc.com/docs/go/getting-started for details.

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	pb "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"
	"github.com/ti/common-go/docs/tutorial/restful/service"
)

// connectServiceName is the gRPC full service name used as the URL prefix.
// ConnectRPC mounts handlers at /<package>.<Service>/<Method>.
const connectServiceName = "/pb.UserService/"

// connectProcedure builds the full ConnectRPC procedure name for a method.
func connectProcedure(method string) string {
	return connectServiceName + method
}

// newConnectHandler creates an http.Handler that handles all UserService RPCs
// using the ConnectRPC protocol. It wraps the existing *service.UserServiceServer
// so there is a single implementation shared by gRPC, REST gateway, and ConnectRPC.
//
// The returned handler should be mounted at connectServiceName:
//
//	gs.Handle(connectServiceName, newConnectHandler(svc))
func newConnectHandler(svc *service.UserServiceServer, opts ...connect.HandlerOption) http.Handler {
	mux := http.NewServeMux()

	// CreateUser — POST /pb.UserService/CreateUser
	mux.Handle(connectProcedure("CreateUser"),
		connect.NewUnaryHandler(
			connectProcedure("CreateUser"),
			func(ctx context.Context, req *connect.Request[pb.CreateUserRequest]) (*connect.Response[pb.UserResponse], error) {
				resp, err := svc.CreateUser(ctx, req.Msg)
				if err != nil {
					return nil, err
				}
				return connect.NewResponse(resp), nil
			},
			opts...,
		),
	)

	// GetUser — POST /pb.UserService/GetUser
	mux.Handle(connectProcedure("GetUser"),
		connect.NewUnaryHandler(
			connectProcedure("GetUser"),
			func(ctx context.Context, req *connect.Request[pb.GetUserRequest]) (*connect.Response[pb.UserResponse], error) {
				resp, err := svc.GetUser(ctx, req.Msg)
				if err != nil {
					return nil, err
				}
				return connect.NewResponse(resp), nil
			},
			opts...,
		),
	)

	// UpdateUser — POST /pb.UserService/UpdateUser
	mux.Handle(connectProcedure("UpdateUser"),
		connect.NewUnaryHandler(
			connectProcedure("UpdateUser"),
			func(ctx context.Context, req *connect.Request[pb.UpdateUserRequest]) (*connect.Response[pb.UserResponse], error) {
				resp, err := svc.UpdateUser(ctx, req.Msg)
				if err != nil {
					return nil, err
				}
				return connect.NewResponse(resp), nil
			},
			opts...,
		),
	)

	// DeleteUser — POST /pb.UserService/DeleteUser
	mux.Handle(connectProcedure("DeleteUser"),
		connect.NewUnaryHandler(
			connectProcedure("DeleteUser"),
			func(ctx context.Context, req *connect.Request[pb.DeleteUserRequest]) (*connect.Response[pb.DeleteUserResponse], error) {
				resp, err := svc.DeleteUser(ctx, req.Msg)
				if err != nil {
					return nil, err
				}
				return connect.NewResponse(resp), nil
			},
			opts...,
		),
	)

	// ListUsers — POST /pb.UserService/ListUsers
	mux.Handle(connectProcedure("ListUsers"),
		connect.NewUnaryHandler(
			connectProcedure("ListUsers"),
			func(ctx context.Context, req *connect.Request[pb.PageQueryRequest]) (*connect.Response[pb.PageUsersResponse], error) {
				resp, err := svc.ListUsers(ctx, req.Msg)
				if err != nil {
					return nil, err
				}
				return connect.NewResponse(resp), nil
			},
			opts...,
		),
	)

	// StreamUsers — POST /pb.UserService/StreamUsers
	mux.Handle(connectProcedure("StreamUsers"),
		connect.NewUnaryHandler(
			connectProcedure("StreamUsers"),
			func(ctx context.Context, req *connect.Request[pb.StreamQueryRequest]) (*connect.Response[pb.StreamUsersResponse], error) {
				resp, err := svc.StreamUsers(ctx, req.Msg)
				if err != nil {
					return nil, err
				}
				return connect.NewResponse(resp), nil
			},
			opts...,
		),
	)

	return mux
}
