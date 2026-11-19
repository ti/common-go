// Package config provide config read from stdin
package config

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/ti/objectbind"

	"github.com/ti/common-go/dependencies"
	"github.com/ti/common-go/log"
)

// Init initialize config from uri address. For exp: configURI ./conf/config.yaml
// etcd://127.0.0.1:6379/config consul://127.0.0.1:6379/config if configURI is
// empty the configURI from flag c.
//
// Concurrency. The configuration source is watched, and a change is applied
// to *configPtr while the process runs. The watcher never touches the struct
// before Init has returned, and it never reads or writes the dependency
// fields (the structs that embed dependencies.Dependency): those are set
// once, by Init. On a change it assigns only the top-level fields whose value
// changed, each as a fresh value (maps, slices and pointers are not shared
// with the previous one), while holding the lock that [RLock] takes. So:
// dependencies and fields that never change in the source are safe to read
// without synchronization; a field that is edited while the process runs is
// read under [RLock], or observed with a hook on [Binder] instead.
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
	target := reflect.ValueOf(configPtr)
	if target.Kind() != reflect.Pointer || target.IsNil() || target.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config %s: the config must be a pointer to a struct, got %T", configURI, configPtr)
	}
	configValue := target.Elem()

	// The watcher works on a private copy of the struct, never on the
	// caller's: it re-reads and rewrites its whole instance on every change
	// (and compares and clones it by walking every field), which must not
	// happen to a struct that holds live dependency clients and is read by
	// request handlers. The copy starts with the caller's values, so
	// defaults set before Init survive for keys the source does not have.
	shadow := reflect.New(configValue.Type())
	synced := syncedFields(configValue.Type())
	for _, i := range synced {
		shadow.Elem().Field(i).Set(deepCopy(configValue.Field(i)))
	}
	var err error
	binder, err = objectbind.Bind(ctx, shadow.Interface(), configURI)
	if err != nil {
		return fmt.Errorf(" error for start config for %s is %w ", configURI, err)
	}
	for _, i := range synced {
		// The hook runs now (the initial value) and, from the watcher, on
		// every change of this field. It runs under the binder's lock, which
		// is what makes reading the shadow here safe, and takes the config
		// lock to write the caller's struct.
		binder.BindField(configValue.Type().Field(i).Name, func(_, _ any) {
			mu.Lock()
			defer mu.Unlock()
			configValue.Field(i).Set(deepCopy(shadow.Elem().Field(i)))
		})
	}
	// Apply Log.Level now and on every reload. Log may be a struct or a
	// pointer to one, Level a string or a named string type. The level is
	// optional: an empty value keeps the current level. An unknown name fails
	// Init, and on a reload it is logged and the previous level stays in
	// force. A Log field that has no string Level is not this package's
	// business and is left alone.
	if hasLogLevel(configValue) {
		mu.RLock()
		level := logLevel(configValue.FieldByName("Log"))
		mu.RUnlock()
		if err := log.TrySetLevel(level); err != nil {
			return fmt.Errorf("config %s: log.level: %w", configURI, err)
		}
		// Bound on Log, not Log.Level: that works when Log is a nil pointer
		// (it is left nil; nothing in the caller's struct is allocated) and
		// for a Level of any string type.
		binder.BindField("Log", func(value, _ any) {
			log.SetLevel(logLevel(reflect.ValueOf(value)))
		})
	}
	// From here until Init returns, a change in the source waits: the
	// caller's struct is written by this goroutine only. (No binder call
	// below this line: the watcher holds the binder's lock while it waits.)
	mu.Lock()
	defer mu.Unlock()
	return initDeps(ctx, configURI, configPtr, opts...)
}

// mu guards the caller's config struct against the watcher: Init holds it
// while it runs, a reload holds it while it assigns changed fields.
var mu sync.RWMutex

// RLock locks the configuration for reading: while it is held, a change in
// the configuration source is not applied to the struct given to [Init].
// Use it around reads of fields that may be edited while the process runs
// (see Init); keep the section short and call [RUnlock] when done.
//
//	config.RLock()
//	greeting := cfg.Service.Greeting
//	config.RUnlock()
func RLock() { mu.RLock() }

// RUnlock undoes one [RLock].
func RUnlock() { mu.RUnlock() }

// syncedFields returns the indexes of the top-level fields the configuration
// source owns: the exported ones, except the dependency structs, which Init
// fills once and the watcher must leave alone.
func syncedFields(t reflect.Type) []int {
	var out []int
	for i := range t.NumField() {
		f := t.Field(i)
		if f.IsExported() && !isDependencyStruct(f.Type) {
			out = append(out, i)
		}
	}
	return out
}

// isDependencyStruct reports whether t is a struct whose first field embeds
// dependencies.Dependency, which is what initDeps initializes.
func isDependencyStruct(t reflect.Type) bool {
	if t.Kind() != reflect.Struct || t.NumField() < 2 {
		return false
	}
	first := t.Field(0)
	return first.Anonymous && reflect.TypeFor[dependencies.Dependency]().AssignableTo(first.Type)
}

// deepCopy returns a copy of v that shares no map, slice or pointer with it,
// so that a later rewrite of the original does not reach the copy. A struct
// with unexported fields (time.Time, for example) is copied as a value.
func deepCopy(v reflect.Value) reflect.Value {
	out := reflect.New(v.Type()).Elem()
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			p := reflect.New(v.Type().Elem())
			p.Elem().Set(deepCopy(v.Elem()))
			out.Set(p)
		}
	case reflect.Interface:
		if !v.IsNil() {
			out.Set(deepCopy(v.Elem()))
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if !v.Type().Field(i).IsExported() {
				out.Set(v)
				return out
			}
		}
		for i := range v.NumField() {
			out.Field(i).Set(deepCopy(v.Field(i)))
		}
	case reflect.Slice:
		if !v.IsNil() {
			s := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			for i := range v.Len() {
				s.Index(i).Set(deepCopy(v.Index(i)))
			}
			out.Set(s)
		}
	case reflect.Array:
		for i := range v.Len() {
			out.Index(i).Set(deepCopy(v.Index(i)))
		}
	case reflect.Map:
		if !v.IsNil() {
			m := reflect.MakeMapWithSize(v.Type(), v.Len())
			for iter := v.MapRange(); iter.Next(); {
				m.SetMapIndex(deepCopy(iter.Key()), deepCopy(iter.Value()))
			}
			out.Set(m)
		}
	default:
		out.Set(v)
	}
	return out
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
	for field := range configType.Fields() {
		if !isDependencyStruct(field.Type) {
			continue
		}
		if err := initDepsFiled(ctx, configPtr, configURI, field.Name, field.Tag, opts...); err != nil {
			return err
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
