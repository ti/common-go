// Package config provide config read from stdin
package config

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/ti/common-go/dependencies"
	"github.com/ti/common-go/log"
	"github.com/ti/objectbind"
)

// Init initialize config from uri address. For exp: configURI ./conf/config.yaml
// etcd://127.0.0.1:6379/config consul://127.0.0.1:6379/config if configURI is
// empty the configURI from flag c.
func Init(ctx context.Context, configURI string, configPtr any, opts ...dependencies.Option) error {
	if configURI == "" {
		configURI = os.Getenv("CONFIG_PATH")
		if configURI == "" {
			configURIAddr := flag.String("c", "configs/config.yaml", "uri to load config")
			flag.Parse()
			configURI = *configURIAddr
		}
	}
	var cc context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		ctx, cc = context.WithTimeout(ctx, 5*time.Second)
	}
	if cc != nil {
		defer cc()
	}
	var err error
	binder, err = objectbind.Bind(ctx, configPtr, configURI)
	if err != nil {
		return fmt.Errorf(" error for start config for %s is %w ", configURI, err)
	}
	configValue := reflect.Indirect(reflect.ValueOf(configPtr))
	// set the log level
	if configValue.FieldByName("Log").IsValid() {
		binder.BindField("Log.Level", func(value, _ any) {
			log.SetLevel(value.(string))
		})
	}
	return initDeps(ctx, configURI, configPtr, opts...)
}

var binder *objectbind.Binder

// Binder get the binder for add the hook for some config field.
func Binder() *objectbind.Binder {
	if binder == nil {
		panic("the config may not init")
	}
	return binder
}

func initDeps(ctx context.Context, configURI string, configPtr any,
	opts ...dependencies.Option,
) error {
	configType := reflect.TypeOf(configPtr).Elem()
	depType := reflect.TypeOf(dependencies.Dependency{})
	for i := 0; i < configType.NumField(); i++ {
		field := configType.Field(i)
		fieldType := field.Type
		if fieldType.Kind() != reflect.Struct {
			continue
		}
		if fieldType.NumField() > 1 {
			fieldTypeFirstField := fieldType.Field(0)
			if fieldTypeFirstField.Anonymous && depType.AssignableTo(fieldTypeFirstField.Type) {
				err := initDepsFiled(ctx, configPtr, configURI, field.Name, field.Tag, opts...)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func initDepsFiled(ctx context.Context, configPtr any, configURI string, fieldName string,
	fieldTag reflect.StructTag, opts ...dependencies.Option,
) (err error) {
	fieldDep := reflect.ValueOf(configPtr).Elem().FieldByName(fieldName)
	if !fieldDep.IsValid() {
		return nil
	}
	depKind := fieldDep.Field(1).Type().Kind()
	depPtr := reflect.New(reflect.Indirect(reflect.ValueOf(fieldDep.Addr().Interface())).Type()).Interface()
	isMulti := depKind == reflect.Struct
	tmpDep := newDepStruct(fieldName, fieldTag, isMulti)
	_, err = objectbind.Bind(ctx, tmpDep, configURI, objectbind.WithoutWatch(true))
	if err != nil {
		return
	}
	filedData := reflect.Indirect(reflect.ValueOf(tmpDep)).Field(0).Interface()
	var hasDep bool
	if isMulti {
		depsData := filedData.(map[string]map[string]string)
		if len(depsData) > 0 {
			err = dependencies.InitMulti(ctx, depPtr, depsData, opts...)
			hasDep = true
		}
	} else {
		depsData := filedData.(map[string]string)
		if len(depsData) > 0 {
			err = dependencies.Init(ctx, depPtr, depsData, opts...)
			hasDep = true
		}
	}
	if err != nil {
		return
	}
	if hasDep {
		fieldDep.Set(reflect.Indirect(reflect.ValueOf(depPtr)))
	}
	return nil
}

func newDepStruct(name string, tag reflect.StructTag, multi bool) any {
	var fieldType reflect.Type
	if multi {
		fieldType = reflect.TypeOf(make(map[string]map[string]string))
	} else {
		fieldType = reflect.TypeOf(make(map[string]string))
	}
	dataType := reflect.StructOf([]reflect.StructField{
		{
			Name: name,
			Type: fieldType,
			Tag:  tag,
		},
	})
	return reflect.New(dataType).Interface()
}
