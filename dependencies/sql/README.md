# SQL Module

MySQL and PostgreSQL database adapters, implementing the `database.Database` interface.

## Features

- Auto Schema generation
- JSON field support
- Array indexing (PostgreSQL)
- NULL value handling
- Batch insert optimization
- Full transaction support
- Stream query
- Connection pool management

## Initialization

### MySQL

```go
import "github.com/ti/common-go/dependencies"

// Method 1: Using dependency injection
type Config struct {
    DB *dependencies.SQL `uri:"mysql://user:password@localhost:3306/mydb?charset=utf8mb4&parseTime=true"`
}

// Method 2: Manual creation
db, err := dependencies.NewSQL(ctx, "mysql://user:password@localhost:3306/mydb?charset=utf8mb4&parseTime=true")
```

### PostgreSQL

```go
db, err := dependencies.NewSQL(ctx, "postgres://user:password@localhost:5432/mydb?sslmode=disable")
```

### Connection Parameters

**MySQL:**
- `charset=utf8mb4` - Character set (recommended)
- `parseTime=true` - Parse TIME/DATETIME
- `loc=Asia%2FShanghai` - Timezone
- `maxAllowedPacket=67108864` - Maximum packet size

**PostgreSQL:**
- `sslmode=disable` - SSL mode (require/disable)
- `connect_timeout=10` - Connection timeout
- `application_name=myapp` - Application name

## Schema Definition

### Struct Tags

```go
type User struct {
    ID        int64     `json:"id" db:"id,primary,auto_increment"`
    Email     string    `json:"email" db:"email,unique,index"`
    Name      string    `json:"name" db:"name,size:100"`
    Age       int       `json:"age" db:"age,default:0"`
    Tags      []string  `json:"tags" db:"tags,json"`
    Settings  Settings  `json:"settings" db:"settings,json"`
    Avatar    *string   `json:"avatar,omitempty" db:"avatar,null"`
    CreatedAt time.Time `json:"created_at" db:"created_at"`
    UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type Settings struct {
    Theme    string `json:"theme"`
    Language string `json:"language"`
}
```

### Tag Description

| Tag | Description | Example |
|-----|-------------|---------|
| `primary` | Primary key | `db:"id,primary"` |
| `auto_increment` | Auto increment (MySQL) | `db:"id,primary,auto_increment"` |
| `unique` | Unique constraint | `db:"email,unique"` |
| `index` | Regular index | `db:"city,index"` |
| `size:N` | String length | `db:"name,size:100"` |
| `default:X` | Default value | `db:"age,default:0"` |
| `json` | JSON type | `db:"settings,json"` |
| `null` | Allow NULL | `db:"avatar,null"` |
| `omitempty` | Ignore empty values | `db:"avatar,omitempty"` |

### Auto-generate Schema

```go
schema := sql.GenerateScheme("users", User{})
fmt.Println(schema)
```

Output (MySQL):
```sql
CREATE TABLE IF NOT EXISTS users (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    email VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    age INT DEFAULT 0,
    tags JSON,
    settings JSON,
    avatar VARCHAR(255),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

Output (PostgreSQL):
```sql
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    age INTEGER DEFAULT 0,
    tags JSONB,
    settings JSONB,
    avatar VARCHAR(255),
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
```

## CRUD Operations

### Insert

```go
user := &User{
    Email: "alice@example.com",
    Name:  "Alice",
    Age:   25,
    Tags:  []string{"golang", "developer"},
}

err := db.Insert(ctx, "users", user)
// user.ID will be automatically populated
```

### Query Single Record

```go
var user User
err := db.FindOne(ctx, "users", 
    database.C{{Key: "email", Value: "alice@example.com"}}, 
    &user)

if errors.Is(err, database.ErrNotFound) {
    // User does not exist
}
```

### Query Multiple Records

```go
var users []User
err := db.Find(ctx, "users",
    database.C{
        {Key: "age", Value: 18, C: database.Gte},
        {Key: "status", Value: "active"},
    },
    []string{"-created_at", "name"}, // Sort
    100, // Limit
    &users)
```

### Update

```go
err := db.Update(ctx, "users",
    database.C{{Key: "id", Value: userId}},
    database.D{
        {Key: "name", Value: "Bob"},
        {Key: "updated_at", Value: time.Now()},
    })
```

### Delete

```go
err := db.Delete(ctx, "users",
    database.C{{Key: "id", Value: userId}})
```

### Count

```go
count, err := db.Count(ctx, "users",
    database.C{{Key: "status", Value: "active"}})
```

## Advanced Queries

### Pagination Query

```go
req := &database.PageQueryRequest{
    PageIndex: 1,
    PageSize:  20,
    Conditions: database.C{
        {Key: "age", Value: 18, C: database.Gte},
    },
    SortBy: []string{"-created_at"},
}

resp, err := sql.PageQuery[User](ctx, db, "users", req)
fmt.Printf("Total: %d, Data: %d\n", resp.Total, len(resp.Data))
```

### LIKE Query

```go
// MySQL/PostgreSQL support LIKE
var users []User
db.Find(ctx, "users",
    database.C{
        {Key: "name", Value: "John%", C: database.Like}, // Prefix match
        {Key: "email", Value: "%@gmail.com", C: database.Like}, // Suffix match
    },
    nil, 100, &users)
```

### IN Query

```go
userIds := []int64{1, 2, 3, 4, 5}
var users []User
db.Find(ctx, "users",
    database.C{{Key: "id", Value: userIds, C: database.In}},
    nil, 0, &users)
```

### Range Query

```go
var products []Product
db.Find(ctx, "products",
    database.C{
        {Key: "price", Value: 10, C: database.Gte},  // >= 10
        {Key: "price", Value: 100, C: database.Lte}, // <= 100
    },
    nil, 0, &products)
```

## Stream Query

Use stream queries to avoid memory overflow when processing large amounts of data:

```go
var user User
rows, err := db.FindRows(ctx, "users",
    database.C{{Key: "status", Value: "active"}},
    []string{"-id"},
    0, // No limit
    &user)
if err != nil {
    return err
}
defer rows.Close()

for rows.Next() {
    if err := rows.Scan(&user); err != nil {
        log.Error("Scan error", "err", err)
        continue
    }
    
    // Process single record
    if err := processUser(ctx, &user); err != nil {
        log.Error("Process error", "err", err)
    }
}
```

## Batch Operations

### Batch Insert

```go
users := []any{
    &User{Name: "Alice", Email: "alice@example.com"},
    &User{Name: "Bob", Email: "bob@example.com"},
    &User{Name: "Charlie", Email: "charlie@example.com"},
}

err := db.BatchInsert(ctx, "users", users)
```

### Batch Update

```go
// Update all records matching conditions
err := db.BatchUpdate(ctx, "users",
    database.C{{Key: "status", Value: "inactive"}},
    database.D{{Key: "deleted_at", Value: time.Now()}})
```

## Transaction Handling

### Basic Transaction

```go
tx, err := db.StartTransaction(ctx)
if err != nil {
    return err
}

// Create transaction database instance
txDB := db.WithTransaction(ctx, tx)

// Execute operations
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

// Commit transaction
return tx.Commit()
```

### Transaction Helper Function

```go
func WithTransaction(ctx context.Context, db database.Database, fn func(database.Database) error) error {
    tx, err := db.StartTransaction(ctx)
    if err != nil {
        return err
    }
    
    txDB := db.WithTransaction(ctx, tx)
    
    if err := fn(txDB); err != nil {
        tx.Rollback()
        return err
    }
    
    return tx.Commit()
}

// Usage
err := WithTransaction(ctx, db, func(txDB database.Database) error {
    if err := txDB.Insert(ctx, "orders", order); err != nil {
        return err
    }
    if err := txDB.Update(ctx, "inventory", conds, updates); err != nil {
        return err
    }
    return nil
})
```

## JSON Fields

### Define JSON Fields

```go
type User struct {
    ID       int64            `db:"id,primary"`
    Settings UserSettings     `db:"settings,json"`
    Tags     []string         `db:"tags,json"`
    Metadata map[string]any   `db:"metadata,json"`
}

type UserSettings struct {
    Theme      string `json:"theme"`
    Language   string `json:"language"`
    Notify     bool   `json:"notify"`
}
```

### Insert JSON

```go
user := &User{
    Settings: UserSettings{
        Theme:    "dark",
        Language: "zh-CN",
        Notify:   true,
    },
    Tags: []string{"vip", "premium"},
    Metadata: map[string]any{
        "source": "mobile",
        "version": "2.0.1",
    },
}

db.Insert(ctx, "users", user)
```

### Query JSON Fields

**MySQL (JSON_EXTRACT):**
```go
// Query users where settings.theme = 'dark'
db.Find(ctx, "users",
    database.C{{Key: "settings->theme", Value: "dark"}},
    nil, 0, &users)
```

**PostgreSQL (JSONB):**
```go
// Query users where settings.theme = 'dark'
db.Find(ctx, "users",
    database.C{{Key: "settings->>'theme'", Value: "dark"}},
    nil, 0, &users)
```

## NULL Value Handling

### Using Pointers

```go
type User struct {
    Avatar   *string    `db:"avatar,null"`
    DeletedAt *time.Time `db:"deleted_at,null"`
}

user := &User{
    Avatar: nil, // NULL
}

// Set value
avatar := "https://example.com/avatar.jpg"
user.Avatar = &avatar
```

### Using sql.Null* Types

```go
import "database/sql"

type User struct {
    Avatar sql.NullString `db:"avatar"`
}

user := &User{
    Avatar: sql.NullString{
        String: "https://example.com/avatar.jpg",
        Valid: true,
    },
}
```

## Database-specific Features

### PostgreSQL Arrays

```go
type Article struct {
    ID   int64    `db:"id,primary"`
    Tags []string `db:"tags,array"` // PostgreSQL ARRAY
}

// Query articles containing a specific tag
db.Find(ctx, "articles",
    database.C{{Key: "tags", Value: "golang", C: database.Contains}},
    nil, 0, &articles)
```

### MySQL Full-text Index

```sql
-- Manually create full-text index
ALTER TABLE articles ADD FULLTEXT INDEX ft_content (content);
```

```go
// Full-text search
db.Find(ctx, "articles",
    database.C{{Key: "MATCH(content) AGAINST(?)", Value: "golang tutorial"}},
    nil, 0, &articles)
```

## Performance Optimization

### 1. Use Batch Insert

```go
// Bad: Insert one by one
for _, user := range users {
    db.Insert(ctx, "users", user) // N database calls
}

// Good: Batch insert
db.BatchInsert(ctx, "users", usersAsAny) // 1 database call
```

### 2. Use Indexes

```go
type User struct {
    Email string `db:"email,unique,index"` // Query optimization
    City  string `db:"city,index"`         // Query optimization
}
```

### 3. Limit Return Fields

```go
req := &database.PageQueryRequest{
    PageIndex: 1,
    PageSize:  10,
    Select: []string{"id", "name", "email"}, // Only return needed fields
}
```

### 4. Use Stream Queries

```go
// Use stream queries when processing large amounts of data
rows, _ := db.FindRows(ctx, "users", nil, nil, 0, &User{})
defer rows.Close()
for rows.Next() {
    // Process one by one, low memory footprint
}
```

### 5. Connection Pool Configuration

```go
// Configure via URI parameters
uri := "mysql://user:pass@host/db?maxOpenConns=100&maxIdleConns=10&connMaxLifetime=3600"
```

## Error Handling

```go
err := db.FindOne(ctx, "users", conds, &user)
if err != nil {
    if errors.Is(err, database.ErrNotFound) {
        return ErrUserNotFound
    }
    
    if errors.Is(err, context.DeadlineExceeded) {
        return ErrTimeout
    }
    
    // Check database error
    if strings.Contains(err.Error(), "Duplicate entry") {
        return ErrDuplicateKey
    }
    
    return fmt.Errorf("database error: %w", err)
}
```

## Migration and Maintenance

### Auto Migration

```go
type User struct {
    // ... field definitions
}

// Generate CREATE TABLE statement
schema := sql.GenerateScheme("users", User{})

// Execute CREATE TABLE
_, err := db.Exec(ctx, schema)
```

### Manual Migration

```go
migrations := []string{
    "ALTER TABLE users ADD COLUMN phone VARCHAR(20)",
    "CREATE INDEX idx_users_phone ON users(phone)",
    "ALTER TABLE users ADD COLUMN verified BOOLEAN DEFAULT FALSE",
}

for _, migration := range migrations {
    if _, err := db.Exec(ctx, migration); err != nil {
        log.Error("Migration failed", "err", err, "sql", migration)
    }
}
```

## Best Practices

1. **Use context timeout**
```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
```

2. **Handle NULL values**
```go
type User struct {
    DeletedAt *time.Time `db:"deleted_at,null"`
}
```

3. **Use transactions to ensure consistency**
```go
WithTransaction(ctx, db, func(txDB database.Database) error {
    // All operations within the same transaction
})
```

4. **Avoid SQL injection**
```go
// The framework automatically handles parameterized queries; do not manually concatenate SQL
```

5. **Use indexes wisely**
```go
type User struct {
    Email string `db:"email,unique,index"` // Add index to frequently queried fields
}
```

## Reference

- [Database Interface Documentation](../database/README.md)
- [Main README](../../README.md)
