package postgres

import (
	"errors"

	"github.com/lib/pq"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ConvertError converts database errors to gRPC status errors
func ConvertError(err error) error {
	if pgError, ok := errors.AsType[*pq.Error](err); ok {
		// if the database is not found, return the error
		var code codes.Code
		switch pgError.Code {
		// unique_violation
		case "23505":
			code = codes.AlreadyExists
		// serialization_failure, deadlock_detected: lost against another
		// transaction; the caller may retry
		case "40001", "40P01":
			code = codes.Aborted
		// not_null_violation
		case "23502":
			code = codes.InvalidArgument
		// undefined_table
		case "42P01":
			code = codes.NotFound
		default:
			code = codes.Unknown
		}
		err = status.Errorf(code, "error for code %s, message %s",
			pgError.Code, pgError.Message)
	}
	return err
}
