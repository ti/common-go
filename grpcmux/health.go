package grpcmux

import (
	"context"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// HealthChecker is a function that performs a custom health check.
// Return a non-nil error to indicate the service is not healthy.
type HealthChecker func(ctx context.Context) error

// simpleHealthServer serves grpc.health.v1.Health for the whole process: the
// overall status (empty service name) and every registered service share one
// status. It is NOT_SERVING until the gRPC listener is up, and again from the
// moment shutdown starts, so readiness probes (gRPC health or HTTP /readyz)
// take the pod out of rotation before it stops accepting connections.
// Registered checkers can also report NOT_SERVING (for example, when a
// database is unreachable). A service name that is not registered on the
// server is answered as the health protocol specifies: NOT_FOUND from Check,
// SERVICE_UNKNOWN from Watch.
type simpleHealthServer struct {
	healthpb.UnimplementedHealthServer
	server *health.Server

	mu       sync.RWMutex
	checkers []HealthChecker
	services map[string]bool // names registered on the gRPC server
}

const allServices = "*"

func newHealthServer(checkers []HealthChecker) *simpleHealthServer {
	s := &simpleHealthServer{server: health.NewServer(), checkers: checkers, services: map[string]bool{}}
	s.setServing(false)
	return s
}

func (s *simpleHealthServer) setServing(serving bool) {
	st := healthpb.HealthCheckResponse_NOT_SERVING
	if serving {
		st = healthpb.HealthCheckResponse_SERVING
	}
	s.server.SetServingStatus(allServices, st)
}

func (s *simpleHealthServer) addChecker(c HealthChecker) {
	s.mu.Lock()
	s.checkers = append(s.checkers, c)
	s.mu.Unlock()
}

// addService records a service name health answers for.
func (s *simpleHealthServer) addService(name string) {
	s.mu.Lock()
	s.services[name] = true
	s.mu.Unlock()
}

// known reports whether health answers for the service name: the empty name
// (overall status) and every registered service.
func (s *simpleHealthServer) known(name string) bool {
	if name == "" {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.services[name]
}

// Check implements grpc.health.v1.Health/Check.
func (s *simpleHealthServer) Check(ctx context.Context,
	req *healthpb.HealthCheckRequest,
) (*healthpb.HealthCheckResponse, error) {
	if !s.known(req.GetService()) {
		return nil, status.Error(codes.NotFound, "unknown service")
	}
	s.mu.RLock()
	checkers := s.checkers
	s.mu.RUnlock()
	for _, checker := range checkers {
		if err := checker(ctx); err != nil {
			// A failing checker is a health status, not an RPC error.
			return &healthpb.HealthCheckResponse{ //nolint:nilerr
				Status: healthpb.HealthCheckResponse_NOT_SERVING,
			}, nil
		}
	}
	return s.server.Check(ctx, &healthpb.HealthCheckRequest{Service: allServices})
}

// ready reports whether Check currently returns SERVING.
func (s *simpleHealthServer) ready(ctx context.Context) bool {
	resp, err := s.Check(ctx, &healthpb.HealthCheckRequest{})
	return err == nil && resp.GetStatus() == healthpb.HealthCheckResponse_SERVING
}

// Watch implements grpc.health.v1.Health/Watch. The stream ends when the
// server drains (see drainStreamInterceptor), so it never holds a shutdown.
func (s *simpleHealthServer) Watch(req *healthpb.HealthCheckRequest, server healthpb.Health_WatchServer) error {
	if !s.known(req.GetService()) {
		// The wrapped server has no such name: it reports SERVICE_UNKNOWN
		// and keeps the stream open, as the protocol specifies.
		return s.server.Watch(&healthpb.HealthCheckRequest{Service: "unknown:" + req.GetService()}, server)
	}
	return s.server.Watch(&healthpb.HealthCheckRequest{Service: allServices}, server)
}

// List implements grpc.health.v1.Health/List: the overall status under the
// empty name, and the same status for every registered service.
func (s *simpleHealthServer) List(ctx context.Context, _ *healthpb.HealthListRequest) (*healthpb.HealthListResponse, error) {
	resp, err := s.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return nil, err
	}
	statuses := map[string]*healthpb.HealthCheckResponse{"": resp}
	s.mu.RLock()
	for name := range s.services {
		statuses[name] = resp
	}
	s.mu.RUnlock()
	return &healthpb.HealthListResponse{Statuses: statuses}, nil
}

// AuthFuncOverride health check without grpc auth middleware.
// refer: [github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth.ServiceAuthFuncOverride]
func (s *simpleHealthServer) AuthFuncOverride(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}
