# Mock Database

Mock Database is an in-memory database implementation for testing and development environments. It implements all methods of the `database.Database` interface, with data stored in memory.

## Features

- Complete implementation of database.Database interface
- In-memory storage, no external dependencies required
- Supports all CRUD operations
- Supports conditional queries (Eq, Ne, Gt, Gte, Lt, Lte, In, Nin)
- Supports sorting and limits
- Supports counter operations
- Supports transactions (simulated)
- Thread-safe
- Structured errors, JSON format uses snake_case
- **Automatic Key Normalization**: Automatically converts camelCase to snake_case, compatible with both naming styles
- Suitable for unit tests

## Installation

Mock database is automatically registered via `database.RegisterImplements`.

```go
import _ "github.com/ti/common-go/dependencies/database/mock"
```

## URL Format

```
mock://host/database_name
```

Examples:
- `mock://local/testdb`
- `mock://memory/myapp`

## Usage Examples

### Basic Usage

```go
package main

import (
    "context"
    "fmt"
    "github.com/ti/common-go/dependencies/database"
    _ "github.com/ti/common-go/dependencies/database/mock"
)

type User struct {
    ID    int64  `json:"id"`
    Name  string `json:"name"`
    Email string `json:"email"`
    Age   int    `json:"age"`
}

func main() {
    ctx := context.Background()

    // Create mock database
    db, err := database.New(ctx, "mock://local/testdb")
    if err != nil {
        panic(err)
    }
    defer db.Close(ctx)

    // Insert data
    user := &User{
        ID:    1,
        Name:  "Alice",
        Email: "alice@example.com",
        Age:   25,
    }

    err = db.InsertOne(ctx, "users", user)
    if err != nil {
        panic(err)
    }

    // Query data
    var result User
    err = db.FindOne(ctx, "users",
        database.C{{Key: "id", Value: int64(1)}},
        &result)
    if err != nil {
        panic(err)
    }

    fmt.Printf("Found user: %s (%s)\n", result.Name, result.Email)
}
```

### Conditional Query

```go
// Find all users older than 18
var users []User
err := db.Find(ctx, "users",
    database.C{{Key: "age", Value: 18, C: database.Gt}},
    []string{"-age"}, // Sort by age descending
    10, // Limit to 10 records
    &users)
```

### Update Operation

```go
// Update a single document
count, err := db.UpdateOne(ctx, "users",
    database.C{{Key: "id", Value: int64(1)}},
    database.D{
        {Key: "age", Value: 26},
        {Key: "email", Value: "newemail@example.com"},
    })
```

### Delete Operation

```go
// Delete a single document
count, err := db.DeleteOne(ctx, "users",
    database.C{{Key: "id", Value: int64(1)}})

// Delete multiple documents
count, err := db.Delete(ctx, "users",
    database.C{{Key: "age", Value: 18, C: database.Lt}})
```

### Count Operation

```go
// Count documents matching conditions
count, err := db.Count(ctx, "users",
    database.C{{Key: "age", Value: 18, C: database.Gte}})
fmt.Printf("Users aged 18+: %d\n", count)

// Check existence
exists, err := db.Exist(ctx, "users",
    database.C{{Key: "email", Value: "alice@example.com"}})
```

### Stream Query

```go
// Suitable for processing large amounts of data
var user User
rows, err := db.FindRows(ctx, "users", nil, nil, 0, &user)
if err != nil {
    panic(err)
}
defer rows.Close()

for rows.Next() {
    data, err := rows.Decode()
    if err != nil {
        continue
    }

    // Convert map to struct
    userMap := data.(map[string]any)
    fmt.Printf("User: %v\n", userMap["name"])
}
```

### Counter Operations

```go
// Initialize counter and increment
err := db.IncrCounter(ctx, "counters", "page_views", 0, 1)

// Decrement counter
err := db.DecrCounter(ctx, "counters", "page_views", 1)

// Get counter value
value, err := db.GetCounter(ctx, "counters", "page_views")
```

### Transaction (Simulated)

```go
// Start transaction
tx, err := db.StartTransaction(ctx)
if err != nil {
    panic(err)
}

// Use transaction database instance
txDB := db.WithTransaction(ctx, tx)

// Execute operations
err = txDB.InsertOne(ctx, "orders", order)
if err != nil {
    tx.Rollback()
    return err
}

_, err = txDB.UpdateOne(ctx, "inventory",
    database.C{{Key: "sku", Value: "ITEM001"}},
    database.D{{Key: "stock", Value: 95}})
if err != nil {
    tx.Rollback()
    return err
}

// Commit transaction
err = tx.Commit()
```

## Usage in Unit Tests

```go
package myapp_test

import (
    "context"
    "testing"

    "github.com/ti/common-go/dependencies/database"
    _ "github.com/ti/common-go/dependencies/database/mock"
)

func TestUserRepository(t *testing.T) {
    ctx := context.Background()

    // Use mock database
    db, err := database.New(ctx, "mock://test/mydb")
    if err != nil {
        t.Fatal(err)
    }
    defer db.Close(ctx)

    // Create repository
    repo := NewUserRepository(db)

    // Test insert
    user := &User{ID: 1, Name: "Test"}
    err = repo.Create(ctx, user)
    if err != nil {
        t.Fatal(err)
    }

    // Test query
    found, err := repo.GetByID(ctx, 1)
    if err != nil {
        t.Fatal(err)
    }

    if found.Name != "Test" {
        t.Errorf("Expected name 'Test', got '%s'", found.Name)
    }
}
```

## Supported Condition Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `Eq` | Equal to | `{Key: "age", Value: 18, C: Eq}` |
| `Ne` | Not equal to | `{Key: "status", Value: "deleted", C: Ne}` |
| `Gt` | Greater than | `{Key: "price", Value: 100, C: Gt}` |
| `Gte` | Greater than or equal to | `{Key: "score", Value: 60, C: Gte}` |
| `Lt` | Less than | `{Key: "age", Value: 65, C: Lt}` |
| `Lte` | Less than or equal to | `{Key: "amount", Value: 1000, C: Lte}` |
| `In` | Contained in | `{Key: "status", Value: []string{"active", "pending"}, C: In}` |
| `Nin` | Not contained in | `{Key: "role", Value: []string{"admin"}, C: Nin}` |

## Error Handling

All errors from Mock database use structured JSON format with **snake_case** field names:

### Error Structure

```go
type Error struct {
    ErrorCode        string `json:"error_code"`         // Error code
    ErrorMessage     string `json:"error_message"`      // Error message
    ErrorDescription string `json:"error_description"`  // Detailed description (optional)
}
```

### JSON Format Example

```json
{
  "error_code": "not_found",
  "error_message": "record not found",
  "error_description": "no record found in table 'users'"
}
```

### Common Error Types

| Error Code | Description | Use Case |
|------------|-------------|----------|
| `not_found` | Record not found | FindOne found no matching record |
| `invalid_argument` | Invalid argument | Parameter validation failed |
| `transaction_error` | Transaction error | Transaction operation failed |
| `database_error` | Database error | General database error |
| `already_exists` | Record already exists | Inserting duplicate record |
| `invalid_operation` | Invalid operation | Unsupported operation |

### Error Handling Example

```go
var user User
err := db.FindOne(ctx, "users",
    database.C{{Key: "id", Value: int64(999)}},
    &user)

if err != nil {
    // Type assertion to get structured error
    if mockErr, ok := err.(*mock.Error); ok {
        fmt.Printf("Error code: %s\n", mockErr.ErrorCode)
        fmt.Printf("Error message: %s\n", mockErr.ErrorMessage)

        // Convert to JSON
        jsonBytes, _ := json.Marshal(mockErr)
        // Output: {"error_code":"not_found","error_message":"record not found",...}
        fmt.Println(string(jsonBytes))
    }
}
```

**Note**: All error fields use `snake_case` format (e.g., `error_code`, `error_message`), not `camelCase` format (e.g., `errorCode`, `errorMessage`).

## Automatic Key Normalization

Mock Database automatically normalizes all field names to `snake_case` format, meaning you can mix `camelCase` and `snake_case`:

### How It Works

All field names (whether in struct tags, conditional queries, update operations, or sort fields) are automatically converted to `snake_case` for storage and matching:

- `userId` -> `user_id`
- `firstName` -> `first_name`
- `UserAge` -> `user_age`
- `HTTPResponse` -> `http_response`
- `user_id` -> `user_id` (already snake_case, unchanged)

### Usage Examples

#### 1. Mixed Naming Style Structs

```go
// Using camelCase JSON tags
type User struct {
    ID        int64  `json:"userId"`
    FirstName string `json:"firstName"`
    Age       int    `json:"userAge"`
}

// Or using snake_case JSON tags
type User struct {
    ID        int64  `json:"user_id"`
    FirstName string `json:"first_name"`
    Age       int    `json:"user_age"`
}

// Both approaches work correctly!
```

#### 2. Query Condition Compatibility

```go
// Query using camelCase
db.FindOne(ctx, "users",
    database.C{{Key: "userId", Value: int64(1)}},
    &user)

// Or query using snake_case (same effect)
db.FindOne(ctx, "users",
    database.C{{Key: "user_id", Value: int64(1)}},
    &user)

// Both methods query the same field!
```

#### 3. Update Operation Compatibility

```go
// Update using camelCase
db.UpdateOne(ctx, "users",
    database.C{{Key: "userId", Value: int64(1)}},
    database.D{{Key: "userAge", Value: 26}})

// Or update using snake_case
db.UpdateOne(ctx, "users",
    database.C{{Key: "user_id", Value: int64(1)}},
    database.D{{Key: "user_age", Value: 26}})
```

#### 4. Sort Field Compatibility

```go
// Sort using camelCase
db.Find(ctx, "users", nil, []string{"userAge"}, 10, &users)

// Or sort using snake_case
db.Find(ctx, "users", nil, []string{"user_age"}, 10, &users)

// Use '-' prefix for descending order
db.Find(ctx, "users", nil, []string{"-userAge"}, 10, &users)
```

#### 5. Cross-style Compatibility

```go
// Insert with camelCase struct
type CamelUser struct {
    ID   int64  `json:"userId"`
    Name string `json:"userName"`
}
camelUser := &CamelUser{ID: 1, Name: "Alice"}
db.InsertOne(ctx, "users", camelUser)

// Query with snake_case struct (still found!)
type SnakeUser struct {
    ID   int64  `json:"user_id"`
    Name string `json:"user_name"`
}
var snakeUser SnakeUser
db.FindOne(ctx, "users",
    database.C{{Key: "user_id", Value: int64(1)}},
    &snakeUser)
// snakeUser.Name == "Alice"
```

### Conversion Rules

| Input | Output | Description |
|-------|--------|-------------|
| `userId` | `user_id` | Standard camelCase |
| `firstName` | `first_name` | Multiple words |
| `UserID` | `user_id` | PascalCase |
| `HTTPResponse` | `http_response` | Consecutive uppercase |
| `parseHTMLDoc` | `parse_html_doc` | Mixed abbreviations |
| `user_id` | `user_id` | Already snake_case |
| `Age` | `age` | Single uppercase letter |

### Advantages

1. **Flexibility**: Frontend can use camelCase, backend can use snake_case
2. **Compatibility**: Can mix different naming style code
3. **Uniformity**: Internal storage uniformly uses snake_case
4. **Simplicity**: No need to manually convert field names

### Best Practices

Although the system supports mixed naming styles, it is recommended to stay consistent within a project:

- **Recommended**: Uniformly use `snake_case` JSON tags (consistent with database standards)
- **Acceptable**: Uniformly use `camelCase` JSON tags (frontend-friendly)
- **Avoid**: Mixing both styles in the same project

## Notes

1. **Data Persistence**: Mock database data is stored in memory; data is lost after program restart
2. **Transaction Isolation**: Transactions are simulated and do not provide true isolation levels
3. **Performance**: Suitable for testing, not for production with large datasets
4. **Concurrency Safety**: Uses sync.RWMutex to ensure thread safety
5. **Field Mapping**: Prioritizes `json` tag, then `bson` tag, finally field name
6. **Key Normalization**: All field names are automatically converted to `snake_case`, supporting mixed camelCase and snake_case usage

## Compatibility with Other Databases

Mock database implements the same interface as MySQL, PostgreSQL, and MongoDB, so code can seamlessly switch between different databases:

```go
// Development environment uses mock
db, _ := database.New(ctx, "mock://local/myapp")

// Production environment uses MongoDB
db, _ := database.New(ctx, "mongodb://localhost:27017/myapp")

// Production environment uses MySQL
db, _ := database.New(ctx, "mysql://user:pass@localhost:3306/myapp")
```

The same code can run on all these databases!

## API Reference

For complete API reference, see [database README](../README.md).
