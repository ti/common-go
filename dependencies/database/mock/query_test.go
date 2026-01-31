package mock_test

import (
	"context"
	"testing"

	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/database/query"
	_ "github.com/ti/common-go/dependencies/database/mock"
)

type QueryUser struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Age   int    `json:"age"`
}

func TestPageQuery(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/pagetest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	// Insert test data
	users := []*QueryUser{
		{ID: 1, Name: "Alice", Email: "alice@example.com", Age: 25},
		{ID: 2, Name: "Bob", Email: "bob@example.com", Age: 30},
		{ID: 3, Name: "Charlie", Email: "charlie@example.com", Age: 35},
		{ID: 4, Name: "David", Email: "david@example.com", Age: 40},
		{ID: 5, Name: "Eve", Email: "eve@example.com", Age: 45},
	}
	_, err = db.Insert(ctx, "users", users)
	if err != nil {
		t.Fatal("Insert failed:", err)
	}

	t.Run("Basic PageQuery", func(t *testing.T) {
		req := &database.PageQueryRequest{
			Page:  1,
			Limit: 2,
		}

		resp, err := query.PageQuery[QueryUser](ctx, db, "users", req)
		if err != nil {
			t.Fatal("PageQuery failed:", err)
		}

		if resp.Total != 5 {
			t.Errorf("Expected total 5, got %d", resp.Total)
		}

		if len(resp.Data) != 2 {
			t.Errorf("Expected 2 items, got %d", len(resp.Data))
		}
	})

	t.Run("PageQuery with Sort", func(t *testing.T) {
		req := &database.PageQueryRequest{
			Sort:  []string{"-age"}, // Sort by age descending
			Limit: 3,
		}

		resp, err := query.PageQuery[QueryUser](ctx, db, "users", req)
		if err != nil {
			t.Fatal("PageQuery failed:", err)
		}

		if len(resp.Data) != 3 {
			t.Errorf("Expected 3 items, got %d", len(resp.Data))
		}

		// Check sort order (descending age)
		if resp.Data[0].Age < resp.Data[1].Age {
			t.Error("Sort order incorrect")
		}
	})

	t.Run("PageQuery with Filters", func(t *testing.T) {
		req := &database.PageQueryRequest{
			Filters: database.C{
				{Key: "age", Value: 30, C: database.Gte},
			},
			Limit: 10,
		}

		resp, err := query.PageQuery[QueryUser](ctx, db, "users", req)
		if err != nil {
			t.Fatal("PageQuery failed:", err)
		}

		if resp.Total != 4 {
			t.Errorf("Expected total 4, got %d", resp.Total)
		}

		if len(resp.Data) != 4 {
			t.Errorf("Expected 4 items, got %d", len(resp.Data))
		}

		// Verify all ages >= 30
		for _, user := range resp.Data {
			if user.Age < 30 {
				t.Errorf("Found user with age %d, expected >= 30", user.Age)
			}
		}
	})

	t.Run("PageQuery with Pagination", func(t *testing.T) {
		// Page 1
		req1 := &database.PageQueryRequest{
			Page:  1,
			Limit: 2,
			Sort:  []string{"id"},
		}

		resp1, err := query.PageQuery[QueryUser](ctx, db, "users", req1)
		if err != nil {
			t.Fatal("PageQuery page 1 failed:", err)
		}

		if len(resp1.Data) != 2 {
			t.Errorf("Expected 2 items on page 1, got %d", len(resp1.Data))
		}

		// Page 2
		req2 := &database.PageQueryRequest{
			Page:  2,
			Limit: 2,
			Sort:  []string{"id"},
		}

		resp2, err := query.PageQuery[QueryUser](ctx, db, "users", req2)
		if err != nil {
			t.Fatal("PageQuery page 2 failed:", err)
		}

		if len(resp2.Data) != 2 {
			t.Errorf("Expected 2 items on page 2, got %d", len(resp2.Data))
		}

		// Verify no overlap
		if resp1.Data[0].ID == resp2.Data[0].ID {
			t.Error("Pages should not overlap")
		}
	})

	t.Run("PageQuery with NoCount", func(t *testing.T) {
		req := &database.PageQueryRequest{
			NoCount: true,
			Limit:   3,
		}

		resp, err := query.PageQuery[QueryUser](ctx, db, "users", req)
		if err != nil {
			t.Fatal("PageQuery failed:", err)
		}

		if resp.Total != 0 {
			t.Errorf("Expected total 0 (NoCount=true), got %d", resp.Total)
		}

		if len(resp.Data) != 3 {
			t.Errorf("Expected 3 items, got %d", len(resp.Data))
		}
	})
}

func TestStreamQuery(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/streamtest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	// Insert test data with sequential IDs
	users := []*QueryUser{
		{ID: 1, Name: "User1", Age: 20},
		{ID: 2, Name: "User2", Age: 25},
		{ID: 3, Name: "User3", Age: 30},
		{ID: 4, Name: "User4", Age: 35},
		{ID: 5, Name: "User5", Age: 40},
	}
	_, err = db.Insert(ctx, "users", users)
	if err != nil {
		t.Fatal("Insert failed:", err)
	}

	t.Run("Basic StreamQuery", func(t *testing.T) {
		req := &database.StreamQueryRequest{
			PageField: "id",
			Ascending: true,
			Limit:     2,
		}

		resp, err := query.StreamQuery[QueryUser](ctx, db, "users", req)
		if err != nil {
			t.Fatal("StreamQuery failed:", err)
		}

		if resp.Total != 5 {
			t.Errorf("Expected total 5, got %d", resp.Total)
		}

		if len(resp.Data) != 2 {
			t.Errorf("Expected 2 items, got %d", len(resp.Data))
		}

		if resp.PageToken == "" {
			t.Error("Expected PageToken to be set")
		}
	})

	t.Run("StreamQuery with PageToken", func(t *testing.T) {
		// First request
		req1 := &database.StreamQueryRequest{
			PageField: "id",
			Ascending: true,
			Limit:     2,
		}

		resp1, err := query.StreamQuery[QueryUser](ctx, db, "users", req1)
		if err != nil {
			t.Fatal("StreamQuery failed:", err)
		}

		if len(resp1.Data) != 2 {
			t.Errorf("Expected 2 items in first page, got %d", len(resp1.Data))
		}

		// Second request using PageToken
		req2 := &database.StreamQueryRequest{
			PageToken: resp1.PageToken,
			PageField: "id",
			Ascending: true,
			Limit:     2,
		}

		resp2, err := query.StreamQuery[QueryUser](ctx, db, "users", req2)
		if err != nil {
			t.Fatal("StreamQuery with PageToken failed:", err)
		}

		if len(resp2.Data) != 2 {
			t.Errorf("Expected 2 items in second page, got %d", len(resp2.Data))
		}

		// Verify no overlap
		for _, u1 := range resp1.Data {
			for _, u2 := range resp2.Data {
				if u1.ID == u2.ID {
					t.Errorf("Found duplicate ID %d in pages", u1.ID)
				}
			}
		}
	})

	t.Run("StreamQuery Descending", func(t *testing.T) {
		req := &database.StreamQueryRequest{
			PageField: "age",
			Ascending: false, // Descending order
			Limit:     2,
		}

		resp, err := query.StreamQuery[QueryUser](ctx, db, "users", req)
		if err != nil {
			t.Fatal("StreamQuery failed:", err)
		}

		if len(resp.Data) != 2 {
			t.Errorf("Expected 2 items, got %d", len(resp.Data))
		}

		// Check descending order
		if len(resp.Data) >= 2 && resp.Data[0].Age < resp.Data[1].Age {
			t.Error("Expected descending order")
		}
	})

	t.Run("StreamQuery with Filters", func(t *testing.T) {
		req := &database.StreamQueryRequest{
			Filters: database.C{
				{Key: "age", Value: 25, C: database.Gte},
			},
			PageField: "id",
			Ascending: true,
			Limit:     10,
		}

		resp, err := query.StreamQuery[QueryUser](ctx, db, "users", req)
		if err != nil {
			t.Fatal("StreamQuery failed:", err)
		}

		if resp.Total != 4 {
			t.Errorf("Expected total 4, got %d", resp.Total)
		}

		// Verify all ages >= 25
		for _, user := range resp.Data {
			if user.Age < 25 {
				t.Errorf("Found user with age %d, expected >= 25", user.Age)
			}
		}
	})

	t.Run("StreamQuery with NoCount", func(t *testing.T) {
		req := &database.StreamQueryRequest{
			PageField: "id",
			Ascending: true,
			Limit:     3,
			NoCount:   true,
		}

		resp, err := query.StreamQuery[QueryUser](ctx, db, "users", req)
		if err != nil {
			t.Fatal("StreamQuery failed:", err)
		}

		if resp.Total != 0 {
			t.Errorf("Expected total 0 (NoCount=true), got %d", resp.Total)
		}

		if len(resp.Data) != 3 {
			t.Errorf("Expected 3 items, got %d", len(resp.Data))
		}
	})

	t.Run("StreamQuery Last Page", func(t *testing.T) {
		// Get all items in one page
		req := &database.StreamQueryRequest{
			PageField: "id",
			Ascending: true,
			Limit:     10, // More than total items
		}

		resp, err := query.StreamQuery[QueryUser](ctx, db, "users", req)
		if err != nil {
			t.Fatal("StreamQuery failed:", err)
		}

		if resp.PageToken != "" {
			t.Error("Expected no PageToken on last page")
		}

		if len(resp.Data) != 5 {
			t.Errorf("Expected 5 items, got %d", len(resp.Data))
		}
	})
}

func TestQueryWithCamelCaseKeys(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(ctx, "mock://local/camelquerytest")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)

	// Insert test data
	users := []*CamelCaseUser{
		{ID: 1, FirstName: "Alice", Age: 25},
		{ID: 2, FirstName: "Bob", Age: 30},
		{ID: 3, FirstName: "Charlie", Age: 35},
	}
	db.Insert(ctx, "users", users)

	t.Run("PageQuery with camelCase filters", func(t *testing.T) {
		req := &database.PageQueryRequest{
			Filters: database.C{
				{Key: "userAge", Value: 25, C: database.Gte}, // camelCase key
			},
			Sort:  []string{"-userAge"}, // camelCase sort
			Limit: 10,
		}

		resp, err := query.PageQuery[CamelCaseUser](ctx, db, "users", req)
		if err != nil {
			t.Fatal("PageQuery failed:", err)
		}

		if len(resp.Data) != 3 {
			t.Errorf("Expected 3 items, got %d", len(resp.Data))
		}
	})

	t.Run("StreamQuery with camelCase PageField", func(t *testing.T) {
		req := &database.StreamQueryRequest{
			PageField: "userId", // camelCase field
			Ascending: true,
			Limit:     2,
		}

		resp, err := query.StreamQuery[CamelCaseUser](ctx, db, "users", req)
		if err != nil {
			t.Fatal("StreamQuery failed:", err)
		}

		if len(resp.Data) != 2 {
			t.Errorf("Expected 2 items, got %d", len(resp.Data))
		}
	})
}
