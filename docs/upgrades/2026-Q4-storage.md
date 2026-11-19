# Upgrading the storage layer (2026-Q4)

Part of the [2026-Q4 upgrade](2026-Q4.md). It covers `dependencies/database`, `dependencies/sql`, `dependencies/mongo` and the mock. Nothing here changes a signature: existing code compiles unchanged. Sections 1 and 2 list every behaviour change; section 3 is what to adopt.

How it was verified: the conformance suite `dependencies/database/dbtest` passes on the mock, PostgreSQL 17, MySQL 8.4 and MongoDB 8 (single-node replica set); `auth/backend`, `agentx/backend/core` and `avatarx/backend` build and pass their tests against this checkout.

## 1. Behaviour changes that can affect you

| # | Change | What to do |
|---|---|---|
| S1 | **MongoDB: `Lt`, `Lte`, `Gt`, `Gte` are range conditions.** They were silently treated as equality. | Queries that used them start returning the intended rows (a keyset cursor with `C: database.Lt` returned only rows *equal* to the cursor). Workarounds that put raw operators in the value (`Value: bson.M{"$gt": x}` with the default `Eq`) keep working and can be simplified. Lint rules that ban `database.Gt` can go. |
| S2 | **SQL: times are written with microseconds**, and on PostgreSQL with the UTC offset of the `loc` zone. They were truncated to whole seconds. Times in conditions and in `database.D` updates are formatted the same way (they were passed to the driver, which could use another zone than `loc`). | Columns with fractional precision (`datetime(6)`, `timestamp(6)`, `timestamptz`) now keep the fraction. A column without it (`datetime`, `timestamp(0)`) **rounds** where it used to receive a truncated value; truncate in the application if that matters. `timestamptz` columns now get the right instant when `loc` differs from the server's zone. |
| S3 | **SQL: a transaction handle reads inside the transaction.** `Find`, `FindOne`, `Count`, `Exist` on `WithTransaction(...)` used the pool, so they did not see the transaction's own writes and were not isolated. A transaction handle now keeps the `loc` of its parent; a read of a time column on it used to panic. `updateTime=` still applies to the root handle only, as before: project views and transaction handles do not turn `ReplaceOne` into update-then-insert. | Nothing, unless code relied on reads escaping the transaction. |
| S4 | **MongoDB: transactions are real.** `StartTransaction` now starts one and operations on the `WithTransaction` handle run in it. Before, writes were applied immediately and `Commit`/`Rollback` failed with "no transaction started". | Needs a replica set or mongos; on a standalone server the first operation in the transaction fails. |
| S5 | **Error codes.** MySQL duplicate key (1062) is `AlreadyExists` (was `Unknown`). Deadlock, lock-wait timeout, serialization failure and MongoDB write conflicts are `Aborted` (were `Unknown`/`Internal`). A context deadline is `DeadlineExceeded` on SQL and MongoDB (was `Unknown`/`Internal`). The SQL `NotFound` message is "record not found" (was empty). MongoDB context errors keep their text ("<table> error for ..."). `StartTransaction` failures on SQL and MongoDB are gRPC status errors (they were raw driver errors, so `errors.Is` against a driver error no longer matches). | Replace checks on MySQL error text or `codes.Unknown` with `database.IsAlreadyExists` / `database.IsConflict`. |
| S6 | **SQL: empty conditions.** Reads with no condition and no project now run without a `WHERE` (they were a syntax error). `Update`, `UpdateOne` and `Delete` with no condition and no project return `InvalidArgument` (also a syntax error before, from the engine). | To delete everything on SQL, pass a condition that is always true or run the statement yourself. MongoDB and the mock still accept an empty condition. |
| S7 | **Mock: comparisons behave like a database.** `Eq`, `Ne`, `In` treat times as instants, numbers by value (`int(1)` equals `int64(1)`) and named string, bool and number types like their underlying type (`Status("open")` equals `"open"`); `Lt`/`Gt` and sorting work for `time.Time`, bools and mixed numeric types. They used to compare Go values with `reflect.DeepEqual` and ordered only `int`, `int64`, `float64` and `string`. | Tests that passed because a condition silently did not match may now see the rows. |
| S8 | **Mock: `Rollback` restores the state at `StartTransaction`.** It used to keep the writes. The restore is of the whole mock: writes made outside the transaction after it started are discarded too. `StartTransaction` now takes the mock's write lock while it copies the data, so it waits for running operations. | Nothing for tests that commit. Do not rely on isolation between concurrent transactions in the mock. |
| S9 | **A new Prometheus collector**, `db_client_operation_duration_seconds`, is registered on the default registerer the first time a `database.Repo` is used. | Nothing unless a metric of that name is already registered with other labels. |
| S10 | **SQL: identifiers are checked.** Table names, condition keys, update keys, sort and select fields must match a strict grammar: letters, digits and underscores, plus the documented JSON forms for keys (`a.b`, `a[*].b`, `a[2].b`, `-a[*].b` in updates) and `schema.table` for tables. Anything else is `InvalidArgument` before a statement is built. They used to be pasted between backticks unescaped, so a name taken from a request (a filter field, a sort parameter, an update mask) could inject SQL. Applies to the CRUD methods, `FindAfter`, counters, `EnsureIndexes`, `PageQuery` and `StreamQuery`. | Rename columns or tables that use other characters (hyphens, spaces), or query them with your own statement through `(*sql.SQL).QueryContext`. Keep validating request-supplied field names against an allow-list in the service; on MongoDB the raw interface still passes keys such as `$or` through. |
| S11 | **SQL: writes with nothing to do are `InvalidArgument`.** `Update`, `UpdateOne` and `ReplaceOne` whose document yields no column to set (for example a struct with only empty fields passed to `UpdateOne`) used to send `UPDATE t SET WHERE ...` and fail with the engine's syntax error. An empty `In` is now false and an empty `Nin` true (`IN ()` was a syntax error); `In`/`Nin` with a value that is not a slice is `InvalidArgument` (it panicked). This holds for the CRUD methods, `FindAfter`, `PageQuery` and `StreamQuery` alike: they build conditions in one place. Page and stream queries therefore also format time values in conditions as in S2 and accept `Contains`. The exported helper `sql.ParseWhere` has no error result; for a condition list that is not valid it returns a clause that matches nothing. | Only the error code and text change. |
| S12 | **`sql.TransformSQLArgs` returns times as text with microseconds** (`2006-01-02 15:04:05.999999`, plus the UTC offset for PostgreSQL), like the rest of S2. It returned whole seconds. | Only if you call this helper yourself. |

## 2. Fixes without a migration step

- SQL: the project of a `GetDatabase` view and the key of `GetCounter` are bound parameters. They were pasted into the statement: a project containing a quote broke every query and was an injection vector.
- SQL: the value of a `-key[*].sub` update (remove from a JSON array) is a bound parameter; it was pasted into the statement.
- SQL: `Exist` no longer needs an `_id` column. `Find` accepts `*[]T` as well as `*[]*T`, and a wrong result type is `InvalidArgument` instead of a panic. Bulk `Replace` uses its `indexKeys` (the condition was always empty). MySQL times with fractional seconds read without `parseTime` are parsed (they were silently zero).
- MongoDB: `Find` accepts `*[]T`. A `GetDatabase` view of a transaction handle stays in the transaction. `Commit` and `Rollback` use a context detached from the request (10 s timeout): with the request context, rolling back after a cancelled request never reached the server.
- Mock: `Find` accepts `*[]*T` (it failed with "dest must be a struct pointer"); a stored nil reads back as the zero value (it panicked).
- `query.PageQuery` / `query.StreamQuery` accept a `*database.DB` dependency field (they failed at runtime with "unimplemented for *database.DB").

## 3. Recommended adoption

Guide: [docs/guides/database.md](../guides/database.md). Worked example: [`examples/internal/notestore`](../../examples/internal/notestore).

| # | Capability | Replaces |
|---|---|---|
| A1 | `database.NewCollection[T]` + `Scope(owner)` / `Unscoped()`: a typed repository per entity. | The owner condition repeated at every call site; per-entity FindOne/UpdateOne boilerplate. |
| A2 | `Repo.Replace` / `Patch` / `Delete` with an expected version: compare-and-swap in one statement, `database.IsConflict` on a stale version. | Read-check-write sequences; `UpdateOne` count checks that differ between SQL and MongoDB; `ReplaceOne`, which upserts. |
| A3 | `Repo.List`: keyset paging with opaque page tokens bound to the query (AIP-158); `Repo.ListAfter` / `database.FindAfter` for the bare keyset query. | Offset tokens, cursors on a non-unique timestamp, `limit`-only lists. Converge on `page_size` / `page_token` / `next_page_token`. |
| A4 | `database.Contains` for array membership. | Engine-specific JSON conditions for the simple case. |
| A5 | `(*sql.SQL).Migrate(ctx, fsys)` (`dependencies/sql/migrate`): embedded forward-only migrations, checksummed, locked across replicas. | Hand-rolled runners and manual DDL. |
| A6 | `database.EnsureIndexes(ctx, db, table, ...)` on MongoDB, the mock and SQL. | Type assertions to `*mongo.Mongo` for `mongo.EnsureIndex`; also gives the mock unique constraints. |
| A7 | `database.Transact(ctx, db, fn)`. | Manual `StartTransaction` / `Rollback` / `Commit` bookkeeping. |
| A8 | `gs.AddHealthChecker(database.HealthCheck(db))`. | Ad-hoc `Count` probes. |
| A9 | `database.IsConflict`, `IsNotFound`, `IsAlreadyExists`. | Matching error strings per backend. |
| A10 | `dbtest.Run` and `TEST_POSTGRES_URI` / `TEST_MYSQL_URI` / `TEST_MONGODB_URI`. | Mock-only store tests. |

## 4. Verify

```bash
go build ./... && go vet ./... && go test ./...
# with real engines (optional, any subset):
TEST_POSTGRES_URI='postgres://user:pass@host:5432/db?sslmode=disable' \
TEST_MYSQL_URI='mysql://user:pass@host:3306/db?parseTime=true&loc=UTC' \
TEST_MONGODB_URI='mongodb://host:27017/db?directConnection=true' \
go test ./dependencies/...
```

For `auth`: nothing to change. Its `compareCondition` helpers (S1) and the AST test that bans `db.Gt` can be retired when convenient.
