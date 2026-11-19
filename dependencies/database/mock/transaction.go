package mock

import (
	"context"
	"maps"

	"github.com/ti/common-go/dependencies/database"
)

// mockTransaction implements database.Transaction interface
type mockTransaction struct {
	mock       *Mock
	committed  bool
	rolledBack bool
	// snapshot is the state at StartTransaction, restored by Rollback.
	tables   map[string]*table
	counters map[string]int64
}

// StartTransaction starts a new transaction
func (m *Mock) StartTransaction(ctx context.Context) (database.Transaction, error) {
	// The mock has one copy of the data, so a transaction is a snapshot:
	// Rollback restores the state of this moment. That gives all-or-nothing
	// for the usual single-transaction test; it does not isolate concurrent
	// transactions (a rollback also undoes writes made outside it since).
	m.mu.Lock()
	defer m.mu.Unlock()
	tx := &mockTransaction{mock: m, tables: make(map[string]*table, len(m.tables)), counters: maps.Clone(m.counters)}
	for name, t := range m.tables {
		rows := make([]map[string]any, len(t.data))
		for i, row := range t.data {
			rows[i] = maps.Clone(row)
		}
		tx.tables[name] = &table{data: rows}
	}
	return tx, nil
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
	t.mock.mu.Lock()
	defer t.mock.mu.Unlock()
	t.mock.tables, t.mock.counters = t.tables, t.counters
	return nil
}
