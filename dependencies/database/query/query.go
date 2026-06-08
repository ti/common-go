package query

import (
	"context"
	"reflect"

	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/database/mock"
	"github.com/ti/common-go/dependencies/mongo"
	"github.com/ti/common-go/dependencies/sql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PageQuery query the documents
func PageQuery[T any](ctx context.Context, d database.Database, table string,
	in *database.PageQueryRequest,
) (*database.PageQueryResponse[T], error) {
	if q, ok := d.(database.PageQuerier); ok {
		out := &database.PageQueryResponse[T]{}
		if err := q.DoPageQuery(ctx, table, in, out); err != nil {
			return nil, err
		}
		return out, nil
	}
	// Fallback to type assertions for backward compatibility.
	if client, ok := d.(*sql.SQL); ok {
		return sql.PageQuery[T](ctx, client, table, in)
	}
	if client, ok := d.(*mongo.Mongo); ok {
		return mongo.PageQuery[T](ctx, client, table, in)
	}
	if client, ok := d.(*mock.Mock); ok {
		return mock.PageQuery[T](ctx, client, table, in)
	}
	return nil, status.Errorf(codes.Unimplemented, "PageQuery unimplemented for %s ",
		reflect.TypeOf(d).String())
}

// StreamQuery query the documents
func StreamQuery[T any](ctx context.Context, d database.Database, table string,
	in *database.StreamQueryRequest,
) (*database.StreamResponse[T], error) {
	if q, ok := d.(database.StreamQuerier); ok {
		out := &database.StreamResponse[T]{}
		if err := q.DoStreamQuery(ctx, table, in, out); err != nil {
			return nil, err
		}
		return out, nil
	}
	// Fallback to type assertions for backward compatibility.
	if client, ok := d.(*sql.SQL); ok {
		return sql.StreamQuery[T](ctx, client, table, in)
	}
	if client, ok := d.(*mongo.Mongo); ok {
		return mongo.StreamQuery[T](ctx, client, table, in)
	}
	if client, ok := d.(*mock.Mock); ok {
		return mock.StreamQuery[T](ctx, client, table, in)
	}
	return nil, status.Errorf(codes.Unimplemented, "StreamQuery unimplemented for %s ",
		reflect.TypeOf(d).String())
}
