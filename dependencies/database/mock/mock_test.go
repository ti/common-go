package mock_test

import (
	"context"
	"testing"

	"github.com/ti/common-go/dependencies/database"
	_ "github.com/ti/common-go/dependencies/database/mock"
)

type TestUser struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Age   int    `json:"age"`
}

func TestMockDatabase(t *testing.T) {
	ctx := context.Background()

	// Create mock database
	db, err := database.New(ctx, "mock://local/testdb")
	if err != nil {
		t.Fatal("Failed to create mock database:", err)
	}
	defer db.Close(ctx)

	t.Run("InsertOne", func(t *testing.T) {
		user := &TestUser{
			ID:    1,
			Name:  "Alice",
			Email: "alice@example.com",
			Age:   25,
		}

		err := db.InsertOne(ctx, "users", user)
		if err != nil {
			t.Fatal("Insert failed:", err)
		}
	})

	t.Run("FindOne", func(t *testing.T) {
		var user TestUser
		err := db.FindOne(ctx, "users",
			database.C{{Key: "id", Value: int64(1)}},
			&user)
		if err != nil {
			t.Fatal("FindOne failed:", err)
		}

		if user.Name != "Alice" {
			t.Errorf("Expected name 'Alice', got '%s'", user.Name)
		}
		if user.Email != "alice@example.com" {
			t.Errorf("Expected email 'alice@example.com', got '%s'", user.Email)
		}
	})

	t.Run("Insert Multiple", func(t *testing.T) {
		users := []*TestUser{
			{ID: 2, Name: "Bob", Email: "bob@example.com", Age: 30},
			{ID: 3, Name: "Charlie", Email: "charlie@example.com", Age: 35},
		}

		count, err := db.Insert(ctx, "users", users)
		if err != nil {
			t.Fatal("Insert multiple failed:", err)
		}

		if count != 2 {
			t.Errorf("Expected count 2, got %d", count)
		}
	})

	t.Run("Find with Condition", func(t *testing.T) {
		var users []TestUser
		err := db.Find(ctx, "users",
			database.C{{Key: "age", Value: 25, C: database.Gt}},
			[]string{"-age"}, // Sort by age descending
			10,
			&users)
		if err != nil {
			t.Fatal("Find failed:", err)
		}

		if len(users) != 2 {
			t.Errorf("Expected 2 users, got %d", len(users))
		}

		// Check order (descending age)
		if len(users) > 0 && users[0].Age < users[len(users)-1].Age {
			t.Error("Results not sorted correctly")
		}
	})

	t.Run("Count", func(t *testing.T) {
		count, err := db.Count(ctx, "users",
			database.C{{Key: "age", Value: 25, C: database.Gte}})
		if err != nil {
			t.Fatal("Count failed:", err)
		}

		if count != 3 {
			t.Errorf("Expected count 3, got %d", count)
		}
	})

	t.Run("Update", func(t *testing.T) {
		count, err := db.UpdateOne(ctx, "users",
			database.C{{Key: "id", Value: int64(1)}},
			database.D{
				{Key: "age", Value: 26},
			})
		if err != nil {
			t.Fatal("Update failed:", err)
		}

		if count != 1 {
			t.Errorf("Expected count 1, got %d", count)
		}

		// Verify update
		var user TestUser
		db.FindOne(ctx, "users",
			database.C{{Key: "id", Value: int64(1)}},
			&user)

		if user.Age != 26 {
			t.Errorf("Expected age 26, got %d", user.Age)
		}
	})

	t.Run("Exist", func(t *testing.T) {
		exists, err := db.Exist(ctx, "users",
			database.C{{Key: "email", Value: "alice@example.com"}})
		if err != nil {
			t.Fatal("Exist failed:", err)
		}

		if !exists {
			t.Error("Expected user to exist")
		}

		exists, err = db.Exist(ctx, "users",
			database.C{{Key: "email", Value: "nonexistent@example.com"}})
		if err != nil {
			t.Fatal("Exist failed:", err)
		}

		if exists {
			t.Error("Expected user not to exist")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		count, err := db.DeleteOne(ctx, "users",
			database.C{{Key: "id", Value: int64(1)}})
		if err != nil {
			t.Fatal("Delete failed:", err)
		}

		if count != 1 {
			t.Errorf("Expected count 1, got %d", count)
		}

		// Verify deletion
		var user TestUser
		err = db.FindOne(ctx, "users",
			database.C{{Key: "id", Value: int64(1)}},
			&user)

		// Should return not found error
		if err == nil {
			t.Error("Expected not found error")
		}
	})

	t.Run("Counter", func(t *testing.T) {
		// Increment counter
		err := db.IncrCounter(ctx, "stats", "page_views", 100, 5)
		if err != nil {
			t.Fatal("IncrCounter failed:", err)
		}

		// Get counter
		value, err := db.GetCounter(ctx, "stats", "page_views")
		if err != nil {
			t.Fatal("GetCounter failed:", err)
		}

		if value != 105 {
			t.Errorf("Expected counter value 105, got %d", value)
		}

		// Decrement counter
		err = db.DecrCounter(ctx, "stats", "page_views", 5)
		if err != nil {
			t.Fatal("DecrCounter failed:", err)
		}

		value, err = db.GetCounter(ctx, "stats", "page_views")
		if err != nil {
			t.Fatal("GetCounter failed:", err)
		}

		if value != 100 {
			t.Errorf("Expected counter value 100, got %d", value)
		}
	})

	t.Run("Transaction", func(t *testing.T) {
		tx, err := db.StartTransaction(ctx)
		if err != nil {
			t.Fatal("StartTransaction failed:", err)
		}

		txDB := db.WithTransaction(ctx, tx)

		// Insert in transaction
		user := &TestUser{
			ID:    10,
			Name:  "Transaction Test",
			Email: "tx@example.com",
			Age:   40,
		}

		err = txDB.InsertOne(ctx, "users", user)
		if err != nil {
			tx.Rollback()
			t.Fatal("Insert in transaction failed:", err)
		}

		// Commit
		err = tx.Commit()
		if err != nil {
			t.Fatal("Commit failed:", err)
		}

		// Verify data was saved
		var result TestUser
		err = db.FindOne(ctx, "users",
			database.C{{Key: "id", Value: int64(10)}},
			&result)
		if err != nil {
			t.Fatal("FindOne after transaction failed:", err)
		}

		if result.Name != "Transaction Test" {
			t.Errorf("Expected name 'Transaction Test', got '%s'", result.Name)
		}
	})
}

func TestReplaceOne(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/replaceonetest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	t.Run("Replace existing document", func(t *testing.T) {
		_ = db.InsertOne(ctx, "users", &TestUser{ID: 1, Name: "Alice", Email: "alice@example.com", Age: 25})

		count, err := db.ReplaceOne(ctx, "users",
			database.C{{Key: "id", Value: int64(1)}},
			&TestUser{ID: 1, Name: "Alice Updated", Email: "alice2@example.com", Age: 26})
		if err != nil {
			t.Fatal("ReplaceOne failed:", err)
		}
		if count != 1 {
			t.Errorf("Expected count 1, got %d", count)
		}

		var result TestUser
		_ = db.FindOne(ctx, "users", database.C{{Key: "id", Value: int64(1)}}, &result)
		if result.Name != "Alice Updated" {
			t.Errorf("Expected 'Alice Updated', got '%s'", result.Name)
		}
		if result.Age != 26 {
			t.Errorf("Expected age 26, got %d", result.Age)
		}

		// Only one document should exist
		total, _ := db.Count(ctx, "users", database.C{})
		if total != 1 {
			t.Errorf("Expected 1 document after replace, got %d", total)
		}
	})

	t.Run("Upsert - insert when not found", func(t *testing.T) {
		count, err := db.ReplaceOne(ctx, "users",
			database.C{{Key: "id", Value: int64(99)}},
			&TestUser{ID: 99, Name: "New User", Email: "new@example.com", Age: 20})
		if err != nil {
			t.Fatal("ReplaceOne upsert failed:", err)
		}
		if count != 1 {
			t.Errorf("Expected count 1 for upsert, got %d", count)
		}

		var result TestUser
		err = db.FindOne(ctx, "users", database.C{{Key: "id", Value: int64(99)}}, &result)
		if err != nil {
			t.Fatal("FindOne after upsert failed:", err)
		}
		if result.Name != "New User" {
			t.Errorf("Expected 'New User', got '%s'", result.Name)
		}
	})
}

func TestReplace(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/replacetest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	// Seed two users
	_ = db.InsertOne(ctx, "users", &TestUser{ID: 1, Name: "Alice", Email: "alice@example.com", Age: 25})
	_ = db.InsertOne(ctx, "users", &TestUser{ID: 2, Name: "Bob", Email: "bob@example.com", Age: 30})

	t.Run("Replace single document by index key", func(t *testing.T) {
		count, err := db.Replace(ctx, "users", []string{"id"},
			&TestUser{ID: 1, Name: "Alice v2", Email: "aliceV2@example.com", Age: 27})
		if err != nil {
			t.Fatal("Replace failed:", err)
		}
		if count != 1 {
			t.Errorf("Expected count 1, got %d", count)
		}

		var result TestUser
		_ = db.FindOne(ctx, "users", database.C{{Key: "id", Value: int64(1)}}, &result)
		if result.Name != "Alice v2" {
			t.Errorf("Expected 'Alice v2', got '%s'", result.Name)
		}

		// Total should still be 2
		total, _ := db.Count(ctx, "users", database.C{})
		if total != 2 {
			t.Errorf("Expected 2 documents after replace, got %d", total)
		}
	})

	t.Run("Replace bulk with upsert for new doc", func(t *testing.T) {
		users := []*TestUser{
			{ID: 2, Name: "Bob v2", Email: "bob2@example.com", Age: 31},
			{ID: 3, Name: "Charlie", Email: "charlie@example.com", Age: 35}, // new
		}
		count, err := db.Replace(ctx, "users", []string{"id"}, users)
		if err != nil {
			t.Fatal("Replace bulk failed:", err)
		}
		if count != 2 {
			t.Errorf("Expected count 2, got %d", count)
		}

		// Bob should be updated
		var bob TestUser
		_ = db.FindOne(ctx, "users", database.C{{Key: "id", Value: int64(2)}}, &bob)
		if bob.Name != "Bob v2" {
			t.Errorf("Expected 'Bob v2', got '%s'", bob.Name)
		}

		// Charlie should be inserted
		var charlie TestUser
		err = db.FindOne(ctx, "users", database.C{{Key: "id", Value: int64(3)}}, &charlie)
		if err != nil {
			t.Fatal("Charlie not found after Replace upsert:", err)
		}

		// Total should now be 3
		total, _ := db.Count(ctx, "users", database.C{})
		if total != 3 {
			t.Errorf("Expected 3 documents after bulk replace, got %d", total)
		}
	})
}

func TestConditions(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/condtest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	// Insert test data
	users := []*TestUser{
		{ID: 1, Name: "Alice", Age: 20},
		{ID: 2, Name: "Bob", Age: 25},
		{ID: 3, Name: "Charlie", Age: 30},
		{ID: 4, Name: "David", Age: 35},
	}

	db.Insert(ctx, "users", users)

	tests := []struct {
		name      string
		condition database.C
		expected  int
	}{
		{
			name: "Eq",
			condition: database.C{
				{Key: "age", Value: 25, C: database.Eq},
			},
			expected: 1,
		},
		{
			name: "Ne",
			condition: database.C{
				{Key: "age", Value: 25, C: database.Ne},
			},
			expected: 3,
		},
		{
			name: "Gt",
			condition: database.C{
				{Key: "age", Value: 25, C: database.Gt},
			},
			expected: 2,
		},
		{
			name: "Gte",
			condition: database.C{
				{Key: "age", Value: 25, C: database.Gte},
			},
			expected: 3,
		},
		{
			name: "Lt",
			condition: database.C{
				{Key: "age", Value: 30, C: database.Lt},
			},
			expected: 2,
		},
		{
			name: "Lte",
			condition: database.C{
				{Key: "age", Value: 30, C: database.Lte},
			},
			expected: 3,
		},
		{
			name: "In",
			condition: database.C{
				{Key: "age", Value: []int{20, 30}, C: database.In},
			},
			expected: 2,
		},
		{
			name: "Nin",
			condition: database.C{
				{Key: "age", Value: []int{20, 30}, C: database.Nin},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, err := db.Count(ctx, "users", tt.condition)
			if err != nil {
				t.Fatal(err)
			}

			if count != int64(tt.expected) {
				t.Errorf("Expected count %d, got %d", tt.expected, count)
			}
		})
	}
}
