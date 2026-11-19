// Package config provide config read from stdin
package config

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/ti/objectbind"

	"github.com/ti/common-go/dependencies"
	"github.com/ti/common-go/log"
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
	// Apply Log.Level now and on every reload. Log may be a struct or a
	// pointer to one, Level a string or a named string type. The level is optional: an empty value keeps the current
	// level. An unknown name fails Init, and on a reload it is logged and the
	// previous level stays in force. A Log field that has no string Level is
	// not this package's business and is left alone.
	if hasLogLevel(configValue) {
		if err := log.TrySetLevel(logLevel(configValue.FieldByName("Log"))); err != nil {
			return fmt.Errorf("config %s: log.level: %w", configURI, err)
		}
		// Bound on Log, not Log.Level: that works when Log is a nil pointer
		// (it is left nil; nothing in the caller's struct is allocated) and
		// for a Level of any string type.
		binder.BindField("Log", func(value, _ any) {
			log.SetLevel(logLevel(reflect.ValueOf(value)))
		})
	}
	return initDeps(ctx, configURI, configPtr, opts...)
}

// hasLogLevel reports whether the config struct has a Log field (a struct or
// a pointer to one) with a Level field of a string kind.
func hasLogLevel(config reflect.Value) bool {
	if config.Kind() != reflect.Struct {
		return false
	}
	field, ok := config.Type().FieldByName("Log")
	if !ok {
		return false
	}
	t := field.Type
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	level, ok := t.FieldByName("Level")
	return ok && level.Type.Kind() == reflect.String
}

// logLevel reads Level from a Log value (struct, or pointer to struct). A
// nil pointer has no level: the empty string, which changes nothing.
func logLevel(logValue reflect.Value) string {
	for logValue.IsValid() && (logValue.Kind() == reflect.Pointer || logValue.Kind() == reflect.Interface) {
		if logValue.IsNil() {
			return ""
		}
		logValue = logValue.Elem()
	}
	if !logValue.IsValid() || logValue.Kind() != reflect.Struct {
		return ""
	}
	level := logValue.FieldByName("Level")
	if !level.IsValid() || level.Kind() != reflect.String {
		return ""
	}
	return level.String()
}

var binder *objectbind.Binder

// Binder returns the global Binder instance for adding hooks to config fields.
// Returns an error if the config has not been initialized.
func Binder() (*objectbind.Binder, error) {
	if binder == nil {
		return nil, fmt.Errorf("config not initialized, please call config.Init() first")
	}
	return binder, nil
}

func initDeps(ctx context.Context, configURI string, configPtr any,
	opts ...dependencies.Option,
) error {
	configType := reflect.TypeOf(configPtr).Elem()
	depType := reflect.TypeFor[dependencies.Dependency]()
	for field := range configType.Fields() {
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
		fieldType = reflect.TypeFor[map[string]map[string]string]()
	} else {
		fieldType = reflect.TypeFor[map[string]string]()
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
