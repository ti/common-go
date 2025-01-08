package sql

import (
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/ti/common-go/dependencies/mongo/codecs"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/ti/common-go/dependencies/database"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TransformDocument transform any object to bson documents
func TransformDocument(scheme string, ptrVal any, keepEmpty bool) database.D {
	v := reflect.ValueOf(ptrVal).Elem()
	t := v.Type()
	var docs database.D
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		sfv := v.Field(i)
		if !keepEmpty && isEmptyValue(sfv) {
			continue
		}
		sv := sfv.Interface()
		if sf.Anonymous {
			docs = append(docs, TransformDocument(scheme, sv, keepEmpty)...)
			continue
		}
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = strings.ToLower(sf.Name)
		} else {
			tag, _, _ = strings.Cut(tag, ",")
		}
		ft := sf.Type
		e := database.E{
			Key: tag,
		}
		skip := fillElements(scheme, &e, ft, sv)
		if skip {
			continue
		}
		docs = append(docs, e)
	}
	return docs
}

// TransformSQLArgs transform any object to sql args
func TransformSQLArgs(scheme string, ptrVal any, keepEmpty bool,
	loc *time.Location,
) (query []string, args []any) {
	v := reflect.ValueOf(ptrVal).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		sfv := v.Field(i)
		if !keepEmpty && isEmptyValue(sfv) {
			continue
		}
		sv := sfv.Interface()
		if sf.Anonymous {
			query, args = TransformSQLArgs(scheme, sv, keepEmpty, loc)
			continue
		}
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = strings.ToLower(sf.Name)
		} else {
			tag, _, _ = strings.Cut(tag, ",")
		}
		ft := sf.Type
		e := database.E{
			Key: tag,
		}
		skip := fillElements(scheme, &e, ft, sv)
		if skip {
			continue
		}
		query = append(query, "`"+e.Key+"`")
		if timeValue, ok := e.Value.(time.Time); ok {
			timeStr := timeValue.In(loc).Format(time.DateTime)
			args = append(args, timeStr)
		} else {
			args = append(args, e.Value)
		}
	}
	return
}

// EX element the kv value
type EX struct {
	Value   any
	Holder  any
	Type    reflect.Type
	Field   reflect.Value
	Kind    reflect.Kind
	IsJSON  bool
	IsTime  bool
	IsSlice bool
	IsMap   bool
}

// TransformSQLQuery transform sql querys
func TransformSQLQuery(ptrVal any) (querys []string) {
	v := reflect.ValueOf(ptrVal).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		sfv := v.Field(i)
		sv := sfv.Interface()
		if sf.Anonymous {
			if sfv.IsNil() {
				newPtr := reflect.New(reflect.TypeOf(sv).Elem())
				sfv.Set(newPtr)
				sv = newPtr.Interface()
			}
			querys = TransformSQLQuery(sv)
			continue
		}
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = strings.ToLower(sf.Name)
		} else {
			tag, _, _ = strings.Cut(tag, ",")
		}
		querys = append(querys, "`"+tag+"`")
	}
	return
}

// TransformSQLDocument transform any object to sql scheme
func TransformSQLDocument(ptrVal any, hasQuery bool,
	selectFields map[string]bool,
) (exs []*EX, query []string, args []any) {
	v := reflect.ValueOf(ptrVal).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		sfv := v.Field(i)
		sv := sfv.Interface()
		if sf.Anonymous {
			if sfv.IsNil() {
				newPtr := reflect.New(reflect.TypeOf(sv).Elem())
				sfv.Set(newPtr)
				sv = newPtr.Interface()
			}
			exs, query, args = TransformSQLDocument(sv, hasQuery, selectFields)
			continue
		}
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		if len(selectFields) > 0 && !selectFields[tag] {
			continue
		}
		ft := sf.Type
		ex := fillHolders(ft, sfv)
		if ex != nil {
			args = append(args, ex.Holder)
			exs = append(exs, ex)
		} else {
			args = append(args, sfv.Addr().Interface())
		}
		if hasQuery {
			if tag == "" {
				tag = strings.ToLower(sf.Name)
			} else {
				tag, _, _ = strings.Cut(tag, ",")
			}
			query = append(query, "`"+tag+"`")
		}
	}
	return
}

func fillHolders(ft reflect.Type, sfv reflect.Value) *EX {
	if ft == timestampPtrType || ft == timeType {
		return &EX{
			Value:  sfv.Addr().Interface(),
			Holder: new(string),
			Type:   ft,
			Field:  sfv,
			Kind:   ft.Kind(),
			IsTime: true,
		}
	}
	kind := ft.Kind()
	switch kind {
	case reflect.Pointer, reflect.Struct, reflect.Array, reflect.Slice, reflect.Map, reflect.Interface:
		return &EX{
			Value:   sfv.Addr().Interface(),
			Holder:  new(string),
			Type:    ft,
			Field:   sfv,
			Kind:    kind,
			IsJSON:  true,
			IsSlice: kind == reflect.Slice || kind == reflect.Array,
			IsMap:   kind == reflect.Map,
		}
	default:
		return nil
	}
}

func fillElements(scheme string, e *database.E, ft reflect.Type, sv any) (skip bool) {
	switch ft.Kind() {
	case reflect.Interface, reflect.Pointer:
		if reflect.ValueOf(sv).IsNil() {
			break
		}
		if ft == timestampPtrType {
			seconds := sv.(*timestamppb.Timestamp).Seconds
			if seconds == 0 {
				return true
			}
			e.Value = time.Unix(seconds, 0)
		} else if ft == boolPtrType {
			e.Value = sv.(*wrapperspb.BoolValue).Value
		} else {
			json, err := marshal(scheme, sv)
			if err != nil {
				log.Printf("marshal %s error %s\n", ft.Kind(), err)
				return true
			}
			e.Value = string(json)
		}
	case reflect.Array, reflect.Map, reflect.Slice:
		json, err := marshal(scheme, sv)
		if err != nil {
			log.Printf("marshal %s error %s\n", ft.Kind(), err)
			return true
		}
		e.Value = string(json)
	case reflect.Struct:
		if ft == timeType {
			if sv.(time.Time).IsZero() {
				return true
			}
			e.Value = sv
		} else {
			json, err := marshal(scheme, sv)
			if err != nil {
				log.Printf("marshal %s error %s\n", ft.Kind(), err)
				return true
			}
			e.Value = string(json)
		}
	default:
		e.Value = sv
	}
	return false
}

func marshal(scheme string, val any) ([]byte, error) {
	data, err := codecs.Marshal(val)
	if err != nil {
		return nil, err
	}
	dataLen := len(data)
	if scheme == schemePostgres && dataLen > 1 {
		if data[0] == '[' && data[1] != '{' {
			data[0] = '{'
			data[dataLen-1] = '}'
		}
	}
	return data, nil
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
}
