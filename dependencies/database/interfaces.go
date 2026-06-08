package database

import "context"

// PageQuerier is an optional interface that Database implementations can satisfy
// to support page queries without central dispatch modification.
//
// The result parameter will always be a *PageQueryResponse[T] where T is the
// element type chosen by the caller. Implementers must perform a type assertion
// (e.g. result.(*PageQueryResponse[MyModel])) to populate it. A type mismatch
// between the caller's T and the implementer's assertion will cause a runtime panic.
type PageQuerier interface {
	DoPageQuery(ctx context.Context, table string, in *PageQueryRequest, result any) error
}

// StreamQuerier is an optional interface that Database implementations can satisfy
// to support stream queries without central dispatch modification.
//
// The result parameter will always be a *StreamResponse[T] where T is the
// element type chosen by the caller. Implementers must perform a type assertion
// (e.g. result.(*StreamResponse[MyModel])) to populate it. A type mismatch
// between the caller's T and the implementer's assertion will cause a runtime panic.
type StreamQuerier interface {
	DoStreamQuery(ctx context.Context, table string, in *StreamQueryRequest, result any) error
}
