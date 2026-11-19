package database_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/database/mock"
)

type doc struct {
	ID      string    `json:"id"`
	Tenant  string    `json:"tenant_id"`
	Name    string    `json:"name"`
	Rev     int64     `json:"rev"`
	Created time.Time `json:"created"`
	Skipped string    `json:"-"`
}

func newDB(t *testing.T) database.Database {
	t.Helper()
	db, err := mock.New(t.Context(), "mock://local/test")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestNewCollectionValidatesSchema(t *testing.T) {
	db := newDB(t)
	for name, schema := range map[string]database.Schema{
		"no table":            {},
		"unknown key":         {Table: "docs", Key: "uuid"},
		"unknown scope":       {Table: "docs", Scope: "owner"},
		"unknown version":     {Table: "docs", Version: "etag"},
		"unknown order field": {Table: "docs", OrderBy: []string{"-nope"}},
		"ignored field":       {Table: "docs", OrderBy: []string{"-"}},
	} {
		if _, err := database.NewCollection[doc](db, schema); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := database.NewCollection[string](db, database.Schema{Table: "docs"}); err == nil {
		t.Error("non-struct type: want an error")
	}
	if _, err := database.NewCollection[doc](nil, database.Schema{Table: "docs"}); err == nil {
		t.Error("nil database: want an error")
	}
	c, err := database.NewCollection[doc](db, database.Schema{Table: "docs", Scope: "tenant_id", OrderBy: []string{"-created"}})
	if err != nil {
		t.Fatal(err)
	}
	s := c.Schema()
	// The key is appended as tiebreaker in the direction of the last field.
	if s.Key != "id" || strings.Join(s.OrderBy, ",") != "-created,-id" ||
		s.DefaultPageSize != database.DefaultPageSize || s.MaxPageSize != database.MaxPageSize {
		t.Fatalf("defaults = %+v", s)
	}
}

func TestScopeFailsClosed(t *testing.T) {
	db := newDB(t)
	unscopedSchema := database.MustCollection[doc](db, database.Schema{Table: "docs"})
	// Scope on an entity without a scope field is a wiring mistake, not "all".
	_, err := unscopedSchema.Scope("t1").Get(t.Context(), "a")
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Scope without Schema.Scope: %v", err)
	}
	scoped := database.MustCollection[doc](db, database.Schema{Table: "docs", Scope: "tenant_id"})
	for _, empty := range []any{"", nil} {
		if err := scoped.Scope(empty).Create(t.Context(), &doc{ID: "a"}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("Create with scope %#v: %v", empty, err)
		}
	}
	if n, _ := scoped.Unscoped().Count(t.Context(), nil); n != 0 {
		t.Fatalf("a failed Create stored %d records", n)
	}
	// Cond is how a hand-written query stays scoped.
	cond := scoped.Scope("t1").Cond(database.C{{Key: "name", Value: "x"}})
	if len(cond) != 2 || cond[0].Key != "tenant_id" || cond[0].Value != "t1" {
		t.Fatalf("Cond = %v", cond)
	}
}

func TestVersionWithoutVersionField(t *testing.T) {
	db := newDB(t)
	docs := database.MustCollection[doc](db, database.Schema{Table: "docs"}).Unscoped()
	if err := docs.Create(t.Context(), &doc{ID: "a", Name: "n"}); err != nil {
		t.Fatal(err)
	}
	err := docs.Delete(t.Context(), "a", int64(1))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected version without Schema.Version: %v", err)
	}
	if err := docs.Delete(t.Context(), "", nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty id: %v", err)
	}
}

// Numeric versions work like string etags.
func TestNumericVersion(t *testing.T) {
	db := newDB(t)
	docs := database.MustCollection[doc](db, database.Schema{Table: "docs", Version: "rev"}).Unscoped()
	ctx := t.Context()
	if err := docs.Create(ctx, &doc{ID: "a", Name: "one", Rev: 1}); err != nil {
		t.Fatal(err)
	}
	if err := docs.Replace(ctx, &doc{ID: "a", Name: "two", Rev: 2}, int64(1)); err != nil {
		t.Fatal(err)
	}
	if err := docs.Replace(ctx, &doc{ID: "a", Name: "three", Rev: 2}, int64(1)); !database.IsConflict(err) {
		t.Fatalf("stale revision: %v", err)
	}
	if v1, v2 := database.NewVersion(), database.NewVersion(); v1 == v2 || len(v1) < 20 {
		t.Fatalf("NewVersion: %q %q", v1, v2)
	}
}

func TestPageTokenKey(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()
	schema := database.Schema{Table: "docs", Scope: "tenant_id", TokenKey: []byte("replica-shared-secret")}
	a := database.MustCollection[doc](db, schema)
	for _, id := range []string{"a", "b", "c"} {
		if err := a.Scope("t1").Create(ctx, &doc{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := a.Scope("t1").List(ctx, database.ListRequest{PageSize: 1})
	if err != nil || page.NextPageToken == "" {
		t.Fatalf("page: %v", err)
	}
	// Another replica with the same key accepts the token.
	b := database.MustCollection[doc](db, schema)
	next, err := b.Scope("t1").List(ctx, database.ListRequest{PageSize: 1, PageToken: page.NextPageToken})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != "b" {
		t.Fatalf("same key: %+v, %v", next, err)
	}
	// Another key (or none) does not: tokens cannot be forged without it.
	for _, key := range [][]byte{[]byte("other"), nil} {
		schema.TokenKey = key
		c := database.MustCollection[doc](db, schema)
		_, err := c.Scope("t1").List(ctx, database.ListRequest{PageSize: 1, PageToken: page.NextPageToken})
		if !database.IsInvalidPageToken(err) {
			t.Fatalf("key %q: %v", key, err)
		}
	}
	// Two tokens for the same position differ (random nonce): nothing to learn by comparing.
	again, _ := a.Scope("t1").List(ctx, database.ListRequest{PageSize: 1})
	if again.NextPageToken == page.NextPageToken {
		t.Fatal("tokens for the same position are identical")
	}
	if strings.Contains(page.NextPageToken, "a") && len(page.NextPageToken) < 10 {
		t.Fatal("token looks transparent")
	}
}

// A backend that implements only the Database interface.
type bare struct{ database.Database }

func TestOptionalCapabilities(t *testing.T) {
	db := bare{newDB(t)}
	ctx := t.Context()
	var rows []doc
	if err := database.FindAfter(ctx, db, "docs", nil, []string{"id"}, nil, 1, &rows); status.Code(err) != codes.Unimplemented {
		t.Fatalf("FindAfter: %v", err)
	}
	if err := database.EnsureIndexes(ctx, db, "docs", database.Index{Fields: []string{"id"}}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("EnsureIndexes: %v", err)
	}
	if err := database.HealthCheck(db)(ctx); err != nil {
		t.Fatalf("HealthCheck without the capability: %v", err)
	}
	if database.SystemName(db) != "unknown" {
		t.Fatalf("SystemName = %q", database.SystemName(db))
	}
	// A *database.DB dependency field is unwrapped.
	wrapped := &database.DB{Database: newDB(t)}
	if database.SystemName(wrapped) != "mock" {
		t.Fatalf("SystemName(*DB) = %q", database.SystemName(wrapped))
	}
	if err := database.FindAfter(ctx, wrapped, "docs", nil, []string{"id"}, nil, 1, &rows); err != nil {
		t.Fatalf("FindAfter(*DB): %v", err)
	}
	idx := database.Index{Fields: []string{"owner", "-created"}, Unique: true}
	if got := idx.IndexName("docs"); got != "idx_docs_owner_created_uniq" {
		t.Fatalf("IndexName = %q", got)
	}
}

func TestHealthCheckReportsFailure(t *testing.T) {
	db := newDB(t)
	check := database.HealthCheck(db)
	if err := check(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = db.Close(t.Context())
	if err := check(t.Context()); err == nil {
		t.Fatal("a closed database is reported healthy")
	}
}

func TestErrorHelpers(t *testing.T) {
	if !database.IsConflict(database.NewConflictError("x")) || database.IsConflict(database.NewNotFoundError("x")) ||
		database.IsConflict(nil) {
		t.Fatal("IsConflict")
	}
	if !database.IsNotFound(database.NewNotFoundError("x")) || !database.IsAlreadyExists(database.NewAlreadyExistsError("x")) {
		t.Fatal("constructors")
	}
	if status.Code(database.ContextError(context.Canceled)) != codes.Canceled ||
		status.Code(database.ContextError(context.DeadlineExceeded)) != codes.DeadlineExceeded ||
		database.ContextError(errors.New("other")) != nil {
		t.Fatal("ContextError")
	}
}

// Every Repo operation is counted, by engine, operation, table and outcome.
// The histogram is process-global, so the test measures what it adds (it must
// pass with -count=2).
func TestOperationMetric(t *testing.T) {
	db := newDB(t)
	docs := database.MustCollection[doc](db, database.Schema{Table: "metric_docs"}).Unscoped()
	ctx := t.Context()
	before := metricSamples(t, "metric_docs")
	_ = docs.Create(ctx, &doc{ID: "a"})
	_, _ = docs.Get(ctx, "a")
	_, _ = docs.Get(ctx, "missing")
	after := metricSamples(t, "metric_docs")
	for _, key := range []string{"create/", "get/", "get/NotFound"} {
		if after[key]-before[key] != 1 {
			t.Fatalf("%s: %d new samples, want 1 (before %v, after %v)", key, after[key]-before[key], before, after)
		}
	}
}

func metricSamples(t *testing.T, table string) map[string]uint64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]uint64{}
	for _, f := range families {
		if f.GetName() != "db_client_operation_duration_seconds" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["db_collection_name"] == table && labels["db_system_name"] == "mock" {
				got[labels["db_operation_name"]+"/"+labels["error_type"]] = m.GetHistogram().GetSampleCount()
			}
		}
	}
	return got
}

// The condition of a repository that fails every operation must never be
// usable as "all records": Cond is meant to be handed to the raw interface.
func TestFailedScopeCondMatchesNothing(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()
	scoped := database.MustCollection[doc](db, database.Schema{Table: "docs", Scope: "tenant_id"})
	for _, tenant := range []string{"t1", "t2"} {
		if err := scoped.Scope(tenant).Create(ctx, &doc{ID: "doc-" + tenant}); err != nil {
			t.Fatal(err)
		}
	}
	plain := database.MustCollection[doc](db, database.Schema{Table: "docs"})
	for name, repo := range map[string]*database.Repo[doc]{
		"empty scope":               scoped.Scope(""),
		"nil scope":                 scoped.Scope(nil),
		"scope on an unscoped type": plain.Scope("t1"),
	} {
		for _, extra := range []database.C{nil, {{Key: "name", Value: ""}}} {
			cond := repo.Cond(extra)
			if len(cond) == 0 {
				t.Fatalf("%s: Cond(%v) is empty, which means every record", name, extra)
			}
			if n, err := db.Count(ctx, "docs", cond); err != nil || n != 0 {
				t.Fatalf("%s: Cond(%v) matches %d records, %v", name, extra, n, err)
			}
			if n, err := db.Delete(ctx, "docs", cond); err != nil || n != 0 {
				t.Fatalf("%s: Delete with Cond(%v) removed %d records, %v", name, extra, n, err)
			}
		}
	}
	if n, _ := scoped.Unscoped().Count(ctx, nil); n != 2 {
		t.Fatalf("%d records left, want 2", n)
	}
}

// Field names in patches and filters often come from a request (update
// masks, filter fields). Only exact declared fields are accepted.
func TestFieldNamesAreChecked(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()
	docs := database.MustCollection[doc](db, database.Schema{Table: "docs", Scope: "tenant_id", Version: "rev"})
	alice := docs.Scope("alice")
	if err := alice.Create(ctx, &doc{ID: "1", Name: "n", Rev: 1}); err != nil {
		t.Fatal(err)
	}
	invalid := func(what string, err error) {
		t.Helper()
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("%s: got %v, want InvalidArgument", what, err)
		}
	}
	// The mock (like MySQL) matches names case-insensitively: "Tenant_ID"
	// must not get past the protection of "tenant_id".
	for _, key := range []string{"Tenant_ID", "TENANT_ID", "tenantId", "ID", "Id"} {
		invalid("Patch "+key, alice.Patch(ctx, "1", nil, database.D{{Key: key, Value: "bob"}}))
	}
	for _, key := range []string{"Name", "nam", "name`", "name` = 'x', `rev", "$set", "name.sub", ""} {
		invalid("Patch "+key, alice.Patch(ctx, "1", nil, database.D{{Key: key, Value: "x"}}))
		_, err := alice.List(ctx, database.ListRequest{Filter: database.C{{Key: key, Value: "x"}}})
		invalid("List filter "+key, err)
		_, err = alice.ListAfter(ctx, database.C{{Key: key, Value: "x"}}, nil, nil, 10)
		invalid("ListAfter filter "+key, err)
		_, err = alice.Count(ctx, database.C{{Key: key, Value: "x"}})
		invalid("Count filter "+key, err)
		_, err = alice.List(ctx, database.ListRequest{OrderBy: []string{key}})
		invalid("List order "+key, err)
	}
	got, err := alice.Get(ctx, "1")
	if err != nil || got.Tenant != "alice" || got.Name != "n" {
		t.Fatalf("the record changed or moved: %+v, %v", got, err)
	}
	if n, _ := docs.Scope("bob").Count(ctx, nil); n != 0 {
		t.Fatalf("bob owns %d records", n)
	}
	// Declared fields still work.
	if err := alice.Patch(ctx, "1", nil, database.D{{Key: "name", Value: "m"}}); err != nil {
		t.Fatal(err)
	}
}

// A zero time is a legal order value: the token must carry it exactly
// (UnixNano overflows for year 1, which made the second page empty).
func TestPagingOverZeroAndExtremeTimes(t *testing.T) {
	db := newDB(t)
	ctx := t.Context()
	docs := database.MustCollection[doc](db, database.Schema{Table: "docs", OrderBy: []string{"created"}}).Unscoped()
	times := map[string]time.Time{
		"a": {}, "b": {}, // zero
		"c": time.Date(1500, 1, 1, 0, 0, 0, 0, time.UTC),
		"d": time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC),
		"e": time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	for id, ts := range times {
		if err := docs.Create(ctx, &doc{ID: id, Created: ts}); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	token := ""
	for range 10 {
		page, err := docs.List(ctx, database.ListRequest{PageSize: 1, PageToken: token})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			got = append(got, it.ID)
		}
		if token = page.NextPageToken; token == "" {
			break
		}
	}
	if strings.Join(got, "") != "abcde" {
		t.Fatalf("paged %v, want a b c d e", got)
	}
}

type withPointers struct {
	ID      string     `json:"id"`
	Created *time.Time `json:"created"`
	Tags    []string   `json:"tags"`
	Name    string     `json:"name"`
}

// Order fields must be orderable at every page, so a pointer or composite
// field is refused up front, not when a next page first exists.
func TestOrderFieldTypes(t *testing.T) {
	db := newDB(t)
	for _, order := range []string{"created", "-created", "tags"} {
		if _, err := database.NewCollection[withPointers](db, database.Schema{Table: "p", OrderBy: []string{order}}); err == nil {
			t.Errorf("Schema.OrderBy %q: want an error", order)
		}
	}
	type pointerKey struct {
		ID *string `json:"id"`
	}
	if _, err := database.NewCollection[pointerKey](db, database.Schema{Table: "p"}); err == nil {
		t.Error("pointer key as tiebreaker: want an error")
	}
	repo := database.MustCollection[withPointers](db, database.Schema{Table: "p"}).Unscoped()
	now := time.Now()
	for _, id := range []string{"a", "b"} {
		if err := repo.Create(t.Context(), &withPointers{ID: id, Created: &now}); err != nil {
			t.Fatal(err)
		}
	}
	// One record per page, so a next page exists: the request fails at once.
	_, err := repo.List(t.Context(), database.ListRequest{OrderBy: []string{"created"}, PageSize: 1})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("List ordered by a pointer field: %v", err)
	}
	if _, err := repo.List(t.Context(), database.ListRequest{OrderBy: []string{"name"}, PageSize: 1}); err != nil {
		t.Fatalf("List ordered by a string field: %v", err)
	}
}

// racingDB lets another writer in around the update statement.
type racingDB struct {
	database.Database
	before, after func()
}

func (r *racingDB) UpdateOne(ctx context.Context, table string, cond database.C, doc any) (int, error) {
	if r.before != nil {
		r.before()
	}
	n, err := r.Database.UpdateOne(ctx, table, cond, doc)
	if r.after != nil {
		r.after()
	}
	return n, err
}

// A conditional write that matched nothing is a conflict even when another
// writer has put the expected version back by the time the outcome is
// classified. It used to be reported as success although nothing was written.
func TestCompareAndSwapNeverReportsAnUnwrittenWrite(t *testing.T) {
	inner := newDB(t)
	ctx := t.Context()
	setRev := func(rev int64) {
		if _, err := inner.UpdateOne(ctx, "docs", database.C{{Key: "id", Value: "a"}}, database.D{{Key: "rev", Value: rev}}); err != nil {
			t.Fatal(err)
		}
	}
	db := &racingDB{Database: inner}
	docs := database.MustCollection[doc](db, database.Schema{Table: "docs", Version: "rev"}).Unscoped()
	if err := docs.Create(ctx, &doc{ID: "a", Name: "original", Rev: 1}); err != nil {
		t.Fatal(err)
	}
	db.before = func() { setRev(7) } // writer A gets in first: our expected version 1 is stale
	db.after = func() { setRev(1) }  // writer B then sets the version back to 1
	err := docs.Replace(ctx, &doc{ID: "a", Name: "mine", Rev: 2}, int64(1))
	if !database.IsConflict(err) {
		t.Fatalf("Replace = %v, want a conflict", err)
	}
	db.before, db.after = nil, nil
	got, err := docs.Get(ctx, "a")
	if err != nil || got.Name != "original" {
		t.Fatalf("stored = %+v, %v", got, err)
	}
	// An unconditional write of the values the record already has succeeds.
	if err := docs.Replace(ctx, &doc{ID: "a", Name: "original", Rev: 1}, nil); err != nil {
		t.Fatalf("unconditional Replace with unchanged values: %v", err)
	}
}

// notFoundWhenUnchanged behaves like MongoDB, and like SQL with updateTime=
// on MySQL: an update that changes nothing is reported as NotFound.
type notFoundWhenUnchanged struct{ database.Database }

func (d notFoundWhenUnchanged) UpdateOne(ctx context.Context, table string, cond database.C, set any) (int, error) {
	var before, after doc
	if err := d.FindOne(ctx, table, cond, &before); err != nil {
		return 0, err
	}
	if _, err := d.Database.UpdateOne(ctx, table, cond, set); err != nil {
		return 0, err
	}
	_ = d.FindOne(ctx, table, cond, &after)
	if before == after {
		return 0, database.NewNotFoundError("condition %s not found", cond)
	}
	return 1, nil
}

// An unconditional write of the values a record already has is a success on
// such a backend too; only a missing record is NotFound.
func TestIdempotentUnconditionalWrite(t *testing.T) {
	db := notFoundWhenUnchanged{newDB(t)}
	ctx := t.Context()
	docs := database.MustCollection[doc](db, database.Schema{Table: "docs", Version: "rev"}).Unscoped()
	if err := docs.Create(ctx, &doc{ID: "a", Name: "n", Rev: 1}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := docs.Patch(ctx, "a", nil, database.D{{Key: "name", Value: "n"}}); err != nil {
			t.Fatalf("idempotent Patch: %v", err)
		}
		if err := docs.Replace(ctx, &doc{ID: "a", Name: "n", Rev: 1}, nil); err != nil {
			t.Fatalf("idempotent Replace: %v", err)
		}
	}
	if err := docs.Patch(ctx, "missing", nil, database.D{{Key: "name", Value: "n"}}); !database.IsNotFound(err) {
		t.Fatalf("Patch of a missing record: %v", err)
	}
	if err := docs.Patch(ctx, "a", int64(9), database.D{{Key: "name", Value: "x"}, {Key: "rev", Value: int64(2)}}); !database.IsConflict(err) {
		t.Fatalf("conditional Patch with a stale version: %v", err)
	}
}
