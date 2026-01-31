package mock_test

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

func ExampleMock() {
	ctx := context.Background()

	// Create mock database with URL: mock://host/database_name
	db, err := database.New(ctx, "mock://local/myapp")
	if err != nil {
		panic(err)
	}
	defer db.Close(ctx)

	// Insert a user
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

	// Find the user
	var result User
	err = db.FindOne(ctx, "users",
		database.C{{Key: "id", Value: int64(1)}},
		&result)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Found user: %s\n", result.Name)
	// Output: Found user: Alice
}

func ExampleMock_conditions() {
	ctx := context.Background()
	db, _ := database.New(ctx, "mock://local/testdb")
	defer db.Close(ctx)

	// Insert multiple users
	users := []*User{
		{ID: 1, Name: "Alice", Age: 20},
		{ID: 2, Name: "Bob", Age: 25},
		{ID: 3, Name: "Charlie", Age: 30},
	}
	db.Insert(ctx, "users", users)

	// Find users with age > 22
	var results []User
	db.Find(ctx, "users",
		database.C{{Key: "age", Value: 22, C: database.Gt}},
		[]string{"-age"}, // Sort by age descending
		10,
		&results)

	for _, u := range results {
		fmt.Printf("%s: %d\n", u.Name, u.Age)
	}
	// Output:
	// Charlie: 30
	// Bob: 25
}

func ExampleMock_update() {
	ctx := context.Background()
	db, _ := database.New(ctx, "mock://local/testdb")
	defer db.Close(ctx)

	// Insert a user
	user := &User{ID: 1, Name: "Alice", Age: 25}
	db.InsertOne(ctx, "users", user)

	// Update the user's age
	db.UpdateOne(ctx, "users",
		database.C{{Key: "id", Value: int64(1)}},
		database.D{{Key: "age", Value: 26}})

	// Find and display
	var result User
	db.FindOne(ctx, "users",
		database.C{{Key: "id", Value: int64(1)}},
		&result)

	fmt.Printf("%s is now %d years old\n", result.Name, result.Age)
	// Output: Alice is now 26 years old
}

func ExampleMock_counter() {
	ctx := context.Background()
	db, _ := database.New(ctx, "mock://local/testdb")
	defer db.Close(ctx)

	// Initialize and increment counter
	db.IncrCounter(ctx, "stats", "page_views", 0, 1)
	db.IncrCounter(ctx, "stats", "page_views", 0, 1)
	db.IncrCounter(ctx, "stats", "page_views", 0, 1)

	// Get counter value
	value, _ := db.GetCounter(ctx, "stats", "page_views")

	fmt.Printf("Page views: %d\n", value)
	// Output: Page views: 3
}

func ExampleMock_transaction() {
	ctx := context.Background()
	db, _ := database.New(ctx, "mock://local/testdb")
	defer db.Close(ctx)

	// Start transaction
	tx, _ := db.StartTransaction(ctx)

	// Use transaction database
	txDB := db.WithTransaction(ctx, tx)

	// Insert in transaction
	user := &User{ID: 1, Name: "Bob", Age: 30}
	err := txDB.InsertOne(ctx, "users", user)
	if err != nil {
		tx.Rollback()
		return
	}

	// Commit transaction
	tx.Commit()

	// Verify
	var result User
	db.FindOne(ctx, "users",
		database.C{{Key: "id", Value: int64(1)}},
		&result)

	fmt.Printf("Committed: %s\n", result.Name)
	// Output: Committed: Bob
}
