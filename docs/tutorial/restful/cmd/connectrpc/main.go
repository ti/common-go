// Package main demonstrates ConnectRPC coexisting with grpcmux (gRPC + REST gateway).
//
// # Architecture
//
//	Client
//	  │
//	  │  (HTTPS, TLS terminated)                (h2c, cleartext HTTP/2 + HTTP/1.1)
//	  ▼                                                     ▼
//	Envoy Gateway ──────────────────────────────► This Server (:8080)
//	                                               ├─ /pb.UserService/*   ConnectRPC handler
//	                                               │    supports: Connect + gRPC + gRPC-Web
//	                                               ├─ /v1/users/**        gRPC-Gateway REST
//	                                               └─ /healthz, /debug/** built-ins
//	                                             gRPC Server (:8081)
//	                                               └─ native gRPC (binary, HTTP/2)
//
// # Server Modes
//
// Mode 1 – h2c (default, recommended for Envoy/proxy deployments):
//
//	TLS terminates at Envoy Gateway. The Go server runs h2c (cleartext HTTP/2),
//	accepting both HTTP/1.1 and HTTP/2 on the same port.
//	Browser → HTTPS → Envoy → h2c → Go server
//
//	  go run . -http :8080 -grpc :8081
//
// Mode 2 – TLS (direct HTTPS, no proxy needed):
//
//	The Go server handles TLS itself. HTTP/2 is negotiated via ALPN.
//	Browser → HTTPS → Go server directly.
//
//	  go run . -http :8443 -grpc :8081 -tls-cert cert.pem -tls-key key.pem
//	  # OR via environment variables:
//	  TLS_CERT=cert.pem TLS_KEY=key.pem go run . -http :8443
//
// # ConnectRPC Protocols (all supported simultaneously, zero config)
//
//   - Connect protocol  → Content-Type: application/connect+json   (browser-native, JSON)
//   - Connect protocol  → Content-Type: application/connect+proto  (binary)
//   - gRPC protocol     → Content-Type: application/grpc+proto     (standard gRPC)
//   - gRPC-Web protocol → Content-Type: application/grpc-web+proto (browser gRPC-Web)
//
// # CORS
//
// For browser access, CORS is required when the frontend origin differs from the API origin.
// This example uses connectrpc.com/cors helpers to configure allowed headers correctly.
// In production behind Envoy Gateway, CORS can also be handled at the gateway level.
//
// # Frontend
//
// See docs/tutorial/restful/frontend/connectrpc/ for the TypeScript/Vite frontend.
package main

import (
	"context"
	"flag"
	"net/http"
	"os"

	"connectrpc.com/connect"
	corsmw "connectrpc.com/cors"
	"github.com/rs/cors"
	"github.com/ti/common-go/config"
	"github.com/ti/common-go/dependencies"
	"github.com/ti/common-go/dependencies/database"
	pb "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"
	"github.com/ti/common-go/docs/tutorial/restful/service"
	"github.com/ti/common-go/grpcmux"
	"github.com/ti/common-go/log"

	// Mock database driver for local development / testing.
	// Replace with the real driver import in production:
	//   _ "github.com/ti/common-go/dependencies/mongo"    // MongoDB
	//   _ "github.com/ti/common-go/dependencies/sql"      // MySQL / PostgreSQL
	_ "github.com/ti/common-go/dependencies/database/mock"
)

func main() {
	// ── CLI flags (override config file) ──────────────────────────────────────
	httpAddr := flag.String("http", "", "HTTP listen address (e.g. :8080)")
	grpcAddr := flag.String("grpc", "", "gRPC listen address (e.g. :8081)")
	tlsCert := flag.String("tls-cert", os.Getenv("TLS_CERT"), "TLS certificate file (enables HTTPS mode)")
	tlsKey := flag.String("tls-key", os.Getenv("TLS_KEY"), "TLS key file (enables HTTPS mode)")
	flag.Parse()

	// ── Load configuration ────────────────────────────────────────────────────
	var cfg Config
	if err := config.Init(context.Background(), "", &cfg, dependencies.WithNewFns(database.New)); err != nil {
		log.Action("InitConfig").Fatal(err.Error())
	}

	// Flag overrides take precedence over config file.
	if *httpAddr != "" {
		cfg.Apis.HTTPAddr = *httpAddr
	}
	if *grpcAddr != "" {
		cfg.Apis.GrpcAddr = *grpcAddr
	}

	// ── Build server options ──────────────────────────────────────────────────
	serverOpts := []grpcmux.Option{
		grpcmux.WithConfig(&cfg.Apis),
	}

	useTLS := *tlsCert != "" && *tlsKey != ""
	if useTLS {
		// HTTPS mode: Go server handles TLS directly, HTTP/2 via ALPN.
		// Suitable for direct browser access without a proxy.
		serverOpts = append(serverOpts, grpcmux.WithTLS(*tlsCert, *tlsKey))
		log.Action("Start").Info("TLS mode enabled", "cert", *tlsCert, "key", *tlsKey)
	} else {
		// h2c mode (default): cleartext HTTP/2 + HTTP/1.1.
		// TLS terminates at Envoy Gateway or another reverse proxy.
		// ConnectRPC uses Connect protocol (HTTP/1.1 JSON) from browsers,
		// and h2c for native gRPC clients.
		log.Action("Start").Info("h2c mode (cleartext HTTP/2) — suitable for Envoy Gateway frontend")
	}

	// ── Create grpcmux server ─────────────────────────────────────────────────
	gs := grpcmux.NewServer(serverOpts...)

	// ── Initialize service ────────────────────────────────────────────────────
	userSrv := service.NewUserServiceServer(&cfg.Dependencies, &cfg.Service)

	// ── Register gRPC server (binary protocol, port 8081) ────────────────────
	// Native gRPC clients (grpc-go, grpcurl, buf curl --protocol grpc) use this.
	pb.RegisterUserServiceServer(gs, userSrv)

	// ── Register gRPC-Gateway REST handlers (port 8080) ──────────────────────
	// REST clients use these endpoints: GET /v1/users, POST /v1/users, etc.
	if err := pb.RegisterUserServiceHandlerServer(context.Background(), gs.ServeMux(), userSrv); err != nil {
		log.Action("RegisterGateway").Fatal(err.Error())
	}

	// ── Register ConnectRPC handlers (port 8080) ──────────────────────────────
	// ConnectRPC clients (browsers, curl, buf curl) use /pb.UserService/<Method>.
	// The handler supports Connect + gRPC + gRPC-Web protocols simultaneously.
	//
	// ConnectRPC interceptors (auth, logging, validation) can be added here:
	//   connectOpts := []connect.HandlerOption{
	//       connect.WithInterceptors(authInterceptor, loggingInterceptor),
	//   }
	connectOpts := []connect.HandlerOption{}
	connectHandler := newConnectHandler(userSrv, connectOpts...)

	// Wrap with CORS middleware for browser access.
	// connectrpc.com/cors provides the correct set of allowed/exposed headers
	// for the Connect, gRPC, and gRPC-Web protocols.
	corsHandler := cors.New(cors.Options{
		// Allow all origins in development. In production, restrict to your domain:
		//   AllowedOrigins: []string{"https://your-app.nx.run"},
		AllowedOrigins: []string{"*"},
		AllowedMethods: corsmw.AllowedMethods(),
		AllowedHeaders: corsmw.AllowedHeaders(),
		ExposedHeaders: corsmw.ExposedHeaders(),
		// Required for requests that include credentials (cookies, auth headers).
		// Cannot be used with AllowedOrigins: ["*"] — set specific origins instead.
		// AllowCredentials: true,
	}).Handler(connectHandler)

	// Mount ConnectRPC at the gRPC service path prefix.
	// Standard http.ServeMux prefix routing: all /pb.UserService/* requests go here,
	// while /v1/users/* and other paths fall through to the gRPC-Gateway mux.
	gs.Handle(connectServiceName, corsHandler)

	// ── Log startup summary ───────────────────────────────────────────────────
	httpScheme := "http (h2c)"
	if useTLS {
		httpScheme = "https (TLS)"
	}
	httpListenAddr := cfg.Apis.HTTPAddr
	if httpListenAddr == "" {
		httpListenAddr = ":8080"
	}
	grpcListenAddr := cfg.Apis.GrpcAddr
	if grpcListenAddr == "" {
		grpcListenAddr = ":8081"
	}

	log.Action("Start").Info("Server ready",
		"scheme", httpScheme,
		"httpAddr", httpListenAddr,
		"grpcAddr", grpcListenAddr,
		"connectrpc", httpListenAddr+connectServiceName+"*",
		"rest_gateway", httpListenAddr+"/v1/users",
		"healthz", httpListenAddr+"/healthz",
	)
	log.Action("Start").Info("ConnectRPC curl example",
		"create_user", `curl -X POST `+httpScheme[0:4]+`://localhost`+httpListenAddr+`/pb.UserService/CreateUser`+
			` -H "Content-Type: application/json" -d '{"name":"Alice","email":"alice@example.com"}'`,
	)

	// ── Start ─────────────────────────────────────────────────────────────────
	gs.Start()
}

// Config is the top-level configuration structure.
type Config struct {
	Dependencies service.Dependencies `yaml:"dependencies"`
	Service      service.Config       `yaml:"service"`
	Apis         grpcmux.Config       `yaml:"apis"`
}

// corsAllowedOrigins returns the allowed origins for CORS.
// Override in production by setting the CORS_ALLOWED_ORIGINS environment variable.
func corsAllowedOrigins() []string {
	if origin := os.Getenv("CORS_ALLOWED_ORIGINS"); origin != "" {
		return []string{origin}
	}
	return []string{"*"}
}

// newConnectTransport returns an h2c-capable HTTP client suitable for
// ConnectRPC clients that communicate with this server over cleartext HTTP/2.
// This is useful for Go-to-Go service calls within the same cluster.
func newConnectTransport() *http.Client {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	return &http.Client{
		Transport: &http.Transport{
			Protocols: p,
		},
	}
}
