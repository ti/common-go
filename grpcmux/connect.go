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

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
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
// Streaming methods are not bridged; serve them with connect-go v2 generated
// handlers registered on Server.ConnectServer.
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

// RegisterConnectHandler mounts an http.Handler for a ConnectRPC service, for
// example one generated by connect-go v1 (pbconnect.NewXxxServiceHandler). It
// runs behind the HTTP middleware but not the gRPC interceptor chain. For
// connect-go v2 generated code, register on [Server.ConnectServer] instead.
func RegisterConnectHandler(s *Server, pattern string, handler http.Handler) {
	s.Handle(pattern, handler)
}

// ConnectServer returns the connect-go v2 server of this grpcmux server.
// Register protoc-gen-connect-go (v2) handlers on it — that is how to serve
// streaming methods over Connect:
//
//	userv1connect.RegisterUserServiceHandler(gs.ConnectServer(), impl)
//
// Its procedures are served on the HTTP port over the Connect, gRPC and
// gRPC-Web protocols (connecthttp), behind the HTTP middleware (CORS, request
// ID, auth, access log). Unary calls run the same interceptor chain as native
// gRPC (see WithoutHTTPInterceptors), JSON follows the server's
// snake_case/camelCase setting, and request size is limited by
// WithMaxRequestBodyBytes. Register handlers before Start.
func (s *Server) ConnectServer() *connect.Server {
	s.connectOnce.Do(func() {
		// Outermost: handlers return gRPC status errors (one implementation
		// for every protocol); connect-go v2 only sends *connect.Error.
		interceptors := []connect.ServerInterceptor{statusErrorInterceptor}
		if s.httpUnary != nil {
			interceptors = append(interceptors, connectUnaryInterceptor(s.httpUnary))
		}
		s.connectServer = connect.NewServer(interceptors...)
	})
	return s.connectServer
}

// mountConnect serves the connect-go v2 server's procedures on mux.
func (s *Server) mountConnect(mux *http.ServeMux) {
	jsonCodec := &protoJSONCodec{
		marshal:   protojson.MarshalOptions{UseProtoNames: !s.opts.useCamelCase},
		unmarshal: connectUnmarshalOptions,
	}
	connecthttp.Mount(mux, s.connectServer,
		connecthttp.WithCodecs(connectproto.NewBinaryCodec(), jsonCodec),
		connecthttp.WithReadMaxBytes(int(s.opts.maxRequestBodyBytes)),
	)
}

// protoJSONCodec is the "json" codec for connect-go v2 handlers.
type protoJSONCodec struct {
	marshal   protojson.MarshalOptions
	unmarshal protojson.UnmarshalOptions
}

func (c *protoJSONCodec) Name() string { return "json" }

func (c *protoJSONCodec) MarshalWrite(_ context.Context, dst io.Writer, msg any) error {
	m, ok := msg.(proto.Message)
	if !ok {
		return fmt.Errorf("json codec: %T is not a proto.Message", msg)
	}
	b, err := c.marshal.Marshal(m)
	if err != nil {
		return err
	}
	_, err = dst.Write(b)
	return err
}

func (c *protoJSONCodec) UnmarshalRead(_ context.Context, src io.Reader, msg any) error {
	m, ok := msg.(proto.Message)
	if !ok {
		return fmt.Errorf("json codec: %T is not a proto.Message", msg)
	}
	b, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return c.unmarshal.Unmarshal(b, m)
}

// statusErrorInterceptor converts gRPC status errors returned by connect-go
// handlers (unary and streaming) into *connect.Error with the same code,
// message and details.
func statusErrorInterceptor(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		err := next(ctx, spec, stream)
		if err == nil {
			return nil
		}
		if _, ok := errors.AsType[*connect.Error](err); ok {
			return err
		}
		return statusToConnect(err)
	}
}

// connectUnaryInterceptor runs a gRPC unary interceptor chain around
// connect-go v2 unary handlers. The request is received first (its type comes
// from the method's protobuf schema) so interceptors see it, then replayed to
// the handler. gRPC status errors from interceptors become *connect.Error
// (connect-go v2 only sends a message for *connect.Error), and handler errors
// pass through unchanged.
func connectUnaryInterceptor(chain grpc.UnaryServerInterceptor) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			md, ok := spec.Schema.(protoreflect.MethodDescriptor)
			if spec.StreamType != connect.StreamTypeUnary || !ok {
				return next(ctx, spec, stream)
			}
			mt, err := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
			if err != nil {
				return next(ctx, spec, stream)
			}
			req := mt.New().Interface()
			if err := stream.Receive(req); err != nil {
				return err
			}
			if info, ok := connect.CallInfoForServerContext(ctx); ok {
				ctx = withHTTPPeer(ctx, &http.Request{RemoteAddr: info.PeerAddr})
			}
			ctx = grpc.NewContextWithServerTransportStream(ctx, &transportStream{method: spec.Procedure})
			replay := &replayStream{ServerStream: stream, first: req}
			var handlerErr error
			_, err = chain(ctx, req, &grpc.UnaryServerInfo{FullMethod: spec.Procedure},
				func(ctx context.Context, _ any) (any, error) {
					handlerErr = next(ctx, spec, replay)
					if handlerErr != nil {
						return nil, connectToStatus(handlerErr)
					}
					return replay.sent, nil
				})
			switch {
			case err == nil:
				return nil
			case handlerErr != nil && status.Code(err) == status.Code(connectToStatus(handlerErr)):
				return handlerErr
			default:
				return statusToConnect(err)
			}
		}
	}
}

// replayStream hands an already-received request to the handler and records
// the response it sends.
type replayStream struct {
	connect.ServerStream
	first any
	sent  any
}

func (r *replayStream) Receive(msg any) error {
	if r.first == nil {
		return io.EOF
	}
	src, ok1 := r.first.(proto.Message)
	dst, ok2 := msg.(proto.Message)
	if !ok1 || !ok2 {
		return fmt.Errorf("replay: %T/%T are not proto messages", r.first, msg)
	}
	proto.Reset(dst)
	proto.Merge(dst, src)
	r.first = nil
	return nil
}

func (r *replayStream) Send(msg any) error {
	r.sent = msg
	return r.ServerStream.Send(msg)
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
	cerr := connect.NewError(connect.Code(st.Code()), st.Message()).WithCause(err)
	for _, d := range st.Proto().GetDetails() {
		cerr = cerr.WithDetail(&connect.ErrorDetail{
			Type:  strings.TrimPrefix(d.GetTypeUrl(), "type.googleapis.com/"),
			Value: d.GetValue(),
		})
	}
	return cerr
}
