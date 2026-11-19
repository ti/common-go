package grpcmux

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/ti/common-go/graceful"
)

// streamEnd is how a client saw a stream end.
type streamEnd struct {
	msgs []pbhealth.HealthCheckResponse_ServingStatus // messages after the first
	err  error                                        // io.EOF: clean end, status OK
	at   time.Time
}

// readToEnd reads st in the background until it ends.
func readToEnd(st testStream) <-chan streamEnd {
	ch := make(chan streamEnd, 1)
	go func() {
		var end streamEnd
		for {
			msg, err := st.Recv()
			if err != nil {
				end.err, end.at = err, time.Now()
				ch <- end
				return
			}
			end.msgs = append(end.msgs, msg.GetStatus())
		}
	}()
	return ch
}

func mustRecv(t *testing.T, name string, st testStream) {
	t.Helper()
	if _, err := st.Recv(); err != nil {
		t.Fatalf("%s: first message: %v", name, err)
	}
}

func waitEnd(t *testing.T, name string, ch <-chan streamEnd) streamEnd {
	t.Helper()
	select {
	case end := <-ch:
		return end
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: stream did not end", name)
		return streamEnd{}
	}
}

// With WithDrainStreams, open streams end when draining begins: promptly, on
// every protocol, with a protocol-level end of stream that carries
// UNAVAILABLE "server is draining" (not a cut connection); a handler blocked
// in Recv is woken; a unary call in flight still completes; and nothing ends
// before the PRE_STOP wait is over.
func TestDrainStreamsEndsOpenStreams(t *testing.T) {
	const preStop = 300 * time.Millisecond
	t.Setenv("PRE_STOP", preStop.String())
	s, svc := streamServer(t, WithDrainStreams(0))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	type open struct {
		name string
		end  <-chan streamEnd
	}
	var streams []open
	for _, p := range streamProtocols {
		watch := p.open(t, s, ctx, streamWatchPath, false, &pbhealth.HealthCheckRequest{})
		mustRecv(t, p.name+"/watch", watch)
		streams = append(streams, open{p.name + "/watch", readToEnd(watch)})
		if p.name == "grpc-web" || p.name == "connect-http1" {
			continue // no bidirectional streams over gRPC-Web or HTTP/1.1
		}
		// A bidi stream whose handler is blocked waiting for the next
		// client message: the hard case, nothing on the wire wakes it.
		chat := p.open(t, s, ctx, streamChatPath, true, &pbhealth.HealthCheckRequest{})
		mustRecv(t, p.name+"/chat", chat)
		streams = append(streams, open{p.name + "/chat", readToEnd(chat)})
	}
	if got := svc.watching.Load() + svc.chatting.Load(); int(got) != len(streams) {
		t.Fatalf("%d handlers running, want %d", got, len(streams))
	}

	// A unary call that is in flight when the drain starts.
	unary := make(chan error, 1)
	go func() {
		time.Sleep(preStop - 100*time.Millisecond)
		cc, err := grpc.NewClient(s.opts.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			unary <- err
			return
		}
		defer func() { _ = cc.Close() }()
		unary <- cc.Invoke(ctx, testCheckPath, &pbhealth.HealthCheckRequest{Service: "sleep"}, &pbhealth.HealthCheckResponse{})
	}()

	select {
	case <-s.Draining():
		t.Fatal("Draining() fired before shutdown")
	default:
	}
	start := time.Now()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer closeCancel()
	if err := s.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	took := time.Since(start)
	// PRE_STOP, plus the unary call finishing (400ms, started 100ms before
	// the drain), plus slack. Without the drain this is the 15s timeout.
	if took > preStop+2*time.Second {
		t.Errorf("Close took %s; open streams held the shutdown", took)
	}
	select {
	case <-s.Draining():
	default:
		t.Error("Draining() did not fire")
	}
	for _, st := range streams {
		end := waitEnd(t, st.name, st.end)
		if code := streamCode(end.err); code != "Unavailable" || !strings.Contains(end.err.Error(), drainingMessage) {
			t.Errorf("%s: ended with %v, want UNAVAILABLE %q", st.name, end.err, drainingMessage)
		}
		if d := end.at.Sub(start); d < preStop-20*time.Millisecond {
			t.Errorf("%s: ended %s after shutdown began, before PRE_STOP (%s) was over", st.name, d, preStop)
		} else if d > preStop+time.Second {
			t.Errorf("%s: ended %s after shutdown began, want promptly after PRE_STOP (%s)", st.name, d, preStop)
		}
	}
	if err := <-unary; err != nil {
		t.Errorf("unary call in flight during the drain: %v", err)
	}
	if n := svc.watching.Load() + svc.chatting.Load(); n != 0 {
		t.Errorf("%d stream handlers still running after Close", n)
	}
}

// Without WithDrainStreams nothing is canceled, but a handler that watches
// grpcmux.Draining(ctx) ends its stream its own way: here a last message and
// status OK, on every protocol.
func TestDrainingSignalLetsHandlersFinish(t *testing.T) {
	t.Setenv("PRE_STOP", "")
	s, _ := streamServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ends := map[string]<-chan streamEnd{}
	for _, p := range streamProtocols {
		st := p.open(t, s, ctx, streamWatchPath, false, &pbhealth.HealthCheckRequest{Service: "polite"})
		mustRecv(t, p.name, st)
		ends[p.name] = readToEnd(st)
	}
	start := time.Now()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer closeCancel()
	if err := s.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Close took %s", took)
	}
	for name, ch := range ends {
		end := waitEnd(t, name, ch)
		if !errors.Is(end.err, io.EOF) {
			t.Errorf("%s: ended with %v, want a clean end (OK)", name, end.err)
		}
		if len(end.msgs) != 1 || end.msgs[0] != pbhealth.HealthCheckResponse_NOT_SERVING {
			t.Errorf("%s: final messages = %v, want the handler's last message", name, end.msgs)
		}
	}
}

// The default is unchanged: a stream whose handler ignores the drain holds
// the shutdown until the deadline, and is then cut.
func TestWithoutDrainStreamsOpenStreamHoldsShutdown(t *testing.T) {
	t.Setenv("PRE_STOP", "")
	s, _ := streamServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st := streamProtocols[0].open(t, s, ctx, streamWatchPath, false, &pbhealth.HealthCheckRequest{})
	mustRecv(t, "grpc", st)
	end := readToEnd(st)
	start := time.Now()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
	defer closeCancel()
	if err := s.Close(closeCtx); err == nil {
		t.Error("Close returned nil; the open stream should have held it until the deadline")
	}
	if took := time.Since(start); took < 900*time.Millisecond {
		t.Errorf("Close took %s, want the full 1s deadline", took)
	}
	if e := waitEnd(t, "grpc", end); strings.Contains(e.err.Error(), drainingMessage) {
		t.Errorf("stream ended with %v without WithDrainStreams", e.err)
	}
}

// A grace period gives handlers time to finish on their own before the
// remaining streams are canceled.
func TestDrainStreamsGrace(t *testing.T) {
	t.Setenv("PRE_STOP", "")
	const grace = 400 * time.Millisecond
	s, _ := streamServer(t, WithDrainStreams(grace))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p := streamProtocols[2] // connect
	polite := p.open(t, s, ctx, streamWatchPath, false, &pbhealth.HealthCheckRequest{Service: "polite"})
	mustRecv(t, "polite", polite)
	stubborn := p.open(t, s, ctx, streamWatchPath, false, &pbhealth.HealthCheckRequest{})
	mustRecv(t, "stubborn", stubborn)
	politeEnd, stubbornEnd := readToEnd(polite), readToEnd(stubborn)

	start := time.Now()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer closeCancel()
	if err := s.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if e := waitEnd(t, "polite", politeEnd); !errors.Is(e.err, io.EOF) || e.at.Sub(start) > grace/2 {
		t.Errorf("polite stream: ended with %v after %s, want OK at once", e.err, e.at.Sub(start))
	}
	e := waitEnd(t, "stubborn", stubbornEnd)
	if streamCode(e.err) != "Unavailable" {
		t.Errorf("stubborn stream: ended with %v, want UNAVAILABLE", e.err)
	}
	if d := e.at.Sub(start); d < grace-20*time.Millisecond || d > grace+time.Second {
		t.Errorf("stubborn stream ended after %s, want after the %s grace", d, grace)
	}
}

// grpc.health.v1.Health/Watch is a long-lived stream that every gRPC client
// with health checking keeps open. It ends when the server drains, whatever
// the options, so it cannot hold a shutdown.
func TestHealthWatchDoesNotHoldShutdown(t *testing.T) {
	t.Setenv("PRE_STOP", "")
	s := newTestServer(t)
	runTestServer(t, s)
	cc, err := grpc.NewClient(s.opts.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cc.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	w, err := pbhealth.NewHealthClient(cc).Watch(ctx, &pbhealth.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := w.Recv(); err != nil || resp.GetStatus() != pbhealth.HealthCheckResponse_SERVING {
		t.Fatalf("first status: %v %v", resp, err)
	}
	start := time.Now()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer closeCancel()
	if err := s.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("Close took %s; the health watch held the shutdown", took)
	}
	// The watcher is told NOT_SERVING before its stream ends.
	var last pbhealth.HealthCheckResponse_ServingStatus
	for {
		resp, err := w.Recv()
		if err != nil {
			if code := statusCode(err); code != "Unavailable" {
				t.Errorf("watch ended with %v, want UNAVAILABLE", err)
			}
			break
		}
		last = resp.GetStatus()
	}
	if last != pbhealth.HealthCheckResponse_NOT_SERVING {
		t.Errorf("last status before the end = %v, want NOT_SERVING", last)
	}
}

// Draining is usable from any context: outside a grpcmux call it never fires.
func TestDrainingOutsideAServer(t *testing.T) {
	select {
	case <-Draining(context.Background()):
		t.Fatal("Draining fired for a context without a server")
	default:
	}
}

const drainStreamHelperEnv = "GRPCMUX_DRAIN_STREAM_HELPER_ADDR"

// TestDrainStreamHelperProcess is the child process of
// TestSIGTERMEndsStreams: a real server run with Start.
func TestDrainStreamHelperProcess(t *testing.T) {
	addr := os.Getenv(drainStreamHelperEnv)
	if addr == "" {
		t.Skip("helper process only")
	}
	s := NewServer(WithHTTPAddr(addr), WithGrpcAddr(os.Getenv(drainStreamHelperEnv+"_GRPC")), WithMetricsAddr(""),
		WithDrainStreams(0))
	svc := &streamService{}
	s.RegisterService(&streamServiceDesc, svc)
	RegisterConnectStreams(s, &streamServiceDesc, svc)
	// The process-wide signal (graceful.Draining, watched by background
	// work) must fire with the server's, after PRE_STOP, not at SIGTERM.
	go func() {
		for !s.managed.Load() {
			time.Sleep(time.Millisecond)
		}
		<-graceful.Draining()
		select {
		case <-s.Draining():
			fmt.Println("PROCESS_DRAIN_WITH_SERVER")
		default:
			fmt.Println("PROCESS_DRAIN_BEFORE_SERVER")
		}
	}()
	s.Start()
	fmt.Println("START_RETURNED")
	os.Exit(0)
}

// The whole path, in a real process: SIGTERM, PRE_STOP, drain. Streams open
// over Connect and native gRPC end cleanly right after PRE_STOP and the
// process exits, long before GRACEFUL_TIMEOUT.
func TestSIGTERMEndsStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a child process")
	}
	const preStop = 500 * time.Millisecond
	httpAddr, grpcAddr := freeAddr(t), freeAddr(t)
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDrainStreamHelperProcess$", "-test.v")
	cmd.Env = append(os.Environ(), drainStreamHelperEnv+"="+httpAddr, drainStreamHelperEnv+"_GRPC="+grpcAddr,
		"PRE_STOP="+preStop.String(), "GRACEFUL_TIMEOUT=30s")
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var lines []string
	linesDone := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		close(linesDone)
	}()
	waitForTCP(t, httpAddr)
	waitForTCP(t, grpcAddr)

	// The helpers only need the addresses.
	s := &Server{opts: &options{httpAddr: httpAddr, grpcAddr: grpcAddr}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ends := map[string]<-chan streamEnd{}
	for _, p := range []streamProtocol{streamProtocols[0], streamProtocols[2]} { // grpc, connect
		st := p.open(t, s, ctx, streamWatchPath, false, &pbhealth.HealthCheckRequest{})
		mustRecv(t, p.name, st)
		ends[p.name] = readToEnd(st)
	}
	start := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for name, ch := range ends {
		end := waitEnd(t, name, ch)
		if streamCode(end.err) != "Unavailable" || !strings.Contains(end.err.Error(), drainingMessage) {
			t.Errorf("%s: ended with %v, want UNAVAILABLE %q", name, end.err, drainingMessage)
		}
		if d := end.at.Sub(start); d < preStop-50*time.Millisecond || d > preStop+2*time.Second {
			t.Errorf("%s: ended %s after SIGTERM, want right after PRE_STOP (%s)", name, d, preStop)
		}
	}
	waitErr := cmd.Wait()
	exited := time.Since(start)
	<-linesDone
	out := strings.Join(lines, "\n")
	if waitErr != nil || !strings.Contains(out, "START_RETURNED") {
		t.Errorf("child exit: %v\n%s", waitErr, out)
	}
	if !strings.Contains(out, "PROCESS_DRAIN_WITH_SERVER") {
		t.Errorf("graceful.Draining did not fire together with the server's drain:\n%s", out)
	}
	if exited > preStop+3*time.Second {
		t.Errorf("process exited %s after SIGTERM, want shortly after PRE_STOP (%s)", exited, preStop)
	}
	t.Logf("streams ended and process exited %s after SIGTERM (PRE_STOP %s)", exited.Round(time.Millisecond), preStop)
}
