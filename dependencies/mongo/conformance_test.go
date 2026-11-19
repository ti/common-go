package mongo_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/database/dbtest"
	_ "github.com/ti/common-go/dependencies/mongo"
)

// The conformance suite against a real MongoDB. Set the URI to run it; the
// transaction tests need a replica set (TEST_MONGODB_STANDALONE=1 skips them):
//
//	TEST_MONGODB_URI='mongodb://host:27017/dbtest?directConnection=true'
func TestConformanceMongo(t *testing.T) {
	uri := os.Getenv("TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("TEST_MONGODB_URI is not set")
	}
	db, err := database.New(t.Context(), uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(t.Context()) })
	dbtest.Run(t, db, dbtest.Options{
		TimePrecision:  time.Millisecond, // BSON dates are milliseconds
		NoTransactions: os.Getenv("TEST_MONGODB_STANDALONE") != "",
	})
}

// Two transactions writing the same document: MongoDB aborts the second one
// with a write conflict, which must surface as database.IsConflict so that
// callers can retry.
func TestTransactionWriteConflict(t *testing.T) {
	uri := os.Getenv("TEST_MONGODB_URI")
	if uri == "" || os.Getenv("TEST_MONGODB_STANDALONE") != "" {
		t.Skip("needs TEST_MONGODB_URI on a replica set")
	}
	ctx := t.Context()
	db, err := database.New(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(t.Context()) })
	const table = "dbtest_conflict"
	type row struct {
		ID string `json:"id"`
		N  int64  `json:"n"`
	}
	_, _ = db.Delete(ctx, table, nil)
	if err := db.InsertOne(ctx, table, &row{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	cond := database.C{{Key: "id", Value: "a"}}
	tx1, err := db.StartTransaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx2, err := db.StartTransaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.WithTransaction(ctx, tx1).UpdateOne(ctx, table, cond, database.D{{Key: "n", Value: int64(1)}}); err != nil {
		t.Fatalf("first writer: %v", err)
	}
	_, err = db.WithTransaction(ctx, tx2).UpdateOne(ctx, table, cond, database.D{{Key: "n", Value: int64(2)}})
	if !database.IsConflict(err) {
		t.Fatalf("second writer: got %v, want a conflict (Aborted)", err)
	}
	_ = tx2.Rollback()
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit of the first writer: %v", err)
	}
	var got row
	if err := db.FindOne(ctx, table, cond, &got); err != nil || got.N != 1 {
		t.Fatalf("after commit: %+v, %v", got, err)
	}
	_, _ = db.Delete(ctx, table, nil)
}

// A transaction is usually rolled back because its request was cancelled.
// The abort must still reach the server: it used the cancelled request
// context and failed, leaving the transaction open until the server timed
// it out.
func TestRollbackAfterCancelledRequest(t *testing.T) {
	uri := os.Getenv("TEST_MONGODB_URI")
	if uri == "" || os.Getenv("TEST_MONGODB_STANDALONE") != "" {
		t.Skip("needs TEST_MONGODB_URI on a replica set")
	}
	db, err := database.New(t.Context(), uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	const table = "dbtest_cancel"
	type row struct {
		ID string `json:"id"`
	}
	_, _ = db.Delete(t.Context(), table, nil)
	if err := db.InsertOne(t.Context(), table, &row{ID: "seed"}); err != nil { // creates the collection
		t.Fatal(err)
	}
	reqCtx, cancel := context.WithCancel(t.Context())
	tx, err := db.StartTransaction(reqCtx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.WithTransaction(reqCtx, tx).InsertOne(reqCtx, table, &row{ID: "in-tx"}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback after the request was cancelled: %v", err)
	}
	// The document lock of the aborted transaction is gone: a write to the
	// same id succeeds at once instead of conflicting.
	if n, err := db.Count(t.Context(), table, database.C{{Key: "id", Value: "in-tx"}}); err != nil || n != 0 {
		t.Fatalf("after rollback: %d records, %v", n, err)
	}
	_, _ = db.Delete(t.Context(), table, nil)
}
