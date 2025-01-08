package database

import (
	"context"
	"fmt"
	"net/url"
)

// Database the database interface that support mysql and mongodb
// nolint: interfacebloat // more method in database.
type Database interface {
	GetDatabase(ctx context.Context, project string) (Database, error)
	Close(ctx context.Context) error
	Insert(ctx context.Context, table string, docs any) (count int, err error)
	InsertOne(ctx context.Context, table string, data any) error
	// Update the doc, the value of doc can be [database.D] or any other pointer.
	Update(ctx context.Context, table string, condition C, doc any) (count int, err error)
	// UpdateOne the value of doc can be [database.D] or any other pointer.
	UpdateOne(ctx context.Context, table string, condition C, doc any) (count int, err error)
	// Replace update data in bulk
	Replace(ctx context.Context, table string, indexKeys []string, docs any) (count int, err error)
	// ReplaceOne replace
	ReplaceOne(ctx context.Context, table string, condition C, data any) (count int, err error)
	// Delete with condition
	Delete(ctx context.Context, table string, condition C) (count int, err error)
	DeleteOne(ctx context.Context, table string, condition C) (count int, err error)
	// Find the data must be a slice, sortBy, ["age"] means age ASC, ["-age"] means age DESC，
	Find(ctx context.Context, table string, condition C, sortBy []string, limit int, arrayPtr any) error
	FindOne(ctx context.Context, table string, condition C, data any) error
	FindRows(ctx context.Context, table string, condition C, sortBy []string, limit int, oneData any) (Row, error)
	Exist(ctx context.Context, table string, condition C) (bool, error)
	Count(ctx context.Context, table string, condition C) (int64, error)
	IncrCounter(ctx context.Context, counterTable, key string, start, count int64) error
	DecrCounter(ctx context.Context, counterTable, key string, count int64) error
	GetCounter(ctx context.Context, counterTable, key string) (int64, error)
	StartTransaction(ctx context.Context) (Transaction, error)
	WithTransaction(ctx context.Context, tx Transaction) Database
}

// Row the row defined.
type Row interface {
	Close() error
	Decode() (any, error)
	Next() bool
}

// New sql client.
func New(ctx context.Context, uri string) (Database, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	d := &DB{}
	err = d.Init(ctx, u)
	if err != nil {
		return nil, err
	}
	return d.Database, nil
}

var implements = make(map[string]func(context.Context, *url.URL) (Database, error))

// RegisterImplements register implements.
func RegisterImplements(scheme string, newFN func(context.Context, *url.URL) (Database, error)) {
	implements[scheme] = newFN
}

// DB the db instance
type DB struct {
	Database
}

// Init by uri
func (d *DB) Init(ctx context.Context, u *url.URL) (err error) {
	for k, v := range implements {
		if k == u.Scheme {
			d.Database, err = v(ctx, u)
			if err != nil {
				return
			}
			return nil
		}
	}
	return fmt.Errorf("%s not implement", u.Scheme)
}

// Close the db.
func (d *DB) Close(ctx context.Context) error {
	return d.Database.Close(ctx)
}
