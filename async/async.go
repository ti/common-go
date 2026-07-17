package async

import (
	"context"
	"reflect"

	"golang.org/x/sync/errgroup"
)

// Future is a function design pattern for async/await
// It can Execute pipeline functions concurrently
// Examples in async_test.go
// Refer: https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Statements/async_function
type Future struct {
	ctx context.Context
	eg  *errgroup.Group
}

// New promise.
func New(ctx context.Context) *Future {
	eg, egCtx := errgroup.WithContext(ctx)
	return &Future{
		ctx: egCtx,
		eg:  eg,
	}
}

// Async execute fn asynchronously on the Future's errgroup.
//
// If O is a pointer type, Async allocates the pointee immediately and
// returns the pointer before fn has finished running; the pointee is
// populated once fn completes, so callers should only read it after
// Await returns. For non-pointer O (e.g. a sentinel struct{}), the
// returned value carries no meaning and should be ignored.
//
// Requires generic methods (Go 1.27, https://go.dev/issue/77273).
func (f *Future) Async[I, O any](fn func(ctx context.Context, in I) (O, error), in I) (out O) {
	outType := reflect.TypeOf(out)
	isPtr := outType != nil && outType.Kind() == reflect.Pointer
	if isPtr {
		out = reflect.New(outType.Elem()).Interface().(O)
	}
	o := out
	f.eg.Go(func() error {
		fnOut, err := fn(f.ctx, in)
		if err != nil {
			return err
		}
		if isPtr {
			if outValue := reflect.ValueOf(o); outValue.IsValid() && !outValue.IsNil() {
				outValue.Elem().Set(reflect.ValueOf(fnOut).Elem())
			}
		}
		return nil
	})
	return out
}

// Await wait for all pipeline functions done or context.Done()
// Await returns the first error from any pipeline function, or ctx.Err() or nil.
func (f *Future) Await() (err error) {
	return f.eg.Wait()
}
