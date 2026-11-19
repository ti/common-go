package mysql

import (
	"errors"

	"github.com/go-sql-driver/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ConvertError converts database errors to gRPC status errors
// nolint: gomnd
func ConvertError(err error) error {
	if mySQLError, ok := errors.AsType[*mysql.MySQLError](err); ok {
		// if the database is not found, return the error
		var code codes.Code
		switch mySQLError.Number {
		case 1049:
			code = codes.NotFound
		// ER_DUP_ENTRY: a unique key is already taken
		case 1062, 1602:
			code = codes.AlreadyExists
		// ER_LOCK_DEADLOCK, ER_LOCK_WAIT_TIMEOUT: lost against another
		// transaction; the caller may retry
		case 1213, 1205:
			code = codes.Aborted
		case 1690:
			code = codes.OutOfRange
		case 2000:
			code = codes.NotFound
		default:
			code = codes.Unknown
		}
		err = status.Errorf(code, "error for code %d, message %s",
			mySQLError.Number, mySQLError.Message)
	}
	return err
}
