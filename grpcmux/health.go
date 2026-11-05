package grpcmux

import (
	"context"
	"sync"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// HealthChecker is a function that performs a custom health check.
// Return a non-nil error to indicate the service is not healthy.
type HealthChecker func(ctx context.Context) error

// simpleHealthServer serves grpc.health.v1.Health for the whole process: every
// service name maps to one status. The status is NOT_SERVING until the gRPC
// listener is up, and again from the moment shutdown starts, so readiness
// probes (gRPC health or HTTP /readyz) take the pod out of rotation before it
// stops accepting connections. Registered checkers can also report
// NOT_SERVING (for example, when a database is unreachable).
type simpleHealthServer struct {
	healthpb.UnimplementedHealthServer
	server *health.Server

	mu       sync.RWMutex
	checkers []HealthChecker
}

const allServices = "*"

func newHealthServer(checkers []HealthChecker) *simpleHealthServer {
	s := &simpleHealthServer{server: health.NewServer(), checkers: checkers}
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

// Check implements grpc.health.v1.Health/Check.
func (s *simpleHealthServer) Check(ctx context.Context,
	_ *healthpb.HealthCheckRequest,
) (*healthpb.HealthCheckResponse, error) {
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

// Watch implements grpc.health.v1.Health/Watch.
func (s *simpleHealthServer) Watch(_ *healthpb.HealthCheckRequest, server healthpb.Health_WatchServer) error {
	return s.server.Watch(&healthpb.HealthCheckRequest{Service: allServices}, server)
}

// List implements grpc.health.v1.Health/List.
func (s *simpleHealthServer) List(ctx context.Context, _ *healthpb.HealthListRequest) (*healthpb.HealthListResponse, error) {
	resp, err := s.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return nil, err
	}
	return &healthpb.HealthListResponse{
		Statuses: map[string]*healthpb.HealthCheckResponse{allServices: resp},
	}, nil
}

// AuthFuncOverride health check without grpc auth middleware.
// refer: [github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth.ServiceAuthFuncOverride]
func (s *simpleHealthServer) AuthFuncOverride(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}
