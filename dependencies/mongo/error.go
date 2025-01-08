package mongo

import (
	"errors"

	"go.mongodb.org/mongo-driver/mongo"
)

// IsNotFoundError check if it is not found
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, mongo.ErrNoDocuments) || errors.Is(err, mongo.ErrNilDocument) || errors.Is(err, mongo.ErrNilCursor) {
		return true
	}
	return false
}

// IsConflictError check if it is not found
func IsConflictError(err error) bool {
	if err == nil {
		return false
	}
	var exception mongo.WriteException
	if errors.As(err, &exception) {
		for _, v := range exception.WriteErrors {
			if v.Code == 11000 {
				return true
			}
		}
	}
	return false
}
