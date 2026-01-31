// Package mock provides an in-memory mock database implementation for testing.
//
// Mock database implements the database.Database interface and stores all data in memory.
// It's designed for testing and development purposes.
//
// URL Format:
//
//	mock://host/database_name
//
// Example URLs:
//
//	mock://local/testdb
//	mock://memory/myapp
//
// Basic Usage:
//
//	import (
//	    "context"
//	    "github.com/ti/common-go/dependencies/database"
//	    _ "github.com/ti/common-go/dependencies/database/mock"
//	)
//
//	db, err := database.New(ctx, "mock://local/testdb")
//	if err != nil {
//	    panic(err)
//	}
//	defer db.Close(ctx)
//
//	// Insert data
//	user := &User{ID: 1, Name: "Alice"}
//	db.InsertOne(ctx, "users", user)
//
//	// Query data
//	var result User
//	db.FindOne(ctx, "users",
//	    database.C{{Key: "id", Value: int64(1)}},
//	    &result)
//
// Features:
//
//   - In-memory storage
//   - Full CRUD operations
//   - Conditional queries (Eq, Ne, Gt, Gte, Lt, Lte, In, Nin)
//   - Sorting and limiting
//   - Counter operations
//   - Transaction support (simulated)
//   - Thread-safe with sync.RWMutex
//   - Perfect for unit testing
//
// See README.md for more examples and detailed documentation.
package mock
