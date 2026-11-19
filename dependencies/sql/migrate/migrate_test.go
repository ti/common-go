package migrate_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/ti/common-go/dependencies/database"
	csql "github.com/ti/common-go/dependencies/sql"
	"github.com/ti/common-go/dependencies/sql/migrate"
)

func TestSplit(t *testing.T) {
	script := `
-- a comment; with a semicolon
CREATE TABLE a (id int, note text DEFAULT 'x;y');
/* block; comment */
INSERT INTO a VALUES (1, 'it''s; fine'), (2, "quoted;");
CREATE FUNCTION f() RETURNS int AS $body$
BEGIN
  RETURN 1; -- inside the body
END;
$body$ LANGUAGE plpgsql;
DO $$ BEGIN PERFORM 1; END $$;
SELECT "weird;name" FROM a WHERE id = $1;
-- trailing comment only
`
	got := migrate.Split(script, migrate.Postgres)
	if len(got) != 5 {
		t.Fatalf("got %d statements, want 5:\n%s", len(got), strings.Join(got, "\n---\n"))
	}
	for i, want := range []string{"CREATE TABLE a", "INSERT INTO a", "CREATE FUNCTION f()", "DO $$", `SELECT "weird;name"`} {
		// The first statement keeps its leading comment, which is harmless.
		if !strings.Contains(got[i], want) {
			t.Errorf("statement %d = %q, want it to contain %q", i, got[i], want)
		}
	}
	if !strings.HasSuffix(got[2], "LANGUAGE plpgsql") || !strings.Contains(got[2], "RETURN 1;") {
		t.Errorf("dollar-quoted body was cut: %q", got[2])
	}
	if n := len(migrate.Split("  \n-- nothing here\n/* at all */ ;;", migrate.Postgres)); n != 0 {
		t.Errorf("comment-only script gave %d statements", n)
	}
}

// The lexical rules that differ between the dialects.
func TestSplitDialects(t *testing.T) {
	for _, tc := range []struct {
		name, dialect, script string
		want                  []string
	}{
		{
			"postgres: a dollar tag may contain digits", migrate.Postgres,
			"CREATE FUNCTION g() RETURNS int AS $body1$ BEGIN RETURN 1; END; $body1$ LANGUAGE plpgsql; SELECT 2",
			[]string{"CREATE FUNCTION g() RETURNS int AS $body1$ BEGIN RETURN 1; END; $body1$ LANGUAGE plpgsql", "SELECT 2"},
		},
		{
			"postgres: a backslash does not escape the closing quote", migrate.Postgres,
			`INSERT INTO p VALUES ('C:\'); INSERT INTO p VALUES ('b')`,
			[]string{`INSERT INTO p VALUES ('C:\')`, `INSERT INTO p VALUES ('b')`},
		},
		{
			"postgres: E-strings have backslash escapes", migrate.Postgres,
			`INSERT INTO p VALUES (E'it\'s; one'); SELECT 2`,
			[]string{`INSERT INTO p VALUES (E'it\'s; one')`, "SELECT 2"},
		},
		{
			"postgres: a quote after a word ending in E is a plain string", migrate.Postgres,
			`SELECT 1 WHERE 'x%' LIKE 'x!%' ESCAPE'\'; SELECT 2; SELECT 'y';`,
			[]string{`SELECT 1 WHERE 'x%' LIKE 'x!%' ESCAPE'\'`, "SELECT 2", "SELECT 'y'"},
		},
		{
			"postgres: E-strings at the start, after punctuation and in lower case", migrate.Postgres,
			`E'a\';b'; SELECT (e'c\';d'); SELECT x_e'\'; SELECT 4`,
			[]string{`E'a\';b'`, `SELECT (e'c\';d')`, `SELECT x_e'\'`, "SELECT 4"},
		},
		{
			"postgres: a backtick and a hash are ordinary characters", migrate.Postgres,
			"SELECT '`'; SELECT 1 # 2; SELECT 3",
			[]string{"SELECT '`'", "SELECT 1 # 2", "SELECT 3"},
		},
		{
			"mysql: a backslash escapes the quote", migrate.MySQL,
			`INSERT INTO p VALUES ('it\'s; one'); SELECT 2`,
			[]string{`INSERT INTO p VALUES ('it\'s; one')`, "SELECT 2"},
		},
		{
			"mysql: hash comments", migrate.MySQL,
			"# leading; comment\nSELECT 1; # trailing; comment\nSELECT 2;\n# only a comment; here",
			[]string{"# leading; comment\nSELECT 1", "# trailing; comment\nSELECT 2"},
		},
		{
			"mysql: backtick identifiers, dollars are ordinary", migrate.MySQL,
			"SELECT `a;b` FROM t; SELECT '$$'; SELECT 3",
			[]string{"SELECT `a;b` FROM t", "SELECT '$$'", "SELECT 3"},
		},
	} {
		if got := migrate.Split(tc.script, tc.dialect); !slices.Equal(got, tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0002_two.sql":          {Data: []byte("SELECT 2")},
		"m/0001_one.postgres.sql": {Data: []byte("SELECT 'pg'")},
		"m/0001_one.mysql.sql":    {Data: []byte("SELECT 'my'")},
		"m/0003_three.sql":        {Data: []byte("SELECT 3")},
		"m/0003_three.mysql.sql":  {Data: []byte("SELECT 'my3'")},
		"m/README.md":             {Data: []byte("ignored")},
	}
	pg, err := migrate.Load(fsys, "m", migrate.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, m := range pg {
		files = append(files, m.File)
	}
	if want := []string{"0001_one.postgres.sql", "0002_two.sql", "0003_three.sql"}; !slices.Equal(files, want) {
		t.Fatalf("postgres files = %v, want %v", files, want)
	}
	my, err := migrate.Load(fsys, "m", migrate.MySQL)
	if err != nil {
		t.Fatal(err)
	}
	if my[0].SQL != "SELECT 'my'" || my[2].SQL != "SELECT 'my3'" {
		t.Fatalf("mysql did not prefer its own files: %+v", my)
	}
	if len(pg[0].Checksum) != 64 || pg[0].Checksum == my[0].Checksum {
		t.Fatalf("checksums: %q %q", pg[0].Checksum, my[0].Checksum)
	}
	for name, bad := range map[string]fstest.MapFS{
		"bad name":          {"x.sql": {Data: []byte("SELECT 1")}},
		"duplicate version": {"0001_a.sql": {Data: []byte("SELECT 1")}, "1_b.sql": {Data: []byte("SELECT 1")}},
		"version zero":      {"0000_a.sql": {Data: []byte("SELECT 1")}},
	} {
		if _, err := migrate.Load(bad, ".", migrate.Postgres); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// Bodies with bare semicolons cannot be split: Load says so instead of
// letting the engine fail on a fragment, and the no-split directive is the
// way to ship them.
func TestLoadRefusesUnsplittableBodies(t *testing.T) {
	const (
		atomic    = "CREATE FUNCTION f(a int) RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT a; SELECT a + 1; END"
		procedure = "CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END"
		trigger   = "CREATE TRIGGER trg BEFORE INSERT ON t FOR EACH ROW BEGIN SET NEW.a = 1; SET NEW.b = 2; END"
	)
	one := func(body string) fstest.MapFS { return fstest.MapFS{"0001_a.sql": {Data: []byte(body)}} }
	for name, tc := range map[string]struct {
		dialect, body string
	}{
		"postgres BEGIN ATOMIC":                   {migrate.Postgres, atomic},
		"mysql procedure":                         {migrate.MySQL, procedure},
		"mysql trigger":                           {migrate.MySQL, "CREATE TABLE t (a int, b int);\n" + trigger},
		"mysql DELIMITER":                         {migrate.MySQL, "DELIMITER //\n" + procedure + "//\nDELIMITER ;"},
		"mysql DELIMITER even with the directive": {migrate.MySQL, "-- migrate:no-split\nDELIMITER //\n" + procedure + "//"},
	} {
		_, err := migrate.Load(one(tc.body), ".", tc.dialect)
		if err == nil || !strings.Contains(err.Error(), "0001_a.sql") || !strings.Contains(err.Error(), "migrate:no-split") {
			t.Errorf("%s: got %v, want an error naming the file and the directive", name, err)
		}
	}
	// With the directive they load, flagged, and unchanged.
	for dialect, body := range map[string]string{migrate.Postgres: atomic, migrate.MySQL: procedure} {
		got, err := migrate.Load(one("-- migrate:no-split\n"+body), ".", dialect)
		if err != nil || len(got) != 1 || !got[0].NoSplit || !strings.HasSuffix(got[0].SQL, body) {
			t.Errorf("%s with the directive: %+v, %v", dialect, got, err)
		}
	}
	// What needs no directive: dollar-quoted bodies, plain BEGIN/COMMIT, and
	// the other dialect's constructs.
	for name, tc := range map[string]struct {
		dialect, body string
	}{
		"postgres dollar body":   {migrate.Postgres, "CREATE FUNCTION g() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql; SELECT 1"},
		"postgres plain BEGIN":   {migrate.Postgres, "CREATE TABLE begin_log (id int); INSERT INTO begin_log VALUES (1)"},
		"mysql plain statements": {migrate.MySQL, "CREATE TABLE a (id int); CREATE INDEX a_idx ON a (id)"},
	} {
		got, err := migrate.Load(one(tc.body), ".", tc.dialect)
		if err != nil || got[0].NoSplit {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
}

// The no-split files really create their objects on the engines.
func TestUpNoSplitPostgres(t *testing.T) {
	testUpNoSplit(t, "TEST_POSTGRES_URI", fstest.MapFS{
		"0001_fn.sql": {Data: []byte("-- migrate:no-split\nCREATE OR REPLACE FUNCTION migrate_test_fn(a int) RETURNS int LANGUAGE sql\n" +
			"BEGIN ATOMIC\n  SELECT a;\n  SELECT a + 1;\nEND")},
	}, "SELECT migrate_test_fn(41)", "DROP FUNCTION IF EXISTS migrate_test_fn(int)")
}

func TestUpNoSplitMySQL(t *testing.T) {
	testUpNoSplit(t, "TEST_MYSQL_URI", fstest.MapFS{
		"0001_fn.sql": {Data: []byte("-- migrate:no-split\nCREATE FUNCTION migrate_test_fn(a int) RETURNS int DETERMINISTIC\n" +
			"BEGIN\n  DECLARE b int;\n  SET b = a + 1;\n  RETURN b;\nEND")},
	}, "SELECT migrate_test_fn(41)", "DROP FUNCTION IF EXISTS migrate_test_fn")
}

func testUpNoSplit(t *testing.T, env string, fsys fstest.MapFS, query, drop string) {
	db, dialect := open(t, env)
	const books = "migrate_test_nosplit"
	cleanup := func() {
		_, _ = db.ExecContext(context.Background(), drop)
		_, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+books)
	}
	cleanup()
	t.Cleanup(cleanup)
	if err := migrate.Up(t.Context(), db, dialect, fsys, migrate.WithTable(books)); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var got int
	if err := db.QueryRowContext(t.Context(), query).Scan(&got); err != nil || got != 42 {
		t.Fatalf("%s = %d, %v (want 42)", query, got, err)
	}
}

func TestUpRejectsBadInput(t *testing.T) {
	if err := migrate.Up(t.Context(), nil, "sqlite", fstest.MapFS{}); err == nil {
		t.Error("unsupported dialect: want an error")
	}
	if err := migrate.Up(t.Context(), nil, migrate.Postgres, fstest.MapFS{}, migrate.WithTable("x; DROP TABLE y")); err == nil {
		t.Error("invalid table name: want an error")
	}
}

// open returns a pool on the engine named by env, or skips.
func open(t *testing.T, env string) (*sql.DB, string) {
	uri := os.Getenv(env)
	if uri == "" {
		t.Skipf("%s is not set", env)
	}
	db, err := database.New(t.Context(), uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	u, _ := url.Parse(uri)
	return db.(*csql.SQL).DB, u.Scheme
}

func TestUpPostgres(t *testing.T) { testUp(t, "TEST_POSTGRES_URI") }

func TestUpMySQL(t *testing.T) { testUp(t, "TEST_MYSQL_URI") }

func testUp(t *testing.T, env string) {
	db, dialect := open(t, env)
	ctx := t.Context()
	const books = "migrate_test_migrations"
	drop := func() {
		// Also runs as cleanup, when t.Context() is already cancelled.
		for _, table := range []string{books, "migrate_test_a", "migrate_test_b"} {
			if _, err := db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table); err != nil {
				t.Error(err)
			}
		}
	}
	drop()
	t.Cleanup(drop)
	fsys := fstest.MapFS{
		"0001_a.sql": {Data: []byte("CREATE TABLE migrate_test_a (id int PRIMARY KEY, v varchar(20));\n" +
			"INSERT INTO migrate_test_a VALUES (1, 'a;b');")},
		"0002_b.sql": {Data: []byte("CREATE TABLE migrate_test_b (id int PRIMARY KEY)")},
	}
	// Several replicas start at once, each with its own connection: every
	// migration must be applied exactly once and nobody may fail.
	const replicas = 6
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		applied []string
		errs    []error
	)
	for range replicas {
		wg.Go(func() {
			err := migrate.Up(ctx, db, dialect, fsys, migrate.WithTable(books),
				migrate.WithLogf(func(format string, args ...any) {
					mu.Lock()
					applied = append(applied, fmt.Sprintf(format, args...))
					mu.Unlock()
				}))
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent Up failed: %v", errs)
	}
	slices.Sort(applied)
	if want := []string{"migrate: applied 0001_a.sql", "migrate: applied 0002_b.sql"}; !slices.Equal(applied, want) {
		t.Fatalf("applied = %v, want each migration exactly once", applied)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM migrate_test_a").Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows in migrate_test_a = %d, %v (a second run would have failed or duplicated)", n, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+books).Scan(&n); err != nil || n != 2 {
		t.Fatalf("bookkeeping rows = %d, %v", n, err)
	}
	// Nothing to do the second time.
	if err := migrate.Up(ctx, db, dialect, fsys, migrate.WithTable(books)); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	// History is immutable: an edited or removed migration is refused.
	edited := fstest.MapFS{"0001_a.sql": {Data: []byte("SELECT 1")}, "0002_b.sql": fsys["0002_b.sql"]}
	if err := migrate.Up(ctx, db, dialect, edited, migrate.WithTable(books)); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("edited migration: got %v", err)
	}
	removed := fstest.MapFS{"0002_b.sql": fsys["0002_b.sql"]}
	if err := migrate.Up(ctx, db, dialect, removed, migrate.WithTable(books)); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("removed migration: got %v", err)
	}
	// A failing migration reports its file and is not recorded.
	failing := fstest.MapFS{"0001_a.sql": fsys["0001_a.sql"], "0002_b.sql": fsys["0002_b.sql"],
		"0003_broken.sql": {Data: []byte("CREATE TABLE migrate_test_a (id int)")}}
	if err := migrate.Up(ctx, db, dialect, failing, migrate.WithTable(books)); err == nil || !strings.Contains(err.Error(), "0003_broken.sql") {
		t.Fatalf("failing migration: got %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+books).Scan(&n); err != nil || n != 2 {
		t.Fatalf("bookkeeping rows after a failed migration = %d, %v", n, err)
	}
	// The lock was released on every path: another run does not block.
	if err := migrate.Up(ctx, db, dialect, fsys, migrate.WithTable(books)); err != nil {
		t.Fatalf("Up after failures: %v", err)
	}
}
