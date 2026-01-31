package mock

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ti/common-go/dependencies/database"
)

// PageQuery implements pagination query for mock database
func PageQuery[T any](ctx context.Context, m *Mock, table string,
	in *database.PageQueryRequest,
) (*database.PageQueryResponse[T], error) {
	// Initialize response
	out := &database.PageQueryResponse[T]{}

	// Get total count if not disabled
	if !in.NoCount {
		total, err := m.Count(ctx, table, in.Filters)
		if err != nil {
			return nil, err
		}
		out.Total = total
	}

	// Set default limit if not specified
	limit := in.Limit
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}

	// Calculate offset from page number
	offset := 0
	if in.Page > 0 {
		offset = (in.Page - 1) * limit
	}

	// Query more data than needed to handle offset+limit
	queryLimit := offset + limit
	if queryLimit > 2000 {
		queryLimit = 2000
	}

	// Query data using Find
	var results []T
	err := m.Find(ctx, table, in.Filters, in.Sort, queryLimit, &results)
	if err != nil {
		return nil, err
	}

	// Apply offset manually (skip first N items)
	if offset > 0 {
		if offset >= len(results) {
			results = []T{}
		} else {
			results = results[offset:]
			// Limit the results to the requested page size
			if len(results) > limit {
				results = results[:limit]
			}
		}
	} else if len(results) > limit {
		// No offset but we might have queried more than limit
		results = results[:limit]
	}

	// Convert to pointer slice
	out.Data = make([]*T, 0, len(results))
	for i := range results {
		out.Data = append(out.Data, &results[i])
	}

	return out, nil
}

// StreamQuery implements stream query for mock database
func StreamQuery[T any](ctx context.Context, m *Mock, table string,
	in *database.StreamQueryRequest,
) (*database.StreamResponse[T], error) {
	// Initialize response
	out := &database.StreamResponse[T]{}

	// Get total count if not disabled
	if !in.NoCount {
		total, err := m.Count(ctx, table, in.Filters)
		if err != nil {
			return nil, err
		}
		out.Total = total
	}

	// Build filter conditions
	filters := in.Filters
	if filters == nil {
		filters = database.C{}
	}

	// Add page token filter if provided
	if in.PageToken != "" && in.PageField != "" {
		// Determine comparison operator based on Ascending flag
		var comp database.Condition
		if in.Ascending {
			comp = database.Gt // Greater than for ascending
		} else {
			comp = database.Lt // Less than for descending
		}

		// Try to convert page token to int64 (most common case)
		tokenValue, err := strconv.ParseInt(in.PageToken, 10, 64)
		var pageTokenValue any = in.PageToken
		if err == nil {
			pageTokenValue = tokenValue
		}

		// Add page token condition
		filters = append(filters, database.CE{
			Key:   in.PageField,
			Value: pageTokenValue,
			C:     comp,
		})
	}

	// Set default limit
	limit := in.Limit
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	// Request one extra item to check if there are more pages
	queryLimit := limit + 1

	// Build sort fields
	sortFields := []string{}
	if in.PageField != "" {
		if in.Ascending {
			sortFields = append(sortFields, in.PageField)
		} else {
			sortFields = append(sortFields, "-"+in.PageField)
		}
	}

	// Query data
	var results []T
	err := m.Find(ctx, table, filters, sortFields, queryLimit, &results)
	if err != nil {
		return nil, err
	}

	// Check if there are more pages
	hasMore := len(results) > limit
	if hasMore {
		// Remove the extra item
		results = results[:limit]

		// Set next page token from the last item
		if len(results) > 0 {
			lastItem := &results[len(results)-1]
			if in.PageField != "" {
				// Extract page token value from the last item
				pageTokenValue := extractFieldValue(lastItem, in.PageField)
				if pageTokenValue != nil {
					// Convert to string for page token
					out.PageToken = convertToString(pageTokenValue)
				}
			}
		}
	}

	// Convert to pointer slice
	out.Data = make([]*T, 0, len(results))
	for i := range results {
		out.Data = append(out.Data, &results[i])
	}

	return out, nil
}

// extractFieldValue extracts field value from struct
func extractFieldValue(data any, fieldName string) any {
	// Convert to map
	m, err := structToMap(data)
	if err != nil {
		return nil
	}

	// Normalize field name and lookup
	normalizedKey := normalizeKey(fieldName)
	return m[normalizedKey]
}

// convertToString converts value to string for page token
func convertToString(value any) string {
	if value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		return v
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%f", v)
	default:
		// Use fmt.Sprint for other types
		return fmt.Sprintf("%v", v)
	}
}
