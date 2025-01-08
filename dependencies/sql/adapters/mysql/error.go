package mysql

import (
	"errors"

	"github.com/go-sql-driver/mysql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ConvertError 转换错误
// nolint: gomnd
func ConvertError(err error) error {
	var mySQLError *mysql.MySQLError
	if errors.As(err, &mySQLError) {
		// if the database is not found, return the error
		var code codes.Code
		switch mySQLError.Number {
		case 1049:
			code = codes.NotFound
		case 1602:
			code = codes.AlreadyExists
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
