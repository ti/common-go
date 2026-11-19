# sql

MySQL and PostgreSQL implementation of [`database.Database`](../database/README.md). Importing the package registers the `mysql` and `postgres` schemes.

```go
import (
	"github.com/ti/common-go/dependencies/database"
	_ "github.com/ti/common-go/dependencies/sql"
)

db, err := database.New(ctx, "mysql://app:secret@db:3306/app?charset=utf8mb4&parseTime=true")
db, err := database.New(ctx, "postgres://app:secret@db:5432/app?sslmode=disable")
```

`sql.New(ctx, uri)` does the same without the registry. `Init` opens a `database/sql` pool and pings it with the given context.

## Connection strings

- MySQL: write `host:port`; the adapter converts it to the driver's `tcp(host:port)` form. The driver-style `user:pass@tcp(host:3306)/db` is not a valid URL and fails to parse. All other query parameters go to [go-sql-driver/mysql](https://github.com/go-sql-driver/mysql#dsn-data-source-name) (`charset`, `parseTime`, `timeout`, `readTimeout`, ...).
- PostgreSQL: a standard [lib/pq](https://pkg.go.dev/github.com/lib/pq) URL (`sslmode`, `connect_timeout`, `application_name`, ...).
- Options the adapter reads itself:
  - `loc=UTC`: time zone used for time values (default: the process's local zone; also passed to the MySQL driver);
  - `compact`: MariaDB-compatible query forms;
  - `bustedIndex`: avoid JSON indexes (MySQL 5.7 and older);
  - `updateTime=<field>`: field updated automatically on writes.

The database name in the path is required for queries; `EnsureDatabase` creates it.

## Mapping

Rows map to structs by `json` tags. Nested structs, slices and maps are stored as JSON columns; `google.protobuf.Timestamp` and wrapper types are supported. Use pointer fields to distinguish NULL from zero values.

```go
type User struct {
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	Tags      []string  `json:"tags,omitempty"`  // JSON column
	CreatedAt time.Time `json:"created_at"`
	Age       *int32    `json:"age,omitempty"`   // NULL when nil
}
```

## Schema helpers

| Function | Purpose |
|---|---|
| `sql.GenerateScheme(table, &User{})` | `CREATE TABLE` statement derived from the struct (with an auto-increment `_id` and a `project` column) |
| `sql.TransformScheme(&User{})` | The column definitions as a `database.D` |
| `(*SQL).EnsureDatabase(ctx)` | Create the database if needed |
| `(*SQL).EnsureIndex(ctx, table, fields, unique, reverse)` | Create an index if needed (array and JSON-key indexes on PostgreSQL) |
| `(*SQL).AutoExpirieData(ctx, table, expiresAtField)` | Delete rows after the time in `expiresAtField` |

These helpers are best for tests and prototypes. For production schemas use migrations.

## Migrations

```go
//go:embed migrations/*.sql
var migrations embed.FS

err := db.(*sql.SQL).Migrate(ctx, migrations, migrate.WithDir("migrations"))
```

[`migrate`](migrate/migrate.go) applies `NNNN_name.sql` files (or `NNNN_name.postgres.sql` / `NNNN_name.mysql.sql`) in order, once, forward only. Applied files are recorded with a checksum in `schema_migrations` and may not be edited afterwards. The run holds a session lock (`pg_advisory_lock` / `GET_LOCK`), so every replica can call it at startup. Files are split at semicolons; an object whose body contains semicolons (MySQL `BEGIN ... END`, PostgreSQL `BEGIN ATOMIC`) goes alone into a file that starts with `-- migrate:no-split`. `migrate.Up(ctx, *sql.DB, dialect, fsys)` works with any `database/sql` pool.

## Column types

| Go field | PostgreSQL | MySQL |
|---|---|---|
| `string`, numbers, `bool` | `text`/`varchar`, `bigint`, `boolean` | `varchar`, `bigint`, `tinyint(1)` |
| `time.Time`, `*timestamppb.Timestamp` | `timestamptz(6)` | `datetime(6)` |
| `[]string`, `[]int64` (scalars) | `text[]`, `bigint[]` | `json` |
| structs, maps, slices of structs | `jsonb` / `json` | `json` |

Times are written with microseconds in the `loc` zone (PostgreSQL also gets the UTC offset); set `loc=UTC`. `database.Contains` is `? = ANY(col)` on PostgreSQL and `? MEMBER OF(col)` on MySQL 8.0.17+.

## Queries

Everything else is the shared interface ([docs/guides/database.md](../../docs/guides/database.md)): `Insert`/`InsertOne` (a slice becomes one multi-row INSERT), `Find`/`FindOne`/`FindRows`, `Update`/`UpdateOne` with `database.D`, `Replace`, `Delete`, `Count`, `Exist`, counters, transactions, and `query.PageQuery` / `query.StreamQuery` for paging.

Errors are gRPC statuses: no rows is `NotFound`, a duplicate key `AlreadyExists`, a deadlock, lock timeout or serialization failure `Aborted`, a canceled context `Canceled`, an expired one `DeadlineExceeded`.

Table names, condition and update keys, and sort and select fields are checked against a strict grammar (letters, digits, underscores, plus the JSON key forms) and are `InvalidArgument` otherwise; values are always bound parameters.

A handle from `WithTransaction` runs its reads and writes in the transaction. `Update`, `UpdateOne` and `Delete` refuse an empty condition (`InvalidArgument`). The adapter also implements `FindAfter` (keyset), `EnsureIndexes` and `HealthCheck`; see the guide.
