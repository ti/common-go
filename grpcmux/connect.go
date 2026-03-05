package grpcmux

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/ti/common-go/grpcmux/mux"
	"github.com/ti/common-go/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// RegisterConnectService registers a gRPC service as ConnectRPC HTTP handlers.
//
// It iterates all unary methods from the ServiceDesc and mounts each as a
// POST handler at /<ServiceName>/<MethodName> on the HTTP server. The handlers
// use the standard Connect protocol (application/json, plain JSON bodies) and
// share the same HTTP middleware chain (logging, auth, recovery, request-id)
// as the gRPC-Gateway routes.
//
// Custom error codes registered via [mux.RegisterErrorCodes] are automatically
// supported — the error code name and HTTP status mapping are shared with the
// gRPC-Gateway error handler.
//
// This function also registers the service on the native gRPC server (port 8081)
// so both protocols share the same implementation.
//
// Usage:
//
//	gs := grpcmux.NewServer(...)
//	grpcmux.RegisterConnectService(gs, &pb.UserService_ServiceDesc, userSrv)
//	gs.Start()
func RegisterConnectService(s *Server, desc *grpc.ServiceDesc, serviceImpl any) {
	// Register on native gRPC server
	s.grpcServer.RegisterService(desc, serviceImpl)

	// Register each unary method as an HTTP handler
	servicePath := "/" + desc.ServiceName + "/"
	for _, m := range desc.Methods {
		method := m
		httpPath := servicePath + method.MethodName
		s.Logger.Log(s.ctx, logging.LevelDebug, "connect", "path", httpPath)
		s.Handle(httpPath, s.newConnectHandler(serviceImpl, method))
	}
}

// connectMarshalOptions — emit all fields for consistent API responses.
var connectMarshalOptions = protojson.MarshalOptions{
	UseProtoNames:   true,
	EmitUnpopulated: false,
}

// connectUnmarshalOptions — ignore unknown fields for forward compatibility.
var connectUnmarshalOptions = protojson.UnmarshalOptions{
	DiscardUnknown: true,
}

func (s *Server) newConnectHandler(server any, m grpc.MethodDesc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeConnectError(w, status.New(codes.Unimplemented, "only POST is supported"))
			return
		}

		ctx := r.Context()

		// Inject method name into logger context (parallel to medaGetter in gRPC-Gateway)
		log.Inject(ctx, map[string]any{
			"method": m.MethodName,
		})

		// Decode request
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
		if err != nil {
			writeConnectError(w, status.New(codes.InvalidArgument, "failed to read request body"))
			return
		}

		// Log request body when logBody is enabled (parallel to medaGetter in gRPC-Gateway)
		if s.opts.logBody && len(body) > 0 {
			reqLog := string(body)
			if len(reqLog) > 1024000 {
				reqLog = reqLog[:1024000]
			}
			log.Inject(ctx, map[string]any{
				"request": reqLog,
			})
		}

		method := reflect.ValueOf(server).MethodByName(m.MethodName)
		if !method.IsValid() {
			writeConnectError(w, status.New(codes.Unimplemented, "method not found"))
			return
		}
		methodType := method.Type()
		if methodType.NumIn() != 2 || methodType.NumOut() != 2 {
			writeConnectError(w, status.New(codes.Unimplemented, "invalid method signature"))
			return
		}

		reqVal := reflect.New(methodType.In(1).Elem())
		reqMsg, ok := reqVal.Interface().(proto.Message)
		if !ok {
			writeConnectError(w, status.New(codes.Internal, "request type is not a proto.Message"))
			return
		}

		if len(body) > 0 {
			if err := connectUnmarshalOptions.Unmarshal(body, reqMsg); err != nil {
				writeConnectError(w, status.New(codes.InvalidArgument, err.Error()))
				return
			}
		}

		// Call service method
		results := method.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})

		// Handle error
		if errVal := results[1].Interface(); errVal != nil {
			st, _ := status.FromError(errVal.(error))
			writeConnectError(w, st)
			return
		}

		// Marshal response
		respMsg, ok := results[0].Interface().(proto.Message)
		if !ok {
			writeConnectError(w, status.New(codes.Internal, "response type is not a proto.Message"))
			return
		}

		opts := connectMarshalOptions
		if s.opts.useCamelCase {
			opts.UseProtoNames = false // false = use JSON names (camelCase)
		}

		respBytes, err := opts.Marshal(respMsg)
		if err != nil {
			writeConnectError(w, status.New(codes.Internal, "failed to marshal response"))
			return
		}

		// Log response body when logBody is enabled (parallel to forwardResponser in gRPC-Gateway)
		if s.opts.logBody {
			respLog := string(respBytes)
			if len(respLog) > 2048 {
				respLog = respLog[:2048]
			}
			log.Inject(ctx, map[string]any{
				"response": respLog,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
	})
}

// connectError is the JSON error body for ConnectRPC responses.
type connectError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeConnectError writes a ConnectRPC error response.
// It uses [mux.CodeToError] and [mux.CodeToHTTPStatus] so that custom error
// codes registered via [mux.RegisterErrorCodes] (e.g. from error.proto) are
// automatically mapped to the correct error name and HTTP status.
func writeConnectError(w http.ResponseWriter, st *status.Status) {
	code := st.Code()
	httpStatus := mux.CodeToHTTPStatus(code)
	codeStr := mux.CodeToError(code)

	body, err := json.Marshal(connectError{
		Code:    codeStr,
		Message: st.Message(),
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"code":"internal","message":"failed to marshal error"}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_, _ = w.Write(body)
}
