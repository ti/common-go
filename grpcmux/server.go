// Package grpcmux implements grpc, grpc gateway, and grpc middlewares.
package grpcmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/ti/common-go/log"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/grpc-ecosystem/grpc-gateway/v2/utilities"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/ti/common-go/graceful"
	"github.com/ti/common-go/grpcmux/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// NewServer new grpc server with all common middleware.
func NewServer(opts ...Option) *Server {
	o := evaluateOptions(opts)
	svc := &Server{
		opts:   o,
		locker: &sync.Mutex{},
	}
	if o.logger == nil {
		o.logger = interceptorLogger()
	}
	unaryServerInterceptors, streamInterceptors := NewFullMiddleWare(withOptions(o))
	serverOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unaryServerInterceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
	}
	if !o.withOutKeepAliveOpts {
		serverOpts = append(serverOpts, grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionAge: time.Minute,
		}))
	}

	if len(o.grpcServerOpts) > 0 {
		serverOpts = append(serverOpts, o.grpcServerOpts...)
	}
	if o.tracing {
		serverOpts = append(serverOpts, grpc.StatsHandler(otelgrpc.NewServerHandler()))
	}
	gs := grpc.NewServer(serverOpts...)
	svc.grpcServer = gs
	svc.Logger = o.logger
	svc.initMemConnListener(serverOpts...)
	muxOpts := []mux.Option{
		mux.WithAuthFunc(o.authFunction),
		mux.WithNoAuthPrefixes(o.noAuthPrefix...),
	}
	if o.logBody {
		muxOpts = append(muxOpts, mux.WithLogBody())
	}
	if noRecovery {
		muxOpts = append(muxOpts, mux.WithoutRecovery())
	}
	if o.useCamelCase {
		muxOpts = append(muxOpts, mux.WithUseCamelCase())
	}
	if svc.useMemConn {
		muxOpts = append(muxOpts, mux.WithOutLog())
	} else {
		muxOpts = append(muxOpts, mux.WithMiddleWares(o.httpMiddleWares...))
	}
	svc.mux = mux.NewServeMux(muxOpts...)
	svc.unaryServerInterceptor = chainUnaryInterceptors(unaryServerInterceptors)
	svc.ctx = log.NewContextWithLogger(context.Background(),
		log.Default(false))
	return svc
}

var noRecovery = false

func init() {
	panicRecovery := os.Getenv("PANIC_RECOVERY")
	if panicRecovery == "false" {
		noRecovery = true
	}
}

// Server the grpc server
type Server struct {
	// Logger the grpc default logger with auto flush on close
	Logger        logging.Logger
	mux           *mux.ServeMux
	httpServerMux *http.ServeMux
	HTTPServer    *http.Server
	healthServer  *simpleHealthServer
	// GrpcServer the *grpc..Server
	grpcServer             *grpc.Server
	bufServer              *grpc.Server
	opts                   *options
	locker                 sync.Locker
	memConn                *grpc.ClientConn
	bufListener            net.Listener
	unaryServerInterceptor grpc.UnaryServerInterceptor
	useMemConn             bool
	ctx                    context.Context
}

// Start the service.
func (s *Server) Start() {
	graceful.AddCloser(s.Close)
	graceful.Start(s.ctx,
		s.startGRPC,
		s.startMetrics,
		s.startHTTP,
	)
}

// RegisterService register service functions
func (s *Server) RegisterService(desc *grpc.ServiceDesc, serviceImpl any) {
	s.grpcServer.RegisterService(desc, serviceImpl)
	if !s.opts.autoHTTP {
		return
	}
	for _, v := range desc.Methods {
		httpPath := "/" + desc.ServiceName + "/" + v.MethodName
		s.Logger.Log(s.ctx, logging.LevelDebug, "handled", "path", httpPath)
		s.mux.Handle(http.MethodPost, httpPath, s.newGRPCAsHTTPHandler(serviceImpl, v))
	}
}

func (s *Server) newGRPCAsHTTPHandler(server any, v grpc.MethodDesc) func(w http.ResponseWriter,
	r *http.Request, _ map[string]string) {
	return func(w http.ResponseWriter, req *http.Request, _ map[string]string) {
		ctx, cancel := context.WithCancel(req.Context())
		defer cancel()
		var stream runtime.ServerTransportStream
		ctx = grpc.NewContextWithServerTransportStream(ctx, &stream)
		serverMux := s.ServeMux()
		inboundMarshaler, outboundMarshaler := runtime.MarshalerForRequest(serverMux, req)
		resp, err := localRequest(ctx, inboundMarshaler, server, req, v.MethodName)
		if err != nil {
			runtime.HTTPError(ctx, serverMux, outboundMarshaler, w, req, err)
			return
		}
		md := runtime.ServerMetadata{
			HeaderMD:  stream.Header(),
			TrailerMD: stream.Trailer(),
		}
		ctx = runtime.NewServerMetadataContext(ctx, md)
		runtime.ForwardResponseMessage(ctx, serverMux, outboundMarshaler, w, req, resp,
			serverMux.GetForwardResponseOptions()...)
	}
}

func localRequest(ctx context.Context, marshaler runtime.Marshaler,
	server any, req *http.Request, methodName string,
) (proto.Message, error) {
	newReader, berr := utilities.IOReaderFactory(req.Body)
	if berr != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", berr)
	}
	method := reflect.ValueOf(server).MethodByName(methodName)
	if !method.IsValid() {
		return nil, status.Errorf(codes.Unimplemented, "method %s is valid", methodName)
	}
	methodType := method.Type()
	if methodType.NumIn() != 2 || methodType.NumOut() != 2 {
		return nil, status.Errorf(codes.Unimplemented, "method %s may not a unary service", methodName)
	}
	callParams := make([]reflect.Value, 2)
	callParams[0] = reflect.ValueOf(ctx)
	in := reflect.New(methodType.In(1).Elem())
	protoReq := in.Interface()
	if err := marshaler.NewDecoder(newReader()).Decode(protoReq); err != nil && !errors.Is(err, io.EOF) {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	callParams[1] = in
	resps := method.Call(callParams)
	if err := resps[1].Interface(); err != nil {
		return nil, err.(error)
	}
	msg := resps[0].Interface()
	return msg.(proto.Message), nil
}

// startMetrics start metrics
func (s *Server) startMetrics(ctx context.Context) error {
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.HandlerFor(
		prometheus.DefaultGatherer,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		},
	))
	s.Logger.Log(ctx, logging.LevelInfo, "Start metrics at "+s.opts.metricsAddr)
	server := http.Server{
		Addr:              s.opts.metricsAddr,
		ReadHeaderTimeout: 3 * time.Second,
		Handler:           httpMux,
	}
	err := server.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return errors.New("Start metrics failed for " + err.Error())
	}
	return nil
}

// Conn get the grpc client conn.
func (s *Server) Conn() *grpc.ClientConn {
	s.useMemConn = true
	return s.memConn
}

func (s *Server) initMemConnListener(opts ...grpc.ServerOption) {
	bufListener := newListener()
	var err error
	s.memConn, err = grpc.DialContext(context.Background(), "buf",
		grpc.WithContextDialer(bufListener.DialContext),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	s.bufListener = bufListener
	if err != nil {
		panic(fmt.Errorf("Conn buf failed for " + err.Error()))
	}
	s.bufServer = grpc.NewServer(opts...)
}

// HandleFunc registers custom HTTP routes.
func (s *Server) HandleFunc(method, path string, h runtime.HandlerFunc) {
	s.mux.Handle(method, path, h)
}

// Handle registers custom HTTP handler
func (s *Server) Handle(pattern string, handler http.Handler) {
	if s.httpServerMux == nil {
		s.httpServerMux = http.NewServeMux()
	}
	s.httpServerMux.Handle(pattern, handler)
}

// ServeMux returns the native server mux of grpc gateay
func (s *Server) ServeMux() *runtime.ServeMux {
	return s.mux.ServeMux()
}

// GrpcServer Return grpc native grpc server
func (s *Server) GrpcServer() *grpc.Server {
	return s.grpcServer
}

// startHttp start http
func (s *Server) startHTTP(ctx context.Context) error {
	s.HandleFunc(http.MethodGet, "/healthz", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		_, _ = w.Write([]byte(pbhealth.HealthCheckResponse_SERVING.String()))
	})
	s.HandleFunc(http.MethodGet, "/favicon.ico", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		w.Header().Set("Cache-Control", "public, max-age=31536000")
		w.Header().Set("Expires", time.Now().AddDate(1, 0, 0).Format(time.RFC1123))
	})
	s.HandleFunc(http.MethodGet, "/debug/**", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		http.DefaultServeMux.ServeHTTP(w, r)
	})
	if s.useMemConn {
		go func() {
			pbhealth.RegisterHealthServer(s.bufServer, s.healthServer)
			if errServer := s.bufServer.Serve(s.bufListener); errServer != nil {
				panic(errServer)
			}
		}()
	}
	var handler http.Handler
	if s.httpServerMux == nil {
		handler = s.mux
	} else {
		s.httpServerMux.Handle("/", s.mux)
		handler = s.httpServerMux
	}
	h2Handler := h2c.NewHandler(handler, &http2.Server{
		IdleTimeout:          time.Minute,
		MaxConcurrentStreams: 1000,
	})
	s.HTTPServer = &http.Server{
		Addr:              s.opts.httpAddr,
		Handler:           h2Handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       5 * time.Minute,
		WriteTimeout:      90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	s.Logger.Log(ctx, logging.LevelInfo, "Start http at "+s.opts.httpAddr)
	err := s.HTTPServer.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return errors.New("Start http failed for " + err.Error())
	}
	return nil
}

// startGRPC start grpc
func (s *Server) startGRPC(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.opts.grpcAddr)
	if err != nil {
		s.Logger.Log(s.ctx, logging.LevelError, "Listen grpc "+s.opts.grpcAddr+" error for "+err.Error())
		return err
	}

	healthServer := health.NewServer()
	s.healthServer = &simpleHealthServer{healthServer}
	pbhealth.RegisterHealthServer(s.grpcServer, s.healthServer)
	s.healthServer.server.SetServingStatus(allServices, pbhealth.HealthCheckResponse_SERVING)
	s.Logger.Log(ctx, logging.LevelInfo, "Start grpc at "+s.opts.grpcAddr)
	err = s.grpcServer.Serve(lis)
	if err != nil {
		err = errors.New("Start grpc failed for " + err.Error())
	}
	return err
}

// Close service
func (s *Server) Close(ctx context.Context) error {
	if s.HTTPServer != nil {
		s.HTTPServer.SetKeepAlivesEnabled(false)
	}
	if s.healthServer != nil {
		s.healthServer.server.SetServingStatus(allServices, pbhealth.HealthCheckResponse_NOT_SERVING)
	}
	// The default timeout is 1 minute before closing, K8S service closing strategy.
	// refer: https://kubernetes.io/docs/concepts/containers/container-lifecycle-hooks/
	preStop := os.Getenv("PRE_STOP")
	if preStop != "" {
		preStopDuration, _ := time.ParseDuration(preStop)
		if preStopDuration == 0 || preStopDuration > 5*time.Minute {
			preStopDuration = time.Minute
		}
		s.Logger.Log(s.ctx, logging.LevelDebug, fmt.Sprintf("wait %s to stop the service", preStopDuration))
		time.Sleep(preStopDuration)
	}
	if s.HTTPServer != nil {
		err := s.HTTPServer.Shutdown(ctx)
		if err != nil {
			s.Logger.Log(s.ctx, logging.LevelWarn, "Shutdown http failed for "+err.Error())
		}
	}
	ok := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(ok)
	}()
	select {
	case <-ok:
		return nil
	case <-ctx.Done():
		s.grpcServer.Stop()
		return ctx.Err()
	}
}

type listener struct {
	addr        net.Addr
	connections chan net.Conn
	done        chan struct{}
	mu          sync.Mutex
}

func newListener() *listener {
	return &listener{connections: make(chan net.Conn), done: make(chan struct{})}
}

// Accept implement Accept
func (l *listener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	case c := <-l.connections:
		return c, nil
	}
}

// Close the implement close
func (l *listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	select {
	case <-l.done:
		break
	default:
		close(l.done)
	}
	return nil
}

// DialContext implement DialContext
func (l *listener) DialContext(_ context.Context, _ string) (net.Conn, error) {
	serverSide, clientSide := net.Pipe()
	l.addr = serverSide.LocalAddr()
	l.connections <- serverSide
	return clientSide, nil
}

// Addr implement Addr
func (l *listener) Addr() net.Addr {
	return l.addr
}

func chainUnaryInterceptors(interceptors []grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	if len(interceptors) == 0 {
		return nil
	}
	if len(interceptors) == 1 {
		return interceptors[0]
	}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		var i int
		var next grpc.UnaryHandler
		next = func(ctx context.Context, req any) (any, error) {
			if i == len(interceptors)-1 {
				return interceptors[i](ctx, req, info, handler)
			}
			i++
			return interceptors[i-1](ctx, req, info, next)
		}
		return next(ctx, req)
	}
}
