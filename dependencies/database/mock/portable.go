package mock

import (
	"context"
	"maps"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/dependencies/database"
)

var (
	_ database.KeysetFinder  = (*Mock)(nil)
	_ database.Indexer       = (*Mock)(nil)
	_ database.HealthChecker = (*Mock)(nil)
	_ database.SystemNamer   = (*Mock)(nil)
)

// FindAfter implements database.KeysetFinder: the matching rows, sorted by
// orderBy, that come strictly after the position `after`.
func (m *Mock) FindAfter(_ context.Context, tableName string, condition database.C, orderBy []string,
	after []any, limit int, arrayPtr any,
) error {
	if len(orderBy) == 0 {
		return status.Errorf(codes.InvalidArgument, "find after in %s: no order", tableName)
	}
	if len(after) > 0 && len(after) != len(orderBy) {
		return status.Errorf(codes.InvalidArgument, "find after in %s: %d cursor values for %d order fields",
			tableName, len(after), len(orderBy))
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	var matches []map[string]any
	for _, row := range m.readTable(tableName).data {
		if matchConditions(row, condition) && (len(after) == 0 || comesAfter(row, orderBy, after)) {
			matches = append(matches, maps.Clone(row))
		}
	}
	sortRows(matches, orderBy)
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return sliceToArray(matches, arrayPtr)
}

// comesAfter reports whether row sorts strictly after the cursor position.
func comesAfter(row map[string]any, orderBy []string, after []any) bool {
	for i, field := range orderBy {
		desc := strings.HasPrefix(field, "-")
		cmp := compareValues(row[normalizeKey(strings.TrimPrefix(field, "-"))], after[i])
		if cmp == 0 {
			continue
		}
		return (cmp > 0) != desc
	}
	return false
}

// EnsureIndexes implements database.Indexer. The mock keeps no index
// structures; it remembers which field sets are unique and rejects an insert
// that would duplicate one with AlreadyExists, like a real unique index.
func (m *Mock) EnsureIndexes(_ context.Context, tableName string, indexes ...database.Index) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unique == nil {
		m.unique = make(map[string][][]string)
	}
	for _, idx := range indexes {
		if len(idx.Fields) == 0 {
			return status.Errorf(codes.InvalidArgument, "index on %s has no fields", tableName)
		}
		if !idx.Unique {
			continue
		}
		fields := make([]string, len(idx.Fields))
		for i, f := range idx.Fields {
			fields[i] = normalizeKey(strings.TrimPrefix(f, "-"))
		}
		if !slices.ContainsFunc(m.unique[tableName], func(have []string) bool { return slices.Equal(have, fields) }) {
			m.unique[tableName] = append(m.unique[tableName], fields)
		}
	}
	return nil
}

// checkUnique fails with AlreadyExists when row repeats the values of a
// unique field set of another row. skip is the index of the row being
// replaced, or -1. The caller holds the write lock.
func (m *Mock) checkUnique(tableName string, t *table, row map[string]any, skip int) error {
	for _, fields := range m.unique[tableName] {
		for i, other := range t.data {
			if i == skip {
				continue
			}
			same := true
			for _, f := range fields {
				if !valuesEqual(row[f], other[f]) {
					same = false
					break
				}
			}
			if same {
				return status.Errorf(codes.AlreadyExists, "%s: duplicate value for unique index (%s)",
					tableName, strings.Join(fields, ", "))
			}
		}
	}
	return nil
}

// HealthCheck implements database.HealthChecker: healthy until closed.
func (m *Mock) HealthCheck(context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.tables == nil {
		return status.Error(codes.Unavailable, "mock database is closed")
	}
	return nil
}

// DBSystem implements database.SystemNamer.
func (m *Mock) DBSystem() string { return "mock" }
