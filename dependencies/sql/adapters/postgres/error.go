package postgres

import (
	"errors"

	"github.com/lib/pq"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ConvertError 转换错误
func ConvertError(err error) error {
	var pgError *pq.Error
	if errors.As(err, &pgError) {
		// if the database is not found, return the error
		var code codes.Code
		switch pgError.Code {
		// unique_violation
		case "23505":
			code = codes.AlreadyExists
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
