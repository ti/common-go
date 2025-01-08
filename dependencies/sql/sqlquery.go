package sql

import (
	"fmt"
	"strings"

	"github.com/ti/common-go/dependencies/database"
)

// Query the seql query
type Query struct {
	Table     string
	Where     string
	Arguments []any
	Select    string
	Order     string
	Offset    int
	Limit     int
	SelectID  bool
}

func (q *Query) String() string {
	if q.SelectID && !strings.HasPrefix(q.Select, "`_id`") {
		q.Select = "`_id`," + q.Select
	}
	query := "SELECT " + q.Select + " FROM " + q.Table
	if q.Where != "" {
		query += " WHERE " + q.Where
	}
	if q.Order != "" {
		query += " ORDER by " + q.Order
	}
	if q.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	if q.Offset > 0 {
		query += fmt.Sprintf(" OFFSET %d", q.Offset)
	}
	return query
}

// ParseSort prase sort
func ParseSort(sorts []string) string {
	sortStr := ""
	for i, v := range sorts {
		if i > 0 {
			sortStr += ","
		}
		if strings.HasPrefix(v, "-") {
			sortStr += v[1:] + " DESC"
		} else {
			sortStr += v
		}
	}
	return sortStr
}

// ParseSelect prase select
func ParseSelect(fullQuery []string, filters []string) (selectStr string, selectFields map[string]bool) {
	if len(filters) == 0 {
		return strings.Join(fullQuery, ","), map[string]bool{}
	}
	minusSign := make(map[string]bool)
	selectFields = make(map[string]bool)
	for i, v := range filters {
		if i > 0 {
			selectStr += ","
		}
		if len(v) < 2 {
			continue
		}
		switch v[:1] {
		case "-":
			minusSign[v[1:]] = true
			continue
		case "$":
			selectStr += "DISTINCT(`" + v[1:] + "`)"
		default:
			selectStr += fmt.Sprintf("`%s`", v)
			selectFields[v] = true
		}
	}
	if len(minusSign) > 0 {
		selectStr = ""
		selectFields = make(map[string]bool)
		for i, v := range fullQuery {
			if !minusSign[v] {
				if i > 0 {
					selectStr += ","
				}
				selectStr += fmt.Sprintf("`%s`", v)
				selectFields[v] = true
			}
		}
	}
	return
}

// ParseOffset prase sort
func ParseOffset(page, limit int) int {
	if page <= 0 {
		return 0
	}
	return limit * (page - 1)
}

// ParseWhere prase where from query
func ParseWhere(filterData database.C, project string) (query string, args []any) {
	if project != "" {
		args = append(args, project)
		query = "`project`=? AND "
	}
	condsQuery, condsArgs := tidySQLConds(filterData, false)
	query += condsQuery
	args = append(args, condsArgs...)
	return
}

// Filter the k, v filters
type Filter struct {
	Key   string
	Value string
}
