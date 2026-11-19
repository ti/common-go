package grpcmux

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/ti/common-go/log"
)

// serveStream answers a server-streaming method as newline-delimited JSON.
//
// The generated gRPC stream handler runs in its own goroutine on a
// grpc.ServerStream whose RecvMsg yields the request decoded from the HTTP
// request and whose SendMsg hands each response to the HTTP writer, through
// the same stream interceptor chain as native gRPC. An error before the first
// message is a normal error response (HTTP status and JSON error body); after
// it the stream ends with an {"error": ...} line, as with grpc-gateway.
func (rt *restRoute) serveStream(w http.ResponseWriter, r *http.Request, params map[string]string) {
	s := rt.s
	gw := s.ServeMux()
	inbound, outbound := runtime.MarshalerForRequest(gw, r)
	ctx, body, err := rt.prepare(r)
	if err != nil {
		runtime.HTTPError(ctx, gw, outbound, w, r, err)
		return
	}
	ctx, cancel := context.WithCancel(withHTTPPeer(ctx, r))
	defer cancel()

	stream := &restServerStream{
		ctx:  ctx,
		msgs: make(chan proto.Message),
		decode: func(m any) error {
			return rt.decode(inbound, r, body, params, m)
		},
	}
	stream.ctx = grpc.NewContextWithServerTransportStream(ctx, &stream.transport)
	stream.transport.method = rt.fullMethod

	done := make(chan error, 1)
	go func() {
		defer func() {
			// The recovery interceptor handles panics; this is the net below
			// it (WithoutHTTPInterceptors), where net/http would recover a
			// handler running on the request goroutine.
			if rec := recover(); rec != nil {
				if noRecovery {
					panic(rec)
				}
				log.Inject(ctx, map[string]any{"panic": fmt.Sprint(rec)})
				done <- status.Errorf(codes.Internal, "%v", rec)
			}
		}()
		info := &grpc.StreamServerInfo{FullMethod: rt.fullMethod, IsServerStream: true}
		handler := rt.stream.Handler
		if chain := s.httpStream; chain != nil {
			inner := handler
			handler = func(srv any, ss grpc.ServerStream) error { return chain(srv, ss, info, inner) }
		}
		// Outermost, as on the gRPC port: the stream sees the draining
		// signal and, with WithDrainStreams, ends with UNAVAILABLE.
		done <- drainStreamInterceptor(s.drain, s.opts.drainStreams)(rt.impl, stream, info, handler)
	}()
	// The handler goroutine must be gone before the HTTP handler returns: it
	// uses the request and its context.
	finished := false
	wait := func() error {
		err := <-done
		finished = true
		return err
	}
	defer func() {
		cancel()
		if !finished {
			_ = wait()
		}
	}()

	// Wait for the first message or the end, so that headers the handler set
	// and an early error still shape the HTTP response.
	var first proto.Message
	select {
	case first = <-stream.msgs:
	case err := <-done:
		finished = true
		if err != nil {
			runtime.HTTPError(stream.metadataContext(ctx), gw, outbound, w, r, contextError(ctx, err))
			return
		}
	}
	recv := func() (proto.Message, error) {
		if first != nil {
			m := first
			first = nil
			return m, nil
		}
		if finished {
			return nil, io.EOF
		}
		select {
		case m := <-stream.msgs:
			return m, nil
		case err := <-done:
			finished = true
			if err != nil {
				return nil, contextError(ctx, err)
			}
			return nil, io.EOF
		}
	}
	runtime.ForwardResponseStream(stream.metadataContext(ctx), gw, outbound, w, r, recv, gw.GetForwardResponseOptions()...)
	if !finished {
		// The client is gone or the write failed: stop the handler.
		cancel()
		_ = wait()
	}
}

// restServerStream is the grpc.ServerStream a server-streaming handler runs
// on when it is called over REST.
type restServerStream struct {
	ctx       context.Context
	transport transportStream
	msgs      chan proto.Message
	decode    func(any) error

	mu       sync.Mutex
	received bool
}

func (s *restServerStream) Context() context.Context { return s.ctx }

func (s *restServerStream) SetHeader(md metadata.MD) error  { return s.transport.SetHeader(md) }
func (s *restServerStream) SendHeader(md metadata.MD) error { return s.transport.SendHeader(md) }
func (s *restServerStream) SetTrailer(md metadata.MD)       { _ = s.transport.SetTrailer(md) }

// RecvMsg yields the request once, then io.EOF (the client sends one
// message).
func (s *restServerStream) RecvMsg(m any) error {
	s.mu.Lock()
	first := !s.received
	s.received = true
	s.mu.Unlock()
	if !first {
		return io.EOF
	}
	return s.decode(m)
}

// SendMsg hands a response message to the HTTP writer and returns when it has
// taken it, or when the request ended.
func (s *restServerStream) SendMsg(m any) error {
	msg, ok := m.(proto.Message)
	if !ok {
		return status.Errorf(codes.Internal, "response type %T is not a proto.Message", m)
	}
	select {
	case s.msgs <- msg:
		return nil
	case <-s.ctx.Done():
		return status.FromContextError(s.ctx.Err()).Err()
	}
}

// metadataContext returns ctx with the header metadata the handler has set so
// far, in the form grpc-gateway forwards as HTTP response headers.
func (s *restServerStream) metadataContext(ctx context.Context) context.Context {
	header, trailer := s.transport.metadata()
	return runtime.NewServerMetadataContext(ctx, runtime.ServerMetadata{HeaderMD: header, TrailerMD: trailer})
}
