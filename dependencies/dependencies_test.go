package dependencies

import (
	"context"
	"net/url"
	"testing"
	"time"
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
		t.Failed()
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
