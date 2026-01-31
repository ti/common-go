package mock

import (
	"context"

	"github.com/ti/common-go/dependencies/database"
)

// mockTransaction implements database.Transaction interface
type mockTransaction struct {
	mock       *Mock
	committed  bool
	rolledBack bool
}

// StartTransaction starts a new transaction
func (m *Mock) StartTransaction(ctx context.Context) (database.Transaction, error) {
	// For mock database, we don't really implement transactions
	// Just return a mock transaction object
	return &mockTransaction{
		mock: m,
	}, nil
}

// WithTransaction returns a database instance with the transaction
func (m *Mock) WithTransaction(ctx context.Context, tx database.Transaction) database.Database {
	// For mock database, transactions don't isolate data
	// Just return self
	return m
}

// Commit commits the transaction
func (t *mockTransaction) Commit() error {
	if t.rolledBack {
		return NewTransactionError("commit", "transaction already rolled back")
	}
	if t.committed {
		return NewTransactionError("commit", "transaction already committed")
	}
	t.committed = true
	return nil
}

// Rollback rolls back the transaction
func (t *mockTransaction) Rollback() error {
	if t.committed {
		return NewTransactionError("rollback", "transaction already committed")
	}
	if t.rolledBack {
		return NewTransactionError("rollback", "transaction already rolled back")
	}
	t.rolledBack = true
	return nil
}
