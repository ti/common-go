package database

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The error taxonomy of the storage layer. Every backend reports failures as
// gRPC status errors with one of these codes, so a handler can return a
// storage error unchanged and a repository can branch on it without knowing
// the engine:
//
//	NotFound          no record matches (IsNotFound)
//	AlreadyExists     a unique key is already taken (IsAlreadyExists)
//	Aborted           the write lost a race: version (etag) mismatch,
//	                  serialization failure, deadlock (IsConflict). Reading
//	                  again and retrying may succeed. AIP-154 prescribes
//	                  ABORTED for an etag mismatch.
//	InvalidArgument   the request is wrong: bad page token, negative page
//	                  size, empty scope
//	Canceled / DeadlineExceeded   the context ended
//	Unimplemented     the backend lacks an optional capability
//	Internal / Unknown   everything else

// IsConflict reports whether err means that a write lost a race with another
// writer: the record no longer has the expected version, or the engine aborted
// the transaction (serialization failure, deadlock). The caller may read the
// record again and retry.
func IsConflict(err error) bool {
	return err != nil && status.Code(err) == codes.Aborted
}

// NewNotFoundError returns the error backends report when no record matches.
func NewNotFoundError(format string, args ...any) error {
	return status.Errorf(codes.NotFound, format, args...)
}

// NewAlreadyExistsError returns the error backends report for a duplicate key.
func NewAlreadyExistsError(format string, args ...any) error {
	return status.Errorf(codes.AlreadyExists, format, args...)
}

// NewConflictError returns the error reported when a compare-and-swap write
// finds another version than the expected one.
func NewConflictError(format string, args ...any) error {
	return status.Errorf(codes.Aborted, format, args...)
}

// ContextError maps a context error to its gRPC status (Canceled or
// DeadlineExceeded). It returns nil when err is not a context error, so
// backends call it first when converting a driver error.
func ContextError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, fmt.Sprintf("db %s", err.Error()))
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, fmt.Sprintf("db %s", err.Error()))
	default:
		return nil
	}
}
