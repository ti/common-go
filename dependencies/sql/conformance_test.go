package sql_test

import (
	"os"
	"testing"
	"time"

	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/database/dbtest"
	"github.com/ti/common-go/dependencies/sql"
	"github.com/ti/common-go/dependencies/sql/migrate"
)

// The conformance suite against real engines. Set the URIs to run it:
//
//	TEST_POSTGRES_URI='postgres://user:pass@host:5432/db?sslmode=disable'
//	TEST_MYSQL_URI='mysql://user:pass@host:3306/db?parseTime=true&loc=UTC'
func TestConformancePostgres(t *testing.T) { runConformance(t, "TEST_POSTGRES_URI") }

func TestConformanceMySQL(t *testing.T) { runConformance(t, "TEST_MYSQL_URI") }

func runConformance(t *testing.T, env string) {
	uri := os.Getenv(env)
	if uri == "" {
		t.Skipf("%s is not set", env)
	}
	db, err := database.New(t.Context(), uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(t.Context()) })
	err = db.(*sql.SQL).Migrate(t.Context(), dbtest.Migrations,
		migrate.WithDir("migrations"), migrate.WithTable("dbtest_schema_migrations"), migrate.WithLogf(t.Logf))
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	dbtest.Run(t, db, dbtest.Options{})
}

// A project view (GetDatabase) binds the project as a parameter. It used to
// be pasted into the statement, so a project containing a quote broke every
// query (and was an injection vector).
func TestProjectIsBound(t *testing.T) {
	for _, env := range []string{"TEST_POSTGRES_URI", "TEST_MYSQL_URI"} {
		t.Run(env, func(t *testing.T) {
			uri := os.Getenv(env)
			if uri == "" {
				t.Skipf("%s is not set", env)
			}
			ctx := t.Context()
			db, err := database.New(ctx, uri)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close(t.Context()) })
			raw := db.(*sql.SQL)
			for _, stmt := range []string{
				"DROP TABLE IF EXISTS dbtest_projects",
				"CREATE TABLE dbtest_projects (project varchar(64) NOT NULL, id varchar(64) NOT NULL, name varchar(64))",
			} {
				if _, err := raw.ExecContext(ctx, stmt); err != nil {
					t.Fatal(err)
				}
			}
			type row struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			const hostile = "o'brien' OR '1'='1"
			a, _ := db.GetDatabase(ctx, hostile)
			b, _ := db.GetDatabase(ctx, "other")
			if err := a.InsertOne(ctx, "dbtest_projects", &row{ID: "1", Name: "a"}); err != nil {
				t.Fatalf("InsertOne: %v", err)
			}
			if n, err := a.Insert(ctx, "dbtest_projects", []*row{{ID: "2", Name: "a"}, {ID: "3", Name: "a"}}); err != nil || n != 2 {
				t.Fatalf("Insert: %d, %v", n, err)
			}
			if err := b.InsertOne(ctx, "dbtest_projects", &row{ID: "9", Name: "b"}); err != nil {
				t.Fatal(err)
			}
			var rows []*row
			if err := a.Find(ctx, "dbtest_projects", nil, []string{"id"}, 0, &rows); err != nil || len(rows) != 3 {
				t.Fatalf("Find in the project: %d rows, %v", len(rows), err)
			}
			if n, err := b.Count(ctx, "dbtest_projects", nil); err != nil || n != 1 {
				t.Fatalf("Count in the other project: %d, %v", n, err)
			}
			if ok, err := a.Exist(ctx, "dbtest_projects", database.C{{Key: "id", Value: "9"}}); err != nil || ok {
				t.Fatalf("the other project's row is visible: %v, %v", ok, err)
			}
			if n, err := a.UpdateOne(ctx, "dbtest_projects", database.C{{Key: "id", Value: "9"}},
				database.D{{Key: "name", Value: "stolen"}}); err != nil || n != 0 {
				t.Fatalf("update across projects: %d, %v", n, err)
			}
			if n, err := a.Delete(ctx, "dbtest_projects", database.C{{Key: "name", Value: "a"}}); err != nil || n != 3 {
				t.Fatalf("Delete: %d, %v", n, err)
			}
			// Without a project and without a condition there is no WHERE at
			// all for reads; writes refuse to touch every row.
			if err := db.Find(ctx, "dbtest_projects", nil, nil, 0, &rows); err != nil {
				t.Fatalf("Find without a condition: %v", err)
			}
			if _, err := db.Delete(ctx, "dbtest_projects", nil); err == nil {
				t.Fatal("Delete without a condition: want an error")
			}
			_, _ = raw.ExecContext(ctx, "DROP TABLE IF EXISTS dbtest_projects")
		})
	}
}

// PageQuery on real engines with the conditions that used to be invalid SQL.
func TestPageQueryEmptyIn(t *testing.T) {
	for _, env := range []string{"TEST_POSTGRES_URI", "TEST_MYSQL_URI"} {
		t.Run(env, func(t *testing.T) {
			uri := os.Getenv(env)
			if uri == "" {
				t.Skipf("%s is not set", env)
			}
			ctx := t.Context()
			db, err := database.New(ctx, uri)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close(t.Context()) })
			raw := db.(*sql.SQL)
			for _, stmt := range []string{
				"DROP TABLE IF EXISTS dbtest_pages",
				"CREATE TABLE dbtest_pages (id varchar(64) NOT NULL PRIMARY KEY, name varchar(64))",
			} {
				if _, err := raw.ExecContext(ctx, stmt); err != nil {
					t.Fatal(err)
				}
			}
			type row struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if _, err := db.Insert(ctx, "dbtest_pages", []*row{{ID: "1", Name: "a"}, {ID: "2", Name: "b"}}); err != nil {
				t.Fatal(err)
			}
			page := func(cond database.C) (int, int64) {
				t.Helper()
				out, err := sql.PageQuery[row](ctx, raw, "dbtest_pages", &database.PageQueryRequest{Filters: cond, Sort: []string{"id"}})
				if err != nil {
					t.Fatalf("PageQuery %v: %v", cond, err)
				}
				return len(out.Data), out.Total
			}
			if n, total := page(database.C{{Key: "id", Value: []string{}, C: database.In}}); n != 0 || total != 0 {
				t.Fatalf("empty In: %d rows, total %d", n, total)
			}
			if n, total := page(database.C{{Key: "id", Value: []string{}, C: database.Nin}}); n != 2 || total != 2 {
				t.Fatalf("empty Nin: %d rows, total %d", n, total)
			}
			if n, total := page(database.C{{Key: "id", Value: []string{"2", "9"}, C: database.In}}); n != 1 || total != 1 {
				t.Fatalf("In: %d rows, total %d", n, total)
			}
			_, _ = raw.ExecContext(ctx, "DROP TABLE IF EXISTS dbtest_pages")
		})
	}
}

// With updateTime=, UpdateOne on MySQL reports NotFound for a record that
// matched but already had the values (0 rows affected). An unconditional
// repository write of unchanged values is still a success.
func TestIdempotentWriteWithUpdateTime(t *testing.T) {
	for _, env := range []string{"TEST_MYSQL_URI", "TEST_POSTGRES_URI"} {
		t.Run(env, func(t *testing.T) {
			uri := os.Getenv(env)
			if uri == "" {
				t.Skipf("%s is not set", env)
			}
			ctx := t.Context()
			db, err := database.New(ctx, uri+"&updateTime=update_time")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close(t.Context()) })
			err = db.(*sql.SQL).Migrate(ctx, dbtest.Migrations,
				migrate.WithDir("migrations"), migrate.WithTable("dbtest_schema_migrations"))
			if err != nil {
				t.Fatal(err)
			}
			items := database.MustCollection[dbtest.Item](db, dbtest.Schema).Scope("alice")
			_ = items.Delete(ctx, "idem", nil)
			it := &dbtest.Item{ID: "idem", Title: "t", Etag: "v1", CreateTime: time.Now().UTC().Truncate(time.Microsecond)}
			if err := items.Create(ctx, it); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := items.Patch(ctx, "idem", nil, database.D{{Key: "title", Value: "t"}}); err != nil {
					t.Fatalf("idempotent Patch: %v", err)
				}
				if err := items.Replace(ctx, it, nil); err != nil {
					t.Fatalf("idempotent Replace: %v", err)
				}
			}
			if err := items.Patch(ctx, "missing", nil, database.D{{Key: "title", Value: "t"}}); !database.IsNotFound(err) {
				t.Fatalf("Patch of a missing record: %v", err)
			}
			if err := items.Patch(ctx, "idem", "stale", database.D{{Key: "title", Value: "x"}, {Key: "etag", Value: "v2"}}); !database.IsConflict(err) {
				t.Fatalf("conditional Patch with a stale version: %v", err)
			}
			_ = items.Delete(ctx, "idem", nil)
		})
	}
}
