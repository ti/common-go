package mongo

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/ti/common-go/dependencies/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PageQuery query the documents
func PageQuery[T any](ctx context.Context, s *Mongo, table string,
	in *database.PageQueryRequest,
) (out *database.PageQueryResponse[T], err error) {
	out = &database.PageQueryResponse[T]{}
	col := s.Collection(table)
	var limit int64
	var filter bson.D
	out.Total, limit, filter, err = parseQuery(ctx, col, s.project, in.Filters, int64(in.Limit), in.NoCount)
	if !in.NoCount && out.Total == 0 {
		return
	}
	selectParams, distinct := parseSelectAndDistinct(in.Select)
	var skip int
	if in.Page > 0 {
		skip = (in.Page - 1) * int(limit)
	}
	opts := &options.FindOptions{}
	if skip > 0 {
		opts.SetSkip(int64(skip))
	}
	if len(selectParams) > 0 {
		opts.SetProjection(selectParams)
	}
	if len(in.Sort) > 0 {
		sortDoc := bson.D{}
		for _, v := range in.Sort {
			if strings.HasPrefix(v, "-") {
				sortDoc = append(sortDoc, bson.E{
					Key:   v[1:],
					Value: -1,
				})
			} else {
				sortDoc = append(sortDoc, bson.E{
					Key:   v,
					Value: 1,
				})
			}
		}
		opts.SetSort(sortDoc)
	} else {
		opts.SetSort(bson.D{
			bson.E{
				Key:   "_id",
				Value: -1,
			},
		})
	}
	if distinct != "" {
		return parseDistinct[T](ctx, col, distinct)
	}
	if limit > 0 {
		opts.SetLimit(limit)
	}
	cur, errFind := col.Find(ctx, filter, opts)
	if errFind != nil {
		if IsNotFoundError(errFind) {
			return nil, status.Error(codes.NotFound, "no data")
		}
		return nil, status.Errorf(codes.Internal, "db find error %s", errFind)
	}
	err = parseData(ctx, out, cur)
	if out.Total == 0 {
		out.Total = int64(len(out.Data))
	}
	return
}

func parseData[T any](ctx context.Context, out *database.PageQueryResponse[T], cur *mongo.Cursor,
) error {
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		result := new(T)
		err := cur.Decode(result)
		if err != nil {
			return status.Errorf(codes.Internal, "find cursor error %s", err)
		}
		out.Data = append(out.Data, result)
	}
	return nil
}

func parseSelectAndDistinct(selectIn []string) (selectParams map[string]int, distinct string) {
	selectParams = make(map[string]int)
	for _, v := range selectIn {
		if len(v) < 2 {
			continue
		}
		switch v[:1] {
		case "-":
			selectParams[v[1:]] = 0
		case "$":
			distinct = v[1:]
		default:
			selectParams[v] = 1
		}
	}
	return
}

func parseDistinct[T any](ctx context.Context, collection *mongo.Collection,
	distinct string,
) (*database.PageQueryResponse[T], error) {
	ret, err := collection.Distinct(ctx, distinct, bson.M{})
	if err != nil {
		return nil, err
	}
	retDistinct := &database.PageQueryResponse[T]{
		Total: 0,
	}
	if len(ret) == 0 {
		return retDistinct, nil
	}
	var retJSON string
	switch t := ret[0].(type) {
	case int:
		retJSON = toJSONArray(ret, distinct, func(v any) string { return strconv.Itoa(v.(int)) })
	case int64:
		retJSON = toJSONArray(ret, distinct, func(v any) string { return strconv.FormatInt(v.(int64), 10) })
	case float64:
		retJSON = toJSONArray(ret, distinct, func(v any) string {
			return strconv.FormatFloat(v.(float64), 'f', -1, 64)
		})
	case string:
		retJSON = toJSONArray(ret, distinct, func(v any) string { return `"` + v.(string) + `"` })
	default:
		log.Println("MgoQuery unknown type ", t)
	}
	retDistinct.Total = int64(len(ret))
	var result []*T
	err = json.Unmarshal([]byte(retJSON), &result)
	if err != nil {
		return nil, fmt.Errorf("conver json %s error %w", retJSON, err)
	}
	retDistinct.Data = result
	return retDistinct, nil
}

type kv struct {
	Key   string
	Value string
}

var _ = filterToMongo

func filterToMongo(project string, filterData []kv) bson.D {
	filter := bson.D{}
	if project != "" {
		filter = append(filter, bson.E{
			Key:   "project",
			Value: project,
		})
	}
	for _, data := range filterData {
		k, v := data.Key, data.Value
		if k == docID {
			if v == "" {
				continue
			}
			if !(v[0] == '{' || v[0] == ']') {
				if id, ok := getMongoID(v); ok {
					filter = append(filter, bson.E{
						Key:   k,
						Value: id,
					})
					continue
				}
			}
		}
		if v == "" {
			filter = append(filter, bson.E{
				Key:   k,
				Value: v,
			})
			continue
		}
		filter = filterKvs(filter, k, v)
	}
	return filter
}

// charDot the ' char
const charDot = 39

//nolint:cyclop,funlen,gocognit // switch case if better than split
func filterKvs(filter bson.D, k, v string) bson.D {
	firstChar := v[0]
	switch firstChar {
	case '"':
		qv := v[1 : len(v)-1]
		filter = append(filter, bson.E{
			Key:   k,
			Value: qv,
		})
	case '{':
		var data map[string]any
		if err := json.Unmarshal([]byte(v), &data); err == nil {
			if k == docID {
				if in, ok := data["$in"]; ok {
					if arr, ok := in.([]any); ok {
						for i, v := range arr {
							if id, ok := getMongoID(v); ok {
								arr[i] = id
							}
						}
						data["$in"] = arr
					}
				}
			}
			filter = append(filter, bson.E{
				Key:   k,
				Value: data,
			})
		}
	case '[':
		var data []any
		if err := json.Unmarshal([]byte(v), &data); err == nil {
			filter = append(filter, bson.E{
				Key:   k,
				Value: data,
			})
			if k == docID {
				for i, v := range data {
					if id, ok := getMongoID(v); ok {
						data[i] = id
					}
				}
			}
		}
	case '/':
		regV := v[1 : len(v)-1]
		filter = append(filter, bson.E{
			Key:   k,
			Value: primitive.Regex{Pattern: regV},
		})
	case charDot:
		filter = append(filter, bson.E{
			Key:   k,
			Value: v[1 : len(v)-1],
		})
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			filter = append(filter, bson.E{
				Key:   k,
				Value: f,
			})
		} else {
			filter = append(filter, bson.E{
				Key:   k,
				Value: v,
			})
		}
	default:
		switch v {
		case "true":
			filter = append(filter, bson.E{
				Key:   k,
				Value: true,
			})
		case "false":
			filter = append(filter, bson.E{
				Key:   k,
				Value: false,
			})
		default:
			filter = append(filter, bson.E{
				Key:   k,
				Value: v,
			})
		}
	}
	return filter
}

func getMongoID(idx any) (id any, validate bool) {
	if idStr, ok := idx.(string); ok {
		if bsonID, err := ObjectIDFromBase64(idStr); err == nil {
			id = bsonID
			validate = true
		} else if i, err := strconv.ParseInt(idStr, 10, 64); err == nil && i > 0 {
			id = i
			validate = true
		}
	}
	if idFloat, ok := idx.(float64); ok {
		id = int64(idFloat)
		validate = true
	}
	return
}

func toJSONArray(array []any, key string, toString func(any) string) (ret string) {
	if key == docID {
		key = "id"
	}
	ret = "["
	for i, v := range array {
		if i > 0 {
			ret += ","
		}
		ret += `{"` + key + `":` + toString(v) + `}`
	}
	return ret + "]"
}

const docID = "_id"
