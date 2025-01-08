package sql

import (
	"context"
	"database/sql"
	"reflect"

	"github.com/ti/common-go/dependencies/database"
)

// PageQuery query the documents
func PageQuery[T any](ctx context.Context, s *SQL, table string,
	in *database.PageQueryRequest,
) (out *database.PageQueryResponse[T], err error) {
	query := &Query{
		Table: table,
	}
	if in.Limit > 0 && in.Limit < 2000 {
		query.Limit = in.Limit
	} else {
		query.Limit = 2000
	}
	var data T
	fullQuery := TransformSQLQuery(&data)
	var selectFields map[string]bool
	query.Select, selectFields = ParseSelect(fullQuery, in.Select)
	query.Order = ParseSort(in.Sort)
	query.Offset = ParseOffset(in.Page, in.Limit)
	query.Where, query.Arguments = ParseWhere(in.Filters, s.project)
	out = &database.PageQueryResponse[T]{}
	var rows *sql.Rows
	// nolint: rowserrcheck
	rows, out.Total, err = queryData(ctx, table, s, in.Filters, query, in.NoCount)
	if err != nil {
		return nil, err
	}
	dataRows := DataRows{
		Rows:         rows,
		scheme:       s.scheme,
		dataType:     reflect.TypeOf(data),
		selectFields: selectFields,
		timeLoc:      s.loc,
	}
	defer dataRows.Close()
	for dataRows.Next() {
		rowData, errDec := dataRows.Decode()
		if errDec != nil {
			return nil, errDec
		}
		out.Data = append(out.Data, rowData.(*T))
	}
	return
}

func queryData(ctx context.Context, table string, s *SQL, filters database.C, query *Query,
	noCount bool,
) (rows *sql.Rows, total int64, err error) {
	if !noCount {
		total, err = s.Count(ctx, table, filters)
		if err != nil {
			return
		}
	}
	rows, err = s.QueryContext(ctx, query.String(), query.Arguments...)
	if err != nil {
		return
	}
	err = rows.Err()
	return
}
