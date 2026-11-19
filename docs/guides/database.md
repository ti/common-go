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

Optional capabilities are separate interfaces, implemented by all four backends and reached through package functions: `database.FindAfter` (keyset query), `database.EnsureIndexes`, `database.HealthCheck`, `database.Transact`. The typed layer for entities is `database.Collection[T]` / `database.Repo[T]`, see [Repositories](#repositories).

Documents are Go structs; field names come from `json` tags. Conditions are `database.C`, a list of `database.CE{Key, Value, C}` where `C` is `database.Eq` (default), `Ne`, `Lt`, `Lte`, `Gt`, `Gte`, `In` or `Nin`. `sortBy` entries are field names, with a `-` prefix for descending. Partial updates use `database.D{{Key: "name", Value: "Ada"}}`.

```go
var u User
err := db.FindOne(ctx, "users", database.C{{Key: "user_id", Value: id}}, &u)
if status.Code(err) == codes.NotFound { ... }

var adults []User
err = db.Find(ctx, "users", database.C{{Key: "age", Value: 18, C: database.Gte}}, []string{"-created_at"}, 100, &adults)

_, err = db.UpdateOne(ctx, "users", database.C{{Key: "user_id", Value: id}}, database.D{{Key: "name", Value: "Ada"}})
```

Errors are gRPC status errors (for example `NotFound` when `FindOne` matches nothing), so handlers can return them directly or map them; see [Errors](#errors).

## Conditions and portability

| Condition | Meaning | Notes |
|---|---|---|
| `Eq` (default), `Ne` | equal / not equal | times compare as instants |
| `Lt`, `Lte`, `Gt`, `Gte` | range | all backends; MongoDB used to treat these as equality, see [upgrades](../upgrades/2026-Q4.md) |
| `In`, `Nin` | value in / not in a slice | |
| `Contains` | the array field holds the value | `text[]`/`bigint[]` on PostgreSQL, a JSON column on MySQL 8.0.17+, an array in MongoDB |

Conditions are combined with AND. What is the same on every backend, and what is not:

| Topic | Portable | Engine-specific |
|---|---|---|
| Result slices | `Find` fills `*[]T` and `*[]*T` | |
| Errors | gRPC codes, see [Errors](#errors) | message texts |
| `time.Time` | round-trips at **microsecond** precision on SQL | MongoDB stores **milliseconds** (BSON date): truncate before writing if you compare what you wrote with what you read. On SQL, times are written in the `loc` zone; use `timestamptz` on PostgreSQL, `datetime(6)` on MySQL, and set `loc=UTC` |
| Slices of scalars (`[]string`) | read and written as a Go slice | PostgreSQL column type is an array (`text[]`), MySQL `json` |
| Structs, maps, slices of structs | stored as JSON (`jsonb`/`json`) or a sub-document | querying inside them (`a.b`, `a[*].b`) is MySQL/MongoDB only |
| String comparison | case-sensitive, bytewise order on PostgreSQL (with `COLLATE "C"` for ordered ids), MongoDB and the mock | MySQL's default collation is case-insensitive: declare id, owner and version columns `COLLATE utf8mb4_bin`, or `Alice` reads `alice`'s records |
| Empty values | `InsertOne` omits empty fields (column default / absent field); they read back as zero values | |
| `UpdateOne` when nothing matches | | SQL and the mock return `0, nil`; MongoDB returns `NotFound`. `Repo` hides the difference |
| `ReplaceOne` | | upserts on MongoDB and the mock; use `Repo.Replace` for a replace that never inserts |
| `Update`/`Delete` with an empty condition | | SQL refuses it (`InvalidArgument`); MongoDB and the mock affect every record |
| OR, regex/LIKE, `$inc`, upsert | not in the interface | use the engine handle (`*sql.SQL`, `*mongo.Mongo`) behind your store interface |
| Field names from a request | `Repo` accepts only declared fields | On the raw interface SQL checks every identifier against a strict grammar (`InvalidArgument` otherwise); MongoDB passes keys through, including operators such as `$or` and `$where`, so never hand it a request-supplied key unchecked |

## Errors

Every backend reports failures as gRPC status errors with the same codes, so a handler can return them unchanged and a repository can branch on them:

| Code | Meaning | Helper |
|---|---|---|
| `NotFound` | no record matches | `database.IsNotFound` |
| `AlreadyExists` | a unique key is taken (needs the unique index) | `database.IsAlreadyExists` |
| `Aborted` | the write lost a race: version mismatch, serialization failure, deadlock. Read again and retry. ([AIP-154](https://google.aip.dev/154) prescribes `ABORTED` for an etag mismatch.) | `database.IsConflict` |
| `InvalidArgument` | bad page token, negative page size, empty scope | `database.IsInvalidPageToken` |
| `Canceled`, `DeadlineExceeded` | the context ended | |
| `Unimplemented` | the backend lacks an optional capability | |

## Repositories

`database.Database` is deliberately small and untyped. For an entity with an id, an owner and concurrent writers, use the typed layer on top of it. It is not an ORM: it issues the same five statements you would write by hand, and your own queries stay available.

The recommended shape, with the scaffold template's `note.v1.NoteService` as the worked example ([`examples/internal/notestore`](../../examples/internal/notestore)):

1. **The interface lives in the domain package**, next to the entity and the domain errors, and knows nothing about databases. The service depends on it.
2. **One implementation on `database.Database`** serves PostgreSQL, MySQL, MongoDB and the mock; the URI of the dependency picks the engine.
3. **Tests run the same behaviour suite** against `mock://` always and against real engines when a URI is configured.

```go
// note.go: the domain. No database import.
type Store interface {
	Insert(ctx context.Context, n Note) error
	Get(ctx context.Context, owner, id string) (Note, error)
	List(ctx context.Context, owner, label string, after Cursor, limit int) ([]Note, error)
	Replace(ctx context.Context, n Note, etag string) error // ErrEtagMismatch when the etag is stale
	Delete(ctx context.Context, owner, id, etag string) error
}
```

```go
// store_db.go: the implementation.
type noteRow struct { // json tags are the column / field names
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	Labels     []string  `json:"labels"`
	Etag       string    `json:"etag"`
	CreateTime time.Time `json:"create_time"`
	UpdateTime time.Time `json:"update_time"`
}

notes, err := database.NewCollection[noteRow](db, database.Schema{
	Table:   "notes",
	Key:     "id",                              // default
	Scope:   "owner",                           // owner / tenant field
	Version: "etag",                            // optimistic-concurrency field
	OrderBy: []string{"-create_time", "-id"},   // list order; the key is appended as tiebreaker if missing
})

func (s *DBStore) Get(ctx context.Context, owner, id string) (Note, error) {
	row, err := s.notes.Scope(owner).Get(ctx, id)
	if err != nil {
		return Note{}, translate(err) // IsNotFound -> ErrNotFound, IsConflict -> ErrEtagMismatch
	}
	return fromRow(row), nil
}

func (s *DBStore) Replace(ctx context.Context, n Note, etag string) error {
	return translate(s.notes.Scope(n.Owner).Replace(ctx, toRow(n), etag))
}
```

### Scoping that cannot be forgotten

A `Collection` has no data methods. Every call site chooses `Scope(owner)` or, visibly, `Unscoped()`:

- `Scope(v)` adds `owner = v` to every read and write. Another owner's record is `NotFound` for `Get`, `Replace`, `Patch` and `Delete` alike, so ids cannot be probed. `Create` stamps the owner on the record and rejects a record that names another one. The scope and the key of a record never change.
- An empty value (`Scope("")`) fails every operation with `InvalidArgument`; it never widens to all records.
- `Unscoped()` is for jobs and administration and is easy to find in review.
- For a query the `Repo` has no method for, `repo.Cond(extra)` returns the scope condition plus yours, to pass to `db.Find`/`db.Count`. For a repository that fails (an empty scope) the condition matches nothing; it is never empty.
- Field names are checked: `Patch`, `List`, `ListAfter` and `Count` accept only exact declared fields of the entity (the `json` tags) in patches, filters and orders; anything else is `InvalidArgument`. You can therefore map a request's update mask or filter onto them, though a service should still decide which fields a caller may filter or change. `Cond(extra)` is not checked: it is for conditions written in code.

`GetDatabase(ctx, project)` is the older, coarser mechanism (a `project` column added by the SQL and MongoDB backends). It still works; the mock ignores it. Prefer `Schema.Scope` for owner and tenant fields.

### Optimistic concurrency

`Replace(ctx, doc, expected)`, `Patch(ctx, id, expected, set)` and `Delete(ctx, id, expected)` compare-and-swap on `Schema.Version`:

- The comparison and the write are one atomic statement (`UPDATE ... WHERE id = ? AND owner = ? AND etag = ?`, the equivalent filter on MongoDB), so two writers cannot both win.
- A stale `expected` is a conflict: `database.IsConflict(err)`, gRPC `ABORTED`. A missing or foreign record is `NotFound`. A conditional write is reported as done only when the engine counted it.
- The write must carry a new version (`database.NewVersion()` returns a random one; an incrementing integer works too). Writing the same version is `InvalidArgument`.
- A nil or zero `expected` writes unconditionally.
- `Replace` writes every field, so empty values overwrite stored ones; `Patch` writes only the fields in `set`.

The usual read-modify-write loop reads, applies the change and retries on conflict:

```go
for range attempts {
	cur, err := repo.Get(ctx, id)
	if err != nil {
		return err
	}
	next := *cur
	next.Title, next.Etag = title, database.NewVersion()
	if err = repo.Replace(ctx, &next, cur.Etag); !database.IsConflict(err) {
		return err
	}
}
```

### Paging

`Repo.List` is keyset ("seek") paging with page tokens, following [AIP-158](https://google.aip.dev/158), identical on every backend:

```go
page, err := notes.Scope(owner).List(ctx, database.ListRequest{
	Filter:    database.C{{Key: "labels", Value: "work", C: database.Contains}},
	PageSize:  int(req.GetPageSize()),   // 0 = default (20), above the maximum (100) is lowered, negative is InvalidArgument
	PageToken: req.GetPageToken(),       // "" = first page
})
// page.Items []*noteRow, page.NextPageToken ("" on the last page)
```

- The order is `Schema.OrderBy` (or `ListRequest.OrderBy`), always ending in the key, so it is total. Order fields must be strings, numbers, bools or `time.Time` (not pointers: a keyset has no place for NULL); other types are refused by `NewCollection` and by `List`.
- The position is the last record of the page, not an offset: records created or deleted while a client pages never shift or repeat later pages. Create the matching index, e.g. `(owner, create_time DESC, id DESC)`.
- Tokens are opaque (AES-GCM sealed) and bound to the query: a token that was altered, or is used with another scope, filter or order, is `InvalidArgument` (`database.IsInvalidPageToken`). The page size may change between pages.
- Set `Schema.TokenKey` (the same secret on every replica) to make tokens unforgeable. Without it they are sealed with a constant of the library: still opaque and still bound to the query, but forgeable by somebody who reads the source. That is acceptable when a position is not sensitive, because scope and filter are applied by the server on every page.
- Tokens do not expire and are not valid across a change of `TokenKey`.

A service that owns its token format (the scaffold's note service does) uses the keyset query directly: `repo.ListAfter(ctx, filter, orderBy, []any{lastCreateTime, lastID}, limit)`, or `database.FindAfter` on the raw interface.

The older helpers remain for existing code: `query.PageQuery` (page numbers with a total; fine for small admin tables) and `query.StreamQuery` (cursor on the engine's `_id`; on SQL it needs an auto-increment `_id` column, and its token is neither sealed nor bound to the query).

```go
page, err := query.PageQuery[User](ctx, db, "users", &database.PageQueryRequest{
	Filters: database.C{{Key: "status", Value: "active"}},
	Sort:    []string{"-created_at"},
	Page:    1,
	Limit:   20,
})
```

For exports and batch jobs, iterate with `FindRows` to keep memory flat.

## Schema and indexes

**SQL: forward-only migrations**, embedded in the binary and applied at startup ([`dependencies/sql/migrate`](../../dependencies/sql/migrate/migrate.go)):

```go
//go:embed migrations/*.sql
var migrations embed.FS

err := db.(*sql.SQL).Migrate(ctx, migrations, migrate.WithDir("migrations"))
```

- Files are `NNNN_name.sql`, or `NNNN_name.postgres.sql` / `NNNN_name.mysql.sql` when the dialects differ. They are applied in version order, once, and recorded with a SHA-256 in `schema_migrations` (`migrate.WithTable` for a component of its own). An applied file that was edited or removed fails the run; fix mistakes with a new migration. There are no down migrations.
- Safe with several replicas starting at once: the run holds a session lock (`pg_advisory_lock` / `GET_LOCK`) on a dedicated connection, so replicas migrate one after the other.
- On PostgreSQL a migration and its bookkeeping row are one transaction. MySQL commits implicitly on DDL, so keep MySQL migrations to one statement each or make them idempotent (`IF NOT EXISTS`).
- A statement whose body contains bare semicolons cannot be split: a MySQL procedure, function, trigger or event with `BEGIN ... END`, and a PostgreSQL function with `BEGIN ATOMIC ... END`. Put such an object alone in a file whose first line is `-- migrate:no-split`; the file is then sent as one statement. Without the directive the run fails when the migrations are loaded, naming the file. `DELIMITER` is a command of the `mysql` client, not SQL, and is always refused. PostgreSQL bodies in dollar quotes (`$$ ... $$`) need no directive.
- Statements are split at semicolons and sent one by one; no multi-statement driver mode is needed. The splitter follows each dialect's rules for quotes, comments (`--`, `/* */`, `#` on MySQL), backslash escapes (MySQL strings and PostgreSQL `E'..'`) and PostgreSQL `$tag$` bodies.

**MongoDB and the mock: ensure indexes at startup.**

```go
err := database.EnsureIndexes(ctx, db, "notes",
	database.Index{Fields: []string{"id"}, Unique: true},
	database.Index{Fields: []string{"owner", "-create_time", "-id"}},
)
```

It is idempotent and safe from several replicas (`createIndexes` is a no-op for an identical index). An index with the same name or keys but other options is an error: that is drift, not a race. The mock keeps no indexes but enforces the unique ones, so duplicate inserts are `AlreadyExists` in tests as in production. `EnsureIndexes` also works on SQL (the table must exist; `CREATE INDEX IF NOT EXISTS` on PostgreSQL, a tolerated duplicate on MySQL), but migrations are the better home for SQL indexes. `NewDBStore` in the example shows one startup path for all engines: migrate on SQL, ensure indexes elsewhere.

## Transactions

```go
err := database.Transact(ctx, db, func(ctx context.Context, tx database.Database) error {
	notes := s.notes.WithDB(tx).Scope(owner) // the same collection on the transaction handle
	if err := notes.Create(ctx, row); err != nil {
		return err // rolls back
	}
	_, err := tx.UpdateOne(ctx, "counters", cond, set)
	return err // nil commits
})
```

`Transact` commits when the function returns nil and rolls back on an error or a panic. Reads through `tx` see the transaction's own writes. MySQL and PostgreSQL always support it; MongoDB needs a replica set or mongos. The mock restores a snapshot on rollback (all-or-nothing for one transaction; it does not isolate concurrent ones). A transaction the engine aborts because of a conflict with another one is `database.IsConflict`.

Most single-record writes do not need a transaction: compare-and-swap on a version is cheaper and works on a standalone MongoDB too.

## Readiness, tracing and metrics

```go
gs.AddHealthChecker(database.HealthCheck(cfg.Dependencies.DB)) // /readyz and gRPC health follow the database
```

The check pings the engine with a 3 s timeout.

Every `Repo` operation runs in an OpenTelemetry client span named `<operation> <table>` (`get notes`, `list notes`, ...) with `db.system.name`, `db.operation.name` and `db.collection.name`, and is observed in the Prometheus histogram `db_client_operation_duration_seconds{db_system_name, db_operation_name, db_collection_name, error_type}` (the stable `db.client.operation.duration` convention). `NotFound`, `AlreadyExists`, `Aborted` and `InvalidArgument` are recorded in `error_type` but do not mark the span as failed. Calls on the raw `Database` interface are not instrumented.

## Testing

- Unit-test services against the mock: `database.New(ctx, "mock://local/"+t.Name())`. It implements unique indexes (after `EnsureIndexes`), range and `Contains` conditions, keyset queries and rollback, and returns the same error codes as the real engines.
- [`dependencies/database/dbtest`](../../dependencies/database/dbtest/dbtest.go) is the conformance suite every backend of this module passes: CRUD and error codes, conditions, scoping, compare-and-swap under concurrent writers, keyset paging and tokens, transactions. Run it against real engines with

  ```bash
  TEST_POSTGRES_URI='postgres://user:pass@host:5432/db?sslmode=disable' \
  TEST_MYSQL_URI='mysql://user:pass@host:3306/db?parseTime=true&loc=UTC' \
  TEST_MONGODB_URI='mongodb://host:27017/db?directConnection=true' \
  go test ./dependencies/...
  ```

  Without the variables those tests are skipped and only the mock runs.
- Give your store one behaviour suite and run it the same way; `examples/internal/notestore/store_test.go` is the template.

## Package documentation

- [dependencies/database](../../dependencies/database/README.md): interface details and transactions
- [dependencies/sql](../../dependencies/sql/README.md): MySQL/PostgreSQL adapter, schema helpers
- [dependencies/sql/migrate](../../dependencies/sql/migrate/migrate.go): forward-only migration runner
- [dependencies/database/dbtest](../../dependencies/database/dbtest/dbtest.go): conformance suite
- [examples/internal/notestore](../../examples/internal/notestore): the worked repository example
- [dependencies/database/mock](../../dependencies/database/mock/README.md): in-memory implementation for tests
