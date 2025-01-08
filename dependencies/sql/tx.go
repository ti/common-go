package sql

import (
	"context"
	"database/sql"

	"github.com/ti/common-go/dependencies/database"
)

// StartTransaction with database transaction
func (s *SQL) StartTransaction(ctx context.Context) (tx database.Transaction, err error) {
	dbTx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return dbTx, err
}

// WithTransaction with database transaction
func (s *SQL) WithTransaction(_ context.Context, tx database.Transaction) database.Database {
	return &SQL{
		DB:          s.DB,
		uri:         s.uri,
		compactMode: s.compactMode,
		bustedIndex: s.bustedIndex,
		tx:          tx.(*sql.Tx),
		dbName:      s.dbName,
		scheme:      s.scheme,
		project:     s.project,
	}
}
