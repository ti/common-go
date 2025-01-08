package sql

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"math/big"
	"reflect"

	"github.com/ti/common-go/dependencies/database"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StreamQuery query the documents
func StreamQuery[T any](ctx context.Context, s *SQL, table string,
	in *database.StreamQueryRequest,
) (out *database.StreamResponse[T], err error) {
	query := &Query{
		Table:    table,
		SelectID: true,
		Limit:    2000,
	}
	if in.Limit > 0 && in.Limit < 2000 {
		query.Limit = in.Limit
	}
	query.Where, query.Arguments, err = parsePageTokenWhere(in.PageToken, in.Ascending)
	if err != nil {
		return nil, err
	}
	var data T
	var selectFields map[string]bool
	fullQuery := TransformSQLQuery(&data)
	query.Select, selectFields = ParseSelect(fullQuery, in.Select)
	parseOrderQuery(query, s, in)
	out = &database.StreamResponse[T]{}
	var rows *sql.Rows
	// nolint: rowserrcheck // it is checked in query data
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
	var firstID int64
	var lastID int64

	defer dataRows.Close()
	for dataRows.Next() {
		rowData, id, errDec := dataRows.DecodeWithID()
		if errDec != nil {
			return nil, errDec
		}
		if firstID == 0 {
			firstID = id
		} else {
			lastID = id
		}
		out.Data = append(out.Data, rowData.(*T))
	}
	if in.Ascending {
		for i, j := 0, len(out.Data)-1; i < j; i, j = i+1, j-1 {
			out.Data[i], out.Data[j] = out.Data[j], out.Data[i]
		}
	}
	if len(out.Data) == 0 {
		return
	}
	if in.PageToken == "" {
		firstID = 0
	}
	if len(out.Data) < in.Limit {
		lastID = 0
	}
	if firstID == 0 || lastID == 0 {
		out.PageToken = encodePageToken(firstID, lastID)
	}
	out.PageToken = encodePageToken(firstID, lastID)
	return
}

func parseOrderQuery(query *Query, s *SQL, in *database.StreamQueryRequest) {
	query.Order = "`_id`"
	if !in.Ascending {
		query.Order = "`_id` DESC"
	}
	if where, args := ParseWhere(in.Filters, s.project); where != "" {
		if query.Where != "" {
			query.Where += queryAnd + where
			query.Arguments = append(query.Arguments, args...)
		} else {
			query.Where = where
			query.Arguments = args
		}
	}
}

func encodePageToken(first, last int64) string {
	var pageTokenBytes []byte
	// store the version
	pageTokenBytes = append(pageTokenBytes, 1)
	firstBytes := big.NewInt(first).Bytes()
	lastBytes := big.NewInt(last).Bytes()
	pageTokenBytes = append(pageTokenBytes, uint8(len(firstBytes)))
	pageTokenBytes = append(pageTokenBytes, firstBytes...)
	pageTokenBytes = append(pageTokenBytes, lastBytes...)
	return base64.RawURLEncoding.EncodeToString(pageTokenBytes)
}

func decodePageToken(src string) (first, last int64, err error) {
	pageBytes, err := base64.RawURLEncoding.DecodeString(src)
	if err != nil || len(pageBytes) < 3 {
		return 0, 0, errors.New("invalid page token")
	}
	if pageBytes[0] != 1 {
		return 0, 0, errors.New("invalid page token version")
	}
	firstBytesLen := pageBytes[1]
	if firstBytesLen > 0 {
		firstBytes := pageBytes[2 : 2+firstBytesLen]
		first = (&big.Int{}).SetBytes(firstBytes).Int64()
	}
	lastBytes := pageBytes[2+firstBytesLen:]
	if len(lastBytes) > 0 {
		last = (&big.Int{}).SetBytes(lastBytes).Int64()
	}
	return
}

// parsePageTokenWhere prase page token to where from query
func parsePageTokenWhere(pageToken string, ascending bool) (string, []any, error) {
	if pageToken == "" {
		return "", nil, nil
	}
	pageFirst, pageLast, errPageToken := decodePageToken(pageToken)
	if errPageToken != nil {
		return "", nil, status.Error(codes.InvalidArgument, errPageToken.Error())
	}
	if !ascending && pageLast > 0 {
		return "`_id` < ? ", []any{pageLast}, nil
	}
	if ascending && pageFirst > 0 {
		return "`_id` > ? ", []any{pageFirst}, nil
	}
	return "", []any{}, nil
}
