package notestore_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ti/common-go/dependencies/database"
	_ "github.com/ti/common-go/dependencies/database/mock"
	_ "github.com/ti/common-go/dependencies/mongo"
	_ "github.com/ti/common-go/dependencies/sql"

	"github.com/ti/common-go/examples/internal/notestore"
)

// One behaviour suite for the store, run against every engine. The mock
// always runs; the real engines run when their URI is set (the same
// variables as the library's conformance tests):
//
//	TEST_POSTGRES_URI  TEST_MYSQL_URI  TEST_MONGODB_URI
func TestStore(t *testing.T) {
	engines := map[string]string{
		"mock":     "mock://local/" + strings.ReplaceAll(t.Name(), "/", "_"),
		"postgres": os.Getenv("TEST_POSTGRES_URI"),
		"mysql":    os.Getenv("TEST_MYSQL_URI"),
		"mongodb":  os.Getenv("TEST_MONGODB_URI"),
	}
	for name, uri := range engines {
		t.Run(name, func(t *testing.T) {
			if uri == "" {
				t.Skip("no URI configured for this engine")
			}
			db, err := database.New(t.Context(), uri)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close(context.Background()) })
			// Two replicas starting at once prepare the schema concurrently.
			var (
				wg     sync.WaitGroup
				stores [2]*notestore.DBStore
				errs   [2]error
			)
			for i := range stores {
				wg.Go(func() { stores[i], errs[i] = notestore.NewDBStore(t.Context(), db) })
			}
			wg.Wait()
			if err := errors.Join(errs[:]...); err != nil {
				t.Fatalf("NewDBStore: %v", err)
			}
			// Start from an empty table.
			if _, err := db.Delete(t.Context(), "notes", database.C{{Key: "id", Value: "", C: database.Ne}}); err != nil {
				t.Fatal(err)
			}
			testStore(t, stores[0])
		})
	}
}

func testStore(t *testing.T, store *notestore.DBStore) {
	ctx := t.Context()
	base := time.Date(2026, 5, 6, 7, 8, 9, 123456000, time.UTC).Truncate(store.TimePrecision())
	note := func(i int, owner string, labels ...string) notestore.Note {
		ts := base.Add(time.Duration(i/2) * time.Second) // pairs share a timestamp
		return notestore.Note{
			ID: fmt.Sprintf("n%02d", i), Owner: owner, Title: fmt.Sprintf("note %d", i), Body: "hello",
			Labels: labels, Etag: "e1", CreateTime: ts, UpdateTime: ts,
		}
	}
	for i := 1; i <= 7; i++ {
		labels := []string{"all"}
		if i%2 == 0 {
			labels = append(labels, "even")
		}
		if err := store.Insert(ctx, note(i, "alice", labels...)); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}
	if err := store.Insert(ctx, note(50, "bob", "all")); err != nil {
		t.Fatal(err)
	}

	// Get returns exactly what was inserted, times included.
	want := note(2, "alice", "all", "even")
	got, err := store.Get(ctx, "alice", "n02")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != want.ID || got.Owner != want.Owner || got.Title != want.Title || got.Body != want.Body ||
		!slices.Equal(got.Labels, want.Labels) || got.Etag != want.Etag ||
		!got.CreateTime.Equal(want.CreateTime) || !got.UpdateTime.Equal(want.UpdateTime) {
		t.Fatalf("Get = %+v\nwant  %+v", got, want)
	}

	// A missing note and another owner's note are the same answer.
	for _, c := range []struct{ owner, id string }{{"alice", "nope"}, {"bob", "n02"}, {"alice", "n50"}} {
		if _, err := store.Get(ctx, c.owner, c.id); !errors.Is(err, notestore.ErrNotFound) {
			t.Fatalf("Get(%s, %s) = %v, want ErrNotFound", c.owner, c.id, err)
		}
	}

	// List: newest first, id breaks ties, cursor continues without gaps.
	ids := func(notes []notestore.Note) []string {
		out := make([]string, len(notes))
		for i, n := range notes {
			out[i] = n.ID
		}
		return out
	}
	page1, err := store.List(ctx, "alice", "", notestore.Cursor{}, 3)
	if err != nil || !slices.Equal(ids(page1), []string{"n07", "n06", "n05"}) {
		t.Fatalf("page 1 = %v, %v", ids(page1), err)
	}
	last := page1[len(page1)-1]
	page2, err := store.List(ctx, "alice", "", notestore.Cursor{CreateTime: last.CreateTime, ID: last.ID}, 3)
	if err != nil || !slices.Equal(ids(page2), []string{"n04", "n03", "n02"}) {
		t.Fatalf("page 2 = %v, %v", ids(page2), err)
	}
	last = page2[len(page2)-1]
	page3, err := store.List(ctx, "alice", "", notestore.Cursor{CreateTime: last.CreateTime, ID: last.ID}, 3)
	if err != nil || !slices.Equal(ids(page3), []string{"n01"}) {
		t.Fatalf("page 3 = %v, %v", ids(page3), err)
	}
	even, err := store.List(ctx, "alice", "even", notestore.Cursor{}, 10)
	if err != nil || !slices.Equal(ids(even), []string{"n06", "n04", "n02"}) {
		t.Fatalf("label filter = %v, %v", ids(even), err)
	}
	if bobs, err := store.List(ctx, "bob", "", notestore.Cursor{}, 10); err != nil || !slices.Equal(ids(bobs), []string{"n50"}) {
		t.Fatalf("bob's list = %v, %v", ids(bobs), err)
	}

	// Replace is compare-and-swap on the etag.
	next := note(2, "alice") // labels cleared
	next.Title, next.Body, next.Etag = "second", "", "e2"
	next.UpdateTime = base.Add(time.Hour)
	if err := store.Replace(ctx, next, "e1"); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got, err = store.Get(ctx, "alice", "n02")
	if err != nil || got.Title != "second" || got.Body != "" || len(got.Labels) != 0 || got.Etag != "e2" ||
		!got.UpdateTime.Equal(next.UpdateTime) || !got.CreateTime.Equal(want.CreateTime) {
		t.Fatalf("after Replace = %+v, %v", got, err)
	}
	lost := note(2, "alice")
	lost.Etag = "e3"
	if err := store.Replace(ctx, lost, "e1"); !errors.Is(err, notestore.ErrEtagMismatch) {
		t.Fatalf("Replace with a stale etag = %v, want ErrEtagMismatch", err)
	}
	foreign := note(2, "bob")
	foreign.Etag = "e3"
	if err := store.Replace(ctx, foreign, "e2"); !errors.Is(err, notestore.ErrNotFound) {
		t.Fatalf("Replace of another owner's note = %v, want ErrNotFound", err)
	}
	ghost := note(40, "alice")
	ghost.Etag = "e2"
	if err := store.Replace(ctx, ghost, "e1"); !errors.Is(err, notestore.ErrNotFound) {
		t.Fatalf("Replace of a missing note = %v, want ErrNotFound", err)
	}

	// Delete likewise.
	if err := store.Delete(ctx, "alice", "n02", "e1"); !errors.Is(err, notestore.ErrEtagMismatch) {
		t.Fatalf("Delete with a stale etag = %v, want ErrEtagMismatch", err)
	}
	if err := store.Delete(ctx, "bob", "n02", "e2"); !errors.Is(err, notestore.ErrNotFound) {
		t.Fatalf("Delete of another owner's note = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, "alice", "n02", "e2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.Delete(ctx, "alice", "n02", "e2"); !errors.Is(err, notestore.ErrNotFound) {
		t.Fatalf("Delete again = %v, want ErrNotFound", err)
	}

	// The service's read-modify-write loop does not lose updates: writers
	// that retry on ErrEtagMismatch all get their change in.
	const writers = 6
	var wg sync.WaitGroup
	failures := make(chan error, writers)
	for w := range writers {
		wg.Go(func() {
			for {
				cur, err := store.Get(ctx, "alice", "n01")
				if err != nil {
					failures <- err
					return
				}
				upd := cur
				upd.Body += fmt.Sprintf("[%d]", w)
				upd.Etag = database.NewVersion()
				err = store.Replace(ctx, upd, cur.Etag)
				if errors.Is(err, notestore.ErrEtagMismatch) {
					continue
				}
				if err != nil {
					failures <- err
				}
				return
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatalf("concurrent writer: %v", err)
	}
	got, err = store.Get(ctx, "alice", "n01")
	if err != nil {
		t.Fatal(err)
	}
	for w := range writers {
		if !strings.Contains(got.Body, fmt.Sprintf("[%d]", w)) {
			t.Fatalf("update of writer %d was lost: body = %q", w, got.Body)
		}
	}
}
