package grpcmux

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// ErrServerDraining is the cancellation cause (context.Cause) of a streaming
// call's context that was canceled because the server is draining; see
// [WithDrainStreams].
var ErrServerDraining = errors.New("grpcmux: server is draining")

// drainingMessage is the status message a drained stream ends with.
const drainingMessage = "server is draining"

// drainer is the server's draining state: a signal handlers can watch, and
// the registry of open streaming calls that are canceled when it fires.
type drainer struct {
	ch   chan struct{} // closed when draining begins
	once sync.Once

	mu       sync.Mutex
	canceled int // 0: none; 1: essential streams only; 2: all streams
	streams  map[*drainEntry]struct{}
}

type drainEntry struct {
	cancel func()
	// always marks streams that never hold a shutdown, whatever the options
	// (the health Watch stream).
	always bool
}

const (
	drainAlways = 1
	drainAll    = 2
)

func newDrainer() *drainer {
	return &drainer{ch: make(chan struct{}), streams: map[*drainEntry]struct{}{}}
}

// begin closes the draining signal.
func (d *drainer) begin() {
	d.once.Do(func() { close(d.ch) })
}

// register adds an open stream. cancel runs once when the stream must end
// (immediately, if that moment has already passed). The returned function
// removes the stream; call it when the handler returns.
func (d *drainer) register(cancel func(), always bool) (unregister func()) {
	e := &drainEntry{cancel: cancel, always: always}
	d.mu.Lock()
	if d.canceled == drainAll || (always && d.canceled == drainAlways) {
		d.mu.Unlock()
		cancel()
		return func() {}
	}
	d.streams[e] = struct{}{}
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		delete(d.streams, e)
		d.mu.Unlock()
	}
}

// cancelStreams cancels the registered streams: all of them, or only the
// ones marked always.
func (d *drainer) cancelStreams(level int) {
	d.mu.Lock()
	if level <= d.canceled {
		d.mu.Unlock()
		return
	}
	d.canceled = level
	var cancels []func()
	for e := range d.streams {
		if level == drainAll || e.always {
			cancels = append(cancels, e.cancel)
			delete(d.streams, e)
		}
	}
	d.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

type drainKey struct{}

func withDrainer(ctx context.Context, d *drainer) context.Context {
	return context.WithValue(ctx, drainKey{}, d)
}

// Draining returns a channel that is closed when the server that is handling
// the call in ctx begins to drain: after shutdown was requested and the
// PRE_STOP wait is over, at the moment the listeners stop accepting. A
// long-lived handler (a watch stream, server-sent events, a long poll) selects
// on it next to ctx.Done() and returns, so it does not hold the shutdown:
//
//	for {
//		select {
//		case <-ctx.Done():
//			return status.FromContextError(ctx.Err()).Err()
//		case <-grpcmux.Draining(ctx):
//			return nil // or a final message, or codes.Unavailable
//		case ev := <-events:
//			...
//		}
//	}
//
// It works in gRPC handlers (unary and streaming), in everything served on
// the HTTP port (Connect, gateway, custom handlers) and for calls through
// [Server.Conn]. For a context that does not come from a grpcmux server the
// channel is nil and never becomes ready, so the select above stays correct.
//
// Unary handlers rarely need it: a draining server lets calls in flight
// finish. See [WithDrainStreams] to end streams without touching handlers.
func Draining(ctx context.Context) <-chan struct{} {
	if d, ok := ctx.Value(drainKey{}).(*drainer); ok {
		return d.ch
	}
	return nil
}

// Draining returns the channel [Draining] returns for this server's calls,
// for code that has no request context: background loops, WebSocket handlers,
// anything that holds the *Server.
func (s *Server) Draining() <-chan struct{} {
	return s.drain.ch
}

// isCanceled reports whether err says "the context was canceled", in any of
// the forms a handler returns it.
func isCanceled(err error) bool {
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return true
	}
	cerr, ok := errors.AsType[*connect.Error](err)
	return ok && cerr.Code() == connect.CodeCanceled
}

// drained reports whether ctx was canceled because the server drains.
func drained(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), ErrServerDraining)
}

func drainUnaryInterceptor(d *drainer) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(withDrainer(ctx, d), req)
	}
}

// drainStreamInterceptor makes the draining signal available to stream
// handlers and, when all is set (WithDrainStreams), registers every stream so
// that its context is canceled when the server drains. The health Watch
// stream is registered in any case: it has no reason to outlive the drain.
func drainStreamInterceptor(d *drainer, all bool) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := withDrainer(ss.Context(), d)
		always := info.FullMethod == healthpb.Health_Watch_FullMethodName
		if !all && !always {
			return handler(srv, &contextStream{ServerStream: ss, ctx: ctx})
		}
		ctx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		defer d.register(func() { cancel(ErrServerDraining) }, always)()
		err := handler(srv, &drainStream{ServerStream: ss, ctx: ctx})
		if err != nil && drained(ctx) && isCanceled(err) {
			return status.Error(codes.Unavailable, drainingMessage)
		}
		return err
	}
}

// contextStream replaces a stream's context.
type contextStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextStream) Context() context.Context { return s.ctx }

// drainStream is a server stream whose context is canceled when the server
// drains, and whose RecvMsg returns at that moment instead of waiting for the
// client's next message.
type drainStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *drainStream) Context() context.Context { return s.ctx }

func (s *drainStream) RecvMsg(m any) error {
	if drained(s.ctx) {
		return status.Error(codes.Unavailable, drainingMessage)
	}
	// gRPC offers no way to interrupt a blocked RecvMsg from the server
	// side, so it runs in its own goroutine. When the drain wins, the
	// handler returns, gRPC ends the stream, and that unblocks the read.
	done := make(chan error, 1)
	go func() { done <- s.ServerStream.RecvMsg(m) }()
	select {
	case err := <-done:
		return err
	case <-s.ctx.Done():
		if drained(s.ctx) {
			return status.Error(codes.Unavailable, drainingMessage)
		}
		// The call itself ended (client gone, deadline): the read returns
		// with the transport's own error.
		return <-done
	}
}

// drainConnectInterceptor is drainStreamInterceptor for the streaming
// procedures of the connect-go server on the HTTP port. Unary procedures
// pass through; the draining signal is already in every HTTP request context.
func drainConnectInterceptor(d *drainer) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if spec.StreamType == connect.StreamTypeUnary {
				return next(ctx, spec, stream)
			}
			ctx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			ds := &drainConnectStream{ServerStream: stream, ctx: ctx}
			rc, _ := ctx.Value(responseControllerKey{}).(*http.ResponseController)
			defer d.register(func() {
				cancel(ErrServerDraining)
				// A handler blocked reading the request body does not see
				// the cancellation; expiring the read deadline wakes it.
				if rc != nil && ds.receiving.Load() {
					_ = rc.SetReadDeadline(time.Now())
				}
			}, false)()
			err := next(ctx, spec, ds)
			if err != nil && drained(ctx) && isCanceled(err) {
				return connect.NewError(connect.CodeUnavailable, drainingMessage)
			}
			return err
		}
	}
}

type drainConnectStream struct {
	connect.ServerStream
	ctx       context.Context
	receiving atomic.Bool
}

func (s *drainConnectStream) Receive(msg any) error {
	// Order matters: the flag is set before the context is checked, and the
	// drain cancels before it reads the flag, so one of the two always sees
	// the other.
	s.receiving.Store(true)
	defer s.receiving.Store(false)
	if drained(s.ctx) {
		return connect.NewError(connect.CodeUnavailable, drainingMessage)
	}
	err := s.ServerStream.Receive(msg)
	if err != nil && !errors.Is(err, io.EOF) && drained(s.ctx) {
		return connect.NewError(connect.CodeUnavailable, drainingMessage)
	}
	return err
}

// responseControllerKey carries the http.ResponseController of an HTTP
// request to the connect interceptors (set when WithDrainStreams is on).
type responseControllerKey struct{}
