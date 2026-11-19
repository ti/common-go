package sql

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/sql/adapters/postgres"
	"github.com/ti/common-go/dependencies/sql/migrate"
)

var (
	_ database.KeysetFinder  = (*SQL)(nil)
	_ database.Indexer       = (*SQL)(nil)
	_ database.HealthChecker = (*SQL)(nil)
	_ database.SystemNamer   = (*SQL)(nil)
)

// location is the time zone times are written and read in.
func (s *SQL) location() *time.Location {
	if s.loc == nil {
		//nolint:gosmopolitan // the documented default of the loc option
		return time.Local
	}
	return s.loc
}

// Time values are sent as text with microsecond precision, in the zone of the
// handle. PostgreSQL also gets the UTC offset, which timestamptz columns use
// and plain timestamp columns ignore; MySQL DATETIME has no zone.
const (
	layoutTimeMySQL    = "2006-01-02 15:04:05.999999"
	layoutTimePostgres = "2006-01-02 15:04:05.999999Z07:00"
)

func formatTime(scheme string, t time.Time, loc *time.Location) string {
	if loc == nil {
		//nolint:gosmopolitan // the documented default of the loc option
		loc = time.Local
	}
	if scheme == schemePostgres {
		return t.In(loc).Format(layoutTimePostgres)
	}
	return t.In(loc).Format(layoutTimeMySQL)
}

// timeArgs formats the time.Time arguments like Insert does, so that a value
// compares equal to itself whether it was inserted, updated or used in a
// condition.
func (s *SQL) timeArgs(args []any) []any {
	for i, a := range args {
		if t, ok := a.(time.Time); ok {
			args[i] = formatTime(s.scheme, t, s.location())
		}
	}
	return args
}

// where builds the body of a WHERE clause (without the keyword) from the
// project of the handle and the conditions. It is empty when there is
// neither. Every value is a bound parameter; every key is checked (ident.go).
func (s *SQL) where(conds database.C) (string, []any, error) {
	return s.whereMode(conds, s.compactMode)
}

// whereMode is where with an explicit choice of the MariaDB-compatible
// forms. The page and stream queries have always used the standard forms.
func (s *SQL) whereMode(conds database.C, compact bool) (string, []any, error) {
	where, args, err := buildWhere(s.scheme, s.project, conds, compact)
	if err != nil {
		return "", nil, err
	}
	return where, s.timeArgs(args), nil
}

// buildWhere is the one place a condition list becomes SQL text: CRUD, keyset,
// page and stream queries all go through it.
func buildWhere(scheme, project string, conds database.C, compact bool) (string, []any, error) {
	var (
		parts []string
		args  []any
	)
	if err := checkConds(conds); err != nil {
		return "", nil, err
	}
	if project != "" {
		parts = append(parts, "`project` = ?")
		args = append(args, project)
	}
	for _, c := range conds {
		if c.C == database.Contains {
			parts = append(parts, containsSQL(scheme, compact, c.Key))
			args = append(args, c.Value)
			continue
		}
		// "in the empty set" is false and "not in the empty set" is true;
		// `IN ()` is a syntax error.
		if c.C == database.In || c.C == database.Nin {
			if v := reflect.ValueOf(c.Value); !v.IsValid() || (v.Kind() != reflect.Slice && v.Kind() != reflect.Array) {
				return "", nil, status.Errorf(codes.InvalidArgument, "condition on %s: In/Nin need a slice", c.Key)
			} else if v.Len() == 0 {
				parts = append(parts, map[bool]string{true: "1 = 0", false: "1 = 1"}[c.C == database.In])
				continue
			}
		}
		q, a := tidySQLConds(database.C{c}, compact)
		parts = append(parts, strings.TrimSpace(q))
		args = append(args, a...)
	}
	return strings.Join(parts, queryJoinAnd), args, nil
}

// containsSQL is the predicate "the array column holds the bound value".
// PostgreSQL stores slices of scalars as arrays (text[], bigint[]); MySQL
// stores them as JSON.
func containsSQL(scheme string, compact bool, column string) string {
	switch {
	case scheme == schemePostgres:
		return fmt.Sprintf("? = ANY(`%s`)", column)
	case compact:
		return fmt.Sprintf("JSON_CONTAINS(`%s`, JSON_QUOTE(?))", column)
	default:
		return fmt.Sprintf("? MEMBER OF(`%s`)", column)
	}
}

// FindAfter implements database.KeysetFinder.
//
// For the order (a, b, c) and the position (x, y, z) the predicate is
//
//	a >= x AND (a > x OR (a = x AND b > y) OR (a = x AND b = y AND c > z))
//
// with < for descending fields. The expansion is used instead of the row
// value form (a, b, c) > (x, y, z) because it allows mixed directions and
// because MySQL does not use an index for row values; the redundant leading
// bound lets both engines start the index scan at the position.
func (s *SQL) FindAfter(ctx context.Context, table string, conds database.C, orderBy []string,
	after []any, limit int, arrayPtr any,
) error {
	eleType, _, err := sliceElem(arrayPtr)
	if err != nil {
		return err
	}
	if len(orderBy) == 0 {
		return status.Errorf(codes.InvalidArgument, "find after in %s: no order", table)
	}
	if err := checkFields("order field", orderBy); err != nil {
		return err
	}
	where, args, err := s.where(conds)
	if err != nil {
		return err
	}
	if len(after) > 0 {
		if len(after) != len(orderBy) {
			return status.Errorf(codes.InvalidArgument, "find after in %s: %d cursor values for %d order fields",
				table, len(after), len(orderBy))
		}
		keyset, keysetArgs := keysetSQL(orderBy, after)
		if where != "" {
			where += queryJoinAnd
		}
		where += keyset
		args = append(args, s.timeArgs(keysetArgs)...)
	}
	rows, err := s.selectRows(ctx, table, where, args, orderBy, limit, eleType)
	if err != nil {
		return err
	}
	return collectRows(rows, arrayPtr)
}

func keysetSQL(orderBy []string, after []any) (string, []any) {
	names := make([]string, len(orderBy))
	ops := make([]string, len(orderBy))
	for i, o := range orderBy {
		names[i], ops[i] = strings.TrimPrefix(o, "-"), ">"
		if strings.HasPrefix(o, "-") {
			ops[i] = "<"
		}
	}
	var (
		b    strings.Builder
		args []any
	)
	fmt.Fprintf(&b, "(`%s` %s= ? AND (", names[0], ops[0])
	args = append(args, after[0])
	for i := range names {
		if i > 0 {
			b.WriteString(" OR ")
		}
		b.WriteString("(")
		for j := range i {
			fmt.Fprintf(&b, "`%s` = ? AND ", names[j])
			args = append(args, after[j])
		}
		fmt.Fprintf(&b, "`%s` %s ?)", names[i], ops[i])
		args = append(args, after[i])
	}
	b.WriteString("))")
	return b.String(), args
}

// EnsureIndexes implements database.Indexer. The table must exist. Prefer
// migrations (Migrate) for SQL schemas; this exists so that code written
// against the portable API also runs on SQL.
func (s *SQL) EnsureIndexes(ctx context.Context, table string, indexes ...database.Index) error {
	if err := checkTable(table); err != nil {
		return err
	}
	for _, idx := range indexes {
		if len(idx.Fields) == 0 {
			return status.Errorf(codes.InvalidArgument, "index on %s has no fields", table)
		}
		if err := checkFields("index field", idx.Fields); err != nil {
			return err
		}
		if !identRE.MatchString(idx.IndexName(table)) {
			return invalidIdent("index", idx.IndexName(table))
		}
		cols := make([]string, len(idx.Fields))
		for i, f := range idx.Fields {
			cols[i] = "`" + strings.TrimPrefix(f, "-") + "`"
			if strings.HasPrefix(f, "-") {
				cols[i] += " DESC"
			}
		}
		unique, ifNotExists := "", ""
		if idx.Unique {
			unique = "UNIQUE "
		}
		if s.scheme == schemePostgres {
			ifNotExists = "IF NOT EXISTS "
		}
		query := fmt.Sprintf("CREATE %sINDEX %s`%s` ON `%s` (%s)",
			unique, ifNotExists, idx.IndexName(table), table, strings.Join(cols, ", "))
		if s.scheme == schemePostgres {
			query = postgres.ConvertSQL(query)
		}
		_, err := s.ExecContext(ctx, query)
		if err == nil {
			continue
		}
		// MySQL has no IF NOT EXISTS for indexes: 1061 is "duplicate key name".
		if myErr, ok := errors.AsType[*gomysql.MySQLError](err); ok && myErr.Number == 1061 {
			continue
		}
		return convertSQLError(s.scheme, err)
	}
	return nil
}

// HealthCheck implements database.HealthChecker with a ping of the pool.
func (s *SQL) HealthCheck(ctx context.Context) error {
	return s.PingContext(ctx)
}

// DBSystem implements database.SystemNamer.
func (s *SQL) DBSystem() string {
	if s.scheme == schemePostgres {
		return "postgresql"
	}
	return s.scheme
}

// Migrate applies the pending migrations of fsys (see package migrate) with
// the dialect of this connection. It is safe to call from every replica at
// startup.
func (s *SQL) Migrate(ctx context.Context, fsys fs.FS, opts ...migrate.Option) error {
	return migrate.Up(ctx, s.DB, s.scheme, fsys, opts...)
}
