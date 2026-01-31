package mock

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/ti/common-go/dependencies/database"
)

// Insert inserts one or more documents
func (m *Mock) Insert(ctx context.Context, tableName string, docs any) (count int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	table := m.getOrCreateTable(tableName)

	// Check if docs is a slice
	v := reflect.ValueOf(docs)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() == reflect.Slice {
		// Multiple documents
		for i := 0; i < v.Len(); i++ {
			doc := v.Index(i).Interface()
			row, err := structToMap(doc)
			if err != nil {
				return count, err
			}
			table.data = append(table.data, row)
			count++
		}
	} else {
		// Single document
		row, err := structToMap(docs)
		if err != nil {
			return 0, err
		}
		table.data = append(table.data, row)
		count = 1
	}

	return count, nil
}

// InsertOne inserts a single document
func (m *Mock) InsertOne(ctx context.Context, tableName string, data any) error {
	_, err := m.Insert(ctx, tableName, data)
	return err
}

// Update updates documents matching the condition
func (m *Mock) Update(ctx context.Context, tableName string, condition database.C, doc any) (count int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	table := m.getOrCreateTable(tableName)

	// Convert doc to map
	var updates map[string]any
	switch v := doc.(type) {
	case database.D:
		updates = make(map[string]any)
		for _, e := range v {
			// Normalize key to snake_case
			normalizedKey := normalizeKey(e.Key)
			updates[normalizedKey] = e.Value
		}
	default:
		updates, err = structToMap(doc)
		if err != nil {
			return 0, err
		}
	}

	// Update matching rows
	for i := range table.data {
		if matchConditions(table.data[i], condition) {
			for key, value := range updates {
				table.data[i][key] = value
			}
			count++
		}
	}

	return count, nil
}

// UpdateOne updates a single document matching the condition
func (m *Mock) UpdateOne(ctx context.Context, tableName string, condition database.C, doc any) (count int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	table := m.getOrCreateTable(tableName)

	// Convert doc to map
	var updates map[string]any
	switch v := doc.(type) {
	case database.D:
		updates = make(map[string]any)
		for _, e := range v {
			// Normalize key to snake_case
			normalizedKey := normalizeKey(e.Key)
			updates[normalizedKey] = e.Value
		}
	default:
		updates, err = structToMap(doc)
		if err != nil {
			return 0, err
		}
	}

	// Update first matching row
	for i := range table.data {
		if matchConditions(table.data[i], condition) {
			for key, value := range updates {
				table.data[i][key] = value
			}
			return 1, nil
		}
	}

	return 0, nil
}

// Replace replaces documents (not implemented yet - similar to Update)
func (m *Mock) Replace(ctx context.Context, tableName string, indexKeys []string, docs any) (count int, err error) {
	// For mock, we treat Replace similar to Insert
	return m.Insert(ctx, tableName, docs)
}

// ReplaceOne replaces a single document
func (m *Mock) ReplaceOne(ctx context.Context, tableName string, condition database.C, data any) (count int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	table := m.getOrCreateTable(tableName)

	newRow, err := structToMap(data)
	if err != nil {
		return 0, err
	}

	// Replace first matching row
	for i := range table.data {
		if matchConditions(table.data[i], condition) {
			table.data[i] = newRow
			return 1, nil
		}
	}

	return 0, nil
}

// Delete deletes documents matching the condition
func (m *Mock) Delete(ctx context.Context, tableName string, condition database.C) (count int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	table := m.getOrCreateTable(tableName)

	// Filter out matching rows
	newData := make([]map[string]any, 0, len(table.data))
	for _, row := range table.data {
		if !matchConditions(row, condition) {
			newData = append(newData, row)
		} else {
			count++
		}
	}

	table.data = newData
	return count, nil
}

// DeleteOne deletes a single document matching the condition
func (m *Mock) DeleteOne(ctx context.Context, tableName string, condition database.C) (count int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	table := m.getOrCreateTable(tableName)

	// Find and delete first matching row
	for i, row := range table.data {
		if matchConditions(row, condition) {
			table.data = append(table.data[:i], table.data[i+1:]...)
			return 1, nil
		}
	}

	return 0, nil
}

// Find finds documents matching the condition
func (m *Mock) Find(ctx context.Context, tableName string, condition database.C, sortBy []string, limit int, arrayPtr any) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	table := m.getOrCreateTable(tableName)

	// Collect matching rows
	var matches []map[string]any
	for _, row := range table.data {
		if len(condition) == 0 || matchConditions(row, condition) {
			// Make a copy to avoid race conditions
			rowCopy := make(map[string]any)
			for k, v := range row {
				rowCopy[k] = v
			}
			matches = append(matches, rowCopy)
		}
	}

	// Sort if needed
	if len(sortBy) > 0 {
		sortRows(matches, sortBy)
	}

	// Apply limit
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}

	// Convert to array
	return sliceToArray(matches, arrayPtr)
}

// FindOne finds a single document matching the condition
func (m *Mock) FindOne(ctx context.Context, tableName string, condition database.C, data any) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	table := m.getOrCreateTable(tableName)

	for _, row := range table.data {
		if matchConditions(row, condition) {
			return mapToStruct(row, data)
		}
	}

	return NewNotFoundError(tableName)
}

// FindRows returns a row iterator for streaming
func (m *Mock) FindRows(ctx context.Context, tableName string, condition database.C, sortBy []string, limit int, oneData any) (database.Row, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	table := m.getOrCreateTable(tableName)

	// Collect matching rows
	var matches []map[string]any
	for _, row := range table.data {
		if len(condition) == 0 || matchConditions(row, condition) {
			rowCopy := make(map[string]any)
			for k, v := range row {
				rowCopy[k] = v
			}
			matches = append(matches, rowCopy)
		}
	}

	// Sort if needed
	if len(sortBy) > 0 {
		sortRows(matches, sortBy)
	}

	// Apply limit
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}

	return &mockRow{
		data:    matches,
		current: -1,
	}, nil
}

// Exist checks if any document matches the condition
func (m *Mock) Exist(ctx context.Context, tableName string, condition database.C) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	table := m.getOrCreateTable(tableName)

	for _, row := range table.data {
		if matchConditions(row, condition) {
			return true, nil
		}
	}

	return false, nil
}

// Count counts documents matching the condition
func (m *Mock) Count(ctx context.Context, tableName string, condition database.C) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	table := m.getOrCreateTable(tableName)

	count := int64(0)
	for _, row := range table.data {
		if len(condition) == 0 || matchConditions(row, condition) {
			count++
		}
	}

	return count, nil
}

// sortRows sorts rows by the given sort fields
func sortRows(rows []map[string]any, sortBy []string) {
	sort.Slice(rows, func(i, j int) bool {
		for _, field := range sortBy {
			desc := false
			if strings.HasPrefix(field, "-") {
				desc = true
				field = field[1:]
			}

			// Normalize field key to snake_case for lookup
			normalizedField := normalizeKey(field)
			vi := rows[i][normalizedField]
			vj := rows[j][normalizedField]

			cmp := compareValues(vi, vj)
			if cmp != 0 {
				if desc {
					return cmp > 0
				}
				return cmp < 0
			}
		}
		return false
	})
}

// sliceToArray converts slice of maps to array of structs
func sliceToArray(matches []map[string]any, arrayPtr any) error {
	v := reflect.ValueOf(arrayPtr)
	if v.Kind() != reflect.Ptr {
		return fmt.Errorf("arrayPtr must be a pointer to slice")
	}

	v = v.Elem()
	if v.Kind() != reflect.Slice {
		return fmt.Errorf("arrayPtr must be a pointer to slice")
	}

	elemType := v.Type().Elem()

	for _, match := range matches {
		elem := reflect.New(elemType)
		if err := mapToStruct(match, elem.Interface()); err != nil {
			return err
		}
		v.Set(reflect.Append(v, elem.Elem()))
	}

	return nil
}

// mockRow implements database.Row interface
type mockRow struct {
	data    []map[string]any
	current int
}

func (r *mockRow) Next() bool {
	r.current++
	return r.current < len(r.data)
}

func (r *mockRow) Decode() (any, error) {
	if r.current < 0 || r.current >= len(r.data) {
		return nil, NewInvalidArgumentError("row_position", "position out of range")
	}
	return r.data[r.current], nil
}

func (r *mockRow) Close() error {
	r.data = nil
	return nil
}
