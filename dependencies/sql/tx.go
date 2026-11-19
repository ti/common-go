package sql

import (
	"context"
	"database/sql"

	"github.com/ti/common-go/dependencies/database"
)

// StartTransaction with database transaction
func (s *SQL) StartTransaction(ctx context.Context) (tx database.Transaction, err error) {
	dbTx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return nil, convertSQLError(s.scheme, err)
	}
	return dbTx, err
}

// WithTransaction returns a handle whose reads and writes run inside tx.
func (s *SQL) WithTransaction(_ context.Context, tx database.Transaction) database.Database {
	c := *s
	c.tx = tx.(*sql.Tx)
	// See GetDatabase: updateTime= applies to the root handle only.
	c.updateDifferent = false
	return &c
}
