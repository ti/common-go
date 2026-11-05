# Databases

`database.Database` is one interface for MySQL, PostgreSQL, MongoDB and an in-memory mock, so application code doesn't change when the store does. The URI scheme picks the driver.

## Drivers

Blank-import the drivers you use; each registers its schemes:

| Scheme | Import | Notes |
|---|---|---|
| `mysql://` | `_ "github.com/ti/common-go/dependencies/sql"` | go-sql-driver/mysql |
| `postgres://` | `_ "github.com/ti/common-go/dependencies/sql"` | lib/pq |
| `mongodb://` | `_ "github.com/ti/common-go/dependencies/mongo"` | mongo-go-driver v2 |
| `mock://` | `_ "github.com/ti/common-go/dependencies/database/mock"` | in-memory, for tests and local development |

Importing `github.com/ti/common-go/dependencies/database/query` (for `PageQuery`/`StreamQuery`) also registers all of them.

```go
db, err := database.New(ctx, "postgres://app:secret@localhost:5432/app?sslmode=disable")
```

or as a dependency field `DB database.Database` with `dependencies.WithNewFns(database.New)` ([dependencies.md](dependencies.md)).

## Connection strings

```yaml
dependencies:
  # In-memory
  db: mock://local/app

  # MongoDB (the path is the default database and is required)
  db: mongodb://localhost:27017/app
  db: mongodb://user:pass@localhost:27017/app?authSource=admin
  db: mongodb://m1:27017,m2:27017,m3:27017/app?replicaSet=rs0&authSource=admin&tls=true&maxPoolSize=100

  # MySQL: write host:port; the adapter turns it into the driver's tcp(host:port) form.
  # (The driver-style "user:pass@tcp(host:3306)" does not parse as a URL.)
  db: mysql://user:pass@localhost:3306/app?charset=utf8mb4&parseTime=true&loc=UTC
  db: mysql://user:pass@db:3306/app?charset=utf8mb4&parseTime=true&timeout=5s&readTimeout=30s&writeTimeout=30s

  # PostgreSQL
  db: postgres://user:pass@localhost:5432/app?sslmode=disable
  db: postgres://user:pass@db:5432/app?sslmode=require&connect_timeout=10&application_name=app
```

Driver options are passed through to the driver (see their documentation). Options read by the SQL adapter itself: `loc` (time zone for time values, default local), `compact` (MariaDB-compatible queries), `bustedIndex` (no JSON indexes, MySQL 5.7 and older), `updateTime`.

## Interface

```go
type Database interface {
	GetDatabase(ctx, project string) (Database, error)       // a per-tenant view
	Insert(ctx, table string, docs any) (count int, err error) // one document or a slice
	InsertOne(ctx, table string, data any) error
	Update / UpdateOne(ctx, table string, cond C, doc any) (int, error) // doc: database.D or a struct pointer
	Replace(ctx, table string, indexKeys []string, docs any) (int, error)
	ReplaceOne(ctx, table string, cond C, data any) (int, error)
	Delete / DeleteOne(ctx, table string, cond C) (int, error)
	Find(ctx, table string, cond C, sortBy []string, limit int, slicePtr any) error
	FindOne(ctx, table string, cond C, data any) error
	FindRows(ctx, table string, cond C, sortBy []string, limit int, oneData any) (Row, error)
	Exist(ctx, table string, cond C) (bool, error)
	Count(ctx, table string, cond C) (int64, error)
	IncrCounter / DecrCounter / GetCounter(...)
	StartTransaction(ctx) (Transaction, error)
	WithTransaction(ctx, tx Transaction) Database
	Close(ctx) error
}
```

Documents are Go structs; field names come from `json` tags. Conditions are `database.C`, a list of `database.CE{Key, Value, C}` where `C` is `database.Eq` (default), `Ne`, `Lt`, `Lte`, `Gt`, `Gte`, `In` or `Nin`. `sortBy` entries are field names, with a `-` prefix for descending. Partial updates use `database.D{{Key: "name", Value: "Ada"}}`.

```go
var u User
err := db.FindOne(ctx, "users", database.C{{Key: "user_id", Value: id}}, &u)
if status.Code(err) == codes.NotFound { ... }

var adults []User
err = db.Find(ctx, "users", database.C{{Key: "age", Value: 18, C: database.Gte}}, []string{"-created_at"}, 100, &adults)

_, err = db.UpdateOne(ctx, "users", database.C{{Key: "user_id", Value: id}}, database.D{{Key: "name", Value: "Ada"}})
```

Errors are gRPC status errors (for example `NotFound` when `FindOne` matches nothing), so handlers can return them directly or map them.

## Paging

```go
import "github.com/ti/common-go/dependencies/database/query"

// Page numbers with a total (admin tables, small result sets).
page, err := query.PageQuery[User](ctx, db, "users", &database.PageQueryRequest{
	Filters: database.C{{Key: "status", Value: "active"}},
	Sort:    []string{"-created_at"},
	Page:    1,
	Limit:   20,
})
// page.Data []*User, page.Total

// Cursor ("page token") paging: stable and fast on large tables and for infinite scroll.
res, err := query.StreamQuery[User](ctx, db, "users", &database.StreamQueryRequest{
	Limit:     50,
	PageToken: req.GetPageToken(),
})
// res.Data, res.PageToken (empty at the end)
```

For exports and batch jobs, iterate with `FindRows` to keep memory flat.

## Package documentation

- [dependencies/database](../../dependencies/database/README.md): interface details and transactions
- [dependencies/sql](../../dependencies/sql/README.md): MySQL/PostgreSQL adapter, schema helpers
- [dependencies/database/mock](../../dependencies/database/mock/README.md): in-memory implementation for tests
