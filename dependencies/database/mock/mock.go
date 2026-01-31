// Package mock provides an in-memory mock database implementation for testing
package mock

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"

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
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("data must be a struct or struct pointer, got %T", data)
	}

	result := make(map[string]any)
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Get field name from json tag or use field name
		fieldName := field.Name
		if tag := field.Tag.Get("json"); tag != "" && tag != "-" {
			if idx := strings.Index(tag, ","); idx >= 0 {
				fieldName = tag[:idx]
			} else {
				fieldName = tag
			}
		} else if tag := field.Tag.Get("bson"); tag != "" && tag != "-" {
			if idx := strings.Index(tag, ","); idx >= 0 {
				fieldName = tag[:idx]
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
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("dest must be a pointer")
	}

	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("dest must be a struct pointer")
	}

	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		// Get field name from json tag or use field name
		fieldName := field.Name
		if tag := field.Tag.Get("json"); tag != "" && tag != "-" {
			if idx := strings.Index(tag, ","); idx >= 0 {
				fieldName = tag[:idx]
			} else {
				fieldName = tag
			}
		} else if tag := field.Tag.Get("bson"); tag != "" && tag != "-" {
			if idx := strings.Index(tag, ","); idx >= 0 {
				fieldName = tag[:idx]
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
				if val.Type().AssignableTo(fieldValue.Type()) {
					fieldValue.Set(val)
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
		return reflect.DeepEqual(value, cond.Value)
	case database.Ne:
		return !reflect.DeepEqual(value, cond.Value)
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

// compareValues compares two values (for Gt, Gte, Lt, Lte)
func compareValues(a, b any) int {
	switch v1 := a.(type) {
	case int:
		v2, ok := b.(int)
		if !ok {
			return 0
		}
		if v1 > v2 {
			return 1
		} else if v1 < v2 {
			return -1
		}
		return 0
	case int64:
		v2, ok := b.(int64)
		if !ok {
			return 0
		}
		if v1 > v2 {
			return 1
		} else if v1 < v2 {
			return -1
		}
		return 0
	case float64:
		v2, ok := b.(float64)
		if !ok {
			return 0
		}
		if v1 > v2 {
			return 1
		} else if v1 < v2 {
			return -1
		}
		return 0
	case string:
		v2, ok := b.(string)
		if !ok {
			return 0
		}
		return strings.Compare(v1, v2)
	default:
		return 0
	}
}

// containsValue checks if value is in the slice
func containsValue(value any, slice any) bool {
	sliceValue := reflect.ValueOf(slice)
	if sliceValue.Kind() != reflect.Slice {
		return false
	}

	for i := 0; i < sliceValue.Len(); i++ {
		if reflect.DeepEqual(value, sliceValue.Index(i).Interface()) {
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
