package mongo

import (
	"context"
	"errors"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/dependencies/database"
)

var (
	_ database.KeysetFinder  = (*Mongo)(nil)
	_ database.Indexer       = (*Mongo)(nil)
	_ database.HealthChecker = (*Mongo)(nil)
	_ database.SystemNamer   = (*Mongo)(nil)
)

// sessionContext binds the transaction of this handle, if any, to ctx: the
// driver runs an operation inside a transaction only when its context
// carries the session.
func (m *Mongo) sessionContext(ctx context.Context) context.Context {
	if m.session == nil || m.session.session == nil {
		return ctx
	}
	return mongo.NewSessionContext(ctx, m.session.session)
}

// isTransientTransactionError reports a write conflict between transactions
// (the driver labels it; code 112 is WriteConflict outside the label).
func isTransientTransactionError(err error) bool {
	if se, ok := errors.AsType[mongo.ServerError](err); ok {
		return se.HasErrorLabel("TransientTransactionError") || se.HasErrorCode(112)
	}
	return false
}

// FindAfter implements database.KeysetFinder with an $or of the positions
// after the cursor:
//
//	{a: {$gt: x}} or {a: x, b: {$gt: y}} or {a: x, b: y, c: {$gt: z}}
//
// ($lt for descending fields). A compound index on the scope and order
// fields serves it.
func (m *Mongo) FindAfter(ctx context.Context, table string, conds database.C, orderBy []string,
	after []any, limit int, arrayPtr any,
) error {
	ctx = m.sessionContext(ctx)
	eleType, _, err := sliceElem(arrayPtr)
	if err != nil {
		return err
	}
	if len(orderBy) == 0 {
		return status.Errorf(codes.InvalidArgument, "find after in %s: no order", table)
	}
	filter := getCondition(m.project, conds)
	if len(after) > 0 {
		if len(after) != len(orderBy) {
			return status.Errorf(codes.InvalidArgument, "find after in %s: %d cursor values for %d order fields",
				table, len(after), len(orderBy))
		}
		// $and keeps a caller's own $or condition intact.
		filter = bson.D{{Key: "$and", Value: bson.A{filter, bson.D{{Key: "$or", Value: keysetFilter(orderBy, after)}}}}}
		if len(conds) == 0 && m.project == "" {
			filter = bson.D{{Key: "$or", Value: keysetFilter(orderBy, after)}}
		}
	}
	rows, err := m.findRows(ctx, table, filter, orderBy, limit, eleType)
	if err != nil {
		return convertToStatusError(table, err)
	}
	return collectRows(table, rows, arrayPtr)
}

func keysetFilter(orderBy []string, after []any) bson.A {
	alternatives := make(bson.A, 0, len(orderBy))
	for i, o := range orderBy {
		op := "$gt"
		if strings.HasPrefix(o, "-") {
			op = "$lt"
		}
		alt := make(bson.D, 0, i+1)
		for j := range i {
			alt = append(alt, bson.E{Key: strings.TrimPrefix(orderBy[j], "-"), Value: after[j]})
		}
		alt = append(alt, bson.E{Key: strings.TrimPrefix(o, "-"), Value: bson.D{{Key: op, Value: after[i]}}})
		alternatives = append(alternatives, alt)
	}
	return alternatives
}

// EnsureIndexes implements database.Indexer. createIndexes is a no-op for an
// index that already exists with the same definition, so replicas may call
// it concurrently at startup. An index of the same name or keys with other
// options is reported as an error (IndexOptionsConflict / IndexKeySpecsConflict):
// that is drift between the code and the database, not a race.
//
// Unlike the package function EnsureIndex, a unique index here is a plain
// unique index (no partial filter), so the uniqueness also holds for records
// that lack the field.
func (m *Mongo) EnsureIndexes(ctx context.Context, table string, indexes ...database.Index) error {
	if len(indexes) == 0 {
		return nil
	}
	models := make([]mongo.IndexModel, len(indexes))
	for i, idx := range indexes {
		if len(idx.Fields) == 0 {
			return status.Errorf(codes.InvalidArgument, "index on %s has no fields", table)
		}
		keys := make(bson.D, len(idx.Fields))
		for j, f := range idx.Fields {
			direction := 1
			if strings.HasPrefix(f, "-") {
				direction = -1
			}
			keys[j] = bson.E{Key: strings.TrimPrefix(f, "-"), Value: direction}
		}
		opts := options.Index().SetName(idx.IndexName(table))
		if idx.Unique {
			opts.SetUnique(true)
		}
		models[i] = mongo.IndexModel{Keys: keys, Options: opts}
	}
	if _, err := m.Collection(table).Indexes().CreateMany(ctx, models); err != nil {
		return status.Errorf(codes.FailedPrecondition, "create indexes for %s: %s", table, err.Error())
	}
	return nil
}

// HealthCheck implements database.HealthChecker with a ping of the primary.
func (m *Mongo) HealthCheck(ctx context.Context) error {
	if m.Client == nil {
		return errors.New("mongo client is closed")
	}
	return m.Ping(ctx, readpref.Primary())
}

// DBSystem implements database.SystemNamer.
func (m *Mongo) DBSystem() string { return "mongodb" }
