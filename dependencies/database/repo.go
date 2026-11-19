package database

import (
	"context"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Paging limits used when a Schema leaves them zero.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Schema describes how one entity type is stored. Field names are the names
// the backends use: the `json` tag of the struct field.
type Schema struct {
	// Table (collection) name. Required.
	Table string
	// Key is the field holding the unique id of a record. Default "id".
	Key string
	// Scope is the field holding the owner or tenant of a record ("owner",
	// "owner_id", "tenant_id"). When set, records are reachable only through
	// Collection.Scope(value) or, explicitly, Collection.Unscoped().
	Scope string
	// Version is the field holding the optimistic-concurrency version of a
	// record (an etag or revision). When set, Replace, Patch and Delete can
	// compare-and-swap on it.
	Version string
	// OrderBy is the default order of List: "field" ascending, "-field"
	// descending. The Key is appended as the tiebreaker when it is missing,
	// so the order is always total. Default: the Key, ascending. Order
	// fields must not be NULL/absent in stored records.
	OrderBy []string
	// DefaultPageSize and MaxPageSize of List. Defaults 20 and 100.
	DefaultPageSize int
	MaxPageSize     int
	// TokenKey is the secret that seals page tokens. See pagetoken.go: set
	// it (same value on every replica) to make tokens unforgeable.
	TokenKey []byte
}

// Collection binds an entity type T to a table of a Database. It has no data
// methods itself: pick a Repo with Scope or Unscoped. That makes the tenant
// decision visible at every call site and impossible to forget.
type Collection[T any] struct {
	db     Database
	schema Schema
	fields map[string][]int // field name -> index path in T
	names  []string         // field names in struct order
	sealer cipher.AEAD
	system string
	typ    reflect.Type
}

// NewCollection validates schema against T (a struct whose fields carry
// `json` tags) and returns the collection. It does not touch the database.
func NewCollection[T any](db Database, schema Schema) (*Collection[T], error) {
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("database: collection type %s must be a struct", t)
	}
	if db == nil {
		return nil, fmt.Errorf("database: collection %q: nil database", schema.Table)
	}
	if schema.Table == "" {
		return nil, fmt.Errorf("database: collection of %s: Schema.Table is empty", t)
	}
	if schema.Key == "" {
		schema.Key = "id"
	}
	if schema.DefaultPageSize <= 0 {
		schema.DefaultPageSize = DefaultPageSize
	}
	if schema.MaxPageSize <= 0 {
		schema.MaxPageSize = MaxPageSize
	}
	schema.DefaultPageSize = min(schema.DefaultPageSize, schema.MaxPageSize)
	c := &Collection[T]{db: Unwrap(db), fields: make(map[string][]int), typ: t}
	c.indexFields(t, nil)
	for _, f := range []string{schema.Key, schema.Scope, schema.Version} {
		if f != "" && c.fields[f] == nil {
			return nil, fmt.Errorf("database: collection %q: %s has no field %q (json tag)", schema.Table, t, f)
		}
	}
	var err error
	if schema.OrderBy, err = c.completeOrder(schema.OrderBy, schema.Key); err != nil {
		return nil, fmt.Errorf("database: collection %q: %w", schema.Table, err)
	}
	if c.sealer, err = newTokenSealer(schema.TokenKey); err != nil {
		return nil, err
	}
	c.schema = schema
	c.system = SystemName(c.db)
	return c, nil
}

// MustCollection is NewCollection for package-level wiring: it panics on a
// schema that does not fit T, which is a programming error.
func MustCollection[T any](db Database, schema Schema) *Collection[T] {
	c, err := NewCollection[T](db, schema)
	if err != nil {
		panic(err)
	}
	return c
}

// indexFields records the storage name of every exported field, flattening
// embedded structs, with the same naming rule as the backends.
func (c *Collection[T]) indexFields(t reflect.Type, prefix []int) {
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		path := append(slices.Clone(prefix), i)
		if sf.Anonymous {
			et := sf.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				c.indexFields(et, path)
				continue
			}
		}
		name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(sf.Name)
		}
		if _, dup := c.fields[name]; !dup {
			c.fields[name] = path
			c.names = append(c.names, name)
		}
	}
}

// completeOrder checks the order fields and appends the key as tiebreaker.
func (c *Collection[T]) completeOrder(orderBy []string, key string) ([]string, error) {
	out := slices.Clone(orderBy)
	hasKey := false
	desc := false
	for _, o := range out {
		name := strings.TrimPrefix(o, "-")
		desc = name != o
		path := c.fields[name]
		if path == nil {
			return nil, fmt.Errorf("order field %q is not a field of the entity", name)
		}
		// A page token carries the order values of the last record, and the
		// keyset comparison has no defined place for NULL: pointer fields and
		// composite types are refused here rather than on the second page.
		if ft := c.typ.FieldByIndex(path).Type; !orderable(ft) {
			return nil, fmt.Errorf("order field %q has type %s: use a string, number, bool or time.Time field", name, ft)
		}
		hasKey = hasKey || name == key
	}
	if !hasKey {
		if ft := c.typ.FieldByIndex(c.fields[key]).Type; !orderable(ft) {
			return nil, fmt.Errorf("key field %q has type %s and cannot be the tiebreaker of a list order", key, ft)
		}
		if desc {
			key = "-" + key
		}
		out = append(out, key)
	}
	return out, nil
}

// orderable reports whether a field type can be an order field.
func orderable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return t == reflect.TypeFor[time.Time]()
	}
}

// checkFields rejects a name that is not exactly a declared field of the
// entity. Filters and patches are often built from request data (filter
// fields, update masks): nothing but a known field may reach a backend,
// where a name becomes part of a statement or selects an operator.
func (c *Collection[T]) checkFields(what string, names ...string) error {
	for _, name := range names {
		if c.fields[name] == nil {
			return status.Errorf(codes.InvalidArgument, "%s: %s: %q is not a field", c.schema.Table, what, name)
		}
	}
	return nil
}

func (c *Collection[T]) checkFilter(filter C) error {
	for _, ce := range filter {
		if err := c.checkFields("filter", ce.Key); err != nil {
			return err
		}
	}
	return nil
}

// Schema returns the schema with its defaults applied.
func (c *Collection[T]) Schema() Schema { return c.schema }

// WithDB returns the same collection on another handle of the same database,
// typically the transaction handle inside Transact.
func (c *Collection[T]) WithDB(db Database) *Collection[T] {
	cp := *c
	cp.db = Unwrap(db)
	return &cp
}

// Scope returns the repository of one owner or tenant. Every operation of it
// is restricted to records whose Schema.Scope field equals value: reads do
// not see other records, writes cannot touch them (a foreign record is
// NotFound, so ids cannot be probed), and Create stamps the value on the
// record.
//
// An empty value, or a schema without a Scope field, yields a repository
// whose every operation fails; it never widens to all records.
func (c *Collection[T]) Scope(value any) *Repo[T] {
	switch {
	case c.schema.Scope == "":
		return &Repo[T]{c: c, err: status.Errorf(codes.FailedPrecondition,
			"%s: Scope used but Schema.Scope is not set", c.schema.Table)}
	case value == nil || reflect.ValueOf(value).IsZero():
		return &Repo[T]{c: c, err: status.Errorf(codes.InvalidArgument,
			"%s: empty %s", c.schema.Table, c.schema.Scope)}
	default:
		return &Repo[T]{c: c, scope: value}
	}
}

// Unscoped returns the repository over all records regardless of owner. For
// an entity with a Scope field this is the deliberate exception (background
// jobs, administration) and is easy to find in review.
func (c *Collection[T]) Unscoped() *Repo[T] {
	return &Repo[T]{c: c}
}

// Repo is the set of operations on the records of one Collection, optionally
// restricted to one scope. It is a small value: create it per request.
//
// Every operation runs in a client span "<operation> <table>" and is counted
// in the db_client_operation_duration_seconds histogram.
type Repo[T any] struct {
	c     *Collection[T]
	scope any
	err   error
}

// ListRequest is one page request of Repo.List.
type ListRequest struct {
	// Filter restricts the list, in addition to the scope.
	Filter C
	// OrderBy overrides Schema.OrderBy for this query. The key is appended
	// as tiebreaker when missing.
	OrderBy []string
	// PageSize is the wanted number of records: 0 means the default, values
	// above the maximum are lowered to it, negative values are rejected
	// (AIP-158).
	PageSize int
	// PageToken is the NextPageToken of the previous page, or "" for the
	// first page. A token is valid only for the same scope, Filter and
	// OrderBy; anything else is InvalidArgument (IsInvalidPageToken).
	PageToken string
}

// Page is one page of a list.
type Page[T any] struct {
	Items []*T
	// NextPageToken is "" on the last page.
	NextPageToken string
}

// Cond returns the scope condition followed by extra. Use it for a query the
// Repo has no method for, so that the hand-written query stays scoped:
//
//	db.Count(ctx, table, repo.Cond(database.C{{Key: "status", Value: "open"}}))
//
// extra is passed on as it is: it is for conditions written in code, not for
// field names taken from a request (use List and Count for those).
//
// For a repository that fails every operation (an empty scope, Scope on an
// entity without a scope field) the condition matches no record on any
// backend; it never degrades to "all records".
func (r *Repo[T]) Cond(extra C) C {
	if r.err != nil {
		field := r.c.schema.Scope
		if field == "" {
			field = r.c.schema.Key
		}
		// "in the empty set" is false for every record.
		return C{{Key: field, Value: []any{}, C: In}}
	}
	out := make(C, 0, len(extra)+1)
	if r.scope != nil {
		out = append(out, CE{Key: r.c.schema.Scope, Value: r.scope})
	}
	return append(out, extra...)
}

func (r *Repo[T]) keyCond(id any) C {
	return r.Cond(C{{Key: r.c.schema.Key, Value: id}})
}

func (r *Repo[T]) run(ctx context.Context, operation string, fn func(ctx context.Context) error) error {
	if r.err != nil {
		return r.err
	}
	return observe(ctx, r.c.system, operation, r.c.schema.Table, fn)
}

// Get returns the record with the given id, or NotFound.
func (r *Repo[T]) Get(ctx context.Context, id any) (*T, error) {
	var out T
	err := r.run(ctx, "get", func(ctx context.Context) error {
		return r.c.db.FindOne(ctx, r.c.schema.Table, r.keyCond(id), &out)
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Exists reports whether a record with the given id exists.
func (r *Repo[T]) Exists(ctx context.Context, id any) (ok bool, err error) {
	err = r.run(ctx, "exists", func(ctx context.Context) error {
		ok, err = r.exists(ctx, r.keyCond(id))
		return err
	})
	return ok, err
}

// exists uses Count: unlike Exist it needs no engine-specific id column and
// behaves the same on every backend.
func (r *Repo[T]) exists(ctx context.Context, cond C) (bool, error) {
	n, err := r.c.db.Count(ctx, r.c.schema.Table, cond)
	return n > 0, err
}

// Count returns the number of records matching filter (within the scope).
func (r *Repo[T]) Count(ctx context.Context, filter C) (n int64, err error) {
	err = r.run(ctx, "count", func(ctx context.Context) error {
		if err := r.c.checkFilter(filter); err != nil {
			return err
		}
		n, err = r.c.db.Count(ctx, r.c.schema.Table, r.Cond(filter))
		return err
	})
	return n, err
}

// Create inserts doc. In a scoped repository the scope field of doc is set
// when it is empty and must equal the scope otherwise. A record with the same
// unique key is AlreadyExists, provided the table has the unique index
// (migration or EnsureIndexes).
func (r *Repo[T]) Create(ctx context.Context, doc *T) error {
	return r.run(ctx, "create", func(ctx context.Context) error {
		if doc == nil {
			return status.Errorf(codes.InvalidArgument, "%s: nil record", r.c.schema.Table)
		}
		if err := r.stampScope(doc); err != nil {
			return err
		}
		return r.c.db.InsertOne(ctx, r.c.schema.Table, doc)
	})
}

// Replace writes every field of doc over the stored record with doc's key.
// The key and the scope of a record never change.
//
// expected is the version the caller read: the write happens only if the
// stored record still has it, otherwise the error is a conflict (IsConflict,
// gRPC ABORTED). The comparison and the write are one atomic statement. doc
// must carry a new, different version (NewVersion). A nil or zero expected
// writes unconditionally.
//
// A missing record, or one of another scope, is NotFound.
func (r *Repo[T]) Replace(ctx context.Context, doc *T, expected any) error {
	return r.run(ctx, "replace", func(ctx context.Context) error {
		if doc == nil {
			return status.Errorf(codes.InvalidArgument, "%s: nil record", r.c.schema.Table)
		}
		if err := r.stampScope(doc); err != nil {
			return err
		}
		set := r.c.document(doc, r.c.schema.Key, r.c.schema.Scope)
		return r.write(ctx, r.c.value(doc, r.c.schema.Key), expected, set)
	})
}

// Patch sets the given fields of the record with the given id; other fields
// keep their stored values. expected works as in Replace; when it is given,
// set must contain a new value for the version field.
func (r *Repo[T]) Patch(ctx context.Context, id, expected any, set D) error {
	return r.run(ctx, "patch", func(ctx context.Context) error {
		for _, e := range set {
			// Case-insensitive on purpose: MySQL column names and the mock's
			// keys are, so "Owner" must not get past a check for "owner".
			if strings.EqualFold(e.Key, r.c.schema.Key) ||
				(r.c.schema.Scope != "" && strings.EqualFold(e.Key, r.c.schema.Scope)) {
				return status.Errorf(codes.InvalidArgument, "%s: %s cannot be changed", r.c.schema.Table, e.Key)
			}
			if err := r.c.checkFields("patch", e.Key); err != nil {
				return err
			}
		}
		return r.write(ctx, id, expected, set)
	})
}

func (r *Repo[T]) write(ctx context.Context, id, expected any, set D) error {
	cond, conditional, err := r.versionCond(id, expected)
	if err != nil {
		return err
	}
	if len(set) == 0 {
		return status.Errorf(codes.InvalidArgument, "%s: nothing to write", r.c.schema.Table)
	}
	if conditional {
		// The version must change on a conditional write: that is what makes
		// the next writer's comparison meaningful, and it guarantees that at
		// least one column changes (MySQL reports unchanged rows as 0).
		i := slices.IndexFunc(set, func(e E) bool { return e.Key == r.c.schema.Version })
		if i < 0 || isZero(set[i].Value) || reflect.DeepEqual(set[i].Value, expected) {
			return status.Errorf(codes.InvalidArgument,
				"%s: a conditional write must set a new %s", r.c.schema.Table, r.c.schema.Version)
		}
	}
	n, err := r.c.db.UpdateOne(ctx, r.c.schema.Table, cond, set)
	if err != nil && !IsNotFound(err) {
		return err
	}
	if err == nil && n > 0 {
		return nil
	}
	return r.explainNoMatch(ctx, id, cond, set, conditional)
}

// Delete removes the record with the given id. expected works as in Replace.
func (r *Repo[T]) Delete(ctx context.Context, id, expected any) error {
	return r.run(ctx, "delete", func(ctx context.Context) error {
		cond, conditional, err := r.versionCond(id, expected)
		if err != nil {
			return err
		}
		n, err := r.c.db.DeleteOne(ctx, r.c.schema.Table, cond)
		if err != nil && !IsNotFound(err) {
			return err
		}
		if err == nil && n > 0 {
			return nil
		}
		if !conditional {
			return NewNotFoundError("%s not found", r.c.schema.Table)
		}
		exists, err := r.exists(ctx, r.keyCond(id))
		if err != nil {
			return err
		}
		if exists {
			return NewConflictError("%s: %s does not match the stored record", r.c.schema.Table, r.c.schema.Version)
		}
		return NewNotFoundError("%s not found", r.c.schema.Table)
	})
}

// versionCond is the condition of a write: key, scope and, when expected is
// given, the version.
func (r *Repo[T]) versionCond(id, expected any) (cond C, conditional bool, err error) {
	if isZero(id) {
		return nil, false, status.Errorf(codes.InvalidArgument, "%s: empty %s", r.c.schema.Table, r.c.schema.Key)
	}
	cond = r.keyCond(id)
	if isZero(expected) {
		return cond, false, nil
	}
	if r.c.schema.Version == "" {
		return nil, false, status.Errorf(codes.FailedPrecondition,
			"%s: a version was given but Schema.Version is not set", r.c.schema.Table)
	}
	return append(cond, CE{Key: r.c.schema.Version, Value: expected}), true, nil
}

// explainNoMatch tells why an update changed no record.
//
// A conditional write always sets a new version, so a record it matched was
// changed and counted by every backend: nothing counted means nothing
// matched, and the only question left is whether the record exists at all
// (conflict) or not (not found). The version is deliberately not read again:
// another writer may have set it back to `expected` meanwhile, and answering
// "it matches, so the write happened" would report a write that never did.
//
// An unconditional write can match and change nothing (the record already had
// these values: MySQL then reports 0 rows, MongoDB 0 modified). If the record
// exists the write is tried once more, in case it appeared between the two
// statements; if that changes nothing either and the record is still there,
// the stored values are the written ones and the write has succeeded.
func (r *Repo[T]) explainNoMatch(ctx context.Context, id any, cond C, set D, conditional bool) error {
	exists, err := r.exists(ctx, r.keyCond(id))
	if err != nil {
		return err
	}
	if !exists {
		return NewNotFoundError("%s not found", r.c.schema.Table)
	}
	if conditional {
		return NewConflictError("%s: %s does not match the stored record", r.c.schema.Table, r.c.schema.Version)
	}
	n, err := r.c.db.UpdateOne(ctx, r.c.schema.Table, cond, set)
	if err != nil && !IsNotFound(err) {
		return err
	}
	if err == nil && n > 0 {
		return nil
	}
	// Still nothing counted. Some backends report that as NotFound (MongoDB;
	// SQL with updateTime=) although the record is there and simply has
	// these values already: only its absence is NotFound.
	if exists, err = r.exists(ctx, r.keyCond(id)); err != nil {
		return err
	}
	if !exists {
		return NewNotFoundError("%s not found", r.c.schema.Table)
	}
	return nil
}

// List returns one page in the order of the request and the token of the next
// page. Paging is by position (keyset), not by offset: records created or
// deleted while a client pages never shift or repeat later pages.
func (r *Repo[T]) List(ctx context.Context, req ListRequest) (*Page[T], error) {
	page := &Page[T]{}
	err := r.run(ctx, "list", func(ctx context.Context) error {
		size, err := r.c.pageSize(req.PageSize)
		if err != nil {
			return err
		}
		if err := r.c.checkFilter(req.Filter); err != nil {
			return err
		}
		orderBy := r.c.schema.OrderBy
		if len(req.OrderBy) > 0 {
			if orderBy, err = r.c.completeOrder(req.OrderBy, r.c.schema.Key); err != nil {
				return status.Errorf(codes.InvalidArgument, "%s: %s", r.c.schema.Table, err.Error())
			}
		}
		digest := queryDigest(r.c.schema.Table, r.scope, req.Filter, orderBy)
		after, err := decodePageToken(r.c.sealer, digest, req.PageToken, len(orderBy))
		if err != nil {
			return err
		}
		// One more than the page, to learn whether another page follows.
		var items []*T
		err = FindAfter(ctx, r.c.db, r.c.schema.Table, r.Cond(req.Filter), orderBy, after, size+1, &items)
		if err != nil {
			return err
		}
		if len(items) > size {
			items = items[:size]
			cursor := make([]any, len(orderBy))
			for i, o := range orderBy {
				cursor[i] = r.c.value(items[size-1], strings.TrimPrefix(o, "-"))
			}
			if page.NextPageToken, err = encodePageToken(r.c.sealer, digest, cursor); err != nil {
				return err
			}
		}
		page.Items = items
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

// ListAfter is the keyset query without the token: at most limit records
// matching filter that come strictly after the position `after` (one value
// per orderBy field; nil for the start). Use it when the service owns the
// page-token format; otherwise prefer List. A nil orderBy means
// Schema.OrderBy.
func (r *Repo[T]) ListAfter(ctx context.Context, filter C, orderBy []string, after []any, limit int) ([]*T, error) {
	var items []*T
	err := r.run(ctx, "list", func(ctx context.Context) error {
		if err := r.c.checkFilter(filter); err != nil {
			return err
		}
		order := r.c.schema.OrderBy
		if len(orderBy) > 0 {
			var err error
			if order, err = r.c.completeOrder(orderBy, r.c.schema.Key); err != nil {
				return status.Errorf(codes.InvalidArgument, "%s: %s", r.c.schema.Table, err.Error())
			}
		}
		if len(after) != 0 && len(after) != len(order) {
			return status.Errorf(codes.InvalidArgument, "%s: cursor has %d values for %d order fields %v",
				r.c.schema.Table, len(after), len(order), order)
		}
		return FindAfter(ctx, r.c.db, r.c.schema.Table, r.Cond(filter), order, after, limit, &items)
	})
	return items, err
}

func (c *Collection[T]) pageSize(requested int) (int, error) {
	switch {
	case requested < 0:
		return 0, status.Error(codes.InvalidArgument, "page_size must not be negative")
	case requested == 0:
		return c.schema.DefaultPageSize, nil
	default:
		return min(requested, c.schema.MaxPageSize), nil
	}
}

// stampScope makes doc belong to the repository's scope.
func (r *Repo[T]) stampScope(doc *T) error {
	if r.scope == nil {
		return nil
	}
	fv, err := reflect.ValueOf(doc).Elem().FieldByIndexErr(r.c.fields[r.c.schema.Scope])
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "%s: %s", r.c.schema.Table, err.Error())
	}
	sv := reflect.ValueOf(r.scope)
	if !sv.Type().ConvertibleTo(fv.Type()) {
		return status.Errorf(codes.InvalidArgument, "%s: scope value %T does not fit field %s (%s)",
			r.c.schema.Table, r.scope, r.c.schema.Scope, fv.Type())
	}
	sv = sv.Convert(fv.Type())
	if fv.IsZero() {
		fv.Set(sv)
		return nil
	}
	if !reflect.DeepEqual(fv.Interface(), sv.Interface()) {
		return status.Errorf(codes.InvalidArgument, "%s: record belongs to another %s",
			r.c.schema.Table, r.c.schema.Scope)
	}
	return nil
}

// value returns the value of the named field of doc, or nil.
func (c *Collection[T]) value(doc *T, name string) any {
	fv, err := reflect.ValueOf(doc).Elem().FieldByIndexErr(c.fields[name])
	if err != nil {
		return nil
	}
	return fv.Interface()
}

// document turns doc into the full set of its fields except skip. Empty
// slices, maps, nil pointers and zero times become nil (NULL), which every
// backend stores and reads back as the empty value.
func (c *Collection[T]) document(doc *T, skip ...string) D {
	rv := reflect.ValueOf(doc).Elem()
	out := make(D, 0, len(c.names))
	for _, name := range c.names {
		if name == "" || slices.Contains(skip, name) {
			continue
		}
		fv, err := rv.FieldByIndexErr(c.fields[name])
		if err != nil {
			continue // inside a nil embedded struct
		}
		var v any
		if !isEmptyContainer(fv) {
			v = fv.Interface()
		}
		out = append(out, E{Key: name, Value: v})
	}
	return out
}

func isEmptyContainer(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	case reflect.Struct:
		if t, ok := reflect.TypeAssert[time.Time](v); ok {
			return t.IsZero()
		}
	}
	return false
}

func isZero(v any) bool {
	return v == nil || reflect.ValueOf(v).IsZero()
}

// NewVersion returns a new random version (etag) for a record: 26 characters
// of base32, unguessable and unique for all practical purposes.
func NewVersion() string {
	return rand.Text()
}
