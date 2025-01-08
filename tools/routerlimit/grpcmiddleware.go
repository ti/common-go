// Package routerlimit the rate limit for routers
package routerlimit

import (
	"context"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/metadata"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor returns a new unary server interceptors that performs request rate limiting.
func UnaryServerInterceptor(limiter *Limiter) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		err := limit(ctx, info.FullMethod, limiter)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor returns a new stream server interceptor that performs rate limiting on the request.
func StreamServerInterceptor(limiter *Limiter) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		err := limit(stream.Context(), info.FullMethod, limiter)
		if err != nil {
			return err
		}
		return handler(srv, stream)
	}
}

func limit(ctx context.Context, fullMethod string, limiter *Limiter) error {
	ctxTagValues := metadata.ExtractIncoming(ctx)
	lv := limiter.Config.MatchHeader(fullMethod, ctxTagValues)
	if lv.Quota == NoLimit {
		return nil
	}
	if lv.Quota == Block {
		return status.Errorf(codes.Aborted, "%s is aborted for %s", fullMethod, lv.Message)
	}
	_, resetIn, allowed := limiter.PersistenceFn(ctx, lv.Key, lv.Quota, lv.Duration, 1)
	if !allowed {
		return status.Errorf(codes.ResourceExhausted, "method is rejected for %s, "+
			"please try in %s. ", lv.Message, resetIn)
	}
	return nil
}
