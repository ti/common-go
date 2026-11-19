package background

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// TestMain fails the package if the runtime's goroutine leak profile (Go
// 1.27) finds a goroutine that can never be unblocked once all tests ran.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if leaks := leakProfile(); leaks != "" {
			fmt.Fprintf(os.Stderr, "goroutine leak profile is not empty:\n%s\n", leaks)
			code = 1
		}
	}
	os.Exit(code)
}

var leakTotal = regexp.MustCompile(`goroutineleak profile: total (\d+)`)

// leakProfile returns the goroutine leak profile if it reports any leak.
func leakProfile() string {
	p := pprof.Lookup("goroutineleak")
	if p == nil {
		return "" // toolchain without the profile
	}
	var buf bytes.Buffer
	if err := p.WriteTo(&buf, 1); err != nil {
		return "cannot write goroutineleak profile: " + err.Error()
	}
	if m := leakTotal.FindSubmatch(buf.Bytes()); m != nil && string(m[1]) == "0" {
		return ""
	}
	return buf.String()
}

// noLeaks registers a check that, when the test ends, no goroutine other
// than the test's own is executing code of this package (they are given a
// moment to wind down), and that the leak profile is empty.
func noLeaks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		var stray []string
		deadline := time.Now().Add(3 * time.Second)
		for {
			stray = strayGoroutines()
			if len(stray) == 0 || time.Now().After(deadline) {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if len(stray) > 0 {
			t.Errorf("%d goroutines of this package are still running:\n%s", len(stray), strings.Join(stray, "\n\n"))
		}
		if leaks := leakProfile(); leaks != "" {
			t.Errorf("goroutine leak profile:\n%s", leaks)
		}
	})
}

func strayGoroutines() []string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var stray []string
	for i, g := range strings.Split(string(buf), "\n\n") {
		if i == 0 { // the calling goroutine
			continue
		}
		if strings.Contains(g, "common-go/background.") && !strings.Contains(g, "testing.tRunner") &&
			!strings.Contains(g, "background.TestMain") {
			stray = append(stray, g)
		}
	}
	return stray
}

// metricValue reads a counter or gauge from the default registry.
func metricValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
	next:
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			for k, v := range labels {
				if got[k] != v {
					continue next
				}
			}
			if m.GetCounter() != nil {
				return m.GetCounter().GetValue()
			}
			return m.GetGauge().GetValue()
		}
	}
	return 0
}

type ctxKey string

func bg() context.Context { return context.Background() }
