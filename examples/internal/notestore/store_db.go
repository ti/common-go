package notestore

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/sql/migrate"
)

// The table (collection) of notes.
const tableNotes = "notes"

//go:embed migrations/*.sql
var migrations embed.FS

// noteRow is a note as stored. It is separate from Note so that storage
// names (the json tags are the column / field names) can change without
// touching the domain.
type noteRow struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	Labels     []string  `json:"labels"`
	Etag       string    `json:"etag"`
	CreateTime time.Time `json:"create_time"`
	UpdateTime time.Time `json:"update_time"`
}

// DBStore is the Store on a database.Database.
type DBStore struct {
	notes  *database.Collection[noteRow]
	system string
}

var _ Store = (*DBStore)(nil)

// NewDBStore prepares the schema and returns the store. It is safe to call
// from every replica at startup:
//
//   - on PostgreSQL and MySQL it applies the embedded migrations (one
//     replica at a time, each migration once);
//   - on MongoDB and the mock it ensures the indexes, which is idempotent.
func NewDBStore(ctx context.Context, db database.Database) (*DBStore, error) {
	db = database.Unwrap(db)
	// The SQL backend has Migrate; asking for the method keeps this file free
	// of engine imports.
	if m, ok := db.(interface {
		Migrate(ctx context.Context, fsys fs.FS, opts ...migrate.Option) error
	}); ok {
		err := m.Migrate(ctx, migrations, migrate.WithDir("migrations"), migrate.WithTable("notes_schema_migrations"))
		if err != nil {
			return nil, fmt.Errorf("migrate notes: %w", err)
		}
	} else {
		err := database.EnsureIndexes(ctx, db, tableNotes,
			database.Index{Fields: []string{"id"}, Unique: true},
			database.Index{Fields: []string{"owner", "-create_time", "-id"}},
		)
		if err != nil {
			return nil, fmt.Errorf("index notes: %w", err)
		}
	}
	notes, err := database.NewCollection[noteRow](db, database.Schema{
		Table:   tableNotes,
		Key:     "id",
		Scope:   "owner", // every query carries the owner; there is no unscoped use here
		Version: "etag",  // writes compare-and-swap on it
		OrderBy: []string{"-create_time", "-id"},
	})
	if err != nil {
		return nil, err
	}
	return &DBStore{notes: notes, system: database.SystemName(db)}, nil
}

// TimePrecision is the precision note times are stored with. The service
// truncates the times it assigns to it, so that a note reads back exactly as
// it was written: microseconds on SQL engines, milliseconds on MongoDB (BSON
// dates).
func (s *DBStore) TimePrecision() time.Duration {
	if s.system == "mongodb" {
		return time.Millisecond
	}
	return time.Microsecond
}

// Insert implements Store.
func (s *DBStore) Insert(ctx context.Context, n Note) error {
	return translate(s.notes.Scope(n.Owner).Create(ctx, toRow(n)))
}

// Get implements Store.
func (s *DBStore) Get(ctx context.Context, owner, id string) (Note, error) {
	row, err := s.notes.Scope(owner).Get(ctx, id)
	if err != nil {
		return Note{}, translate(err)
	}
	return fromRow(row), nil
}

// List implements Store. The keyset query is served by the index on
// (owner, create_time DESC, id DESC).
func (s *DBStore) List(ctx context.Context, owner, label string, after Cursor, limit int) ([]Note, error) {
	var filter database.C
	if label != "" {
		filter = database.C{{Key: "labels", Value: label, C: database.Contains}}
	}
	var position []any // nil is the start
	if after != (Cursor{}) {
		position = []any{after.CreateTime, after.ID}
	}
	rows, err := s.notes.Scope(owner).ListAfter(ctx, filter, nil, position, limit)
	if err != nil {
		return nil, translate(err)
	}
	notes := make([]Note, len(rows))
	for i, row := range rows {
		notes[i] = fromRow(row)
	}
	return notes, nil
}

// Replace implements Store: one atomic
// UPDATE notes SET ... WHERE owner = ? AND id = ? AND etag = ?.
func (s *DBStore) Replace(ctx context.Context, n Note, etag string) error {
	return translate(s.notes.Scope(n.Owner).Replace(ctx, toRow(n), etag))
}

// Delete implements Store.
func (s *DBStore) Delete(ctx context.Context, owner, id, etag string) error {
	return translate(s.notes.Scope(owner).Delete(ctx, id, etag))
}

// translate maps the storage taxonomy to the domain errors. Everything else
// (unavailable, cancelled, ...) passes through as it is.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case database.IsNotFound(err):
		return ErrNotFound
	case database.IsConflict(err):
		return ErrEtagMismatch
	default:
		return err
	}
}

func toRow(n Note) *noteRow {
	return &noteRow{
		ID: n.ID, Owner: n.Owner, Title: n.Title, Body: n.Body, Labels: n.Labels, Etag: n.Etag,
		CreateTime: n.CreateTime, UpdateTime: n.UpdateTime,
	}
}

func fromRow(r *noteRow) Note {
	return Note{
		ID: r.ID, Owner: r.Owner, Title: r.Title, Body: r.Body, Labels: r.Labels, Etag: r.Etag,
		CreateTime: r.CreateTime.UTC(), UpdateTime: r.UpdateTime.UTC(),
	}
}
