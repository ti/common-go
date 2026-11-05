package dependencies

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ti/common-go/graceful"
)

// TestInitRequired test required test for init
func TestInitRequired(t *testing.T) {
	// Dep the dep struct for test
	type Dep struct {
		Dep1 *dummyDep `required:"true"`
		Dep2 *dummyDep `required:"false"`
		Dep3 *dummyDep
	}
	var depTest1 Dep
	ctx, cc := context.WithTimeout(context.Background(), time.Second)
	cc()
	err := Init(ctx, &depTest1, map[string]string{
		"dep1": "dummy://localhost/test",
		"dep2": "",
		"dep3": "dummy://localhost/test",
	})
	if err != nil {
		t.Fatalf("init error for %s", err)
	}
	if depTest1.Dep1 == nil || depTest1.Dep3 == nil || depTest1.Dep2 != nil {
		t.Fatalf("unexpected result: %+v", depTest1)
	}
	var depTest2 Dep
	err = Init(ctx, &depTest2, map[string]string{
		"dep1": "",
		"dep2": "dummy://localhost/test",
		"dep3": "dummy://localhost/test",
	})
	if err == nil {
		t.Fatal("return required error is expected")
	}
}

type dummyDep struct {
	data bool
}

// Init just implement the init
func (d *dummyDep) Init(_ context.Context, _ *url.URL) error {
	d.data = true
	return nil
}

type closerDep struct{}

func (d *closerDep) Init(_ context.Context, _ *url.URL) error { return nil }

func (d *closerDep) Close(context.Context) error {
	closeCount.Add(1)
	return nil
}

var closeCount atomic.Int32

// Every dependency implementing Close is registered with graceful, even when
// many are initialized concurrently (run with -race), and closed on shutdown.
func TestInitRegistersClosers(t *testing.T) {
	type Deps struct {
		A, B, C, D, E, F, G, H *closerDep
	}
	cfg := map[string]string{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		cfg[k] = "dummy://localhost/" + k
	}
	closeCount.Store(0)
	var deps Deps
	if err := Init(context.Background(), &deps, cfg); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		graceful.Start(context.Background())
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	graceful.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("graceful.Start did not return")
	}
	if n := closeCount.Load(); n != 8 {
		t.Fatalf("closed %d dependencies, want 8", n)
	}
}

type urlClient struct{ host string }
type strClient struct{ raw string }

// WithNewFns accepts both func(ctx, *url.URL) and func(ctx, string) creators.
func TestWithNewFns(t *testing.T) {
	type Deps struct {
		U *urlClient
		S *strClient
	}
	var deps Deps
	err := Init(context.Background(), &deps, map[string]string{
		"u": "svc://example:1234/path",
		"s": "anything goes",
	}, WithNewFns(
		func(_ context.Context, u *url.URL) (*urlClient, error) { return &urlClient{host: u.Host}, nil },
		func(_ context.Context, s string) (*strClient, error) { return &strClient{raw: s}, nil },
	))
	if err != nil {
		t.Fatal(err)
	}
	if deps.U.host != "example:1234" || deps.S.raw != "anything goes" {
		t.Fatalf("deps = %+v %+v", deps.U, deps.S)
	}
}

// A creator error is returned with the field name and URI for context.
func TestInitCreatorError(t *testing.T) {
	type Deps struct{ U *urlClient }
	var deps Deps
	err := Init(context.Background(), &deps, map[string]string{"u": "svc://h"},
		WithNewFns(func(context.Context, *url.URL) (*urlClient, error) { return nil, errors.New("dial failed") }))
	if err == nil || !strings.Contains(err.Error(), "dial failed") || !strings.Contains(err.Error(), "U") {
		t.Fatalf("err = %v", err)
	}
}

// A field type without Init and without a creator gets an actionable hint.
func TestInitUnknownTypeHint(t *testing.T) {
	type Deps struct{ U *urlClient }
	var deps Deps
	err := Init(context.Background(), &deps, map[string]string{"u": "svc://h"})
	if err == nil || !strings.Contains(err.Error(), "WithNewFns") {
		t.Fatalf("err = %v", err)
	}
}

func TestInitRequiresPointer(t *testing.T) {
	type Deps struct{ U *dummyDep }
	if err := Init(context.Background(), Deps{}, nil); err == nil {
		t.Fatal("want an error for a non-pointer")
	}
}

// InitMulti initializes nested groups by (case-insensitive) group name.
func TestInitMulti(t *testing.T) {
	type Storage struct{ DB *dummyDep }
	type Remote struct {
		API *dummyDep `required:"false"`
	}
	type Deps struct {
		Storage Storage
		Remote  Remote
	}
	var deps Deps
	err := InitMulti(context.Background(), &deps, map[string]map[string]string{
		"Storage": {"db": "dummy://localhost/db"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deps.Storage.DB == nil || !deps.Storage.DB.data {
		t.Fatal("Storage.DB not initialized")
	}
	if deps.Remote.API != nil {
		t.Fatal("Remote.API should be skipped (no config)")
	}
}
