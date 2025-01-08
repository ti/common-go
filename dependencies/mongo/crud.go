package mongo

import (
	"context"
	"errors"
	"log"
	"reflect"
	"strings"

	"github.com/ti/common-go/dependencies/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GetDatabase get database with project ns.
func (m *Mongo) GetDatabase(_ context.Context, project string) (database.Database, error) {
	return &Mongo{
		Client:          m.Client,
		defaultDatabase: m.defaultDatabase,
		project:         project,
	}, nil
}

// Insert insert many data
func (m *Mongo) Insert(ctx context.Context, table string, docs any) (count int, err error) {
	data := reflect.ValueOf(docs)
	isSlice := data.Kind() == reflect.Slice
	if !isSlice {
		err = m.InsertOne(ctx, table, docs)
		count = 1
		return
	}
	dataLen := data.Len()
	if dataLen == 0 {
		return 0, errors.New("no insert data found")
	}
	if dataLen == 1 {
		err = m.InsertOne(ctx, table, data.Index(0).Interface())
		count = 1
		return
	}
	col := m.Collection(table)
	mgoDocs := make([]any, dataLen)
	for i := 0; i < dataLen; i++ {
		mgoDocs[i] = m.transformData(data.Index(i).Interface(), false)
	}
	ret, err := col.InsertMany(ctx, mgoDocs, options.InsertMany().SetOrdered(false))
	if err != nil {
		ok, errResp := getBulkError(err)
		if ok {
			return dataLen - len(errResp.Elements), errResp
		}
		err = convertToStatusError(table, err)
		return
	}
	return len(ret.InsertedIDs), nil
}

func convertToStatusError(table string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return status.Errorf(codes.Canceled, "%s error for %s", table, err.Error())
	}
	if IsConflictError(err) {
		return status.Errorf(codes.AlreadyExists, "%s may already exists for %s", table, err.Error())
	}
	if IsNotFoundError(err) {
		return status.Errorf(codes.NotFound, "%s may not found", table)
	}
	return status.Errorf(codes.Internal, "%s db error %s", table, err)
}

// InsertOne insert one data
func (m *Mongo) InsertOne(ctx context.Context, table string, data any) error {
	col := m.Collection(table)
	doc := m.transformData(data, false)
	_, err := col.InsertOne(ctx, doc)
	if err != nil {
		if IsConflictError(err) {
			return status.Errorf(codes.AlreadyExists, "%s may already exists for %s", table, err)
		}
		return status.Errorf(codes.Internal, "create %s db error %s", table, err)
	}
	return nil
}

// Update update data
func (m *Mongo) Update(ctx context.Context, table string, conds database.C, data any) (int, error) {
	col := m.Collection(table)
	filter := getCondition(m.project, conds)
	doc := m.convertUpdateDoc(data)
	ret, err := col.UpdateMany(ctx, filter, bson.M{"$set": doc})
	if err != nil {
		err = convertToStatusError(table, err)
		return 0, err
	}
	return int(ret.ModifiedCount), nil
}

// UpdateOne update one data
func (m *Mongo) UpdateOne(ctx context.Context, table string, conds database.C, data any) (int, error) {
	col := m.Collection(table)
	filter := getCondition(m.project, conds)
	doc := m.convertUpdateDoc(data)
	ret, err := col.UpdateOne(ctx, filter, bson.M{"$set": doc})
	if err != nil {
		return 0, convertToStatusError(table, err)
	}
	if ret.MatchedCount == 0 {
		return 0, status.Errorf(codes.NotFound, "condition %s not found", conds)
	}
	return int(ret.ModifiedCount), nil
}

func (m *Mongo) convertUpdateDoc(d any) (doc any) {
	if reflect.TypeOf(d) == databaseDocType {
		doc = convertDocs(d.(database.D))
	} else if reflect.TypeOf(d) == databaseMapType {
		data := d.(map[string]any)
		d := make(database.D, len(data))
		var i int
		for k, v := range data {
			d[i] = database.E{
				Key:   k,
				Value: v,
			}
			i++
		}
		doc = convertDocs(d)
	} else {
		doc = m.transformData(d, true)
	}
	return doc
}

// ReplaceOne replace one data
func (m *Mongo) ReplaceOne(ctx context.Context, table string, conds database.C, data any) (count int, err error) {
	col := m.Collection(table)
	doc := m.transformData(data, false)
	filter := getCondition(m.project, conds)
	ret, err := col.ReplaceOne(ctx, filter, doc, options.Replace().SetUpsert(true))
	if err != nil {
		return 0, convertToStatusError(table, err)
	}
	return int(ret.ModifiedCount), nil
}

// Replace update data in bulk
func (m *Mongo) Replace(ctx context.Context, table string, indexKeys []string, docs any) (count int, err error) {
	data := reflect.ValueOf(docs)
	isSlice := data.Kind() == reflect.Slice
	col := m.Collection(table)
	if !isSlice {
		filter, doc := m.getFilterByIndexKeys(indexKeys, docs)
		ret, errReplace := col.ReplaceOne(ctx, filter, doc)
		if errReplace != nil {
			return 0, convertToStatusError(table, err)
		}
		return int(ret.ModifiedCount), nil
	}
	dataLen := data.Len()
	if dataLen == 0 {
		return 0, errors.New("no insert data found")
	}
	if len(indexKeys) == 0 {
		indexKeys = []string{"_id"}
	}
	bulkModels := make([]mongo.WriteModel, dataLen)
	for i := 0; i < dataLen; i++ {
		filter, doc := m.getFilterByIndexKeys(indexKeys, docs)
		model := mongo.NewReplaceOneModel().SetFilter(filter).SetReplacement(doc)
		bulkModels[i] = model
	}
	_, err = col.BulkWrite(ctx, bulkModels, options.BulkWrite().SetOrdered(false))
	if err != nil {
		_, errResp := getBulkError(err)
		return dataLen - len(errResp.Elements), errResp
	}
	return dataLen, nil
}

func (m *Mongo) getFilterByIndexKeys(indexKeys []string, data any) (bson.D, bson.D) {
	conds := make(database.C, len(indexKeys))
	hasAnonymous := reflect.ValueOf(data).Elem().Type().Field(0).Anonymous
	doc := transformDocument(data, "", hasAnonymous)
	docMap := make(map[string]bson.E)
	for _, v := range doc {
		docMap[v.Key] = v
	}
	for index, key := range indexKeys {
		if docValue, ok := docMap[key]; ok {
			conds[index] = database.CE{
				Key:   key,
				Value: docValue,
			}
		}
	}
	filter := getCondition(m.project, conds)
	return filter, doc
}

func getBulkError(err error) (ok bool, errorBulk *database.BulkError) {
	var bulkErr mongo.BulkWriteException
	errorBulk = &database.BulkError{
		Err: err,
	}
	if errors.As(err, &bulkErr) {
		errorBulk.Elements = make([]*database.BulkElement, len(bulkErr.WriteErrors))
		for i, v := range bulkErr.WriteErrors {
			errorBulk.Elements[i] = &database.BulkElement{
				Index:   v.Index,
				Message: v.Message,
			}
		}
		ok = true
	}
	return
}

// Delete delete data
func (m *Mongo) Delete(ctx context.Context, table string, conds database.C) (int, error) {
	col := m.Collection(table)
	filter := getCondition(m.project, conds)
	ret, err := col.DeleteMany(ctx, filter)
	if err != nil {
		return 0, convertToStatusError(table, err)
	}
	return int(ret.DeletedCount), err
}

// DeleteOne delete one
func (m *Mongo) DeleteOne(ctx context.Context, table string, conds database.C) (int, error) {
	col := m.Collection(table)
	filter := getCondition(m.project, conds)
	ret, err := col.DeleteOne(ctx, filter)
	if err != nil {
		return 0, convertToStatusError(table, err)
	}
	return int(ret.DeletedCount), err
}

// Find data.
func (m *Mongo) Find(ctx context.Context, table string, conds database.C, order []string,
	limit int, arryPtr any,
) error {
	valuePtr := reflect.ValueOf(arryPtr)
	value := valuePtr.Elem()
	eleType := reflect.TypeOf(arryPtr).Elem().Elem().Elem()
	rows, err := m.findRows(ctx, table, conds, order, limit, eleType)
	if err != nil {
		return convertToStatusError(table, err)
	}
	defer rows.Close()
	for rows.Next() {
		rowData, err := rows.Decode()
		if err != nil {
			return convertToStatusError(table, err)
		}
		value = reflect.Append(value, reflect.ValueOf(rowData))
	}
	valuePtr.Elem().Set(value)
	return nil
}

// FindOne find one
func (m *Mongo) FindOne(ctx context.Context, table string, conds database.C, data any) error {
	col := m.Collection(table)
	filter := getCondition(m.project, conds)
	result := col.FindOne(ctx, filter)
	err := result.Err()
	if err != nil {
		return convertToStatusError(table, err)
	}
	err = result.Decode(data)
	if err != nil {
		if IsNotFoundError(err) {
			return status.Errorf(codes.NotFound, "%s not found", table)
		}
		return status.Errorf(codes.Internal, "db error %s", err)
	}
	// if first element is anonymous
	firstField, newValue, isPointer, ok := isFirstFieldAnonymous(data)
	if ok {
		err = result.Decode(newValue.Interface())
		if err != nil {
			return status.Errorf(codes.Internal, "db decode error %s", err)
		}
		if isPointer {
			firstField.Set(newValue)
		} else {
			firstField.Set(newValue.Elem())
		}
	}
	return nil
}

// FindRows find rows
func (m *Mongo) FindRows(ctx context.Context, table string, conds database.C, sortBy []string, limit int,
	data any,
) (database.Row, error) {
	elemType := reflect.TypeOf(data).Elem()
	return m.findRows(ctx, table, conds, sortBy, limit, elemType)
}

// findRows find rows
func (m *Mongo) findRows(ctx context.Context, table string, conds database.C, sortBy []string, limit int,
	elemType reflect.Type,
) (database.Row, error) {
	col := m.Collection(table)
	filter := getCondition(m.project, conds)
	opts := &options.FindOptions{}
	var sortFields bson.D
	if len(sortBy) == 0 {
		sortFields = bson.D{
			bson.E{
				Key:   "_id",
				Value: -1,
			},
		}
	} else {
		sortFields = make(bson.D, len(sortBy))
		for i, v := range sortBy {
			orderValue := 1
			if strings.HasPrefix(v, "-") {
				orderValue = -1
				v = v[1:]
			}
			sortFields[i] = bson.E{
				Key:   v,
				Value: orderValue,
			}
		}
	}
	opts.SetSort(sortFields)
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}
	cur, err := col.Find(ctx, filter, opts)
	if err != nil {
		if IsNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound, "%s not found", table)
		}
		return nil, status.Errorf(codes.Internal, "db error %s", err.Error())
	}
	return &monogRow{cur: cur, ctx: ctx, dataType: elemType}, nil
}

// Exist check if cond exist
func (m *Mongo) Exist(ctx context.Context, table string, conds database.C) (bool, error) {
	col := m.Collection(table)
	filter := getCondition(m.project, conds)
	err := col.FindOne(ctx, filter).Err()
	if err != nil {
		if IsNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Count data.
func (m *Mongo) Count(ctx context.Context, table string, conds database.C) (int64, error) {
	col := m.Collection(table)
	filter := getCondition(m.project, conds)
	ret, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "count %s error %s", table, err)
	}
	return ret, nil
}

// IncrCounter incr counter
func (m *Mongo) IncrCounter(ctx context.Context, counterTable, key string, start, count int64) error {
	col := m.Collection(counterTable)
	filter := getCondition(m.project, database.C{
		{
			Key:   "key",
			Value: key,
		},
	})
	var docsNew counter
	err := col.FindOneAndUpdate(ctx, filter, bson.M{"$inc": bson.M{"count": count}}).Decode(&docsNew)
	if err != nil {
		if IsNotFoundError(err) {
			_, errInsert := col.InsertOne(ctx, counter{
				Project: m.project,
				Key:     key,
				Count:   start + count - 1,
			})
			if errInsert != nil {
				if IsConflictError(errInsert) {
					return col.FindOneAndUpdate(ctx, filter, bson.M{"$inc": bson.M{"count": count}}).Err()
				}
				return status.Errorf(codes.Internal, "insert counter error %s", errInsert)
			}
			return nil
		}
		return status.Errorf(codes.Internal, "find docs error %s", err)
	}
	return nil
}

// counter
type counter struct {
	ID      primitive.ObjectID `bson:"_id,omitempty"`
	Project string             `bson:"project"`
	Key     string             `bson:"key"`
	Count   int64              `bson:"count"`
}

// DecrCounter Decr Counter
func (m *Mongo) DecrCounter(ctx context.Context, counterTable, key string, count int64) error {
	col := m.Collection(counterTable)
	filter := getCondition(m.project, database.C{
		{
			Key:   "key",
			Value: key,
		},
	})
	var docsNew counter
	err := col.FindOneAndUpdate(ctx, filter, bson.M{"$inc": bson.M{"count": -1 * count}}).Decode(&docsNew)
	if err != nil {
		if IsNotFoundError(err) {
			return nil
		}
		return status.Errorf(codes.Internal, "find docs error %s", err)
	}
	if docsNew.Count <= 0 {
		_, err = col.DeleteOne(ctx, filter)
		return err
	}
	return nil
}

// GetCounter Decr Counter
func (m *Mongo) GetCounter(ctx context.Context, counterTable, key string) (int64, error) {
	col := m.Collection(counterTable)
	doc := counter{}
	filter := getCondition(m.project, database.C{
		{
			Key:   "key",
			Value: key,
		},
	})
	err := col.FindOne(ctx, filter).Decode(&doc)
	if err != nil {
		if IsNotFoundError(err) {
			return 0, nil
		}
		return 0, err
	}
	return doc.Count, nil
}

// StartTransaction start transaction
func (m *Mongo) StartTransaction(ctx context.Context) (database.Transaction, error) {
	session, err := m.Client.StartSession()
	if err != nil {
		return nil, err
	}
	return &sessionTransaction{
		session: session,
		ctx:     ctx,
	}, nil
}

type sessionTransaction struct {
	session mongo.Session
	ctx     context.Context
}

// Commit implements database.Transaction
func (s *sessionTransaction) Commit() error {
	err := s.session.CommitTransaction(s.ctx)
	if err != nil {
		s.session.EndSession(s.ctx)
		return err
	}
	s.session.EndSession(s.ctx)
	return nil
}

// Rollback implements database.Transaction
func (s *sessionTransaction) Rollback() error {
	err := s.session.AbortTransaction(s.ctx)
	if err != nil {
		s.session.EndSession(s.ctx)
		return err
	}
	s.session.EndSession(s.ctx)
	return nil
}

// WithTransaction with transaction
func (m *Mongo) WithTransaction(_ context.Context, tx database.Transaction) database.Database {
	sessionTx, ok := tx.(*sessionTransaction)
	if !ok {
		panic("invalid transaction type")
	}
	return &Mongo{
		Client:          m.Client,
		defaultDatabase: m.defaultDatabase,
		project:         m.project,
		session:         sessionTx,
	}
}

const fieldProject = "project"

func getCondition(projectID string, conds database.C) bson.D {
	var cond bson.D
	if projectID != "" {
		cond = append(cond, bson.E{
			Key:   fieldProject,
			Value: projectID,
		})
	}
	for _, v := range conds {
		key := fixQueryKey(v.Key)
		value := v.Value
		if v.C == database.In {
			value = bson.D{{Key: "$in", Value: value}}
		} else if v.C == database.Nin {
			value = bson.D{{Key: "$nin", Value: value}}
		} else if v.C == database.Ne {
			value = bson.D{{Key: "$ne", Value: value}}
		}
		cond = append(cond, bson.E{
			Key:   key,
			Value: value,
		})
	}
	return cond
}

func convertDocs(src database.D) bson.D {
	result := make(bson.D, len(src))
	for i, v := range src {
		// {"-groups[*].id": "role"} means remove the groups if groups[*].id = role
		if strings.HasPrefix(v.Key, "-") {
			result[i].Key = "$pull"
			dot := strings.Index(v.Key, ".")
			subArrayIndex := strings.Index(v.Key, "[")
			if dot < 0 || subArrayIndex < 0 {
				log.Printf("can not conver to %s to -key[*].subKey pattern", v.Key)
				continue
			}
			jsonKey := v.Key[1:subArrayIndex]
			jsonSubKey := v.Key[dot+1:]
			result[i].Value = bson.D{{
				Key: jsonKey,
				Value: bson.D{{
					Key:   jsonSubKey,
					Value: v.Value,
				}},
			}}
			continue
		}
		result[i].Key = v.Key
		valueType := reflect.TypeOf(v.Value)
		if valueType == timestampPtrType {
			result[i].Value = v.Value.(*timestamppb.Timestamp).AsTime()
		} else {
			result[i].Value = v.Value
		}
	}
	return result
}

var timestampPtrType = reflect.TypeOf(&timestamppb.Timestamp{})

type monogRow struct {
	cur      *mongo.Cursor
	ctx      context.Context
	dataType reflect.Type
}

// Close implements database.Row
func (m *monogRow) Close() error {
	return m.cur.Close(m.ctx)
}

// Decode implements database.Row
func (m *monogRow) Decode() (any, error) {
	data := reflect.New(m.dataType).Interface()
	err := m.cur.Decode(data)
	return data, err
}

// Next implements database.Row
func (m *monogRow) Next() bool {
	return m.cur.Next(m.ctx)
}

//	 mongodb query array
//	 1. data[*].test=1 or data.test=1 or data[*]=1
//		 2. data[1].test=1
func fixQueryKey(key string) string {
	leftSquareBracket := strings.Index(key, "[")
	if leftSquareBracket > 0 && len(key)-leftSquareBracket > 2 {
		rightSquareBracket := strings.Index(key, "]")
		if rightSquareBracket > 0 && rightSquareBracket <= len(key)-1 {
			bracketValue := key[leftSquareBracket+1 : rightSquareBracket]
			remaining := key[rightSquareBracket+1:]
			if bracketValue == "*" {
				key = key[:leftSquareBracket]
			} else {
				key = key[:leftSquareBracket] + "." + bracketValue
			}
			if remaining != "" {
				key += remaining
			}
		}
	}
	return key
}
