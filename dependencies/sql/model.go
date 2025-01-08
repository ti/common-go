package sql

import (
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/ti/common-go/dependencies/database"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GenerateScheme gen scheme
func GenerateScheme(table string, data any) string {
	scheme := fmt.Sprintf("create table %s \n("+
		"\t`_id`\t\tbigint unsigned auto_increment,\n"+
		"\t`project`\tCHAR(64)\tnot null,\n", table)
	docs := TransformScheme(data)
	for _, v := range docs {
		scheme += fmt.Sprintf("\t`%s`\t\t%s,\n", v.Key, v.Value)
	}
	scheme += fmt.Sprintf("\tKEY(`project`),\n\tconstraint %s_pk primary key (_id)\n);", table)
	return scheme
}

// GenerateDBScheme gen scheme
func GenerateDBScheme(database string) string {
	return fmt.Sprintf("CREATE DATABASE IF NOT EXISTS " + database + ";")
}

// GenerateIndexScheme gen scheme
func GenerateIndexScheme(table string, field []string, unique, reverOrder bool) string {
	indexName := strings.Join(field, "_")
	if i := strings.Index(indexName, "["); i > 0 {
		indexName = indexName[0:i] + indexName[i+3:]
	}
	for i, v := range field {
		if strings.Contains(v, "[") {
			field[i] = generateArrayIndexScheme(v)
		} else if strings.Contains(v, ".") {
			field[i] = generateJSONKeyIndexScheme(v)
		} else {
			field[i] = "`" + v + "`"
		}
	}
	indexValue := strings.Join(field, ",")
	if reverOrder {
		indexValue += " DESC"
	}
	tpl := "CREATE%s INDEX `%s` ON %s (%s);"
	uniqueArgs := " UNIQUE"
	if !unique {
		uniqueArgs = ""
	}
	return fmt.Sprintf(tpl, uniqueArgs, indexName, table, indexValue)
}

// generateArrayIndexScheme generate array index scheme, for exp: data[*].id
func generateArrayIndexScheme(field string) string {
	dot := strings.Index(field, ".")
	subArrayIndex := strings.Index(field, "[")
	if dot < 0 || subArrayIndex < 0 {
		return ""
	}
	jsonKey := field[0:subArrayIndex]
	jsonSubKey := field[dot+1:]
	// for an exp: `CREATE INDEX myidx ON test.account ( (CAST(`groups`->'$[*].id' AS CHAR(64) ARRAY)) );`
	// in mysql: `SELECT * FROM test.account WHERE 'role' MEMBER OF(`groups`->'$[*].id');`
	// in simple way: `SELECT * FROM  test.account WHERE JSON_CONTAINS(`groups`->'$[*].id', CAST('["role"]' AS JSON));`
	// `ALTER TABLE test.account ADD groups_ids json GENERATED`
	// ALWAYS AS ( CAST(`groups`->'$[*].id' AS CHAR(64) ARRAY) ) VIRTUAL;
	// EXPLAIN SELECT * FROM test.account WHERE  `project` = 'xbase' AND 'role' MEMBER OF(`groups`->'$[*].id');
	return fmt.Sprintf("(cast(json_extract(`%s`, '$[*].%s') as char(64) array))", jsonKey, jsonSubKey)
}

// generateJSONKeyIndexScheme generate array index scheme
func generateJSONKeyIndexScheme(field string) string {
	fields := strings.Split(field, ".")
	// mysql not support unique index
	if len(fields) != 2 {
		return ""
	}
	// EXPLAIN SELECT * from auth.account WHERE `groups`->'$.id' = '222';
	return fmt.Sprintf("(JSON_VALUE(`%s`, '$.%s' RETURNING CHAR(64)))", fields[0], fields[1])
}

// GenerateAutoExpScheme gen scheme
func GenerateAutoExpScheme(dbName, table, expiriedAtField string) string {
	query := GenerateIndexScheme(table, []string{expiriedAtField}, false, false)
	query += "\n"
	tpl := `CREATE EVENT IF NOT EXISTS exp_%s_%s
	ON SCHEDULE
	EVERY 1 DAY
	DO
	BEGIN
	DELETE FROM %s.%s WHERE %s < NOW();
	END;`
	query += fmt.Sprintf(tpl, dbName, table, dbName, table, expiriedAtField)
	return query
}

// TransformScheme transform any object to sql scheme
func TransformScheme(ptrVal any) database.D {
	v := reflect.ValueOf(ptrVal).Elem()
	t := v.Type()
	var docs database.D
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		sfv := v.Field(i)
		if sf.Anonymous {
			sv := sfv.Interface()
			if sfv.IsNil() {
				sv = reflect.New(reflect.TypeOf(sv).Elem()).Interface()
			}
			docs = append(docs, TransformScheme(sv)...)
			continue
		}
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = strings.ToLower(sf.Name)
		} else {
			tag, _, _ = strings.Cut(tag, ",")
		}
		ft := sf.Type
		e := database.E{
			Key: tag,
		}
		switch ft.Kind() {
		case reflect.Interface, reflect.Pointer, reflect.Array, reflect.Map, reflect.Struct, reflect.Slice:
			if ft == timestampPtrType || ft == timeType {
				e.Value = "datetime(6)"
			} else {
				e.Value = "json"
			}
		case reflect.Bool:
			e.Value = "bool"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8,
			reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			e.Value = "int"
		case reflect.Float32, reflect.Float64:
			e.Value = "float"
		case reflect.String:
			e.Value = "CHAR(64)"
		default:
			e.Value = ft.Kind()
		}
		docs = append(docs, e)
	}
	return docs
}

func convertDocsToSet(scheme string, d database.D) (query string, args []any) {
	var cond string
	for i, v := range d {
		if i > 0 {
			cond = ", "
		}
		if v.Value == nil {
			query += fmt.Sprintf("%s`%s` = ? ", cond, v.Key)
			args = append(args, v.Value)
			continue
		}
		// add: set -key[*].subKey='test'
		if strings.HasPrefix(v.Key, "-") {
			dot := strings.Index(v.Key, ".")
			subArrayIndex := strings.Index(v.Key, "[")
			if dot < 0 || subArrayIndex < 0 {
				log.Printf("can not conver to %s to -key[*].subKey pattern", v.Key)
				continue
			}
			jsonKey := v.Key[1:subArrayIndex]
			jsonSubKey := v.Key[dot+1:]
			v.Key = jsonKey
			// for exp: UPDATE test.account SET `groups` = JSON_REMOVE(`groups`,
			// JSON_UNQUOTE(JSON_SEARCH(JSON_EXTRACT(`groups` ,'$[*].id'), 'one', '2333'))) WHERE `sub` = 'xx_1';
			v.Value = fmt.Sprintf("JSON_REMOVE(`%s`, JSON_UNQUOTE(JSON_SEARCH(JSON_EXTRACT(`%s` ,'$[*].%s'), 'one', '%s')))",
				jsonKey, jsonKey, jsonSubKey, v.Value)
			query += fmt.Sprintf("%s`%s` = %s ", cond, jsonKey, v.Value)
			continue
		}
		sf := reflect.ValueOf(v.Value)
		if !jsonKinds[sf.Kind()] {
			query += fmt.Sprintf("%s`%s` = ? ", cond, v.Key)
			args = append(args, v.Value)
			continue
		}
		ft := sf.Type()
		if ft == timeType {
			query += fmt.Sprintf("%s`%s` = ? ", cond, v.Key)
			args = append(args, v.Value)
			continue
		}
		if ft == timestampPtrType {
			ts := v.Value.(*timestamppb.Timestamp)
			d[i].Value = time.Unix(ts.Seconds, 0)
			query += fmt.Sprintf("%s`%s` = ? ", cond, v.Key)
			args = append(args, d[i].Value)
			continue
		}
		jsonBytes, err := marshal(scheme, v.Value)
		if err != nil {
			log.Printf("can not conver to %s to json", v.Key)
			continue
		}
		d[i].Value = string(jsonBytes)
		query += fmt.Sprintf("%s`%s` = ? ", cond, v.Key)
		args = append(args, d[i].Value)
	}
	return
}

func tidySQLConds(conds database.C, compactMode bool) (query string, args []any) {
	for i, v := range conds {
		condQuery := tidySQLCond(v, compactMode)
		if i > 0 {
			query += queryAnd
		}
		query += condQuery + " "
		if v.C == database.In || v.C == database.Nin {
			data := reflect.ValueOf(v.Value)
			for i := 0; i < data.Len(); i++ {
				args = append(args, data.Index(i).Interface())
			}
		} else {
			args = append(args, v.Value)
		}
	}
	return
}

// tidySQLConn support test=1 or test.id=1 or test[*].id = 1
func tidySQLCond(cond database.CE, compactMode bool) string {
	key := cond.Key
	i := strings.Index(key, ".")
	condition := conditionMap[cond.C]
	if i <= 0 {
		if cond.C == database.In || cond.C == database.Nin {
			data := reflect.ValueOf(cond.Value)
			n := data.Len()
			holder := "?"
			for j := 1; j < n; j++ {
				holder += ",?"
			}
			return fmt.Sprintf("`%s` %s (%s)", key, condition, holder)
		}
		return fmt.Sprintf("`%s` %s ?", key, condition)
	}
	jsonKey := key[0:i]
	jsonSubKey := key[i+1:]
	subArrayIndex := strings.Index(jsonKey, "[")
	if subArrayIndex < 0 {
		if compactMode {
			return fmt.Sprintf("JSON_SEARCH(json_extract(`%s`,'$.%s'), 'one', ?) is not null", jsonKey, jsonSubKey)
		}
		return fmt.Sprintf("`%s`->'$.%s' %s ?", jsonKey, jsonSubKey, condition)
	}
	jsonKey = jsonKey[0:subArrayIndex]
	queryDot := "."
	if jsonSubKey == "" {
		queryDot = ""
	}
	if compactMode {
		if queryDot == "" {
			return fmt.Sprintf("JSON_CONTAINS(`%s`, '?', '$')", jsonKey)
		}
		// same as MariaDB: JSON_CONTAINS(json_extract(`groups`,'$[*].id'), '"?"', '$');
		// same as Mysql: JSON_CONTAINS(`groups`->'$[*].id', '"?"');
		return fmt.Sprintf("JSON_SEARCH(json_extract(`%s`,'$[*]%s%s'), 'one', ?) is not null", jsonKey, queryDot, jsonSubKey)
	}
	return fmt.Sprintf("? MEMBER OF(`%s`->'$[*]%s%s')", jsonKey, queryDot, jsonSubKey)
}

var jsonKinds = map[reflect.Kind]bool{
	reflect.Array:     true,
	reflect.Slice:     true,
	reflect.Interface: true,
	reflect.Pointer:   true,
	reflect.Map:       true,
	reflect.Struct:    true,
}

var conditionMap = map[database.Condition]string{
	database.Eq:  "=",
	database.Ne:  "!=",
	database.Lt:  "<",
	database.Lte: "<=",
	database.Gt:  ">",
	database.Gte: ">=",
	database.In:  "IN",
	database.Nin: "NOT IN",
}
