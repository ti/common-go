// Package grpcmux serves one gRPC service implementation over native gRPC,
// grpc-gateway REST, ConnectRPC and plain HTTP, with logging, metrics,
// tracing, auth, health and graceful shutdown built in.
//
// Ports (all configurable): HTTP on :8080 (HTTP/1.1 + h2c, or TLS), native
// gRPC on :8081, Prometheus metrics on :9090.
package grpcmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/ti/common-go/graceful"
	"github.com/ti/common-go/grpcmux/mux"
	"github.com/ti/common-go/log"
)

// NewServer creates a server with the default middleware. Register services
// on it, then call [Server.Start].
func NewServer(opts ...Option) *Server {
	o := evaluateOptions(opts)
	if o.logger == nil {
		o.logger = interceptorLogger()
	}
	chains := buildInterceptors(o)
	serverOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(chains.grpcUnary...),
		grpc.ChainStreamInterceptor(chains.grpcStream...),
	}
	if !o.withOutKeepAliveOpts {
		serverOpts = append(serverOpts,
			grpc.KeepaliveParams(o.keepaliveParams),
			grpc.KeepaliveEnforcementPolicy(o.keepalivePolicy))
	}
	serverOpts = append(serverOpts, o.grpcServerOpts...)
	if o.tracing {
		serverOpts = append(serverOpts, grpc.StatsHandler(otelgrpc.NewServerHandler()))
	}

	s := &Server{
		Logger:     o.logger,
		opts:       o,
		grpcServer: grpc.NewServer(serverOpts...),
		health:     newHealthServer(o.healthCheckers),
		ctx:        log.NewContextWithLogger(context.Background(), log.Default(false)),
	}
	if !o.withoutHTTPInterceptors {
		s.httpUnary = chainUnaryInterceptors(chains.httpUnary)
	}
	s.initMemConn(serverOpts...)
	pbhealth.RegisterHealthServer(s.grpcServer, s.health)
	pbhealth.RegisterHealthServer(s.bufServer, s.health)

	muxOpts := []mux.Option{
		mux.WithAuthFunc(o.authFunction),
		mux.WithNoAuthPrefixes(o.noAuthPrefix...),
		mux.WithMiddleWares(o.httpMiddleWares...),
	}
	if len(o.preHTTPMiddleWares) > 0 {
		muxOpts = append(muxOpts, mux.WithPreMiddleWares(o.preHTTPMiddleWares...))
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
	if o.cors != nil {
		muxOpts = append(muxOpts, mux.WithCORS(*o.cors))
	}
	s.mux = mux.NewServeMux(muxOpts...)
	return s
}

var noRecovery = os.Getenv("PANIC_RECOVERY") == "false"

// Server serves gRPC, HTTP (REST, ConnectRPC, custom handlers) and metrics.
type Server struct {
	// Logger is the logger used for server lifecycle and gRPC access logs.
	Logger logging.Logger
	// HTTPServer is the running HTTP server. It is nil until Start has
	// created it; do not replace it.
	HTTPServer *http.Server

	opts          *options
	mux           *mux.ServeMux
	httpServerMux *http.ServeMux // custom handlers registered with Handle
	wsServeMux    *http.ServeMux // WebSocket handlers
	health        *simpleHealthServer
	grpcServer    *grpc.Server
	// httpUnary is the interceptor chain run by the HTTP bridges; nil when
	// disabled with WithoutHTTPInterceptors.
	httpUnary grpc.UnaryServerInterceptor

	// In-memory gRPC server and client connection (see Conn).
	bufServer   *grpc.Server
	bufListener *listener
	memConn     *grpc.ClientConn

	handlerOnce sync.Once
	handler     http.Handler

	connectOnce   sync.Once
	connectServer *connect.Server // connect-go v2 handlers, see ConnectServer

	ctx context.Context // carries the server logger

	mu            sync.Mutex // guards HTTPServer and metricsServer, set by the start functions
	metricsServer *http.Server
}

// Start runs the gRPC, HTTP and metrics servers and blocks until the process
// is asked to stop (SIGINT/SIGTERM, graceful.Close, or a server failing to
// start). It then calls [Server.Close] and every other closer registered with
// graceful.AddCloser, and returns.
func (s *Server) Start() {
	graceful.AddCloser(s.Close)
	graceful.Start(s.ctx,
		s.startGRPC,
		s.startMetrics,
		s.startHTTP,
	)
}

// RegisterService registers a service on the gRPC server (and the in-memory
// connection). With [WithAutoHTTP] each unary method is also served as
// POST /{package.Service}/{Method} with a JSON body.
//
// It implements grpc.ServiceRegistrar, so generated pb.RegisterXxxServer(gs, impl)
// functions call it.
func (s *Server) RegisterService(desc *grpc.ServiceDesc, serviceImpl any) {
	s.registerGRPC(desc, serviceImpl)
	if !s.opts.autoHTTP {
		return
	}
	for _, m := range desc.Methods {
		fullMethod := "/" + desc.ServiceName + "/" + m.MethodName
		s.Logger.Log(s.ctx, logging.LevelDebug, "handled", "path", fullMethod)
		s.mux.Handle(http.MethodPost, fullMethod, s.newAutoHTTPHandler(serviceImpl, fullMethod, m))
	}
}

// registerGRPC registers a service on both gRPC servers.
func (s *Server) registerGRPC(desc *grpc.ServiceDesc, serviceImpl any) {
	s.grpcServer.RegisterService(desc, serviceImpl)
	s.bufServer.RegisterService(desc, serviceImpl)
}

// newAutoHTTPHandler serves one unary method as a JSON POST route on the
// grpc-gateway mux, running the HTTP interceptor chain.
func (s *Server) newAutoHTTPHandler(server any, fullMethod string, m grpc.MethodDesc) runtime.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request, _ map[string]string) {
		ctx, cancel := context.WithCancel(req.Context())
		defer cancel()
		serverMux := s.ServeMux()
		inbound, outbound := runtime.MarshalerForRequest(serverMux, req)
		body := io.LimitReader(req.Body, s.opts.maxRequestBodyBytes)
		dec := func(v any) error {
			if err := inbound.NewDecoder(body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
				return invalidArgument(err)
			}
			return nil
		}
		resp, stream, err := s.invokeUnary(ctx, req, server, fullMethod, m, dec)
		if err != nil {
			runtime.HTTPError(ctx, serverMux, outbound, w, req, err)
			return
		}
		header, trailer := stream.metadata()
		ctx = runtime.NewServerMetadataContext(ctx, runtime.ServerMetadata{HeaderMD: header, TrailerMD: trailer})
		runtime.ForwardResponseMessage(ctx, serverMux, outbound, w, req, resp,
			serverMux.GetForwardResponseOptions()...)
	}
}

// Conn returns an in-memory client connection to this server's services. It
// goes through the full gRPC interceptor chain (including auth), without a
// network hop. Typical use: serve grpc-gateway routes with interceptors,
//
//	pb.RegisterXxxHandlerClient(ctx, gs.ServeMux(), pb.NewXxxClient(gs.Conn()))
func (s *Server) Conn() *grpc.ClientConn {
	return s.memConn
}

func (s *Server) initMemConn(opts ...grpc.ServerOption) {
	s.bufListener = newListener()
	conn, err := grpc.NewClient("passthrough:///buf",
		grpc.WithContextDialer(s.bufListener.DialContext),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		panic(errors.New("grpcmux: in-memory connection: " + err.Error()))
	}
	s.memConn = conn
	s.bufServer = grpc.NewServer(opts...)
}

// HandleFunc registers a route on the grpc-gateway mux (supports
// path templates such as /v1/items/{id}).
func (s *Server) HandleFunc(method, path string, h runtime.HandlerFunc) {
	s.mux.Handle(method, path, h)
}

// AddHealthChecker registers a custom health check function. All checkers
// run on every health Check (gRPC health and HTTP /readyz); any error reports
// NOT_SERVING. Safe to call at any time.
func (s *Server) AddHealthChecker(checker HealthChecker) {
	s.health.addChecker(checker)
}

// Handle registers a custom HTTP handler (net/http ServeMux pattern). It runs
// behind the same middleware as gateway routes: CORS, request ID, logging,
// auth and recovery. Register handlers before Start.
func (s *Server) Handle(pattern string, handler http.Handler) {
	if s.httpServerMux == nil {
		s.httpServerMux = http.NewServeMux()
	}
	s.httpServerMux.Handle(pattern, handler)
}

// HandleWebSocket registers a WebSocket handler for the given pattern.
// Unlike Handle/HandleFunc, WebSocket handlers registered here are served
// by a dedicated mux that intercepts upgrade requests before the main handler.
// This ensures WebSocket upgrade requests bypass HTTP/2 routing.
//
// The handler receives plain HTTP/1.1 upgrade requests and should perform
// the WebSocket handshake itself (e.g. via gorilla/websocket Upgrader).
//
// Auth middleware is NOT applied to these handlers — add your own auth logic
// inside the handler if needed.
func (s *Server) HandleWebSocket(pattern string, handler http.Handler) {
	if s.wsServeMux == nil {
		s.wsServeMux = http.NewServeMux()
	}
	s.wsServeMux.Handle(pattern, handler)
}

// ServeMux returns the grpc-gateway mux, for pb.RegisterXxxHandlerServer.
func (s *Server) ServeMux() *runtime.ServeMux {
	return s.mux.ServeMux()
}

// GrpcServer returns the native *grpc.Server.
func (s *Server) GrpcServer() *grpc.Server {
	return s.grpcServer
}

// httpHandler builds the HTTP handler once: built-in routes, custom handlers
// with the gateway mux as fallback, WebSocket dispatch and tracing.
func (s *Server) httpHandler() http.Handler {
	s.handlerOnce.Do(func() {
		// Liveness: the process is up and serving HTTP. Never fails because of
		// dependencies or draining, so a liveness probe never restarts a pod
		// that is merely unready.
		s.HandleFunc(http.MethodGet, "/healthz", func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
			_, _ = io.WriteString(w, pbhealth.HealthCheckResponse_SERVING.String())
		})
		// Readiness: the gRPC health status (NOT_SERVING before start, while
		// draining, or when a health checker fails).
		s.HandleFunc(http.MethodGet, "/readyz", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
			if !s.health.ready(r.Context()) {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, pbhealth.HealthCheckResponse_NOT_SERVING.String())
				return
			}
			_, _ = io.WriteString(w, pbhealth.HealthCheckResponse_SERVING.String())
		})
		s.HandleFunc(http.MethodGet, "/favicon.ico", func(w http.ResponseWriter, _ *http.Request, _ map[string]string) {
			w.Header().Set("Cache-Control", "public, max-age=31536000")
			w.Header().Set("Expires", time.Now().AddDate(1, 0, 0).Format(time.RFC1123))
		})
		s.HandleFunc(http.MethodGet, "/debug/**", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
			http.DefaultServeMux.ServeHTTP(w, r)
		})

		if s.connectServer != nil {
			if s.httpServerMux == nil {
				s.httpServerMux = http.NewServeMux()
			}
			s.mountConnect(s.httpServerMux)
		}
		var handler http.Handler
		if s.httpServerMux == nil {
			handler = s.mux
		} else {
			// Custom handlers (registered via Handle) are served by httpServerMux,
			// with the raw gateway mux as the fallback for every other path. The
			// middleware chain (logging, auth, recovery, request ID, ...) wraps
			// the combined handler exactly once.
			s.httpServerMux.Handle("/", s.mux.ServeMux())
			handler = s.mux.Middleware(s.httpServerMux)
		}
		// WebSocket upgrades go to wsServeMux before anything else.
		if s.wsServeMux != nil {
			wsMux, inner := s.wsServeMux, handler
			handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
					wsMux.ServeHTTP(w, r)
					return
				}
				inner.ServeHTTP(w, r)
			})
		}
		// Connect and gateway requests on the HTTP port don't pass through the
		// gRPC StatsHandler, so trace them here. Spans are named after the
		// request path (for an RPC, /pkg.Service/Method).
		if s.opts.tracing {
			handler = otelhttp.NewHandler(handler, "http.server",
				otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
					if r.URL != nil && r.URL.Path != "" {
						return r.Method + " " + r.URL.Path
					}
					return r.Method
				}),
			)
		}
		s.handler = handler
	})
	return s.handler
}

// newHTTPServer configures HTTP/1.1 plus HTTP/2 (h2c by default, h2 with TLS)
// using only the standard library.
//
// There is no ReadTimeout or WriteTimeout on purpose: streaming RPCs (gRPC
// server streaming, Connect streams, WebSockets) are long-lived, and a write
// timeout kills them mid-stream ("missing EndStreamResponse"). Only the header
// phase is bounded.
func (s *Server) newHTTPServer(handler http.Handler, useTLS bool) *http.Server {
	protocols := &http.Protocols{}
	protocols.SetHTTP1(true)
	if useTLS {
		protocols.SetHTTP2(true)
	} else {
		protocols.SetUnencryptedHTTP2(true)
	}
	return &http.Server{
		Addr:                  s.opts.httpAddr,
		Handler:               handler,
		Protocols:             protocols,
		ReadHeaderTimeout:     s.opts.readHeaderTimeout,
		IdleTimeout:           s.opts.idleTimeout,
		MaxHeaderBytes:        s.opts.maxHeaderBytes,
		DisableClientPriority: s.opts.disableClientPriority,
		HTTP2: &http.HTTP2Config{
			MaxConcurrentStreams: s.opts.maxConcurrentStreams,
		},
	}
}

func (s *Server) startHTTP(ctx context.Context) error {
	tlsCert, tlsKey := s.opts.tlsCertFile, s.opts.tlsKeyFile
	useTLS := tlsCert != "" && tlsKey != ""
	srv := s.newHTTPServer(s.httpHandler(), useTLS)
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.opts.httpAddr)
	if err != nil {
		return fmt.Errorf("start http: %w", err)
	}
	s.mu.Lock()
	s.HTTPServer = srv
	s.mu.Unlock()
	if useTLS {
		s.Logger.Log(ctx, logging.LevelInfo, "Start https (TLS) at "+s.opts.httpAddr)
		err = srv.ServeTLS(lis, tlsCert, tlsKey)
	} else {
		s.Logger.Log(ctx, logging.LevelInfo, "Start http at "+s.opts.httpAddr)
		err = srv.Serve(lis)
	}
	if !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http: %w", err)
	}
	return nil
}

func (s *Server) startGRPC(ctx context.Context) error {
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.opts.grpcAddr)
	if err != nil {
		s.Logger.Log(s.ctx, logging.LevelError, "Listen grpc "+s.opts.grpcAddr+" error for "+err.Error())
		return fmt.Errorf("start grpc: %w", err)
	}
	go func() { _ = s.bufServer.Serve(s.bufListener) }()
	s.health.setServing(true)
	s.Logger.Log(ctx, logging.LevelInfo, "Start grpc at "+s.opts.grpcAddr)
	if err := s.grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serve grpc: %w", err)
	}
	return nil
}

// startMetrics serves Prometheus metrics. An empty metrics address disables it.
func (s *Server) startMetrics(ctx context.Context) error {
	if s.opts.metricsAddr == "" {
		return nil
	}
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.HandlerFor(prometheus.DefaultGatherer,
		promhttp.HandlerOpts{EnableOpenMetrics: true}))
	srv := &http.Server{
		Addr:              s.opts.metricsAddr,
		ReadHeaderTimeout: 3 * time.Second,
		Handler:           httpMux,
	}
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.opts.metricsAddr)
	if err != nil {
		return fmt.Errorf("start metrics: %w", err)
	}
	s.mu.Lock()
	s.metricsServer = srv
	s.mu.Unlock()
	s.Logger.Log(ctx, logging.LevelInfo, "Start metrics at "+s.opts.metricsAddr)
	if err := srv.Serve(lis); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve metrics: %w", err)
	}
	return nil
}

// maxPreStop caps the PRE_STOP wait.
const maxPreStop = 5 * time.Minute

// preStopDelay parses PRE_STOP: unset, "0" or "0s" → no wait; a duration →
// that wait, capped at 5 minutes; anything unparsable → 1 minute.
func preStopDelay() time.Duration {
	v := os.Getenv("PRE_STOP")
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	switch {
	case err != nil:
		return time.Minute
	case d <= 0:
		return 0
	case d > maxPreStop:
		return maxPreStop
	}
	return d
}

// Close shuts the server down gracefully:
//
//  1. health switches to NOT_SERVING (gRPC health and /readyz), so readiness
//     probes take the pod out of rotation;
//  2. it waits PRE_STOP (see preStopDelay) while load balancers catch up;
//     requests keep being served during the wait;
//  3. HTTP Shutdown and gRPC GracefulStop run in parallel and wait for
//     in-flight requests and streams;
//  4. if ctx expires first, remaining connections are closed forcibly.
//
// Kubernetes' terminationGracePeriodSeconds must exceed PRE_STOP plus the
// longest request you want to let finish.
func (s *Server) Close(ctx context.Context) error {
	s.health.setServing(false)
	s.mu.Lock()
	httpServer, metricsServer := s.HTTPServer, s.metricsServer
	s.mu.Unlock()
	if httpServer != nil {
		httpServer.SetKeepAlivesEnabled(false)
	}
	if d := preStopDelay(); d > 0 {
		s.Logger.Log(s.ctx, logging.LevelInfo, fmt.Sprintf("wait %s (PRE_STOP) before stopping", d))
		select {
		case <-time.After(d):
		case <-ctx.Done():
		}
	}

	var wg sync.WaitGroup
	var httpErr error
	if httpServer != nil {
		wg.Go(func() {
			if err := httpServer.Shutdown(ctx); err != nil {
				httpErr = fmt.Errorf("shutdown http: %w", err)
				_ = httpServer.Close()
			}
		})
	}
	var grpcErr error
	wg.Go(func() {
		stopped := make(chan struct{})
		go func() {
			s.grpcServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-ctx.Done():
			s.grpcServer.Stop()
			grpcErr = fmt.Errorf("stop grpc: %w", ctx.Err())
		}
	})
	wg.Wait()

	s.bufServer.Stop()
	_ = s.bufListener.Close()
	_ = s.memConn.Close()
	var metricsErr error
	if metricsServer != nil {
		metricsErr = metricsServer.Shutdown(ctx)
	}
	return errors.Join(httpErr, grpcErr, metricsErr)
}

// listener is an in-memory net.Listener backing Conn.
type listener struct {
	connections chan net.Conn
	done        chan struct{}
	closeOnce   sync.Once
}

func newListener() *listener {
	return &listener{connections: make(chan net.Conn), done: make(chan struct{})}
}

// Accept implements net.Listener.
func (l *listener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	case c := <-l.connections:
		return c, nil
	}
}

// Close implements net.Listener.
func (l *listener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return nil
}

// DialContext connects a client to the in-memory server.
func (l *listener) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	serverSide, clientSide := net.Pipe()
	select {
	case l.connections <- serverSide:
		return clientSide, nil
	case <-l.done:
	case <-ctx.Done():
	}
	_ = serverSide.Close()
	_ = clientSide.Close()
	return nil, net.ErrClosed
}

// Addr implements net.Listener.
func (l *listener) Addr() net.Addr {
	return memAddr{}
}

type memAddr struct{}

func (memAddr) Network() string { return "memory" }
func (memAddr) String() string  { return "grpcmux-memconn" }

// chainUnaryInterceptors composes interceptors into one, outermost first.
// Each call builds its own chain, so an interceptor may invoke its handler
// more than once (for example, to retry).
func chainUnaryInterceptors(interceptors []grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	switch len(interceptors) {
	case 0:
		return nil
	case 1:
		return interceptors[0]
	}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return interceptors[0](ctx, req, info, chainedHandler(interceptors, 0, info, handler))
	}
}

func chainedHandler(interceptors []grpc.UnaryServerInterceptor, curr int,
	info *grpc.UnaryServerInfo, final grpc.UnaryHandler,
) grpc.UnaryHandler {
	if curr == len(interceptors)-1 {
		return final
	}
	return func(ctx context.Context, req any) (any, error) {
		return interceptors[curr+1](ctx, req, info, chainedHandler(interceptors, curr+1, info, final))
	}
}
