package dependencies

import (
	"context"
	"fmt"
	"net/url"
	"reflect"

	"google.golang.org/grpc"
)

var defaultOptions = &options{}

type options struct {
	typeCreators    map[reflect.Type]*creator
	grpcDialOptions []grpc.DialOption
	sync            bool
}

type creator struct {
	fn   any
	kind reflect.Kind
}

func evaluateOptions(opts []Option) *options {
	optCopy := &options{}
	*optCopy = *defaultOptions
	for _, o := range opts {
		o(optCopy)
	}
	if optCopy.typeCreators == nil {
		optCopy.typeCreators = make(map[reflect.Type]*creator)
	}
	return optCopy
}

// Option the option for this module.
type Option func(*options)

// WithNewFns register newFn New(ctx, uri)
// newFn supports
// grpc: pb.NewXxxClient(grpc.ClientConnInterface) pb.Xxx
// uri:
//
//	func(context.Context, *url.URL) (Xxx, error)
//	func(context.Context, url string) (Xxx, error)
func WithNewFns(newFn ...any) Option {
	if len(newFn) == 0 {
		return nil
	}
	typeCreators := make(map[reflect.Type]*creator)
	ctxInterfaceType := reflect.TypeOf((*context.Context)(nil)).Elem()
	grpcConnInterfaceType := reflect.TypeOf((*grpc.ClientConnInterface)(nil)).Elem()
	stringType := reflect.String
	urlType := reflect.TypeOf((*url.URL)(nil)).Kind()

	for _, v := range newFn {
		t := reflect.TypeOf(v)
		var isCreator bool
		var kind reflect.Kind
		if (t.NumIn() == 2 || t.NumIn() == 3) && t.NumOut() == 2 {
			if t.In(0) == ctxInterfaceType {
				kind = t.In(1).Kind()
				if kind == stringType || kind == urlType {
					isCreator = true
				}
			}
		} else if t.NumOut() == 1 && t.NumIn() == 1 {
			if t.In(0) == grpcConnInterfaceType {
				kind = reflect.Interface
				isCreator = true
			}
		}

		if !isCreator {
			panic(fmt.Errorf("function %s is not pb.New(grpc.ClientConnInterface) interface) or"+
				"New(context.Context, *url.URL)(interface, error) or ", v))
		}
		typeCreators[t.Out(0)] = &creator{
			fn:   v,
			kind: kind,
		}
	}
	return func(o *options) {
		o.typeCreators = typeCreators
	}
}

// WithGRPCDialOptions add more ClientOptions for
func WithGRPCDialOptions(opts ...grpc.DialOption) Option {
	return func(o *options) {
		o.grpcDialOptions = opts
	}
}

// WithSync init dependencies in one concurrency
func WithSync() Option {
	return func(o *options) {
		o.sync = true
	}
}
