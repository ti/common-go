# database

`database.Database` is the storage interface shared by the MySQL/PostgreSQL ([`dependencies/sql`](../sql/README.md)), MongoDB (`dependencies/mongo`) and in-memory ([`dependencies/database/mock`](mock/README.md)) implementations. Implementations register a URI scheme with `database.RegisterImplements`; `database.New(ctx, uri)` (or a `*database.DB` dependency field) picks one by scheme.

Usage, connection strings and paging are covered in [docs/guides/database.md](../../docs/guides/database.md). This page lists the model.

## Model

| Type | Meaning |
|---|---|
| `C` / `CE{Key, Value, C}` | Conditions, combined with AND. `C` is a `Condition`: `Eq` (zero value), `Ne`, `Lt`, `Lte`, `Gt`, `Gte`, `In`, `Nin` |
| `D` / `E{Key, Value}` | An ordered document, for partial updates |
| `Row` | Iterator from `FindRows`: `Next()`, `Decode() (any, error)`, `Close()` |
| `Transaction` | `Commit()`, `Rollback()` |
| `PageQueryRequest` / `PageQueryResponse[T]` (`PageResult[T]`) | Page-number queries: `Filters`, `Select`, `Sort`, `Page`, `Limit`, `NoCount` → `Data`, `Total` |
| `StreamQueryRequest` / `StreamResponse[T]` (`StreamResult[T]`) | Cursor queries: `PageToken`, `PageField`, `Filters`, `Select`, `Limit`, `Ascending`, `NoCount` → `Data`, `PageToken`, `Total` |
| `BulkError` | Per-element errors of bulk writes |

Struct fields map to columns/fields by their `json` tag. Errors are gRPC status errors (`codes.NotFound` when nothing matches, `codes.AlreadyExists` on unique-key conflicts where the driver reports them).

Page and cursor queries live in `github.com/ti/common-go/dependencies/database/query` (`query.PageQuery[T]`, `query.StreamQuery[T]`), which works with every implementation. An implementation can also provide them directly by implementing `PageQuerier` / `StreamQuerier`.

## Multi-tenancy

`GetDatabase(ctx, project)` returns a view scoped to a project: SQL and MongoDB add a `project` field to written documents and to every query's conditions.

## Transactions

```go
tx, err := db.StartTransaction(ctx)
if err != nil {
	return err
}
txDB := db.WithTransaction(ctx, tx)
if _, err := txDB.Insert(ctx, "orders", order); err != nil {
	_ = tx.Rollback()
	return err
}
if _, err := txDB.UpdateOne(ctx, "stock", database.C{{Key: "sku", Value: order.SKU}},
	database.D{{Key: "reserved", Value: order.Qty}}); err != nil {
	_ = tx.Rollback()
	return err
}
return tx.Commit()
```

MongoDB transactions need a replica set. The mock simulates transactions.

## Streaming large results

```go
var u User
rows, err := db.FindRows(ctx, "users", nil, []string{"created_at"}, 0, &u)
if err != nil {
	return err
}
defer rows.Close()
for rows.Next() {
	v, err := rows.Decode() // *User
	if err != nil {
		return err
	}
	process(v.(*User))
}
```
