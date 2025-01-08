package mongo

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ObjectIDToBase64 object id to base64
func ObjectIDToBase64(id primitive.ObjectID) string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

// ObjectIDFromBase64 creates a new ObjectID from a hex string. It returns an error if the src string is not a
// valid ObjectID.
func ObjectIDFromBase64(s string) (primitive.ObjectID, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return primitive.NilObjectID, err
	}

	if len(b) != 12 {
		return primitive.NilObjectID, errors.New("the provided base64 string is not a valid id")
	}

	var oid [12]byte
	copy(oid[:], b)
	return oid, nil
}

// Index the index instance
type Index struct {
	Field        string
	ReverseOrder bool
	Unique       bool
	Expires      time.Duration
}

// EnsureIndex creates an index
// Feature: Empty fields automatically ignore indexes
func EnsureIndex(ctx context.Context, col *mongo.Collection, indexs ...*Index) (err error) {
	indexKeys := make([]mongo.IndexModel, len(indexs))
	for i, v := range indexs {
		value := 1
		if v.ReverseOrder {
			value = -1
		}
		opts := options.Index()
		fields := strings.Split(v.Field, ",")
		if v.Field == "" {
			return errors.New("filed can not be empty")
		}
		partialFilter := make(bson.D, len(fields))
		var indexName string
		for i, v := range fields {
			partialFilter[i] = bson.E{Key: v, Value: bson.D{{Key: "$exists", Value: true}}}
			indexName += "_" + v
		}
		opts.SetName(indexName)
		if v.Unique {
			opts.SetUnique(v.Unique)
			opts.SetPartialFilterExpression(partialFilter)
		}
		if v.Expires > 1 {
			opts.SetExpireAfterSeconds(int32(v.Expires / time.Second))
		}
		var keys bson.D
		for _, v := range fields {
			keys = append(keys, bson.E{
				Key:   v,
				Value: value,
			})
		}
		indexKeys[i] = mongo.IndexModel{
			Keys:    keys,
			Options: opts,
		}
	}
	indexView := col.Indexes()
	_, err = indexView.CreateMany(ctx, indexKeys)
	if err != nil {
		return fmt.Errorf("create index for %s error for %w", col.Name(), err)
	}
	return nil
}
