// Package mock provides an in-memory mock database implementation for testing
package mock

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/ti/common-go/dependencies/database"
)

func init() {
	database.RegisterImplements("mock", func(ctx context.Context, u *url.URL) (database.Database, error) {
		m := &Mock{}
		return m, m.Init(ctx, u)
	})
}

// Mock is an in-memory database implementation for testing
type Mock struct {
	mu              sync.RWMutex
	tables          map[string]*table
	defaultDatabase string
	counters        map[string]int64
	// unique holds, per table, the field sets declared unique with
	// EnsureIndexes; Insert enforces them like a real unique index.
	unique map[string][][]string
}

type table struct {
	data []map[string]any
}

// New creates a new mock database instance
func New(ctx context.Context, uri string) (*Mock, error) {
	m := &Mock{}
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	return m, m.Init(ctx, u)
}

// Init initializes the mock database from URL
// URL format: mock://host/database?option=value
func (m *Mock) Init(ctx context.Context, u *url.URL) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.tables = make(map[string]*table)
	m.counters = make(map[string]int64)

	// Parse database name from path
	if u.Path == "" || u.Path == "/" {
		return NewInvalidArgumentError("uri_path", "database name not specified in mock URI")
	}
	m.defaultDatabase = strings.TrimPrefix(u.Path, "/")

	return nil
}

// GetDatabase returns a database instance for the specified project
func (m *Mock) GetDatabase(ctx context.Context, project string) (database.Database, error) {
	// For mock, we just return self with different namespace
	return m, nil
}

// Close closes the database connection
func (m *Mock) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear all data
	m.tables = nil
	m.counters = nil

	return nil
}

// getOrCreateTable gets or creates a table
func (m *Mock) getOrCreateTable(tableName string) *table {
	if m.tables[tableName] == nil {
		m.tables[tableName] = &table{
			data: make([]map[string]any, 0),
		}
	}
	return m.tables[tableName]
}

// readTable returns the table for a read path, which holds only the read lock
// and therefore must not insert into m.tables. A table nobody has written to
// yet reads as empty: the returned value is private to the caller and is never
// stored.
func (m *Mock) readTable(tableName string) *table {
	if t := m.tables[tableName]; t != nil {
		return t
	}
	return &table{data: make([]map[string]any, 0)}
}

// toSnakeCase converts camelCase or PascalCase to snake_case
// Examples:
//   - userId -> user_id
//   - UserName -> user_name
//   - HTTPResponse -> http_response
//   - user_id -> user_id (already snake_case)
func toSnakeCase(s string) string {
	if s == "" {
		return s
	}

	var result strings.Builder
	result.Grow(len(s) + 5) // preallocate some extra space

	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			// Check if previous char is lowercase or if next char is lowercase
			// This handles cases like "HTTPResponse" -> "http_response"
			prevLower := s[i-1] >= 'a' && s[i-1] <= 'z'
			nextLower := i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z'

			if prevLower || nextLower {
				result.WriteByte('_')
			}
		}
		result.WriteRune(r)
	}

	return strings.ToLower(result.String())
}

// normalizeKey converts a key to snake_case for consistent storage
func normalizeKey(key string) string {
	return toSnakeCase(key)
}

// structToMap converts a struct to map
func structToMap(data any) (map[string]any, error) {
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("data must be a struct or struct pointer, got %T", data)
	}

	result := make(map[string]any)
	t := v.Type()

	for i := range v.NumField() {
		field := t.Field(i)
		value := v.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Get field name from json tag or use field name
		fieldName := field.Name
		if tag := field.Tag.Get("json"); tag != "" && tag != "-" {
			if before, _, ok := strings.Cut(tag, ","); ok {
				fieldName = before
			} else {
				fieldName = tag
			}
		} else if tag := field.Tag.Get("bson"); tag != "" && tag != "-" {
			if before, _, ok := strings.Cut(tag, ","); ok {
				fieldName = before
			} else {
				fieldName = tag
			}
		}

		// Normalize key to snake_case for consistent storage
		normalizedKey := normalizeKey(fieldName)
		result[normalizedKey] = value.Interface()
	}

	return result, nil
}

// mapToStruct converts a map to struct
func mapToStruct(m map[string]any, dest any) error {
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Pointer {
		return fmt.Errorf("dest must be a pointer")
	}

	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("dest must be a struct pointer")
	}

	t := v.Type()

	for i := range v.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		// Get field name from json tag or use field name
		fieldName := field.Name
		if tag := field.Tag.Get("json"); tag != "" && tag != "-" {
			if before, _, ok := strings.Cut(tag, ","); ok {
				fieldName = before
			} else {
				fieldName = tag
			}
		} else if tag := field.Tag.Get("bson"); tag != "" && tag != "-" {
			if before, _, ok := strings.Cut(tag, ","); ok {
				fieldName = before
			} else {
				fieldName = tag
			}
		}

		// Normalize key to snake_case for lookup
		normalizedKey := normalizeKey(fieldName)
		if value, ok := m[normalizedKey]; ok {
			fieldValue := v.Field(i)
			if fieldValue.CanSet() {
				val := reflect.ValueOf(value)
				if !val.IsValid() {
					// A stored nil (NULL) reads back as the zero value.
					fieldValue.SetZero()
					continue
				}
				if val.Type().AssignableTo(fieldValue.Type()) {
					fieldValue.Set(val)
				} else if val.Type().ConvertibleTo(fieldValue.Type()) {
					// Handle numeric type mismatches (e.g. int stored, int64 field)
					fieldValue.Set(val.Convert(fieldValue.Type()))
				}
			}
		}
	}

	return nil
}

// matchCondition checks if a row matches the given condition
func matchCondition(row map[string]any, cond database.CE) bool {
	// Normalize condition key to snake_case for matching
	normalizedKey := normalizeKey(cond.Key)
	value, ok := row[normalizedKey]
	if !ok {
		return false
	}

	switch cond.C {
	case database.Eq:
		return valuesEqual(value, cond.Value)
	case database.Ne:
		return !valuesEqual(value, cond.Value)
	case database.Contains:
		return containsValue(cond.Value, value)
	case database.Gt:
		return compareValues(value, cond.Value) > 0
	case database.Gte:
		return compareValues(value, cond.Value) >= 0
	case database.Lt:
		return compareValues(value, cond.Value) < 0
	case database.Lte:
		return compareValues(value, cond.Value) <= 0
	case database.In:
		return containsValue(value, cond.Value)
	case database.Nin:
		return !containsValue(value, cond.Value)
	default:
		return false
	}
}

// valuesEqual is equality as a database sees it: times are equal when they
// are the same instant (whatever the zone), numbers when they have the same
// value (whatever the Go type).
func valuesEqual(a, b any) bool {
	if cmp, ok := compareOrdered(a, b); ok {
		return cmp == 0
	}
	return reflect.DeepEqual(a, b)
}

// compareValues compares two values (for Gt, Gte, Lt, Lte and sorting).
// Values that cannot be ordered against each other compare as equal.
func compareValues(a, b any) int {
	cmp, _ := compareOrdered(a, b)
	return cmp
}

// compareOrdered orders strings, numbers of any kind, bools and times.
func compareOrdered(a, b any) (int, bool) {
	if ta, ok := a.(time.Time); ok {
		tb, ok := b.(time.Time)
		if !ok {
			return 0, false
		}
		return ta.Compare(tb), true
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if !va.IsValid() || !vb.IsValid() {
		return 0, false
	}
	switch {
	case va.Kind() == reflect.String && vb.Kind() == reflect.String:
		return strings.Compare(va.String(), vb.String()), true
	case va.Kind() == reflect.Bool && vb.Kind() == reflect.Bool:
		switch {
		case va.Bool() == vb.Bool():
			return 0, true
		case vb.Bool():
			return -1, true
		default:
			return 1, true
		}
	case va.CanInt() && vb.CanInt():
		return cmp.Compare(va.Int(), vb.Int()), true
	case va.CanUint() && vb.CanUint():
		return cmp.Compare(va.Uint(), vb.Uint()), true
	}
	fa, okA := toFloat(va)
	fb, okB := toFloat(vb)
	if okA && okB {
		return cmp.Compare(fa, fb), true
	}
	return 0, false
}

func toFloat(v reflect.Value) (float64, bool) {
	switch {
	case v.CanInt():
		return float64(v.Int()), true
	case v.CanUint():
		return float64(v.Uint()), true
	case v.CanFloat():
		return v.Float(), true
	default:
		return 0, false
	}
}

// containsValue checks if value is in the slice
func containsValue(value any, slice any) bool {
	sliceValue := reflect.ValueOf(slice)
	if sliceValue.Kind() != reflect.Slice {
		return false
	}

	for i := range sliceValue.Len() {
		if valuesEqual(value, sliceValue.Index(i).Interface()) {
			return true
		}
	}

	return false
}

// matchConditions checks if a row matches all conditions
func matchConditions(row map[string]any, conditions database.C) bool {
	for _, cond := range conditions {
		if !matchCondition(row, cond) {
			return false
		}
	}
	return true
}
