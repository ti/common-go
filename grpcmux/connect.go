package grpcmux

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"

	"connectrpc.com/connect"
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
// implement the Connect protocol and support both encodings:
//
//   - application/proto or application/connect+proto  → protobuf binary
//   - application/json or application/connect+json    → JSON  (default)
//
// The response Content-Type mirrors the request encoding so that ConnectRPC
// clients using useBinaryFormat:true receive binary responses and JSON clients
// receive JSON responses without any configuration change on the server.
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

// connectEncoding represents the wire encoding negotiated from Content-Type.
type connectEncoding int

const (
	encodingJSON  connectEncoding = iota // application/json or application/connect+json
	encodingProto                        // application/proto or application/connect+proto
)

// connectMarshalOptions — JSON marshal options for consistent API responses.
var connectMarshalOptions = protojson.MarshalOptions{
	UseProtoNames:   true,
	EmitUnpopulated: false,
}

// connectUnmarshalOptions — JSON unmarshal options; ignore unknown fields for
// forward compatibility.
var connectUnmarshalOptions = protojson.UnmarshalOptions{
	DiscardUnknown: true,
}

// negotiateEncoding inspects the request Content-Type and returns the wire
// encoding that should be used for both decoding the request body and encoding
// the response body.
//
// The Connect protocol specifies:
//
//	application/connect+proto  → binary protobuf
//	application/connect+json   → JSON
//	application/proto          → binary protobuf (used by connect-web useBinaryFormat:true)
//	application/json           → JSON
//
// Anything else falls back to JSON so existing callers are unaffected.
func negotiateEncoding(r *http.Request) connectEncoding {
	ct := r.Header.Get("Content-Type")
	// Trim any parameters (e.g. "; charset=utf-8")
	if idx := strings.IndexByte(ct, ';'); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}
	ct = strings.ToLower(ct)
	switch ct {
	case "application/proto", "application/connect+proto":
		return encodingProto
	default:
		return encodingJSON
	}
}

func (s *Server) newConnectHandler(server any, m grpc.MethodDesc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeConnectError(w, status.New(codes.Unimplemented, "only POST is supported"), encodingJSON)
			return
		}

		enc := negotiateEncoding(r)
		ctx := r.Context()

		// Inject method name into logger context (parallel to medaGetter in gRPC-Gateway)
		log.Inject(ctx, map[string]any{
			"method": m.MethodName,
		})

		// Read request body (limit to 32 MB to match gRPC default max message size)
		body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
		if err != nil {
			writeConnectError(w, status.New(codes.InvalidArgument, "failed to read request body"), enc)
			return
		}

		// Log request body when logBody is enabled (parallel to medaGetter in gRPC-Gateway).
		// For binary payloads we log the byte length instead of raw bytes.
		if s.opts.logBody && len(body) > 0 {
			var reqLog string
			if enc == encodingProto {
				reqLog = "<binary proto, " + itoa(len(body)) + " bytes>"
			} else {
				reqLog = string(body)
				if len(reqLog) > 1024000 {
					reqLog = reqLog[:1024000]
				}
			}
			log.Inject(ctx, map[string]any{
				"request": reqLog,
			})
		}

		// Resolve the service method via reflection.
		method := reflect.ValueOf(server).MethodByName(m.MethodName)
		if !method.IsValid() {
			writeConnectError(w, status.New(codes.Unimplemented, "method not found"), enc)
			return
		}
		methodType := method.Type()
		if methodType.NumIn() != 2 || methodType.NumOut() != 2 {
			writeConnectError(w, status.New(codes.Unimplemented, "invalid method signature"), enc)
			return
		}

		reqVal := reflect.New(methodType.In(1).Elem())
		reqMsg, ok := reqVal.Interface().(proto.Message)
		if !ok {
			writeConnectError(w, status.New(codes.Internal, "request type is not a proto.Message"), enc)
			return
		}

		// Decode request body according to negotiated encoding.
		if len(body) > 0 {
			if enc == encodingProto {
				if err := proto.Unmarshal(body, reqMsg); err != nil {
					writeConnectError(w, status.New(codes.InvalidArgument, err.Error()), enc)
					return
				}
			} else {
				if err := connectUnmarshalOptions.Unmarshal(body, reqMsg); err != nil {
					writeConnectError(w, status.New(codes.InvalidArgument, err.Error()), enc)
					return
				}
			}
		}

		// Call service method.
		results := method.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})

		// Handle error.
		if errVal := results[1].Interface(); errVal != nil {
			st, _ := status.FromError(errVal.(error))
			writeConnectError(w, st, enc)
			return
		}

		// Marshal response according to negotiated encoding.
		respMsg, ok := results[0].Interface().(proto.Message)
		if !ok {
			writeConnectError(w, status.New(codes.Internal, "response type is not a proto.Message"), enc)
			return
		}

		var respBytes []byte
		var contentType string

		if enc == encodingProto {
			respBytes, err = proto.Marshal(respMsg)
			if err != nil {
				writeConnectError(w, status.New(codes.Internal, "failed to marshal response"), enc)
				return
			}
			contentType = "application/proto"
		} else {
			opts := connectMarshalOptions
			if s.opts.useCamelCase {
				opts.UseProtoNames = false // false = use JSON names (camelCase)
			}
			respBytes, err = opts.Marshal(respMsg)
			if err != nil {
				writeConnectError(w, status.New(codes.Internal, "failed to marshal response"), enc)
				return
			}
			contentType = "application/json"
		}

		// Log response body when logBody is enabled.
		if s.opts.logBody {
			var respLog string
			if enc == encodingProto {
				respLog = "<binary proto, " + itoa(len(respBytes)) + " bytes>"
			} else {
				respLog = string(respBytes)
				if len(respLog) > 2048 {
					respLog = respLog[:2048]
				}
			}
			log.Inject(ctx, map[string]any{
				"response": respLog,
			})
		}

		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
	})
}

// connectError is the JSON error body for ConnectRPC responses.
// Follows the Connect protocol error format:
// https://connectrpc.com/docs/protocol/#error-end-stream
type connectError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeConnectError writes a ConnectRPC-compatible error response.
//
// For binary requests the Connect protocol still mandates that error responses
// are JSON-encoded (the error envelope is always JSON regardless of the request
// encoding), so we always write JSON errors with Content-Type: application/json.
//
// It uses [mux.CodeToError] and [mux.CodeToHTTPStatus] so that custom error
// codes registered via [mux.RegisterErrorCodes] (e.g. from error.proto) are
// automatically mapped to the correct error name and HTTP status.
func writeConnectError(w http.ResponseWriter, st *status.Status, _ connectEncoding) {
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

	// Connect protocol: errors are always JSON, even for binary-encoded streams.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_, _ = w.Write(body)
}

// itoa is a minimal int-to-string helper to avoid importing "strconv" just for
// log messages.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

// ConnectHandlerFunc is a convenience type alias for the handler registration
// function signature used by [grpcmux.Server.Handle].
// It allows callers to pass gs.Handle directly when registering ConnectRPC
// service handlers generated by protoc-gen-connect-go:
//
//	path, handler := pb.NewMyServiceHandler(impl)
//	grpcmux.ConnectHandlerFunc(gs.Handle)(path, handler)
//
// Or more idiomatically:
//
//	grpcmux.RegisterConnectHandler(gs, pb.NewMyServiceHandler(impl))
type ConnectHandlerFunc func(pattern string, handler http.Handler)

// RegisterConnectHandler registers a single ConnectRPC service handler
// (as returned by protoc-gen-connect-go's NewXxxServiceHandler) on the server.
//
// This is the preferred registration path when using protoc-gen-connect-go
// generated handlers (which implement the full Connect protocol natively,
// including binary proto, JSON, and streaming). Use this instead of
// [RegisterConnectService] when you have generated handler interfaces.
//
// Usage:
//
//	impl := &myServiceImpl{}
//	grpcmux.RegisterConnectHandler(gs, pb.NewMyServiceHandler(impl))
func RegisterConnectHandler(s *Server, pattern string, handler http.Handler) {
	s.Handle(pattern, handler)
}

// WithConnectOptions returns a [connect.HandlerOption] slice pre-configured
// for use with protoc-gen-connect-go generated handlers on this server.
//
// Currently returns an empty slice; reserved for future interceptor injection
// (e.g. auth, logging) from the grpcmux option set.
func WithConnectOptions(_ *Server) []connect.HandlerOption {
	return nil
}
