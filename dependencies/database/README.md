# Database Module

Database abstraction layer providing a unified CRUD interface, supporting MySQL, PostgreSQL, and MongoDB.

## Core Interface

### Database Interface

```go
type Database interface {
    // Basic CRUD
    Insert(ctx context.Context, table string, data any) error
    Update(ctx context.Context, table string, conds C, updates D) error
    Delete(ctx context.Context, table string, conds C) error
    FindOne(ctx context.Context, table string, conds C, result any) error
    Find(ctx context.Context, table string, conds C, sortBy []string, limit int, results any) error
    
    // Count and Aggregation
    Count(ctx context.Context, table string, conds C) (int64, error)
    Aggregate(ctx context.Context, table string, pipeline any, result any) error
    
    // Transaction Support
    StartTransaction(ctx context.Context) (Transaction, error)
    WithTransaction(ctx context.Context, tx Transaction) Database
    
    // Stream Query
    FindRows(ctx context.Context, table string, conds C, sortBy []string, limit int, data any) (Row, error)
    
    // Batch Operations
    BatchInsert(ctx context.Context, table string, documents []any) error
    BatchUpdate(ctx context.Context, table string, conds C, updates D) error
}
```

## Conditions Builder (Conditions DSL)

### Condition Type

```go
type Condition struct {
    Key   string      // Field name
    Value any         // Value
    C     ConditionOp // Operator
}

type C []Condition // Condition list
```

### Supported Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `Eq` | Equal to | `{Key: "age", Value: 18, C: Eq}` |
| `Ne` | Not equal to | `{Key: "status", Value: "deleted", C: Ne}` |
| `Gt` | Greater than | `{Key: "price", Value: 100, C: Gt}` |
| `Gte` | Greater than or equal to | `{Key: "score", Value: 60, C: Gte}` |
| `Lt` | Less than | `{Key: "age", Value: 65, C: Lt}` |
| `Lte` | Less than or equal to | `{Key: "amount", Value: 1000, C: Lte}` |
| `In` | Contained in | `{Key: "status", Value: []string{"active", "pending"}, C: In}` |
| `Nin` | Not contained in | `{Key: "role", Value: []string{"admin", "root"}, C: Nin}` |
| `Like` | Fuzzy match (SQL) | `{Key: "name", Value: "John%", C: Like}` |
| `Regex` | Regular expression (Mongo) | `{Key: "email", Value: ".*@example\\.com", C: Regex}` |
| `Exists` | Field exists | `{Key: "optional_field", Value: true, C: Exists}` |

### Usage Examples

```go
// Simple condition
conds := database.C{
    {Key: "age", Value: 18, C: database.Gt},
}

// Compound conditions (AND relationship)
conds := database.C{
    {Key: "age", Value: 18, C: database.Gt},
    {Key: "status", Value: "active", C: database.Eq},
    {Key: "city", Value: []string{"Beijing", "Shanghai"}, C: database.In},
}

// Query
var users []User
db.Find(ctx, "users", conds, []string{"-created_at"}, 100, &users)
```

## Update Builder (Updates DSL)

```go
type Element struct {
    Key   string
    Value any
}

type D []Element // Update list
```

### Usage Examples

```go
updates := database.D{
    {Key: "status", Value: "active"},
    {Key: "updated_at", Value: time.Now()},
    {Key: "login_count", Value: 1}, // Increment requires special handling
}

db.Update(ctx, "users", 
    database.C{{Key: "id", Value: userId}}, 
    updates)
```

## Pagination Query

### PageQueryRequest

```go
type PageQueryRequest struct {
    PageIndex  int         `json:"pageIndex"`  // Page number (starting from 1)
    PageSize   int         `json:"pageSize"`   // Page size
    Conditions C           `json:"conditions"` // Query conditions
    SortBy     []string    `json:"sortBy"`     // Sort fields
    Select     []string    `json:"select"`     // Select fields (optional)
}
```

### PageQueryResponse

```go
type PageQueryResponse[T any] struct {
    Data       []T   `json:"data"`       // Data list
    Total      int64 `json:"total"`      // Total records
    PageIndex  int   `json:"pageIndex"`  // Current page number
    PageSize   int   `json:"pageSize"`   // Page size
    TotalPages int   `json:"totalPages"` // Total pages
}
```

### Usage Examples

```go
req := &database.PageQueryRequest{
    PageIndex: 1,
    PageSize:  20,
    Conditions: database.C{
        {Key: "status", Value: "active"},
        {Key: "age", Value: 18, C: database.Gte},
    },
    SortBy: []string{"-created_at", "name"}, // - indicates descending order
}

// SQL
resp, err := sql.PageQuery[User](ctx, db, "users", req)

// Mongo
resp, err := mongo.PageQuery[User](ctx, db, "users", req)

fmt.Printf("Total: %d, Pages: %d\n", resp.Total, resp.TotalPages)
for _, user := range resp.Data {
    fmt.Printf("User: %s\n", user.Name)
}
```

## Stream Query

Used for processing large amounts of data to avoid memory overflow.

```go
type Row interface {
    Next() bool
    Scan(dest any) error
    Close() error
}
```

### Usage Examples

```go
var user User
rows, err := db.FindRows(ctx, "users", 
    database.C{{Key: "status", Value: "active"}},
    []string{"-id"},
    0, // No limit
    &user)
defer rows.Close()

for rows.Next() {
    if err := rows.Scan(&user); err != nil {
        log.Error("Scan error", "err", err)
        continue
    }
    
    // Process single user
    processUser(&user)
}
```

## Transaction Handling

### Transaction Interface

```go
type Transaction interface {
    Commit() error
    Rollback() error
}
```

### Usage Examples

```go
// 1. Start transaction
tx, err := db.StartTransaction(ctx)
if err != nil {
    return err
}

// 2. Create transaction database instance
txDB := db.WithTransaction(ctx, tx)

// 3. Execute operations
if err := txDB.Insert(ctx, "orders", order); err != nil {
    tx.Rollback()
    return err
}

if err := txDB.Update(ctx, "inventory", 
    database.C{{Key: "sku", Value: order.SKU}},
    database.D{{Key: "stock", Value: newStock}}); err != nil {
    tx.Rollback()
    return err
}

// 4. Commit transaction
if err := tx.Commit(); err != nil {
    return err
}
```

## Sorting Rules

Use the `sortBy` parameter to specify sorting:

```go
sortBy := []string{
    "-created_at",  // Descending (prefix -)
    "name",         // Ascending
    "-priority",    // Descending
}
```

## Field Selection

Query only specified fields (optional optimization):

```go
req := &database.PageQueryRequest{
    PageIndex: 1,
    PageSize:  10,
    Select: []string{"id", "name", "email"}, // Only return these fields
}
```

## Best Practices

### 1. Use Context Timeout

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

db.FindOne(ctx, "users", conds, &user)
```

### 2. Batch Operations

```go
// Batch insert (better performance)
users := []any{&user1, &user2, &user3}
db.BatchInsert(ctx, "users", users)
```

### 3. Index Optimization

```go
// Ensure queried fields have indexes
type User struct {
    Email string `db:"email,unique,index"` // Unique index
    City  string `db:"city,index"`         // Regular index
}
```

### 4. Avoid N+1 Queries

```go
// Bad: Loop queries
for _, orderId := range orderIds {
    db.FindOne(ctx, "orders", database.C{{Key: "id", Value: orderId}}, &order)
}

// Good: Use In
db.Find(ctx, "orders", database.C{{Key: "id", Value: orderIds, C: database.In}}, nil, 0, &orders)
```

### 5. Use Stream Queries for Large Data

```go
// Bad: Load all data at once
var allUsers []User
db.Find(ctx, "users", nil, nil, 0, &allUsers) // May cause OOM

// Good: Stream processing
rows, _ := db.FindRows(ctx, "users", nil, nil, 0, &User{})
defer rows.Close()
for rows.Next() {
    // Process one by one
}
```

## Database Differences

### SQL vs Mongo

| Feature | SQL | MongoDB |
|---------|-----|---------|
| Condition operators | All supported | All supported |
| Like | Supports `%` wildcard | Uses Regex |
| Transactions | Full support | Supported (requires replica set) |
| Schema | Requires pre-definition | Flexible Schema |
| Aggregation | SQL statements | Aggregation Pipeline |

### Cross-database Compatible Code

```go
// This code can run on MySQL/PostgreSQL/MongoDB
func FindActiveUsers(ctx context.Context, db database.Database) ([]User, error) {
    var users []User
    err := db.Find(ctx, "users", 
        database.C{{Key: "status", Value: "active"}},
        []string{"-created_at"},
        100,
        &users)
    return users, err
}
```

## Error Handling

```go
err := db.FindOne(ctx, "users", conds, &user)
if err != nil {
    if errors.Is(err, database.ErrNotFound) {
        // Record does not exist
        return nil, ErrUserNotFound
    }
    // Other database errors
    return nil, fmt.Errorf("database error: %w", err)
}
```

## Reference

- [SQL Adapter Documentation](../sql/README.md)
- [MongoDB Adapter Documentation](../mongo/README.md)
- [Main README](../../README.md)
