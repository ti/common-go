package grpcmux

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/ti/common-go/grpcmux/mux"
	"github.com/ti/common-go/log"
)

// RegisterConnectService serves a gRPC service over the Connect protocol on
// the HTTP port, without protoc-gen-connect-go, and registers it on the gRPC
// server too, so one implementation answers native gRPC and Connect.
//
// Each unary method is mounted at /<package.Service>/<Method>. The handler
// implements the Connect unary protocol (https://connectrpc.com/docs/protocol):
//
//   - POST with Content-Type application/proto or application/json (a missing
//     or other non-Connect content type is read as JSON, for curl and legacy
//     callers);
//   - GET (?encoding=…&message=…[&base64=1][&compression=gzip]) for methods
//     declared with option idempotency_level = NO_SIDE_EFFECTS;
//   - gzip request bodies (Content-Encoding) and gzip responses when the
//     client sends Accept-Encoding: gzip;
//   - Connect-Timeout-Ms as the handler's deadline;
//   - errors as Connect JSON with code, message and details; codes registered
//     with mux.RegisterErrorCodes keep their names and HTTP statuses;
//   - header/trailer metadata set with grpc.SetHeader / grpc.SetTrailer.
//
// Requests run the same unary interceptor chain as native gRPC (see
// WithoutHTTPInterceptors), behind the HTTP middleware (CORS, request ID,
// auth, access log). Request bodies are limited by WithMaxRequestBodyBytes.
// Streaming methods are not bridged; serve them with protoc-gen-connect-go
// handlers and RegisterConnectHandler.
//
//	gs := grpcmux.NewServer(...)
//	grpcmux.RegisterConnectService(gs, &pb.UserService_ServiceDesc, userSrv)
//	gs.Start()
func RegisterConnectService(s *Server, desc *grpc.ServiceDesc, serviceImpl any) {
	s.registerGRPC(desc, serviceImpl)
	for _, m := range desc.Methods {
		fullMethod := "/" + desc.ServiceName + "/" + m.MethodName
		s.Logger.Log(s.ctx, logging.LevelDebug, "connect", "path", fullMethod)
		h := &connectHandler{
			s:          s,
			impl:       serviceImpl,
			fullMethod: fullMethod,
			method:     m,
			allowGET:   hasNoSideEffects(desc.ServiceName, m.MethodName),
		}
		s.Handle(fullMethod, h)
	}
}

// hasNoSideEffects reports whether the method is declared
// idempotency_level = NO_SIDE_EFFECTS in its proto definition.
func hasNoSideEffects(service, method string) bool {
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service + "." + method))
	if err != nil {
		return false
	}
	md, ok := d.(protoreflect.MethodDescriptor)
	if !ok {
		return false
	}
	opts, ok := md.Options().(*descriptorpb.MethodOptions)
	return ok && opts.GetIdempotencyLevel() == descriptorpb.MethodOptions_NO_SIDE_EFFECTS
}

const (
	connectProtocolVersion = "1"
	// compressMinBytes: smaller responses are sent uncompressed.
	compressMinBytes = 1024
)

type connectEncoding int

const (
	encodingJSON connectEncoding = iota
	encodingProto
)

func (e connectEncoding) contentType() string {
	if e == encodingProto {
		return "application/proto"
	}
	return "application/json"
}

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

var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

type connectHandler struct {
	s          *Server
	impl       any
	fullMethod string
	method     grpc.MethodDesc
	allowGET   bool
}

// connectRequest is a parsed unary Connect request.
type connectRequest struct {
	encoding    connectEncoding
	compression string // "" or "gzip"
	body        []byte
}

func (h *connectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			// Reached only with WithoutHTTPInterceptors (otherwise the
			// recovery interceptor handles it); keep the Connect error format.
			log.Inject(r.Context(), map[string]any{"panic": fmt.Sprint(rec)})
			writeConnectError(w, status.New(codes.Internal, fmt.Sprint(rec)))
		}
	}()

	req, st := h.parse(r)
	if st != nil {
		if st.Code() == codes.Unimplemented && r.Method != http.MethodPost && r.Method != http.MethodGet {
			w.Header().Set("Allow", h.allowHeader())
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeConnectError(w, st)
		return
	}

	ctx := r.Context()
	if v := r.Header.Get("Connect-Timeout-Ms"); v != "" {
		ms, err := strconv.ParseInt(v, 10, 64)
		if err != nil || ms <= 0 || len(v) > 10 {
			writeConnectError(w, status.Newf(codes.InvalidArgument, "invalid Connect-Timeout-Ms %q", v))
			return
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
		defer cancel()
	}

	log.Inject(ctx, map[string]any{"method": h.method.MethodName})
	if h.s.opts.logBody && len(req.body) > 0 {
		log.Inject(ctx, map[string]any{"request": bodyForLog(req.encoding, req.body, 1024000)})
	}

	dec := func(v any) error {
		msg, ok := v.(proto.Message)
		if !ok {
			return status.Errorf(codes.Internal, "request type %T is not a proto.Message", v)
		}
		if len(req.body) == 0 {
			return nil
		}
		var err error
		if req.encoding == encodingProto {
			err = proto.Unmarshal(req.body, msg)
		} else {
			err = connectUnmarshalOptions.Unmarshal(req.body, msg)
		}
		if err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		return nil
	}

	resp, stream, err := h.s.invokeUnary(ctx, r, h.impl, h.fullMethod, h.method, dec)
	header, trailer := stream.metadata()
	writeConnectMetadata(w.Header(), header, trailer)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && status.Code(err) == codes.Unknown {
			err = status.FromContextError(ctxErr).Err()
		}
		st, _ := status.FromError(err)
		writeConnectError(w, st)
		return
	}

	var out []byte
	if req.encoding == encodingProto {
		out, err = proto.Marshal(resp)
	} else {
		opts := connectMarshalOptions
		opts.UseProtoNames = !h.s.opts.useCamelCase
		out, err = opts.Marshal(resp)
	}
	if err != nil {
		writeConnectError(w, status.New(codes.Internal, "failed to marshal response: "+err.Error()))
		return
	}
	if h.s.opts.logBody {
		log.Inject(ctx, map[string]any{"response": bodyForLog(req.encoding, out, 2048)})
	}

	w.Header().Set("Content-Type", req.encoding.contentType())
	w.Header().Add("Vary", "Accept-Encoding")
	if len(out) >= compressMinBytes && acceptsGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		zw := gzipWriters.Get().(*gzip.Writer)
		zw.Reset(w)
		_, _ = zw.Write(out)
		_ = zw.Close()
		gzipWriters.Put(zw)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

func (h *connectHandler) allowHeader() string {
	if h.allowGET {
		return "GET, POST"
	}
	return http.MethodPost
}

// parse validates the request and returns its decoded (decompressed) body.
func (h *connectHandler) parse(r *http.Request) (*connectRequest, *status.Status) {
	if v := r.Header.Get("Connect-Protocol-Version"); v != "" && v != connectProtocolVersion {
		return nil, status.Newf(codes.InvalidArgument, "unsupported Connect-Protocol-Version %q", v)
	}
	req := &connectRequest{}
	var raw []byte
	switch r.Method {
	case http.MethodPost:
		ct := strings.ToLower(r.Header.Get("Content-Type"))
		if mt, _, err := mime.ParseMediaType(ct); err == nil {
			ct = mt
		}
		switch {
		case ct == "application/proto":
			req.encoding = encodingProto
		case strings.HasPrefix(ct, "application/connect+"), strings.HasPrefix(ct, "application/grpc"):
			// Streaming or gRPC framing: not a unary Connect request.
			return nil, status.New(codes.Code(connectUnsupportedMediaType), "unsupported content type "+ct+" for a unary procedure")
		default:
			req.encoding = encodingJSON
		}
		switch enc := strings.ToLower(r.Header.Get("Content-Encoding")); enc {
		case "", "identity":
		case "gzip":
			req.compression = "gzip"
		default:
			return nil, status.Newf(codes.Unimplemented, "unsupported Content-Encoding %q (supported: gzip, identity)", enc)
		}
		var st *status.Status
		raw, st = readLimited(r.Body, h.s.opts.maxRequestBodyBytes)
		if st != nil {
			return nil, st
		}
	case http.MethodGet:
		if !h.allowGET {
			return nil, status.New(codes.Unimplemented, "GET is only allowed for methods with idempotency_level = NO_SIDE_EFFECTS")
		}
		q := r.URL.Query()
		if v := q.Get("connect"); v != "" && v != "v1" {
			return nil, status.Newf(codes.InvalidArgument, "unsupported connect version %q", v)
		}
		switch q.Get("encoding") {
		case "proto":
			req.encoding = encodingProto
		case "json":
			req.encoding = encodingJSON
		default:
			return nil, status.Newf(codes.InvalidArgument, "missing or unsupported encoding %q", q.Get("encoding"))
		}
		switch c := q.Get("compression"); c {
		case "", "identity":
		case "gzip":
			req.compression = "gzip"
		default:
			return nil, status.Newf(codes.Unimplemented, "unsupported compression %q", c)
		}
		raw = []byte(q.Get("message"))
		if q.Get("base64") == "1" {
			decoded, err := decodeBase64URL(q.Get("message"))
			if err != nil {
				return nil, status.New(codes.InvalidArgument, "invalid base64 message: "+err.Error())
			}
			raw = decoded
		}
		if int64(len(raw)) > h.s.opts.maxRequestBodyBytes {
			return nil, status.New(codes.ResourceExhausted, "request message too large")
		}
	default:
		return nil, status.New(codes.Unimplemented, "method not allowed")
	}
	if req.compression == "gzip" && len(raw) > 0 {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, status.New(codes.InvalidArgument, "invalid gzip body: "+err.Error())
		}
		var st *status.Status
		raw, st = readLimited(zr, h.s.opts.maxRequestBodyBytes)
		if st != nil {
			return nil, st
		}
	}
	req.body = raw
	return req, nil
}

// connectUnsupportedMediaType is a sentinel code (not a real gRPC code) for
// the one Connect error that is reported as a bare HTTP 415.
const connectUnsupportedMediaType = 415

func readLimited(r io.Reader, limit int64) ([]byte, *status.Status) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, status.New(codes.InvalidArgument, "failed to read request body: "+err.Error())
	}
	if int64(len(b)) > limit {
		return nil, status.Newf(codes.ResourceExhausted, "request body larger than %d bytes", limit)
	}
	return b, nil
}

func decodeBase64URL(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

func acceptsGzip(r *http.Request) bool {
	for part := range strings.SplitSeq(r.Header.Get("Accept-Encoding"), ",") {
		if name, _, _ := strings.Cut(strings.TrimSpace(part), ";"); strings.EqualFold(name, "gzip") {
			return true
		}
	}
	return false
}

func bodyForLog(enc connectEncoding, b []byte, limit int) string {
	if enc == encodingProto {
		return "<binary proto, " + strconv.Itoa(len(b)) + " bytes>"
	}
	if len(b) > limit {
		b = b[:limit]
	}
	return string(b)
}

// writeConnectMetadata copies handler metadata into HTTP headers: header
// metadata as-is, trailer metadata with the "Trailer-" prefix the Connect
// unary protocol uses.
func writeConnectMetadata(h http.Header, header, trailer metadata.MD) {
	for k, vs := range header {
		if reservedMetadataKey(k) {
			continue
		}
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	for k, vs := range trailer {
		if reservedMetadataKey(k) {
			continue
		}
		for _, v := range vs {
			h.Add("Trailer-"+k, v)
		}
	}
}

func reservedMetadataKey(k string) bool {
	k = strings.ToLower(k)
	return strings.HasPrefix(k, ":") || strings.HasPrefix(k, "grpc-") ||
		k == "content-type" || k == "content-length" || k == "content-encoding"
}

// connectError is the Connect protocol error body.
// https://connectrpc.com/docs/protocol/#error-end-stream
type connectError struct {
	Code    string               `json:"code"`
	Message string               `json:"message,omitempty"`
	Details []connectErrorDetail `json:"details,omitempty"`
}

type connectErrorDetail struct {
	Type  string `json:"type"`
	Value string `json:"value"` // base64 (standard, unpadded) of the serialized message
}

// writeConnectError writes a Connect error. Error bodies are always JSON, also
// for binary requests. Codes and HTTP statuses come from mux.CodeToError and
// mux.CodeToHTTPStatus, so codes registered with mux.RegisterErrorCodes keep
// their names and statuses.
func writeConnectError(w http.ResponseWriter, st *status.Status) {
	if st.Code() == codes.Code(connectUnsupportedMediaType) {
		w.Header().Set("Accept-Post", "application/json, application/proto")
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	body := connectError{Code: mux.CodeToError(st.Code()), Message: st.Message()}
	for _, d := range st.Proto().GetDetails() {
		body.Details = append(body.Details, connectErrorDetail{
			Type:  strings.TrimPrefix(d.GetTypeUrl(), "type.googleapis.com/"),
			Value: base64.RawStdEncoding.EncodeToString(d.GetValue()),
		})
	}
	b, err := json.Marshal(body)
	if err != nil {
		b = []byte(`{"code":"internal","message":"failed to marshal error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(mux.CodeToHTTPStatus(st.Code()))
	_, _ = w.Write(b)
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

// RegisterConnectHandler registers a ConnectRPC service handler generated by
// protoc-gen-connect-go (pb.NewXxxServiceHandler). Use it for streaming
// methods, which RegisterConnectService does not bridge. Pass
// WithConnectOptions(gs) to the generated constructor so the handler runs the
// grpcmux interceptor chain and body limit:
//
//	grpcmux.RegisterConnectHandler(gs, pbconnect.NewMyServiceHandler(impl, grpcmux.WithConnectOptions(gs)...))
func RegisterConnectHandler(s *Server, pattern string, handler http.Handler) {
	s.Handle(pattern, handler)
}

// WithConnectOptions returns connect-go handler options that make
// protoc-gen-connect-go handlers behave like the rest of the server: unary
// calls run the grpcmux unary interceptor chain (validator, authz, metrics,
// rate limit, custom interceptors, recovery; see WithoutHTTPInterceptors), and
// request size is limited by WithMaxRequestBodyBytes.
func WithConnectOptions(s *Server) []connect.HandlerOption {
	opts := []connect.HandlerOption{connect.WithReadMaxBytes(int(s.opts.maxRequestBodyBytes))}
	if s.httpUnary != nil {
		opts = append(opts, connect.WithInterceptors(connectUnaryBridge(s.httpUnary)))
	}
	return opts
}

// connectUnaryBridge runs a gRPC unary interceptor chain around connect-go
// unary handlers. Errors cross the boundary in both directions: connect
// errors are presented to interceptors as gRPC statuses and restored after.
func connectUnaryBridge(chain grpc.UnaryServerInterceptor) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if req.Spec().IsClient {
				return next(ctx, req)
			}
			procedure := req.Spec().Procedure
			ctx = grpc.NewContextWithServerTransportStream(ctx, &transportStream{method: procedure})
			ctx = withHTTPPeer(ctx, &http.Request{RemoteAddr: req.Peer().Addr})
			var resp connect.AnyResponse
			var handlerErr error
			info := &grpc.UnaryServerInfo{FullMethod: procedure}
			_, err := chain(ctx, req.Any(), info, func(ctx context.Context, _ any) (any, error) {
				resp, handlerErr = next(ctx, req)
				if handlerErr != nil {
					return nil, connectToStatus(handlerErr)
				}
				return resp.Any(), nil
			})
			if err == nil {
				return resp, nil
			}
			if handlerErr != nil && status.Code(err) == status.Code(connectToStatus(handlerErr)) {
				return nil, handlerErr // pass the handler's error through untouched
			}
			return nil, statusToConnect(err)
		}
	}
}

func connectToStatus(err error) error {
	if cerr, ok := errors.AsType[*connect.Error](err); ok {
		return status.Error(codes.Code(cerr.Code()), cerr.Message())
	}
	return err
}

func statusToConnect(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	cerr := connect.NewError(connect.Code(st.Code()), errors.New(st.Message()))
	for _, d := range st.Proto().GetDetails() {
		if detail, err := connect.NewErrorDetail(d); err == nil {
			cerr.AddDetail(detail)
		}
	}
	return cerr
}
