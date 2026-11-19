// Package notestore is the worked example of the repository pattern on
// common-go's storage layer (docs/guides/database.md, "Repositories").
//
// It implements the Store interface of the scaffold template's reference
// service note.v1.NoteService: five methods, every one scoped to an owner,
// writes compare-and-swap on an etag, the list by position. The layering is
// the one a product should copy:
//
//	note.go         the domain type, the domain errors and the Store interface
//	                (this file): no database import
//	store_db.go     Store on database.Database: PostgreSQL, MySQL, MongoDB and
//	                the in-memory mock, chosen by the URI of the dependency
//	migrations/     the SQL schema, embedded and applied at startup
//	store_test.go   one behaviour suite, run against every engine
//
// The service above the store (use cases, page tokens, change events) is not
// part of this example; it is the scaffold's backend/internal/note/service.go.
package notestore

import (
	"context"
	"errors"
	"time"
)

// Note is a short text owned by one user.
type Note struct {
	ID     string
	Owner  string
	Title  string
	Body   string
	Labels []string
	// Etag is an opaque version that changes on every write.
	Etag       string
	CreateTime time.Time
	UpdateTime time.Time
}

// Errors of the domain. A store returns these, never an engine error, for
// the outcomes the service branches on.
var (
	// ErrNotFound covers a missing note and a note of another owner alike,
	// so a caller cannot probe for foreign ids.
	ErrNotFound     = errors.New("note not found")
	ErrEtagMismatch = errors.New("etag does not match the stored note")
)

// Cursor is a position in one owner's notes in list order: newest first,
// that is by CreateTime and then ID, both descending. The zero Cursor is the
// start.
type Cursor struct {
	CreateTime time.Time
	ID         string
}

// Store persists notes. Every method is scoped to an owner; a note of
// another owner is ErrNotFound.
type Store interface {
	Insert(ctx context.Context, n Note) error
	Get(ctx context.Context, owner, id string) (Note, error)
	// List returns at most limit notes of owner that come after the cursor
	// in list order, optionally only those carrying label.
	List(ctx context.Context, owner, label string, after Cursor, limit int) ([]Note, error)
	// Replace stores n in place of the note with n's owner and id, provided
	// that note still has the given etag; otherwise ErrEtagMismatch.
	Replace(ctx context.Context, n Note, etag string) error
	// Delete removes the note, provided it still has the given etag;
	// otherwise ErrEtagMismatch.
	Delete(ctx context.Context, owner, id, etag string) error
}
