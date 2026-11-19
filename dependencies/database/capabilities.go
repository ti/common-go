package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Optional capabilities. They are separate interfaces rather than methods of
// Database so that existing implementations keep compiling; the SQL, Mongo
// and mock backends of this module implement all of them. Use the package
// functions (FindAfter, EnsureIndexes, HealthCheck, Transact), which report
// codes.Unimplemented for a backend without the capability.

// KeysetFinder is implemented by backends that can continue a sorted query
// after a given position ("keyset" or "seek" pagination).
type KeysetFinder interface {
	// FindAfter is Find restricted to the records that come strictly after
	// the position `after` in the order `orderBy`. orderBy uses the Find
	// syntax ("field" ascending, "-field" descending) and must end in a
	// field that makes the order total (a unique id). after holds one value
	// per orderBy field; nil or empty means "from the start".
	FindAfter(ctx context.Context, table string, condition C, orderBy []string, after []any,
		limit int, arrayPtr any) error
}

// Indexer is implemented by backends that can create indexes.
type Indexer interface {
	// EnsureIndexes creates the indexes that do not exist yet. It is
	// idempotent and safe to call from several replicas starting at once.
	EnsureIndexes(ctx context.Context, table string, indexes ...Index) error
}

// HealthChecker is implemented by backends that can verify their connection.
type HealthChecker interface {
	HealthCheck(ctx context.Context) error
}

// SystemNamer is implemented by backends to name their engine with the
// OpenTelemetry `db.system.name` value ("postgresql", "mysql", "mongodb").
type SystemNamer interface {
	DBSystem() string
}

// Index describes one index for EnsureIndexes.
type Index struct {
	// Fields in index order; a "-" prefix means descending.
	Fields []string
	// Unique makes inserting a second record with the same field values
	// fail with AlreadyExists.
	Unique bool
	// Name of the index. Empty derives one from the fields.
	Name string
}

// IndexName returns Name, or a name derived from the table and the fields.
func (i Index) IndexName(table string) string {
	if i.Name != "" {
		return i.Name
	}
	var b strings.Builder
	b.WriteString("idx_" + table)
	for _, f := range i.Fields {
		b.WriteString("_" + strings.TrimPrefix(f, "-"))
	}
	if i.Unique {
		b.WriteString("_uniq")
	}
	return b.String()
}

// Unwrap returns the backend behind a *DB dependency wrapper, or db itself.
func Unwrap(db Database) Database {
	for {
		w, ok := db.(*DB)
		if !ok || w == nil || w.Database == nil {
			return db
		}
		db = w.Database
	}
}

// FindAfter runs a keyset query; see KeysetFinder.
func FindAfter(ctx context.Context, db Database, table string, condition C, orderBy []string,
	after []any, limit int, arrayPtr any,
) error {
	f, ok := Unwrap(db).(KeysetFinder)
	if !ok {
		return unimplemented("FindAfter", db)
	}
	return f.FindAfter(ctx, table, condition, orderBy, after, limit, arrayPtr)
}

// EnsureIndexes creates missing indexes; see Indexer. Call it at startup for
// document stores (MongoDB, the mock). For SQL prefer migrations
// (dependencies/sql/migrate); EnsureIndexes works there too but the table
// must already exist.
func EnsureIndexes(ctx context.Context, db Database, table string, indexes ...Index) error {
	i, ok := Unwrap(db).(Indexer)
	if !ok {
		return unimplemented("EnsureIndexes", db)
	}
	return i.EnsureIndexes(ctx, table, indexes...)
}

// healthCheckTimeout bounds one readiness probe of the database.
const healthCheckTimeout = 3 * time.Second

// HealthCheck returns a readiness check for db. Its type matches
// grpcmux.HealthChecker:
//
//	gs.AddHealthChecker(database.HealthCheck(db))
//
// The check pings the engine with a short timeout. A backend without the
// HealthChecker capability is always reported healthy.
func HealthCheck(db Database) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		h, ok := Unwrap(db).(HealthChecker)
		if !ok {
			return nil
		}
		ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
		defer cancel()
		if err := h.HealthCheck(ctx); err != nil {
			return fmt.Errorf("database: %w", err)
		}
		return nil
	}
}

// SystemName returns the OpenTelemetry `db.system.name` of db, or "unknown".
func SystemName(db Database) string {
	if n, ok := Unwrap(db).(SystemNamer); ok {
		return n.DBSystem()
	}
	return "unknown"
}

// Transact runs fn inside a transaction. It commits when fn returns nil and
// rolls back when fn returns an error or panics. tx is the handle whose
// operations belong to the transaction; ctx is passed through unchanged.
//
// Transactions need engine support: MySQL/PostgreSQL always, MongoDB only on
// a replica set or mongos. An engine may abort a transaction that conflicts
// with another one; that is reported as a conflict (IsConflict) and fn may be
// run again by the caller.
func Transact(ctx context.Context, db Database, fn func(ctx context.Context, tx Database) error) (err error) {
	db = Unwrap(db)
	tx, err := db.StartTransaction(ctx)
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			// fn panicked: roll back and let the panic continue.
			_ = tx.Rollback()
		}
	}()
	err = fn(ctx, db.WithTransaction(ctx, tx))
	done = true
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("%w (rollback: %s)", err, rbErr.Error())
		}
		return err
	}
	return tx.Commit()
}

func unimplemented(what string, db Database) error {
	return status.Errorf(codes.Unimplemented, "%s is not implemented by %T", what, Unwrap(db))
}
