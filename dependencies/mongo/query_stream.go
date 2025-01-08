package mongo

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"

	"github.com/ti/common-go/dependencies/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StreamQuery query the documents
func StreamQuery[T any](ctx context.Context, mgo *Mongo, table string,
	in *database.StreamQueryRequest,
) (out *database.StreamResponse[T], err error) {
	col := mgo.Collection(table)
	out = &database.StreamResponse[T]{}
	var limit int64
	var filter bson.D
	out.Total, limit, filter, err = parseQuery(ctx, col, mgo.project, in.Filters, int64(in.Limit), in.NoCount)
	if !in.NoCount && out.Total == 0 {
		return
	}
	opts := options.Find().SetLimit(limit)
	parseSelect(opts, in.Select)
	var sort bson.D
	sortKey := strings.ToLower(in.PageField)
	if sortKey == "" {
		sortKey = "_id"
	}
	if !in.Ascending {
		sort = append(sort, bson.E{
			Key:   sortKey,
			Value: -1,
		})
	} else {
		sort = append(sort, bson.E{
			Key:   sortKey,
			Value: 1,
		})
	}
	if len(sort) > 0 {
		opts.SetSort(sort)
	}
	// page token, By default the page is turned by _id, and it can also be turned by offset time, etc.
	if in.PageToken != "" {
		var pageToken PageToken
		err = decodeConditionPageToken(in.PageToken, &pageToken)
		if err != nil {
			return nil, err
		}
		if pageToken.PageLastValue == nil {
			return out, nil
		}
		filter = append(filter, pageTokenToFilter(in.PageField, in.Ascending, &pageToken)...)
	}
	cur, errFind := col.Find(ctx, filter, opts)
	if errFind != nil {
		if IsNotFoundError(errFind) {
			return nil, status.Error(codes.NotFound, "no data")
		}
		return nil, status.Errorf(codes.Internal, "db find error %s", errFind)
	}
	err = decodeData(ctx, in, out, cur)
	if out.Total == 0 {
		out.Total = int64(len(out.Data))
	}
	return
}

func parseQuery(ctx context.Context, col *mongo.Collection,
	project string, filters database.C, limit int64, noCount bool) (total, newLimit int64,
	filter bson.D, err error,
) {
	filter = getCondition(project, filters)
	if !noCount && len(filter) > 0 {
		total, err = col.CountDocuments(ctx, filter)
	} else {
		total, err = col.EstimatedDocumentCount(ctx)
	}
	if err != nil {
		err = status.Errorf(codes.Internal, "count data %s error %s in condition %s", col.Name(), err, filter)
		return
	}
	if total == 0 {
		return
	}
	newLimit = 2000
	if limit > 0 && limit < newLimit {
		newLimit = limit
	}
	if noCount {
		total = 0
	}
	return
}

func parseSelect(opts *options.FindOptions, selectData []string) {
	var selectFields bson.D
	for _, v := range selectData {
		if len(v) < 2 {
			continue
		}
		switch v[:1] {
		case "-":
			selectFields = append(selectFields, bson.E{
				Key:   v[1:],
				Value: 0,
			})
		default:
			selectFields = append(selectFields, bson.E{
				Key:   v,
				Value: 1,
			})
		}
	}
	if len(selectFields) > 0 {
		opts.SetProjection(selectFields)
	}
}

func decodeData[T any](ctx context.Context, in *database.StreamQueryRequest,
	out *database.StreamResponse[T], cur *mongo.Cursor,
) error {
	if in.PageField == "" {
		in.PageField = docID
	}
	var i int
	var last *mongo.Cursor
	var lastResult any
	defer cur.Close(ctx)
	var pageToken PageToken
	for {
		hasNext := cur.Next(ctx)
		if !hasNext {
			if last != nil && len(out.Data) == in.Limit {
				if in.PageField == docID {
					pageToken.PageLastValue = getDocObjectIDFromCursor(last)
				} else {
					pageToken.PageLastValue = getFiledByName(lastResult, in.PageField)
				}
			}
			break
		}
		result := new(T)
		err := cur.Decode(result)
		lastResult = result
		if err != nil {
			return status.Errorf(codes.Internal, "find cursor error %s", err)
		}
		if i == 0 && in.PageToken != "" {
			if in.PageField == docID {
				pageToken.PageFirstValue = getDocObjectIDFromCursor(cur)
			} else {
				pageToken.PageFirstValue = getFiledByName(result, in.PageField)
			}
		} else {
			last = cur
			lastResult = result
		}
		out.Data = append(out.Data, result)
		i++
	}
	out.PageToken = pageToken.String()
	return nil
}

func pageTokenToFilter(pageField string, ascending bool, pageToken *PageToken) (filter bson.D) {
	if pageField == "" {
		pageField = docID
	} else {
		pageField = strings.ToLower(pageField)
	}
	if pageToken.PageLastValue != nil {
		if ascending {
			filter = append(filter, bson.E{
				Key: pageField,
				Value: bson.D{
					bson.E{
						Key:   "$gt",
						Value: pageToken.PageLastValue,
					},
				},
			})
		} else {
			filter = append(filter, bson.E{
				Key: pageField,
				Value: bson.D{
					bson.E{
						Key:   "$lt",
						Value: pageToken.PageLastValue,
					},
				},
			})
		}
	}
	return
}

// PageToken page turning token information
//
// For example, take offset as an example, then the first page: {sort:offset, filter.offset: > 0)
// The second page: {sort:offset, filter.offset: > 4), where 4 is the last piece of data.
type PageToken struct {
	PageFirstValue any `bson:"f,omitempty"`
	PageLastValue  any `bson:"l,omitempty"`
}

type objectData struct {
	ID primitive.ObjectID `bson:"_id"`
}

func getDocObjectIDFromCursor(cur *mongo.Cursor) primitive.ObjectID {
	var data objectData
	if err := cur.Decode(&data); err == nil {
		return data.ID
	}
	return primitive.ObjectID{}
}

func getFiledByName(src any, field string) any {
	return reflect.Indirect(reflect.ValueOf(src)).FieldByName(field).Interface()
}

// String page token as string
func (p *PageToken) String() string {
	if p.PageLastValue == nil && p.PageFirstValue == nil {
		return ""
	}
	b, _ := bson.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeConditionPageToken(src string, t *PageToken) error {
	b, err := base64.RawURLEncoding.DecodeString(src)
	if err != nil {
		return err
	}
	return bson.Unmarshal(b, t)
}

// IsPageTokenValid check if a page token is valid
func IsPageTokenValid(src string) bool {
	if src == "" {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(src)
	if err != nil {
		return false
	}
	var t PageToken
	err = bson.Unmarshal(b, &t)
	if err != nil {
		return false
	}
	if t.PageFirstValue == nil && t.PageLastValue == nil {
		return false
	}
	return true
}
