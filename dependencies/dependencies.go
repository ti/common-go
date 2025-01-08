// Package dependencies add all dependencies of a project.
package dependencies

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"github.com/ti/common-go/async"
	"github.com/ti/common-go/graceful"
	"github.com/ti/common-go/grpcmux"
	"google.golang.org/grpc"
)

// Init Initialize dependencies.
// for example, dependencies:
//
// sql: mysql://root:PassW0rd@9.135.107.220:3306/demo
// redis: redis://:PassW0rd@9.134.105.165:6380
// demoClient: dns://demoservice.ns.svc:8081?log=true&metrics=true&try=true
// polarisDemoClient: polaris://namespace/demo?log=true
// testHttp: http://baidu.com?try=3&log=true&timeout=5s
// Then you can use it directly anywhere in the project
// dependencies.SQL.Query("SELECT *form ...") to execute the corresponding method of the mysql library.
// or dependencies.demoClient.SayHello(ctx, helloReq) to execute the grpc method.
func Init(ctx context.Context, dependenciesPtr any, dependenciesConfig map[string]string,
	opts ...Option,
) error {
	o := evaluateOptions(opts)
	dep := reflect.ValueOf(dependenciesPtr)
	if dep.Kind() != reflect.Ptr {
		return errors.New("dependencies must be pointer")
	}
	return initDepField(ctx, dep.Elem(), dependenciesConfig, o)
}

// InitMulti initializes multi-layer dependencies (used but the project depends too much, or needs to layer
// the dependencies on the scene)
// For example: the initialization configuration is as follows:
//
// dependencies:
// storage:
// sql: mysql://root:PassW0rd@9.135.107.220:3306/demo
// redis: redis://:PassW0rd@9.134.105.165:6380
// serviceA:
// queryUser: http://192.168.1.1:8080/v1/query //http method
// login: polaris://1324242:324312341/v2/login?timeout=5s //http method, but the address source is L5
// checkUser: polaris://trpc.service.test/v2/check?try=2 //http method, but the address source is L5
// checkPass: dns://checkpass.namespace.svc:8081?log=false&metrics=true&try=true
// grpc method: load balancing through k8s service name (dns)
// serviceB:
// grpcDemo: dns:///demo-service.namespace.svc:8081 //grpc method
// grpcPDemo: polaris:///Production/demo-service.namespace.svc:8081 //grpc method, but address resolution uses Polaris
// customSdk custom://apiKey:apiSecret@192.168.1.1:9090/go
//
// Call this method, the system will automatically generate related dependent objects
// Then you can use it directly anywhere in the project
//
// dependencies.storage.SQL.Query("SELECT *form ...") to execute the mysql library
//
// or dependencies.serviceA.queryUser.RequestJSON(ctx, {"username":"test"}) to execute the http post method
// or dependencies.serviceB.grpcDemo.SayHello(ctx, helloReq) to execute the grpc method to execute the grpc method
func InitMulti(ctx context.Context, dependenciesPtr any, dependenciesConfigs map[string]map[string]string,
	opts ...Option,
) error {
	o := evaluateOptions(opts)
	// tidy the config
	depConfig := make(map[string]map[string]string)
	for module, cnf := range dependenciesConfigs {
		depConfig[strings.ToLower(module)] = cnf
	}
	dep := reflect.ValueOf(dependenciesPtr)
	if dep.Kind() != reflect.Ptr {
		return errors.New("dependencies must be pointer")
	}
	depStruct := reflect.ValueOf(reflect.Indirect(dep).Interface())
	var future *async.Future
	if !o.sync {
		future = async.New(ctx)
	}
	var tmpDep Dependency
	for i := 0; i < depStruct.NumField(); i++ {
		f := depStruct.Type().Field(i)
		if f.Type.Kind() != reflect.Struct {
			if depStruct.Type() == reflect.TypeOf(tmpDep) {
				continue
			}
			return errors.New("dependenciesPtr first filed must be Struct")
		}
		config := depConfig[strings.ToLower(f.Name)]
		if len(config) == 0 {
			continue
		}
		if future != nil {
			future.Async(initDepField, dep.Elem().Field(i), config, o)
		} else if err := initDepField(ctx, dep.Elem().Field(i), config, o); err != nil {
			return err
		}
	}
	if future != nil {
		return future.Await()
	}
	return nil
}

const tagRequired = "required"

func initDepField(ctx context.Context, depElem reflect.Value, dependenciesConfig map[string]string, o *options) error {
	config := make(map[string]string)
	for k, v := range dependenciesConfig {
		config[strings.ToLower(k)] = v
	}
	var future *async.Future
	if !o.sync {
		future = async.New(ctx)
	}
	var tmpDep Dependency
	for i := 0; i < depElem.NumField(); i++ {
		f := depElem.Type().Field(i)
		urlStr := config[strings.ToLower(f.Name)]
		configData := depElem.Field(i)
		kind := configData.Kind()
		if !(kind == reflect.Ptr || kind == reflect.Interface) {
			if configData.Type() == reflect.TypeOf(tmpDep) {
				continue
			}
			return fmt.Errorf("dependency %s 's kind is %s, but expect to pointer or interface", f.Name, kind.String())
		}
		if !configData.IsNil() {
			continue
		}
		required := f.Tag.Get(tagRequired) != "false"
		if required && urlStr == "" {
			return fmt.Errorf("dependency %s is required", f.Name)
		} else if urlStr == "" {
			continue
		}
		if future != nil {
			_ = future.Async(initItem, depElem, &f, i, urlStr, o)
		} else if err := initItem(ctx, depElem, &f, i, urlStr, o); err != nil {
			return err
		}
	}
	if future != nil {
		return future.Await()
	}
	return nil
}

func initItem(ctx context.Context, depElem reflect.Value, f *reflect.StructField,
	i int, uriStr string, o *options,
) error {
	if f.Type.Kind() == reflect.Ptr {
		dependency, err := initPtrDependency(ctx, f.Name, f.Type, uriStr, o)
		if err != nil {
			return fmt.Errorf("init ptr dependency %s by uri %s error for %w", f.Name, uriStr, err)
		}
		depElem.Field(i).Set(dependency)
		return nil
	}
	v, ok := o.typeCreators[f.Type]
	if !ok {
		return errors.New(hint(f.Name, f.Type.String(), true))
	}
	var client any
	var err error
	var uri *url.URL
	switch v.kind {
	case reflect.Interface:
		uri, err = url.Parse(uriStr)
		if err != nil {
			return fmt.Errorf("dependency %s parse uri %s error for %w", f.Name, uriStr, err)
		}
		client, err = newClientWithURL(ctx, uri, v.fn, o.grpcDialOptions...)
		if err != nil {
			err = fmt.Errorf(" can not create grpc client %s with %s for %w ", f.Name, uri, err)
		}
	case reflect.String:
		client, err = callNew(ctx, v.fn, reflect.ValueOf(uriStr))
	default:
		uri, err = url.Parse(uriStr)
		if err != nil {
			return fmt.Errorf("dependency %s parse uri %s error for %w", f.Name, uriStr, err)
		}
		client, err = callNew(ctx, v.fn, reflect.ValueOf(uri))
	}
	if err != nil {
		return fmt.Errorf("init creator dependency %s by uri %s error for %w", f.Name, uri.String(), err)
	}
	depElem.Field(i).Set(reflect.ValueOf(client))
	return nil
}

// New client with uri
func newClientWithURL(ctx context.Context, uri *url.URL, pbNewXxxClient any,
	opts ...grpc.DialOption,
) (client any, err error) {
	var conn *grpc.ClientConn
	conn, err = grpcmux.NewClientConnWithURI(ctx, uri, opts...)
	if err != nil {
		return
	}
	// Add link close on global exit
	graceful.AddCloser(func(ctx context.Context) error {
		return conn.Close()
	})
	ret := reflect.ValueOf(pbNewXxxClient).Call([]reflect.Value{reflect.ValueOf(conn)})
	return ret[0].Interface(), nil
}

func hint(fieldName, fieldType string, mayGrpc bool) string {
	i := strings.LastIndex(fieldType, ".")
	pkg := fieldType[:i]
	fn := fieldType[i+1:]
	hitFn := "dependency for " + fieldName + " cannot be initialized with " + fieldType +
		" for it does not inherit Dependency's Init method," +
		" please consider these options, "
	if mayGrpc {
		hitFn += " `if the dependency is gRPC, with dependencies.WithNewFns(" + pkg + ".New" + fn + ")`, or for others"
	}
	hitFn += " `dependencies.WithNewFns(func(context.Context, *url.URL) (" + fieldType + ", error))`"
	return hitFn
}

func initPtrDependency(ctx context.Context, filedName string,
	filedType reflect.Type, uriStr string, opts *options,
) (rv reflect.Value, err error) {
	// check if is not Pointer
	rv = reflect.New(filedType.Elem())
	if dependencyCloser, ok := rv.Interface().(dependencyCloser); ok {
		graceful.AddCloser(dependencyCloser.Close)
	}
	dependency, ok := rv.Interface().(dependencyInit)
	var uri *url.URL
	if ok {
		uri, err = url.Parse(uriStr)
		if err != nil {
			err = fmt.Errorf("dependency %s parse uri %s error for %w", filedName, uriStr, err)
			return
		}
		if err = dependency.Init(ctx, uri); err != nil {
			return
		}
		rv = reflect.ValueOf(dependency)
		return
	}
	dependencyStr, ok := rv.Interface().(dependencyInitStr)
	if ok {
		if err = dependencyStr.Init(ctx, uriStr); err != nil {
			return
		}
		rv = reflect.ValueOf(dependencyStr)
		return
	}
	v, ok := opts.typeCreators[filedType]
	if !ok {
		err = errors.New(hint(filedName, filedType.String(), false))
		return
	}
	var client any
	if v.kind == reflect.String {
		client, err = callNew(ctx, v.fn, reflect.ValueOf(uriStr))
	} else {
		uri, err = url.Parse(uriStr)
		if err != nil {
			err = fmt.Errorf("dependency %s parse uri %s error for %w", filedName, uriStr, err)
			return
		}
		client, err = callNew(ctx, v.fn, reflect.ValueOf(uri))
	}
	if err != nil {
		return
	}
	rv = reflect.ValueOf(client)
	return
}

func callNew(ctx context.Context, fn any, uri reflect.Value) (any, error) {
	ret := reflect.ValueOf(fn).Call([]reflect.Value{reflect.ValueOf(ctx), uri})
	resultLastValue := ret[len(ret)-1].Interface()
	if resultLastValue != nil {
		return nil, resultLastValue.(error)
	}
	return ret[0].Interface(), nil
}

// dependencyInit just implement init function
type dependencyInit interface {
	Init(ctx context.Context, uri *url.URL) error
}

// dependencyInit just implement init function
type dependencyInitStr interface {
	Init(ctx context.Context, data string) error
}

// dependencyCloser just implement close function
type dependencyCloser interface {
	Close(ctx context.Context) error
}

// Dependency the dep dependency
type Dependency struct{}

// UnmarshalJSON Implements the Unmarshaler interface of the json pkg.
func (d *Dependency) UnmarshalJSON(_ []byte) error {
	return nil
}

// MarshalJSON Implements the marshaler interface of the json pkg.
func (d *Dependency) MarshalJSON() ([]byte, error) {
	return []byte("{}"), nil
}

// UnmarshalYAML Implements the Unmarshaler interface of the yaml pkg.
func (d *Dependency) UnmarshalYAML(_ func(any) error) error {
	return nil
}

// UnmarshalTOML Implements the Unmarshaler interface of the toml pkg.
func (d *Dependency) UnmarshalTOML(_ any) error {
	return nil
}
