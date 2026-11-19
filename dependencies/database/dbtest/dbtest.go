// Package dbtest is the conformance suite of the storage layer: one set of
// behavioural tests that every database.Database backend must pass, so that
// code tested against the mock behaves the same on PostgreSQL, MySQL and
// MongoDB.
//
// The backends of this module run it in their own tests (the real engines
// when TEST_POSTGRES_URI, TEST_MYSQL_URI or TEST_MONGODB_URI is set). A
// product with its own backend, or its own wrapper around one, can run it
// too.
package dbtest

import (
	"context"
	"embed"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/dependencies/database"
)

// Table is the table the suite reads and writes. SQL backends must create it
// first with Migrations; document stores create it on the first write.
const Table = "dbtest_items"

// Migrations holds the SQL schema of Table for dependencies/sql/migrate
// (directory "migrations").
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Item is the entity of the suite.
type Item struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	Labels     []string  `json:"labels"`
	Rank       int64     `json:"rank"`
	Etag       string    `json:"etag"`
	CreateTime time.Time `json:"create_time"`
}

// Options describe what the engine under test can do.
type Options struct {
	// TimePrecision is the precision the engine stores a time.Time with:
	// time.Microsecond for SQL (the default), time.Millisecond for MongoDB.
	TimePrecision time.Duration
	// NoTransactions skips the transaction tests (a standalone MongoDB).
	NoTransactions bool
}

// Schema is the schema of the suite's collection.
var Schema = database.Schema{
	Table:   Table,
	Scope:   "owner",
	Version: "etag",
	OrderBy: []string{"-create_time", "-id"},
}

// Run runs the suite against db. It deletes the content of Table first.
func Run(t *testing.T, db database.Database, opts Options) {
	t.Helper()
	if opts.TimePrecision == 0 {
		opts.TimePrecision = time.Microsecond
	}
	s := &suite{db: db, opts: opts, base: time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC).Truncate(opts.TimePrecision)}
	s.items = database.MustCollection[Item](db, Schema)
	if err := database.EnsureIndexes(t.Context(), db, Table,
		database.Index{Fields: []string{"id"}, Unique: true},
		database.Index{Fields: []string{"owner", "-create_time", "-id"}},
	); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}
	// Idempotent: a second replica doing the same at startup must be fine.
	if err := database.EnsureIndexes(t.Context(), db, Table, database.Index{Fields: []string{"id"}, Unique: true}); err != nil {
		t.Fatalf("EnsureIndexes again: %v", err)
	}
	for _, tc := range []struct {
		name string
		fn   func(*testing.T)
	}{
		{"HealthCheck", s.testHealth},
		{"CreateGet", s.testCreateGet},
		{"Conditions", s.testConditions},
		{"Scope", s.testScope},
		{"CompareAndSwap", s.testCompareAndSwap},
		{"ConcurrentWriters", s.testConcurrentWriters},
		{"List", s.testList},
		{"PageTokens", s.testPageTokens},
		{"Transactions", s.testTransactions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.reset(t)
			tc.fn(t)
		})
	}
	s.reset(t)
}

type suite struct {
	db    database.Database
	opts  Options
	items *database.Collection[Item]
	base  time.Time
}

func (s *suite) reset(t *testing.T) {
	t.Helper()
	if _, err := s.db.Delete(t.Context(), Table, database.C{{Key: "id", Value: "", C: database.Ne}}); err != nil {
		t.Fatalf("reset: %v", err)
	}
}

func (s *suite) item(n int, owner string) *Item {
	return &Item{
		ID: fmt.Sprintf("i%03d", n), Owner: owner, Title: fmt.Sprintf("title %d", n), Body: "body",
		Labels: []string{"all", fmt.Sprintf("mod%d", n%3)}, Rank: int64(n), Etag: "v1",
		// Two items share each timestamp, so the id must break ties.
		CreateTime: s.base.Add(time.Duration(n/2) * time.Second),
	}
}

func (s *suite) seed(t *testing.T, owner string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if err := s.items.Scope(owner).Create(t.Context(), s.item(i, owner)); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
}

func wantCode(t *testing.T, err error, want codes.Code, what string) {
	t.Helper()
	if status.Code(err) != want {
		t.Fatalf("%s: got %v (code %s), want code %s", what, err, status.Code(err), want)
	}
}

func (s *suite) testHealth(t *testing.T) {
	if err := database.HealthCheck(s.db)(t.Context()); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if name := database.SystemName(s.db); name == "" || name == "unknown" {
		t.Fatalf("SystemName = %q", name)
	}
}

func (s *suite) testCreateGet(t *testing.T) {
	ctx := t.Context()
	alice := s.items.Scope("alice")
	in := s.item(1, "")
	if err := alice.Create(ctx, in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if in.Owner != "alice" {
		t.Fatalf("Create did not stamp the scope: owner = %q", in.Owner)
	}
	got, err := alice.Get(ctx, "i001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != in.ID || got.Owner != "alice" || got.Title != in.Title || got.Body != in.Body ||
		!slices.Equal(got.Labels, in.Labels) || got.Rank != in.Rank || got.Etag != in.Etag {
		t.Fatalf("Get = %+v, want %+v", got, in)
	}
	if !got.CreateTime.Equal(in.CreateTime) {
		t.Fatalf("time did not round-trip at %s precision: got %s, want %s",
			s.opts.TimePrecision, got.CreateTime.Format(time.RFC3339Nano), in.CreateTime.Format(time.RFC3339Nano))
	}
	wantCode(t, alice.Create(ctx, s.item(1, "alice")), codes.AlreadyExists, "duplicate Create")
	if !database.IsAlreadyExists(alice.Create(ctx, s.item(1, "alice"))) {
		t.Fatal("IsAlreadyExists is false for a duplicate")
	}
	_, err = alice.Get(ctx, "missing")
	wantCode(t, err, codes.NotFound, "Get missing")
	if !database.IsNotFound(err) {
		t.Fatal("IsNotFound is false for a missing record")
	}
	if ok, err := alice.Exists(ctx, "i001"); err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if ok, err := alice.Exists(ctx, "missing"); err != nil || ok {
		t.Fatalf("Exists(missing) = %v, %v", ok, err)
	}
	// A record without the optional fields reads back with empty values.
	if err := alice.Create(ctx, &Item{ID: "bare", CreateTime: s.base}); err != nil {
		t.Fatalf("Create bare: %v", err)
	}
	bare, err := alice.Get(ctx, "bare")
	if err != nil || bare.Body != "" || len(bare.Labels) != 0 || bare.Rank != 0 {
		t.Fatalf("bare = %+v, %v", bare, err)
	}
}

// testConditions exercises the raw Database interface the way repositories
// with hand-written queries use it.
func (s *suite) testConditions(t *testing.T) {
	ctx := t.Context()
	s.seed(t, "alice", 6) // ranks 1..6; create_time base+0s (1), +1s (2,3), +2s (4,5), +3s (6)
	ids := func(cond database.C, sortBy []string, limit int) []string {
		t.Helper()
		var rows []*Item
		if err := s.db.Find(ctx, Table, cond, sortBy, limit, &rows); err != nil {
			t.Fatalf("Find %v: %v", cond, err)
		}
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.ID
		}
		return out
	}
	check := func(what string, got []string, want ...string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Fatalf("%s: got %v, want %v", what, got, want)
		}
	}
	asc := []string{"id"}
	check("Eq", ids(database.C{{Key: "rank", Value: int64(3)}}, asc, 0), "i003")
	check("Ne", ids(database.C{{Key: "rank", Value: int64(3), C: database.Ne}}, asc, 0), "i001", "i002", "i004", "i005", "i006")
	check("Lt", ids(database.C{{Key: "rank", Value: int64(3), C: database.Lt}}, asc, 0), "i001", "i002")
	check("Lte", ids(database.C{{Key: "rank", Value: int64(3), C: database.Lte}}, asc, 0), "i001", "i002", "i003")
	check("Gt", ids(database.C{{Key: "rank", Value: int64(4), C: database.Gt}}, asc, 0), "i005", "i006")
	check("Gte", ids(database.C{{Key: "rank", Value: int64(4), C: database.Gte}}, asc, 0), "i004", "i005", "i006")
	check("In", ids(database.C{{Key: "id", Value: []string{"i002", "i005"}, C: database.In}}, asc, 0), "i002", "i005")
	check("Nin", ids(database.C{{Key: "id", Value: []string{"i002", "i005"}, C: database.Nin}}, asc, 0), "i001", "i003", "i004", "i006")
	check("In nothing", ids(database.C{{Key: "id", Value: []string{}, C: database.In}}, asc, 0))
	check("Nin nothing", ids(database.C{{Key: "id", Value: []string{}, C: database.Nin}, {Key: "rank", Value: int64(2), C: database.Lte}}, asc, 0),
		"i001", "i002")
	check("Contains", ids(database.C{{Key: "labels", Value: "mod0", C: database.Contains}}, asc, 0), "i003", "i006")
	check("time Lt", ids(database.C{{Key: "create_time", Value: s.base.Add(2 * time.Second), C: database.Lt}}, asc, 0),
		"i001", "i002", "i003")
	check("time Eq", ids(database.C{{Key: "create_time", Value: s.base.Add(2 * time.Second)}}, asc, 0), "i004", "i005")
	check("AND", ids(database.C{{Key: "owner", Value: "alice"}, {Key: "rank", Value: int64(2), C: database.Gt},
		{Key: "labels", Value: "mod1", C: database.Contains}}, asc, 0), "i004")
	check("sort desc, limit", ids(database.C{{Key: "owner", Value: "alice"}}, []string{"-create_time", "-id"}, 3),
		"i006", "i005", "i004")

	// Both slice shapes are accepted by every backend.
	var values []Item
	if err := s.db.Find(ctx, Table, database.C{{Key: "owner", Value: "alice"}}, asc, 2, &values); err != nil || len(values) != 2 {
		t.Fatalf("Find into []T: %d rows, %v", len(values), err)
	}
	if n, err := s.db.Count(ctx, Table, database.C{{Key: "rank", Value: int64(2), C: database.Gt}}); err != nil || n != 4 {
		t.Fatalf("Count = %d, %v", n, err)
	}
	if ok, err := s.db.Exist(ctx, Table, database.C{{Key: "id", Value: "i004"}}); err != nil || !ok {
		t.Fatalf("Exist = %v, %v", ok, err)
	}
	if ok, err := s.db.Exist(ctx, Table, database.C{{Key: "id", Value: "nope"}}); err != nil || ok {
		t.Fatalf("Exist(nope) = %v, %v", ok, err)
	}
	var one Item
	wantCode(t, s.db.FindOne(ctx, Table, database.C{{Key: "id", Value: "nope"}}, &one), codes.NotFound, "FindOne missing")
}

func (s *suite) testScope(t *testing.T) {
	ctx := t.Context()
	alice, bob := s.items.Scope("alice"), s.items.Scope("bob")
	s.seed(t, "alice", 2)
	if err := bob.Create(ctx, s.item(9, "bob")); err != nil {
		t.Fatal(err)
	}
	// Another owner's record does not exist, for reads and for writes.
	_, err := bob.Get(ctx, "i001")
	wantCode(t, err, codes.NotFound, "Get foreign")
	stolen := s.item(1, "bob")
	stolen.Etag = "v2"
	wantCode(t, bob.Replace(ctx, stolen, "v1"), codes.NotFound, "Replace foreign")
	wantCode(t, bob.Replace(ctx, stolen, nil), codes.NotFound, "unconditional Replace foreign")
	wantCode(t, bob.Patch(ctx, "i001", nil, database.D{{Key: "title", Value: "x"}}), codes.NotFound, "Patch foreign")
	wantCode(t, bob.Delete(ctx, "i001", nil), codes.NotFound, "Delete foreign")
	wantCode(t, bob.Delete(ctx, "i001", "v1"), codes.NotFound, "conditional Delete foreign")
	if got, err := alice.Get(ctx, "i001"); err != nil || got.Title != "title 1" || got.Owner != "alice" {
		t.Fatalf("alice's record was touched: %+v, %v", got, err)
	}
	// A record cannot be created or moved into another scope.
	wantCode(t, bob.Create(ctx, s.item(7, "alice")), codes.InvalidArgument, "Create with a foreign owner")
	wantCode(t, alice.Patch(ctx, "i001", nil, database.D{{Key: "owner", Value: "bob"}}), codes.InvalidArgument, "Patch owner")
	// Lists and counts are per owner.
	page, err := bob.List(ctx, database.ListRequest{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "i009" {
		t.Fatalf("bob's list = %+v, %v", page, err)
	}
	if n, err := alice.Count(ctx, nil); err != nil || n != 2 {
		t.Fatalf("alice's count = %d, %v", n, err)
	}
	// Keys, owners and versions are case-sensitive on every engine (MySQL
	// needs a binary collation on those columns for it).
	_, err = alice.Get(ctx, "I001")
	wantCode(t, err, codes.NotFound, "Get with another case of the id")
	if n, err := s.items.Scope("Alice").Count(ctx, nil); err != nil || n != 0 {
		t.Fatalf("owner \"Alice\" sees %d of alice's records, %v", n, err)
	}
	wantCode(t, alice.Delete(ctx, "i001", "V1"), codes.Aborted, "Delete with another case of the version")
	// Only declared fields are accepted, compared exactly: a field name that
	// differs in case, or that carries statement text, is refused by the
	// repository before it reaches the engine.
	for _, key := range []string{"Owner", "OWNER", "Title", "title` = 'x', `owner", "$set", "title; DROP TABLE dbtest_items"} {
		wantCode(t, alice.Patch(ctx, "i001", nil, database.D{{Key: key, Value: "bob"}}), codes.InvalidArgument, "Patch "+key)
		_, err = alice.List(ctx, database.ListRequest{Filter: database.C{{Key: key, Value: "bob"}}})
		wantCode(t, err, codes.InvalidArgument, "List filter "+key)
	}
	if got, err := alice.Get(ctx, "i001"); err != nil || got.Owner != "alice" || got.Title != "title 1" {
		t.Fatalf("after rejected patches: %+v, %v", got, err)
	}
	// The condition of a failed repository matches nothing when it is handed
	// to the raw interface.
	for _, repo := range []*database.Repo[Item]{s.items.Scope(""), s.items.Scope(nil)} {
		if n, err := s.db.Count(ctx, Table, repo.Cond(nil)); err != nil || n != 0 {
			t.Fatalf("Count with the condition of an empty scope = %d, %v", n, err)
		}
		if n, err := s.db.Delete(ctx, Table, repo.Cond(nil)); err != nil || n != 0 {
			t.Fatalf("Delete with the condition of an empty scope removed %d, %v", n, err)
		}
	}
	if n, err := s.items.Unscoped().Count(ctx, nil); err != nil || n != 3 {
		t.Fatalf("records left = %d, %v (want 3)", n, err)
	}
	// An empty scope never widens to everything.
	_, err = s.items.Scope("").List(ctx, database.ListRequest{})
	wantCode(t, err, codes.InvalidArgument, "List with an empty scope")
	_, err = s.items.Scope("").Get(ctx, "i001")
	wantCode(t, err, codes.InvalidArgument, "Get with an empty scope")
	// Unscoped is the explicit way to see all owners.
	if n, err := s.items.Unscoped().Count(ctx, nil); err != nil || n != 3 {
		t.Fatalf("unscoped count = %d, %v", n, err)
	}
}

func (s *suite) testCompareAndSwap(t *testing.T) {
	ctx := t.Context()
	alice := s.items.Scope("alice")
	s.seed(t, "alice", 2)

	next := s.item(1, "alice")
	next.Title, next.Body, next.Labels, next.Etag = "second", "", nil, "v2"
	if err := alice.Replace(ctx, next, "v1"); err != nil {
		t.Fatalf("Replace with the current version: %v", err)
	}
	got, err := alice.Get(ctx, "i001")
	if err != nil || got.Title != "second" || got.Etag != "v2" {
		t.Fatalf("after Replace: %+v, %v", got, err)
	}
	// Replace writes every field: empty values overwrite stored ones.
	if got.Body != "" || len(got.Labels) != 0 {
		t.Fatalf("Replace did not clear body/labels: %+v", got)
	}
	// The same expected version again is stale now.
	stale := s.item(1, "alice")
	stale.Title, stale.Etag = "lost update", "v3"
	err = alice.Replace(ctx, stale, "v1")
	wantCode(t, err, codes.Aborted, "Replace with a stale version")
	if !database.IsConflict(err) {
		t.Fatal("IsConflict is false for a stale version")
	}
	if got, _ := alice.Get(ctx, "i001"); got == nil || got.Title != "second" {
		t.Fatalf("a rejected Replace changed the record: %+v", got)
	}
	// A conditional write must change the version.
	same := s.item(1, "alice")
	same.Etag = "v2"
	wantCode(t, alice.Replace(ctx, same, "v2"), codes.InvalidArgument, "Replace keeping the version")
	// Missing record.
	ghost := s.item(77, "alice")
	ghost.Etag = "v2"
	wantCode(t, alice.Replace(ctx, ghost, "v1"), codes.NotFound, "Replace missing")
	wantCode(t, alice.Replace(ctx, ghost, nil), codes.NotFound, "unconditional Replace missing")
	// Unconditional replace, twice with the same content: both succeed.
	free := s.item(2, "alice")
	free.Title = "unconditional"
	for range 2 {
		if err := alice.Replace(ctx, free, nil); err != nil {
			t.Fatalf("unconditional Replace: %v", err)
		}
	}
	// Patch: only the named fields change.
	err = alice.Patch(ctx, "i002", "v1", database.D{{Key: "rank", Value: int64(42)}, {Key: "etag", Value: "v2"}})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	got, err = alice.Get(ctx, "i002")
	if err != nil || got.Rank != 42 || got.Title != "unconditional" || got.Etag != "v2" {
		t.Fatalf("after Patch: %+v, %v", got, err)
	}
	wantCode(t, alice.Patch(ctx, "i002", "v1", database.D{{Key: "rank", Value: int64(1)}, {Key: "etag", Value: "v3"}}),
		codes.Aborted, "Patch with a stale version")
	// Delete compares too.
	wantCode(t, alice.Delete(ctx, "i002", "v1"), codes.Aborted, "Delete with a stale version")
	if err := alice.Delete(ctx, "i002", "v2"); err != nil {
		t.Fatalf("Delete with the current version: %v", err)
	}
	wantCode(t, alice.Delete(ctx, "i002", "v2"), codes.NotFound, "Delete again")
	if err := alice.Delete(ctx, "i001", nil); err != nil {
		t.Fatalf("unconditional Delete: %v", err)
	}
	wantCode(t, alice.Delete(ctx, "i001", nil), codes.NotFound, "unconditional Delete missing")
}

// testConcurrentWriters proves that the comparison and the write are atomic:
// writers that read-modify-write the same record with retries must not lose
// an update.
func (s *suite) testConcurrentWriters(t *testing.T) {
	ctx := t.Context()
	alice := s.items.Scope("alice")
	start := s.item(1, "alice")
	start.Rank = 0
	if err := alice.Create(ctx, start); err != nil {
		t.Fatal(err)
	}
	const writers, each = 8, 5
	var (
		wg        sync.WaitGroup
		conflicts atomic.Int64
		failed    atomic.Value
	)
	for range writers {
		wg.Go(func() {
			for range each {
				for {
					cur, err := alice.Get(ctx, "i001")
					if err != nil {
						failed.Store(err)
						return
					}
					err = alice.Patch(ctx, "i001", cur.Etag, database.D{
						{Key: "rank", Value: cur.Rank + 1}, {Key: "etag", Value: database.NewVersion()},
					})
					if database.IsConflict(err) {
						conflicts.Add(1)
						continue
					}
					if err != nil {
						failed.Store(err)
						return
					}
					break
				}
			}
		})
	}
	wg.Wait()
	if err, _ := failed.Load().(error); err != nil {
		t.Fatalf("writer failed: %v", err)
	}
	got, err := alice.Get(ctx, "i001")
	if err != nil || got.Rank != writers*each {
		t.Fatalf("rank = %d after %d increments (%d conflicts retried), %v", got.Rank, writers*each, conflicts.Load(), err)
	}
	t.Logf("%d increments, %d conflicts retried", writers*each, conflicts.Load())
}

func (s *suite) testList(t *testing.T) {
	ctx := t.Context()
	alice := s.items.Scope("alice")
	s.seed(t, "alice", 25)
	for i := 31; i <= 33; i++ { // another owner's records never show up
		if err := s.items.Scope("bob").Create(ctx, s.item(i, "bob")); err != nil {
			t.Fatal(err)
		}
	}
	want := make([]string, 0, 25)
	for i := 25; i >= 1; i-- {
		want = append(want, fmt.Sprintf("i%03d", i))
	}
	var (
		got   []string
		token string
		sizes []int
	)
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging does not terminate")
		}
		page, err := alice.List(ctx, database.ListRequest{PageSize: 10, PageToken: token})
		if err != nil {
			t.Fatalf("List page %d: %v", pages, err)
		}
		sizes = append(sizes, len(page.Items))
		for _, it := range page.Items {
			got = append(got, it.ID)
		}
		if pages == 0 {
			// A record created while the client pages is newer than every
			// position: it must not shift or repeat the later pages.
			newer := s.item(60, "alice")
			newer.ID = "i900"
			if err := alice.Create(ctx, newer); err != nil {
				t.Fatal(err)
			}
		}
		if token = page.NextPageToken; token == "" {
			break
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("list order:\n got %v\nwant %v", got, want)
	}
	if !slices.Equal(sizes, []int{10, 10, 5}) {
		t.Fatalf("page sizes = %v, want [10 10 5]", sizes)
	}
	// Filter and another order.
	page, err := alice.List(ctx, database.ListRequest{
		Filter:  database.C{{Key: "labels", Value: "mod0", C: database.Contains}, {Key: "rank", Value: int64(10), C: database.Lt}},
		OrderBy: []string{"rank"},
	})
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if ids := itemIDs(page.Items); !slices.Equal(ids, []string{"i003", "i006", "i009"}) || page.NextPageToken != "" {
		t.Fatalf("filtered list = %v, token %q", ids, page.NextPageToken)
	}
	// Page size rules (AIP-158): 0 is the default, too large is lowered,
	// negative is an error; an exact fit has no next page.
	page, err = alice.List(ctx, database.ListRequest{})
	if err != nil || len(page.Items) != database.DefaultPageSize || page.NextPageToken == "" {
		t.Fatalf("default page: %d items, token %q, %v", len(page.Items), page.NextPageToken, err)
	}
	page, err = alice.List(ctx, database.ListRequest{PageSize: 100000})
	if err != nil || len(page.Items) != 26 || page.NextPageToken != "" {
		t.Fatalf("oversized page: %d items, token %q, %v", len(page.Items), page.NextPageToken, err)
	}
	page, err = alice.List(ctx, database.ListRequest{PageSize: 26})
	if err != nil || len(page.Items) != 26 || page.NextPageToken != "" {
		t.Fatalf("exact page: %d items, token %q, %v", len(page.Items), page.NextPageToken, err)
	}
	_, err = alice.List(ctx, database.ListRequest{PageSize: -1})
	wantCode(t, err, codes.InvalidArgument, "negative page size")
	// The raw keyset query, for services that own their token format.
	rows, err := alice.ListAfter(ctx, nil, nil, []any{s.base.Add(2 * time.Second), "i005"}, 3)
	if err != nil || !slices.Equal(itemIDs(rows), []string{"i004", "i003", "i002"}) {
		t.Fatalf("ListAfter = %v, %v", itemIDs(rows), err)
	}
}

func itemIDs(items []*Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func (s *suite) testPageTokens(t *testing.T) {
	ctx := t.Context()
	alice, bob := s.items.Scope("alice"), s.items.Scope("bob")
	s.seed(t, "alice", 7)
	for i := 11; i <= 17; i++ {
		if err := bob.Create(ctx, s.item(i, "bob")); err != nil {
			t.Fatal(err)
		}
	}
	first, err := alice.List(ctx, database.ListRequest{PageSize: 3})
	if err != nil || first.NextPageToken == "" {
		t.Fatalf("first page: %v, token %q", err, first.NextPageToken)
	}
	token := first.NextPageToken
	// The page size may change between pages.
	second, err := alice.List(ctx, database.ListRequest{PageSize: 2, PageToken: token})
	if err != nil || !slices.Equal(itemIDs(second.Items), []string{"i004", "i003"}) {
		t.Fatalf("second page with another size = %v, %v", itemIDs(second.Items), err)
	}
	// Everything else is bound to the token.
	invalid := func(what string, repo *database.Repo[Item], req database.ListRequest) {
		t.Helper()
		_, err := repo.List(ctx, req)
		wantCode(t, err, codes.InvalidArgument, what)
		if !database.IsInvalidPageToken(err) {
			t.Fatalf("%s: IsInvalidPageToken is false for %v", what, err)
		}
	}
	invalid("another owner", bob, database.ListRequest{PageSize: 3, PageToken: token})
	invalid("another filter", alice, database.ListRequest{PageSize: 3, PageToken: token,
		Filter: database.C{{Key: "labels", Value: "mod0", C: database.Contains}}})
	invalid("another order", alice, database.ListRequest{PageSize: 3, PageToken: token, OrderBy: []string{"rank"}})
	invalid("garbage", alice, database.ListRequest{PageToken: "not-a-token"})
	invalid("truncated", alice, database.ListRequest{PageToken: token[:len(token)-4]})
	flipped := []byte(token)
	if flipped[len(flipped)/2] == 'A' {
		flipped[len(flipped)/2] = 'B'
	} else {
		flipped[len(flipped)/2] = 'A'
	}
	invalid("altered", alice, database.ListRequest{PageToken: string(flipped)})
	// Tokens are opaque: the cursor values are not readable in them.
	if len(token) < 20 {
		t.Fatalf("token %q is suspiciously short", token)
	}
}

func (s *suite) testTransactions(t *testing.T) {
	if s.opts.NoTransactions {
		t.Skip("the engine under test has no transactions")
	}
	ctx := t.Context()
	boom := fmt.Errorf("boom")
	err := database.Transact(ctx, s.db, func(ctx context.Context, tx database.Database) error {
		items := s.items.WithDB(tx).Scope("alice")
		if err := items.Create(ctx, s.item(1, "alice")); err != nil {
			return err
		}
		// Reads inside the transaction see its own writes.
		if _, err := items.Get(ctx, "i001"); err != nil {
			return fmt.Errorf("read own write: %w", err)
		}
		if err := items.Create(ctx, s.item(2, "alice")); err != nil {
			return err
		}
		return boom
	})
	if err == nil || err.Error() != boom.Error() {
		t.Fatalf("Transact error = %v, want boom", err)
	}
	if n, err := s.items.Unscoped().Count(ctx, nil); err != nil || n != 0 {
		t.Fatalf("after rollback: %d records, %v (want 0)", n, err)
	}
	err = database.Transact(ctx, s.db, func(ctx context.Context, tx database.Database) error {
		items := s.items.WithDB(tx).Scope("alice")
		if err := items.Create(ctx, s.item(1, "alice")); err != nil {
			return err
		}
		return items.Patch(ctx, "i001", "v1", database.D{{Key: "title", Value: "in tx"}, {Key: "etag", Value: "v2"}})
	})
	if err != nil {
		t.Fatalf("Transact commit: %v", err)
	}
	got, err := s.items.Scope("alice").Get(ctx, "i001")
	if err != nil || got.Title != "in tx" {
		t.Fatalf("after commit: %+v, %v", got, err)
	}
	// A panic rolls back as well.
	func() {
		defer func() { _ = recover() }()
		_ = database.Transact(ctx, s.db, func(ctx context.Context, tx database.Database) error {
			_ = s.items.WithDB(tx).Scope("alice").Create(ctx, s.item(3, "alice"))
			panic("panic inside the transaction")
		})
	}()
	if ok, err := s.items.Scope("alice").Exists(ctx, "i003"); err != nil || ok {
		t.Fatalf("after panic: exists = %v, %v (want false)", ok, err)
	}
}
