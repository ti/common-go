package mock

import (
	"encoding/json/v2"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Error represents a structured error with snake_case JSON format
type Error struct {
	ErrorCode        string `json:"error_code"`
	ErrorMessage     string `json:"error_message"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// Error implements error interface
func (e *Error) Error() string {
	return e.ErrorMessage
}

// GRPCStatus maps the error to a gRPC status, so status.Code(err) and
// database.IsNotFound(err) work the same for the mock as for the SQL and
// Mongo backends.
func (e *Error) GRPCStatus() *status.Status {
	code := codes.Unknown
	switch e.ErrorCode {
	case "not_found":
		code = codes.NotFound
	case "invalid_argument":
		code = codes.InvalidArgument
	case "already_exists":
		code = codes.AlreadyExists
	case "invalid_operation", "transaction_closed":
		code = codes.FailedPrecondition
	}
	msg := e.ErrorMessage
	if e.ErrorDescription != "" {
		msg += ": " + e.ErrorDescription
	}
	return status.New(code, msg)
}

// MarshalJSON returns the JSON encoding with snake_case format
func (e *Error) MarshalJSON() ([]byte, error) {
	type Alias Error
	return json.Marshal((*Alias)(e))
}

// NewError creates a new structured error
func NewError(code, message string) *Error {
	return &Error{
		ErrorCode:    code,
		ErrorMessage: message,
	}
}

// NewErrorWithDescription creates a new error with description
func NewErrorWithDescription(code, message, description string) *Error {
	return &Error{
		ErrorCode:        code,
		ErrorMessage:     message,
		ErrorDescription: description,
	}
}

// Common error codes and constructors
var (
	ErrNotFound          = NewError("not_found", "record not found")
	ErrInvalidArgument   = NewError("invalid_argument", "invalid argument")
	ErrAlreadyExists     = NewError("already_exists", "record already exists")
	ErrInvalidOperation  = NewError("invalid_operation", "invalid operation")
	ErrTransactionClosed = NewError("transaction_closed", "transaction already closed")
)

// NewNotFoundError creates a not found error
func NewNotFoundError(tableName string) *Error {
	return &Error{
		ErrorCode:        "not_found",
		ErrorMessage:     "record not found",
		ErrorDescription: fmt.Sprintf("no record found in table '%s'", tableName),
	}
}

// NewInvalidArgumentError creates an invalid argument error
func NewInvalidArgumentError(argument, reason string) *Error {
	return &Error{
		ErrorCode:        "invalid_argument",
		ErrorMessage:     "invalid argument",
		ErrorDescription: fmt.Sprintf("argument '%s': %s", argument, reason),
	}
}

// NewTransactionError creates a transaction error
func NewTransactionError(operation, reason string) *Error {
	return &Error{
		ErrorCode:        "transaction_error",
		ErrorMessage:     "transaction operation failed",
		ErrorDescription: fmt.Sprintf("%s: %s", operation, reason),
	}
}

// NewDatabaseError creates a generic database error
func NewDatabaseError(message string) *Error {
	return &Error{
		ErrorCode:    "database_error",
		ErrorMessage: message,
	}
}
