package grpcmux

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
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

// runTestServer starts s's gRPC and HTTP listeners in the background (without
// graceful.Start, so tests don't share global shutdown state) and closes the
// server when the test ends.
func runTestServer(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()
	go func() { _ = s.startGRPC(ctx) }()
	go func() { _ = s.startHTTP(ctx) }()
	waitForTCP(t, s.opts.grpcAddr)
	waitForTCP(t, s.opts.httpAddr)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Close(ctx)
	})
}

func newTestServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	base := []Option{
		WithHTTPAddr(freeAddr(t)),
		WithGrpcAddr(freeAddr(t)),
		WithMetricsAddr(""),
	}
	return NewServer(append(base, opts...)...)
}

// NewServer must be callable more than once per process (tests, multiple
// servers). It used to panic on duplicate Prometheus collector registration.
func TestNewServerTwiceWithMetrics(t *testing.T) {
	for range 2 {
		_ = NewServer(WithHTTPAddr(freeAddr(t)), WithGrpcAddr(freeAddr(t)), WithMetricsAddr(freeAddr(t)))
	}
}

// An empty metrics address disables the metrics listener instead of binding :80.
func TestEmptyMetricsAddrDisablesMetricsListener(t *testing.T) {
	s := newTestServer(t)
	done := make(chan error, 1)
	go func() { done <- s.startMetrics(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("startMetrics with empty addr: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("startMetrics with empty addr should return immediately")
	}
}

// Server.Conn() is an in-memory connection; it must reach every registered
// service, not only health.
func TestConnReachesRegisteredServices(t *testing.T) {
	s := newTestServer(t)
	svc := &testService{}
	s.RegisterService(&testServiceDesc, svc)
	conn := s.Conn()
	runTestServer(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out := &pbhealth.HealthCheckResponse{}
	if err := conn.Invoke(ctx, testCheckPath, &pbhealth.HealthCheckRequest{}, out); err != nil {
		t.Fatalf("Invoke via Conn(): %v", err)
	}
	if out.GetStatus() != pbhealth.HealthCheckResponse_SERVING {
		t.Fatalf("status = %v", out.GetStatus())
	}
}

// Native gRPC still works end to end and runs the validator.
func TestNativeGRPCValidates(t *testing.T) {
	s := newTestServer(t)
	s.RegisterService(&testServiceDesc, &testService{})
	runTestServer(t, s)

	cc, err := grpc.NewClient(s.opts.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cc.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out := &pbhealth.HealthCheckResponse{}
	err = cc.Invoke(ctx, testCheckPath, &pbhealth.HealthCheckRequest{Service: "invalid"}, out)
	if got := statusCode(err); got != "InvalidArgument" {
		t.Fatalf("code = %s, err = %v", got, err)
	}
}

// /healthz is a liveness endpoint: always 200 while the process serves HTTP.
// /readyz mirrors the gRPC health status, so it fails during the drain.
func TestHealthzAndReadyz(t *testing.T) {
	s := newTestServer(t)
	runTestServer(t, s)
	base := "http://" + s.opts.httpAddr
	if code := httpGetStatus(t, base+"/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz = %d", code)
	}
	if code := httpGetStatus(t, base+"/readyz"); code != http.StatusOK {
		t.Fatalf("/readyz = %d", code)
	}
	s.health.setServing(false)
	if code := httpGetStatus(t, base+"/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz while draining = %d", code)
	}
	if code := httpGetStatus(t, base+"/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz while draining = %d", code)
	}
}

func httpGetStatus(t *testing.T, url string) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// --- SIGTERM drain, in a real child process ---------------------------------

const drainHelperEnv = "GRPCMUX_DRAIN_HELPER_ADDR"

// TestDrainHelperProcess is not a real test: it is the child process for
// TestSIGTERMDrainsInFlightRequests.
func TestDrainHelperProcess(t *testing.T) {
	addr := os.Getenv(drainHelperEnv)
	if addr == "" {
		t.Skip("helper process only")
	}
	s := NewServer(WithHTTPAddr(addr), WithGrpcAddr("127.0.0.1:0"), WithMetricsAddr(""))
	s.Handle("/slow", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second)
		_, _ = io.WriteString(w, "done")
	}))
	graceful.AddCloser(func(context.Context) error {
		fmt.Println("CLOSER_RAN")
		return nil
	})
	s.Start()
	fmt.Println("START_RETURNED")
	os.Exit(0)
}

// SIGTERM must flip health, let in-flight requests finish, run every closer
// and exit 0. Before the fix the default signal handler killed the process
// immediately (exit 143) and no closer ran.
func TestSIGTERMDrainsInFlightRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a child process")
	}
	addr := freeAddr(t)
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDrainHelperProcess$", "-test.v")
	cmd.Env = append(os.Environ(), drainHelperEnv+"="+addr, "PRE_STOP=")
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
	waitForTCP(t, addr)

	type result struct {
		body string
		err  error
	}
	res := make(chan result, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/slow", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			res <- result{err: err}
			return
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		res <- result{body: string(b)}
	}()
	time.Sleep(200 * time.Millisecond) // the request is now in flight
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	r := <-res
	if r.err != nil || r.body != "done" {
		t.Errorf("in-flight request: body=%q err=%v", r.body, r.err)
	}
	waitErr := cmd.Wait()
	<-linesDone
	out := strings.Join(lines, "\n")
	if waitErr != nil {
		t.Errorf("child exit: %v\n%s", waitErr, out)
	}
	if !strings.Contains(out, "CLOSER_RAN") {
		t.Errorf("closer did not run:\n%s", out)
	}
	if !strings.Contains(out, "START_RETURNED") {
		t.Errorf("Start did not return after shutdown:\n%s", out)
	}
}
