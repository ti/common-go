package grpcmux

import (
	"context"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// simpleHealthServer the simple health server
type simpleHealthServer struct {
	server *health.Server
}

const allServices = "*"

// Check implement check.
func (s *simpleHealthServer) Check(ctx context.Context,
	in *healthpb.HealthCheckRequest,
) (*healthpb.HealthCheckResponse, error) {
	in.Service = allServices
	return s.server.Check(ctx, in)
}

// Watch implement watch.
func (s *simpleHealthServer) Watch(in *healthpb.HealthCheckRequest,
	server healthpb.Health_WatchServer,
) error {
	in.Service = allServices
	return s.server.Watch(in, server)
}

func (s *simpleHealthServer) List(_ context.Context, in *healthpb.HealthListRequest) (*healthpb.HealthListResponse, error) {
	return &healthpb.HealthListResponse{
		Statuses: map[string]*healthpb.HealthCheckResponse{
			"*": {
				Status: healthpb.HealthCheckResponse_SERVING,
			},
		},
	}, nil
}

// AuthFuncOverride health check without grpc auth middleware.
// refer: [github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/auth.ServiceAuthFuncOverride]
func (s *simpleHealthServer) AuthFuncOverride(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}
