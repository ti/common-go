package mock_test

import (
	"context"
	"testing"

	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/database/mock"
	_ "github.com/ti/common-go/dependencies/database/mock"
)

// TestUser with camelCase json tags
type CamelCaseUser struct {
	ID        int64  `json:"userId"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Age       int    `json:"userAge"`
}

// TestUser with snake_case json tags
type SnakeCaseUser struct {
	ID        int64  `json:"user_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Age       int    `json:"user_age"`
}

// TestUser with PascalCase field names (no tags)
type PascalCaseUser struct {
	UserID    int64
	FirstName string
	LastName  string
	UserAge   int
}

func TestCamelCaseToSnakeCase(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple camelCase", "userId", "user_id"},
		{"multiple words", "firstName", "first_name"},
		{"already snake_case", "user_id", "user_id"},
		{"PascalCase", "UserName", "user_name"},
		{"acronym", "HTTPResponse", "http_response"},
		{"mixed acronym", "parseHTMLDocument", "parse_html_document"},
		{"single char", "a", "a"},
		{"empty", "", ""},
		{"all caps", "ID", "id"},
		{"multiple caps", "XMLParser", "xml_parser"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mock.ExportToSnakeCase(tt.input)
			if result != tt.expected {
				t.Errorf("toSnakeCase(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestInsertWithCamelCase(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/cameltest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	// Insert with camelCase tags
	camelUser := &CamelCaseUser{
		ID:        1,
		FirstName: "John",
		LastName:  "Doe",
		Age:       30,
	}

	err = db.InsertOne(ctx, "users", camelUser)
	if err != nil {
		t.Fatal("Insert failed:", err)
	}

	// Query using camelCase key
	var result1 CamelCaseUser
	err = db.FindOne(ctx, "users",
		database.C{{Key: "userId", Value: int64(1)}},
		&result1)
	if err != nil {
		t.Fatal("FindOne with camelCase key failed:", err)
	}

	if result1.FirstName != "John" {
		t.Errorf("Expected FirstName 'John', got '%s'", result1.FirstName)
	}

	// Query using snake_case key (should also work)
	var result2 CamelCaseUser
	err = db.FindOne(ctx, "users",
		database.C{{Key: "user_id", Value: int64(1)}},
		&result2)
	if err != nil {
		t.Fatal("FindOne with snake_case key failed:", err)
	}

	if result2.FirstName != "John" {
		t.Errorf("Expected FirstName 'John', got '%s'", result2.FirstName)
	}
}

func TestQueryWithCamelCaseConditions(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/querytest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	// Insert test data
	users := []*CamelCaseUser{
		{ID: 1, FirstName: "Alice", LastName: "Smith", Age: 25},
		{ID: 2, FirstName: "Bob", LastName: "Jones", Age: 30},
		{ID: 3, FirstName: "Charlie", LastName: "Brown", Age: 35},
	}

	_, err = db.Insert(ctx, "users", users)
	if err != nil {
		t.Fatal("Insert failed:", err)
	}

	t.Run("Query with camelCase field name", func(t *testing.T) {
		var results []CamelCaseUser
		err := db.Find(ctx, "users",
			database.C{{Key: "userAge", Value: 25, C: database.Gt}},
			nil, 10, &results)
		if err != nil {
			t.Fatal(err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 users, got %d", len(results))
		}
	})

	t.Run("Query with snake_case field name", func(t *testing.T) {
		var results []CamelCaseUser
		err := db.Find(ctx, "users",
			database.C{{Key: "user_age", Value: 25, C: database.Gt}},
			nil, 10, &results)
		if err != nil {
			t.Fatal(err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 users, got %d", len(results))
		}
	})
}

func TestUpdateWithCamelCase(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/updatetest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	// Insert
	user := &CamelCaseUser{
		ID:        1,
		FirstName: "Alice",
		LastName:  "Smith",
		Age:       25,
	}
	db.InsertOne(ctx, "users", user)

	t.Run("Update with camelCase key", func(t *testing.T) {
		_, err := db.UpdateOne(ctx, "users",
			database.C{{Key: "userId", Value: int64(1)}},
			database.D{{Key: "userAge", Value: 26}})
		if err != nil {
			t.Fatal(err)
		}

		var result CamelCaseUser
		db.FindOne(ctx, "users",
			database.C{{Key: "userId", Value: int64(1)}},
			&result)

		if result.Age != 26 {
			t.Errorf("Expected age 26, got %d", result.Age)
		}
	})

	t.Run("Update with snake_case key", func(t *testing.T) {
		_, err := db.UpdateOne(ctx, "users",
			database.C{{Key: "user_id", Value: int64(1)}},
			database.D{{Key: "user_age", Value: 27}})
		if err != nil {
			t.Fatal(err)
		}

		var result CamelCaseUser
		db.FindOne(ctx, "users",
			database.C{{Key: "user_id", Value: int64(1)}},
			&result)

		if result.Age != 27 {
			t.Errorf("Expected age 27, got %d", result.Age)
		}
	})
}

func TestSortWithCamelCase(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/sorttest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	// Insert test data
	users := []*CamelCaseUser{
		{ID: 1, FirstName: "Charlie", Age: 30},
		{ID: 2, FirstName: "Alice", Age: 25},
		{ID: 3, FirstName: "Bob", Age: 35},
	}
	db.Insert(ctx, "users", users)

	t.Run("Sort by camelCase field", func(t *testing.T) {
		var results []CamelCaseUser
		err := db.Find(ctx, "users",
			nil,
			[]string{"userAge"}, // Sort by userAge ascending
			10,
			&results)
		if err != nil {
			t.Fatal(err)
		}

		if len(results) != 3 {
			t.Errorf("Expected 3 users, got %d", len(results))
		}

		if results[0].Age != 25 || results[1].Age != 30 || results[2].Age != 35 {
			t.Errorf("Sort order incorrect: got ages %d, %d, %d", results[0].Age, results[1].Age, results[2].Age)
		}
	})

	t.Run("Sort by snake_case field", func(t *testing.T) {
		var results []CamelCaseUser
		err := db.Find(ctx, "users",
			nil,
			[]string{"-user_age"}, // Sort by user_age descending
			10,
			&results)
		if err != nil {
			t.Fatal(err)
		}

		if len(results) != 3 {
			t.Errorf("Expected 3 users, got %d", len(results))
		}

		if results[0].Age != 35 || results[1].Age != 30 || results[2].Age != 25 {
			t.Errorf("Sort order incorrect: got ages %d, %d, %d", results[0].Age, results[1].Age, results[2].Age)
		}
	})
}

func TestMixedCaseCompatibility(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/mixedtest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	// Insert with camelCase
	camelUser := &CamelCaseUser{
		ID:        1,
		FirstName: "John",
		LastName:  "Doe",
		Age:       30,
	}
	db.InsertOne(ctx, "users", camelUser)

	// Read as snake_case struct
	var snakeResult SnakeCaseUser
	err = db.FindOne(ctx, "users",
		database.C{{Key: "user_id", Value: int64(1)}},
		&snakeResult)
	if err != nil {
		t.Fatal("FindOne failed:", err)
	}

	if snakeResult.FirstName != "John" {
		t.Errorf("Expected FirstName 'John', got '%s'", snakeResult.FirstName)
	}

	// Insert with snake_case
	snakeUser := &SnakeCaseUser{
		ID:        2,
		FirstName: "Jane",
		LastName:  "Smith",
		Age:       28,
	}
	db.InsertOne(ctx, "users", snakeUser)

	// Read as camelCase struct
	var camelResult CamelCaseUser
	err = db.FindOne(ctx, "users",
		database.C{{Key: "userId", Value: int64(2)}},
		&camelResult)
	if err != nil {
		t.Fatal("FindOne failed:", err)
	}

	if camelResult.FirstName != "Jane" {
		t.Errorf("Expected FirstName 'Jane', got '%s'", camelResult.FirstName)
	}
}

func TestPascalCaseFields(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/pascaltest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	// Insert with PascalCase fields (no tags)
	user := &PascalCaseUser{
		UserID:    1,
		FirstName: "Alice",
		LastName:  "Smith",
		UserAge:   30,
	}
	db.InsertOne(ctx, "users", user)

	// Query with snake_case (field names should be normalized)
	var result PascalCaseUser
	err = db.FindOne(ctx, "users",
		database.C{{Key: "user_id", Value: int64(1)}},
		&result)
	if err != nil {
		t.Fatal("FindOne failed:", err)
	}

	if result.FirstName != "Alice" {
		t.Errorf("Expected FirstName 'Alice', got '%s'", result.FirstName)
	}
}
