package postgres

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/lib/pq"
)

// ConvertSQL convert sql to pgsql
func ConvertSQL(query string) string {
	query = strings.ReplaceAll(query, "`", `"`)
	query = replaceQuestionMarks(query)
	return query
}

// ScanArrayToJSON convert postgres array to JSON
func ScanArrayToJSON(src string) (string, error) {
	var data []string
	err := pq.Array(&data).Scan([]byte(src))
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func replaceQuestionMarks(sql string) string {
	re := regexp.MustCompile(`\?`)
	count := 0
	// 替换 ? 为 $n，其中 n 为递增的数字
	sql = re.ReplaceAllStringFunc(sql, func(match string) string {
		count++
		return "$" + strconv.Itoa(count)
	})
	return sql
}
