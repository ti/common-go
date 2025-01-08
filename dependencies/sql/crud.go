package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/ti/common-go/dependencies/mongo/codecs"

	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/ti/common-go/dependencies/sql/adapters/mysql"
	"github.com/ti/common-go/dependencies/sql/adapters/postgres"

	"github.com/ti/common-go/dependencies/database"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	queryProject      = "project = ? AND "
	queryAnd          = "AND "
	queryKey          = "`key` = ?"
	queryAppendValues = ") VALUES ("
	queryFieldProject = "`project`,"
	limit1            = " LIMIT 1"
	lenLayoutDateTime = len(time.DateTime)
)

var (
	timeType         = reflect.TypeOf(time.Time{})
	timestampPtrType = reflect.TypeOf(&timestamppb.Timestamp{})
	boolPtrType      = reflect.TypeOf(&wrapperspb.BoolValue{})
)

// InsertOne insert one data
func (s *SQL) InsertOne(ctx context.Context, table string, data any) (err error) {
	query := fmt.Sprintf("INSERT INTO `%s` (", table)
	if s.project != "" {
		query += queryFieldProject
	}
	querys, args := TransformSQLArgs(s.scheme, data, false, s.loc)
	query += strings.Join(querys, ",")
	query += queryAppendValues
	if s.project != "" {
		query += fmt.Sprintf("'%s',", s.project)
	}
	for i := 0; i < len(args); i++ {
		if i > 0 {
			query += ","
		}
		query += "?"
	}
	query += `)`
	_, err = s.ExecQuery(ctx, query, args...)
	return
}

// Insert  single data or slice
func (s *SQL) Insert(ctx context.Context, table string, docs any) (count int, err error) {
	data := reflect.ValueOf(docs)
	isSlice := data.Kind() == reflect.Slice
	if !isSlice {
		err = s.InsertOne(ctx, table, docs)
		count = 1
		return
	}
	if data.Len() == 0 {
		return 0, errors.New("no insert data found")
	}
	querys, args := TransformSQLArgs(s.scheme, data.Index(0).Interface(),
		false, s.loc)
	query := fmt.Sprintf("INSERT INTO `%s` (", table)
	if s.project != "" {
		query += queryFieldProject
	}
	query += strings.Join(querys, ",")
	query += ") VALUES "
	queryValues := "("
	if s.project != "" {
		queryValues += fmt.Sprintf(`'%s',`, s.project)
	}
	for i := 0; i < len(args); i++ {
		if i > 0 {
			queryValues += ","
		}
		queryValues += "?"
	}
	queryValues += `)`
	query += queryValues
	for i := 1; i < data.Len(); i++ {
		_, dataArgs := TransformSQLArgs(s.scheme, data.Index(i).Interface(),
			false, s.loc)
		args = append(args, dataArgs...)
		query += "," + queryValues
	}
	return s.ExecQuery(ctx, query, args...)
}

var (
	databaseDocType = reflect.TypeOf(database.D{})
	databaseMapType = reflect.TypeOf(map[string]any{})
)

// Update update all matched data
func (s *SQL) Update(ctx context.Context, table string, conds database.C, d any) (int, error) {
	return s.update(ctx, table, conds, d, false)
}

// Update update all matched data
func (s *SQL) update(ctx context.Context, table string, conds database.C, d any,
	keepEmpty bool,
) (int, error) {
	query := fmt.Sprintf("UPDATE `%s` SET ", table)
	var doc database.D
	if reflect.TypeOf(d) == databaseDocType {
		doc = d.(database.D)
	} else if reflect.TypeOf(d) == databaseMapType {
		for k, v := range d.(map[string]any) {
			doc = append(doc, database.E{
				Key:   k,
				Value: v,
			})
		}
	} else {
		doc = TransformDocument(s.scheme, d, keepEmpty)
	}
	querySet, args := convertDocsToSet(s.scheme, doc)
	query += querySet
	query += "WHERE "
	if s.project != "" {
		query += fmt.Sprintf("`project` = '%s' ", s.project)
	}
	conQuery, conArgs := tidySQLConds(conds, s.compactMode)
	if conQuery != "" {
		if s.project != "" {
			query += queryAnd
		}
		query += conQuery
	}
	args = append(args, conArgs...)
	return s.ExecQuery(ctx, query, args...)
}

// UpdateOne update one data
func (s *SQL) UpdateOne(ctx context.Context, table string, conds database.C, data any) (count int, err error) {
	count, err = s.update(ctx, table, conds, data, false)
	if err == nil && s.updateDifferent && count == 0 {
		err = status.Errorf(codes.NotFound, "condition %s not found", conds)
	}
	return
}

// ReplaceOne replace one data
func (s *SQL) ReplaceOne(ctx context.Context, table string, conds database.C, data any) (count int, err error) {
	count, err = s.update(ctx, table, conds, data, true)
	if err == nil && s.updateDifferent && count == 0 {
		err = s.InsertOne(ctx, table, data)
		if err != nil {
			if status.Code(err) != codes.AlreadyExists {
				return
			}
			err = nil
		}
		count = 1
	}
	return
}

// Replace update data in bulk
func (s *SQL) Replace(ctx context.Context, table string, indexKeys []string, docs any) (count int, err error) {
	data := reflect.ValueOf(docs)
	indexKeysMap := make(map[string]int)
	for i, v := range indexKeys {
		indexKeysMap[v] = i
	}
	isSlice := data.Kind() == reflect.Slice
	if !isSlice {
		conds, doc := s.getFilterByIndexKeys(indexKeysMap, docs)
		_, err = s.update(ctx, table, conds, doc, true)
		if err != nil {
			return 0, err
		}
		return 1, nil
	}
	errResp := &database.BulkError{}
	dataLen := data.Len()
	if dataLen == 0 {
		return 0, errors.New("no insert data found")
	}
	for i := 0; i < dataLen; i++ {
		conds, doc := s.getFilterByIndexKeys(indexKeysMap, data.Index(i).Interface())
		// TODO: performance optimization
		_, err = s.update(ctx, table, conds, doc, true)
		if err != nil {
			errResp.Elements = append(errResp.Elements, &database.BulkElement{
				Index:   i,
				Message: err.Error(),
			})
		} else {
			count++
		}
	}
	if len(errResp.Elements) > 0 {
		return count, errResp
	}
	return
}

func (s *SQL) getFilterByIndexKeys(indexKeysMap map[string]int, data any) (database.C, database.D) {
	doc := TransformDocument(s.scheme, data, true)
	indexValues := make([]any, len(indexKeysMap))
	conds := make(database.C, len(indexKeysMap))
	for _, field := range doc {
		if iDoc, ok := indexKeysMap[field.Key]; ok {
			indexValues[iDoc] = field.Value
		}
	}
	return conds, doc
}

// FindOne find one data
func (s *SQL) FindOne(ctx context.Context, table string, conds database.C, data any) error {
	exs, querys, args := TransformSQLDocument(data, true, map[string]bool{})
	query := "SELECT " + strings.Join(querys, ",")
	query += fmt.Sprintf(" FROM `%s` WHERE ", table)
	if s.project != "" {
		query += fmt.Sprintf("`project` = '%s' ", s.project)
	}
	conQuery, conArgs := tidySQLConds(conds, s.compactMode)
	if conQuery != "" {
		if s.project != "" {
			query += queryAnd
		}
		query += conQuery
	}
	query += limit1
	row := s.QueryRowContext(ctx, query, conArgs...)
	if err := row.Err(); err != nil {
		return convertSQLError(s.scheme, err)
	}
	convertNullScanner(args)
	if err := row.Scan(args...); err != nil {
		return convertSQLError(s.scheme, err)
	}

	return decodeExs(s.scheme, exs, s.loc)
}

// Find all data
func (s *SQL) Find(ctx context.Context, table string, conds database.C, order []string, limit int, arryPtr any) error {
	valuePtr := reflect.ValueOf(arryPtr)
	value := valuePtr.Elem()
	eleType := reflect.TypeOf(arryPtr).Elem().Elem().Elem()
	rows, err := s.findRows(ctx, table, conds, order, limit, eleType)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		rowData, err := rows.Decode()
		if err != nil {
			return err
		}
		value = reflect.Append(value, reflect.ValueOf(rowData))
	}
	valuePtr.Elem().Set(value)
	return nil
}

// Count the data
func (s *SQL) Count(ctx context.Context, table string, conds database.C) (int64, error) {
	query := "SELECT count(*)"
	query += fmt.Sprintf(" FROM `%s` ", table)
	var whereQuery string
	if s.project != "" {
		whereQuery += fmt.Sprintf("`project` = '%s' ", s.project)
	}
	conQuery, conArgs := tidySQLConds(conds, s.compactMode)
	if conQuery != "" {
		if s.project != "" {
			whereQuery += queryAnd
		}
		whereQuery += conQuery
	}
	if whereQuery != "" {
		query += "WHERE " + whereQuery
	}
	var count int64
	err := s.QueryRowContext(ctx, query, conArgs...).Scan(&count)
	if err != nil {
		return 0, convertSQLError(s.scheme, err)
	}
	return count, nil
}

// FindRows find rows data
func (s *SQL) FindRows(ctx context.Context, table string, conds database.C, sortBy []string,
	limit int, data any,
) (database.Row, error) {
	eleType := reflect.TypeOf(data).Elem()
	return s.findRows(ctx, table, conds, sortBy, limit, eleType)
}

// findRows find rows data
func (s *SQL) findRows(ctx context.Context, table string, conds database.C,
	sortBy []string, limit int, eleType reflect.Type,
) (database.Row, error) {
	querys := TransformSQLQuery(reflect.New(eleType).Interface())
	query := "SELECT " + strings.Join(querys, ",")
	query += fmt.Sprintf(" FROM `%s` WHERE ", table)
	if s.project != "" {
		query += fmt.Sprintf("`project` = '%s' ", s.project)
	}
	conQuery, conArgs := tidySQLConds(conds, s.compactMode)
	if conQuery != "" {
		if s.project != "" {
			query += queryAnd
		}
		query += conQuery
	}
	if len(sortBy) > 0 {
		query += " ORDER BY " + getSortValue(sortBy[0])
		sortBy = sortBy[1:]
		for _, v := range sortBy {
			query += ", " + getSortValue(v)
		}
	}
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.QueryContext(ctx, query, conArgs...)
	if err != nil {
		return nil, convertSQLError(s.scheme, err)
	}
	if rows.Err() != nil {
		return nil, convertSQLError(s.scheme, err)
	}
	return &DataRows{
		Rows:         rows,
		scheme:       s.scheme,
		dataType:     eleType,
		selectFields: make(map[string]bool),
		timeLoc:      s.loc,
	}, nil
}

// QueryContext executes a query that returns rows, typically a SELECT.
// The args are for any placeholder parameters in the query.
func (s *SQL) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if s.scheme == schemePostgres {
		query = postgres.ConvertSQL(query)
	}
	return s.DB.QueryContext(ctx, query, args...)
}

// QueryRowContext executes a query that is expected to return at most one row.
func (s *SQL) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if s.scheme == schemePostgres {
		query = postgres.ConvertSQL(query)
	}
	return s.DB.QueryRowContext(ctx, query, args...)
}

func getSortValue(src string) string {
	orderValue := "ASC"
	if strings.HasPrefix(src, "-") {
		orderValue = "DESC"
		src = src[1:]
	}
	return fmt.Sprintf("`%s` %s", src, orderValue)
}

// Exist check if cond exist
func (s *SQL) Exist(ctx context.Context, table string, conds database.C) (bool, error) {
	query := fmt.Sprintf("SELECT `_id` FROM `%s` WHERE ", table)
	if s.project != "" {
		query += fmt.Sprintf("`project` = '%s' AND ", s.project)
	}
	conQuery, conArgs := tidySQLConds(conds, s.compactMode)
	query += conQuery
	query = fmt.Sprintf("SELECT EXISTS(%s);", query)
	var (
		row   = s.QueryRowContext(ctx, query, conArgs...)
		exist bool
		err   error
	)
	if s.scheme == schemePostgres {
		err = row.Scan(&exist)
	} else {
		var data int64
		err = row.Scan(&data)
		exist = data == 1
	}
	if err != nil {
		return false, convertSQLError(s.scheme, err)
	}
	return exist, nil
}

// Run a query by sql
func (s *SQL) Run(ctx context.Context, query string, args []any, arrayPtr any) error {
	valuePtr := reflect.ValueOf(arrayPtr)
	value := valuePtr.Elem()
	eleType := reflect.TypeOf(arrayPtr).Elem().Elem().Elem()
	sqlRows, err := s.QueryContext(ctx, query, args...)
	if err != nil {
		return convertSQLError(s.scheme, err)
	}
	if sqlRows.Err() != nil {
		return convertSQLError(s.scheme, err)
	}
	rows := &DataRows{
		Rows:         sqlRows,
		dataType:     eleType,
		scheme:       s.scheme,
		selectFields: make(map[string]bool),
		timeLoc:      s.loc,
	}
	defer rows.Close()
	for rows.Next() {
		rowData, errDecode := rows.Decode()
		if errDecode != nil {
			return errDecode
		}
		value = reflect.Append(value, reflect.ValueOf(rowData))
	}
	valuePtr.Elem().Set(value)
	return nil
}

// DataRows the data rows
type DataRows struct {
	*sql.Rows
	scheme       string
	dataType     reflect.Type
	selectFields map[string]bool
	timeLoc      *time.Location
}

// Close the data rows.
func (d *DataRows) Close() error {
	return d.Rows.Close()
}

// Decode the data rows.
func (d *DataRows) Decode() (any, error) {
	data := reflect.New(d.dataType).Interface()
	err := DecodeRow(d.scheme, d.Rows, d.selectFields, nil, d.timeLoc, data)
	return data, err
}

// DecodeWithID decode the rows with id.
func (d *DataRows) DecodeWithID() (data any, id int64, err error) {
	data = reflect.New(d.dataType).Interface()
	err = DecodeRow(d.scheme, d.Rows, d.selectFields, &id, d.timeLoc, data)
	if err != nil {
		return
	}
	return
}

func decodeExs(scheme string, exs []*EX, timeLoc *time.Location) error {
	for _, v := range exs {
		vString, ok := v.Holder.(*string)
		if !ok {
			continue
		}
		vs := *vString
		if vs == "" {
			continue
		}
		if v.IsJSON {
			sv := v.Field.Interface()
			st := reflect.TypeOf(sv)
			var newPtr reflect.Value
			if v.IsMap {
				newPtr = reflect.New(st)
			} else if v.IsSlice {
				newPtr = reflect.New(st)
				var err error
				if scheme == schemePostgres && len(vs) > 1 && vs[0] == '{' {
					eleKind := reflect.TypeOf(sv).Elem().Kind()
					if eleKind == reflect.String {
						vs, err = postgres.ScanArrayToJSON(vs)
						if err != nil {
							return fmt.Errorf("convert field %s to array error %w", st.Name(), err)
						}
					} else {
						vs = "[" + vs[1:len(vs)-1] + "]"
					}
				}
			} else {
				newPtr = reflect.New(st.Elem())
			}
			if err := codecs.Unmarshal([]byte(vs), newPtr.Interface()); err == nil {
				if v.IsMap {
					v.Field.Set(newPtr.Elem())
				} else if v.IsSlice {
					v.Field.Set(newPtr.Elem())
				} else {
					v.Field.Set(newPtr)
				}
			} else {
				return fmt.Errorf("error unmarshal %s for %w", vs, err)
			}
		} else if v.IsTime {
			t, err := parseSQLTime(vs, timeLoc)
			if err != nil || t.IsZero() {
				continue
			}
			switch v.Type {
			case timestampPtrType:
				ts := &timestamppb.Timestamp{Seconds: t.UTC().Unix()}
				v.Field.Set(reflect.ValueOf(ts))
			case timeType:
				v.Field.Set(reflect.ValueOf(t))
			default:
				return fmt.Errorf("error time type %s", v.Type)
			}
		}
	}
	return nil
}

func parseSQLTime(src string, timeLoc *time.Location) (t time.Time, err error) {
	if lenSrc := len(src); lenSrc == lenLayoutDateTime {
		t, err = time.ParseInLocation(time.DateTime, src, timeLoc)
	} else if lenSrc < lenLayoutDateTime {
		t, err = time.ParseInLocation(time.DateOnly, src, timeLoc)
	} else {
		t, err = time.Parse(time.RFC3339, src)
	}
	return
}

// DecodeRow Decode rows
func DecodeRow(scheme string, row *sql.Rows, selectFields map[string]bool, id *int64,
	timeLoc *time.Location, data any,
) error {
	exs, _, args := TransformSQLDocument(data, false, selectFields)
	if id != nil {
		args = append([]any{id}, args...)
	}
	convertNullScanner(args)
	if err := row.Scan(args...); err != nil {
		return convertSQLError(scheme, err)
	}
	return decodeExs(scheme, exs, timeLoc)
}

// Delete delete data
func (s *SQL) Delete(ctx context.Context, table string, conds database.C) (int, error) {
	query := fmt.Sprintf("DELETE FROM `%s` WHERE ", table)
	if s.project != "" {
		query += fmt.Sprintf("`project` = '%s' ", s.project)
	}
	conQuery, args := tidySQLConds(conds, s.compactMode)
	if conQuery != "" {
		if s.project != "" {
			query += queryAnd
		}
		query += conQuery
	}
	return s.ExecQuery(ctx, query, args...)
}

// ExecQuery query data
func (s *SQL) ExecQuery(ctx context.Context, query string, args ...any) (int, error) {
	var result sql.Result
	var err error
	// 兼容非mysql版本
	if s.scheme == schemePostgres {
		query = postgres.ConvertSQL(query)
	}
	if s.tx != nil {
		result, err = s.tx.ExecContext(ctx, query, args...)
	} else {
		result, err = s.ExecContext(ctx, query, args...)
	}
	if err != nil {
		return 0, convertSQLError(s.scheme, err)
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

func convertSQLError(scheme string, err error) error {
	code := codes.Unknown
	var msg string
	if errors.Is(err, context.Canceled) {
		msg = fmt.Sprintf("db %s", err.Error())
		code = codes.Canceled
	} else if errors.Is(err, sql.ErrNoRows) {
		code = codes.NotFound
	} else {
		if scheme == schemeMysql {
			return mysql.ConvertError(err)
		} else if scheme == schemePostgres {
			return postgres.ConvertError(err)
		}
	}
	return status.Error(code, msg)
}

// DeleteOne delete one data
func (s *SQL) DeleteOne(ctx context.Context, table string, conds database.C) (count int, err error) {
	return s.Delete(ctx, table, conds)
}

// IncrCounter +1 a value
func (s *SQL) IncrCounter(ctx context.Context, counterTable, key string, start int64, count int64) error {
	query := "UPDATE " + counterTable + fmt.Sprintf(" SET count = count + %d WHERE ", count)
	var args []any
	if s.project != "" {
		query += queryProject
		args = append(args, s.project)
	}
	query += queryKey
	args = append(args, key)
	ret, err := s.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	rowsAffected, _ := ret.RowsAffected()
	if rowsAffected == 0 {
		query = "INSERT INTO " + counterTable + " ("
		args = []any{}
		if s.project != "" {
			query += queryFieldProject
			args = append(args, s.project)
		}
		query += fmt.Sprintf("`key`,count) VALUES (?,?,?) ON DUPLICATE KEY UPDATE count = count + %d;", count)
		args = append(args, key, start+count-1)
		_, err = s.ExecContext(ctx, query, args...)
		if err != nil {
			return convertSQLError(s.scheme, err)
		}
		return nil
	}
	return nil
}

// DecrCounter decr the counter of a table
func (s *SQL) DecrCounter(ctx context.Context, counterTable, key string, count int64) error {
	query := "UPDATE " + counterTable + fmt.Sprintf(" SET count = count - %d WHERE ", count)
	var args []any
	if s.project != "" {
		query += queryProject
		args = append(args, s.project)
	}
	query += queryKey
	args = append(args, key)
	_, err := s.ExecContext(ctx, query, args...)
	if err != nil {
		return convertSQLError(s.scheme, err)
	}
	return nil
}

// GetCounter Decr Counter
// the counter table for example:
// ```create table counter (`_id` bigint unsigned auto_increment, `project` CHAR(64) not null, `key` CHAR(12),
// `count` int, KEY (`project`), UNIQUE KEY (`project`,`key`), constraint counter_pk primary key (_id));```
func (s *SQL) GetCounter(ctx context.Context, counterTable, key string) (int64, error) {
	query := fmt.Sprintf("SELECT count FROM `%s` WHERE ", counterTable)
	if s.project != "" {
		query += fmt.Sprintf("`project` = '%s' AND ", s.project)
	}
	query += fmt.Sprintf("`key` = '%s'", key)
	var count int64
	err := s.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, convertSQLError(s.scheme, err)
	}
	return count, err
}
