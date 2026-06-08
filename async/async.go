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

// Async execute some fn in async
// the fn is the executor, the async pattern must be:
//
//	Async[I, O any](fn func(ctx context.Context, params ...I) (O, error), params ...I) O
//
// the context.Context is the global context.
func Async[I, O any](f *Future, fn func(ctx context.Context, in I) (O, error), in I) (out O) {
	if outType := reflect.TypeOf(out); outType != nil {
		out = reflect.New(outType.Elem()).Interface().(O)
	}
	o := out
	f.eg.Go(func() error {
		fnOut, err := fn(f.ctx, in)
		if err != nil {
			return err
		}
		if outValue := reflect.ValueOf(o); outValue.IsValid() {
			outValue.Elem().Set(reflect.ValueOf(fnOut).Elem())
		}
		return nil
	})
	return out
}

// Async do functions in
// Async execute some fn in async
// the fn is the executor, the async pattern must one of the:
//
//	Async[I, O any](fn func(ctx context.Context, params ...I) (O, error), params ...I) O
//	Async[I, O any](fn func(ctx context.Context, params ...I) O, params ...I) O
//	Async[I any](fn func(ctx context.Context, params ...I) error, params ...I) void
//	Async[O any](fn func(ctx context.Context) O) O
//	Async[O any](fn func(ctx context.Context) (O, error)) O
//
// the ctx is global context by default.
func (f *Future) Async(fn any, params ...any) any {
	t := reflect.TypeOf(fn)
	nOut := t.NumOut()
	if nOut > 2 {
		panic("fn return value must less than 2")
	}
	returnValueWithOutError := !t.Out(0).Implements(errorInterface)
	var out any
	if nOut > 1 || (nOut == 1 && returnValueWithOutError) {
		out = reflect.New(t.Out(0).Elem()).Interface()
	}
	fnIn := &fnGenericIn{
		fn:                      fn,
		in:                      params,
		out:                     out,
		returnValueWithOutError: returnValueWithOutError,
	}
	_ = Async(f, fnGeneric, fnIn)
	return out
}

type fnGenericIn struct {
	fn                      any
	in                      []any
	out                     any
	returnValueWithOutError bool
}

func fnGeneric(ctx context.Context, fnIn *fnGenericIn) (o any, err error) {
	callParams := make([]reflect.Value, len(fnIn.in)+1)
	callParams[0] = reflect.ValueOf(ctx)
	for i, v := range fnIn.in {
		callParams[i+1] = reflect.ValueOf(v)
	}
	returnResults := reflect.ValueOf(fnIn.fn).Call(callParams)
	if len(returnResults) > 0 {
		resultLastValue := returnResults[len(returnResults)-1].Interface()
		if !fnIn.returnValueWithOutError {
			if resultLastValue != nil {
				err = resultLastValue.(error)
			} else if !returnResults[0].IsNil() {
				// check if it is error
				reflect.ValueOf(fnIn.out).Elem().Set(returnResults[0].Elem())
				return
			}
		} else {
			reflect.ValueOf(fnIn.out).Elem().Set(returnResults[0].Elem())
		}
	}
	return
}

var errorInterface = reflect.TypeOf((*error)(nil)).Elem()

// Await wait for all pipeline functions done or context.Done()
// Await returns the first error from any pipeline function, or ctx.Err() or nil.
func (f *Future) Await() (err error) {
	return f.eg.Wait()
}
