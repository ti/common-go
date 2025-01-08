// Package sql implements dependency of sql
package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ti/common-go/dependencies/sql/adapters/mysql"
	"github.com/ti/common-go/dependencies/sql/adapters/postgres"

	sqldblogger "github.com/simukti/sqldb-logger"
	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/log"
)

// SQL sql instance
type SQL struct {
	*sql.DB
	uri             *url.URL
	loc             *time.Location
	compactMode     bool
	updateDifferent bool
	logMode         string
	bustedIndex     bool
	tx              *sql.Tx
	dbName          string
	scheme          string
	project         string
}

// New sql client, exp: mysql://user:password@127.0.0.1:3306?log=info
func New(ctx context.Context, uri string) (database.Database, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	s := &SQL{}
	return s, s.Init(ctx, u)
}

const (
	schemeMysql    = "msyql"
	schemePostgres = "postgres"
)

func init() {
	impl := func(ctx context.Context, u *url.URL) (database.Database, error) {
		s := &SQL{}
		return s, s.Init(ctx, u)
	}
	database.RegisterImplements(schemeMysql, impl)
	database.RegisterImplements(schemePostgres, impl)
}

// Init Sql initialization
func (s *SQL) Init(_ context.Context, u *url.URL) error {
	// Compatible with mariadb mode. Compatible with mariadb mode, some inefficient query statements can be used.
	const compactKey = "compact"
	// bustedIndex is compatible with cases where json indexes are not supported (for mysql 5.7 or below)
	const bustedIndexKey = "bustedIndex"
	const logKey = "log"
	const updateTime = "updateTime"
	query := u.Query()
	s.compactMode = query.Has(compactKey)
	s.bustedIndex = query.Has(bustedIndexKey)
	updateTimeFiled := query.Get(updateTime)
	if updateTimeFiled != "" {
		s.updateDifferent = true
	}
	if s.compactMode || s.bustedIndex || s.logMode != "" {
		query.Del(compactKey)
		query.Del(bustedIndexKey)
		query.Del(logKey)
		query.Del(updateTime)
		u.RawQuery = query.Encode()
	}
	var err error
	s.DB, err = newSQLClient(u, s.logMode)
	if err != nil {
		return err
	}
	s.uri = u
	s.dbName = u.Path[1:]
	if s.dbName == "" {
		return nil
	}
	if loc := query.Get("loc"); loc != "" {
		s.loc, err = time.LoadLocation(loc)
		if err != nil {
			return err
		}
	} else {
		// they don't want: cosmopolitan
		// nolint: gosmopolitan // loc by default
		s.loc = time.Now().Location()
	}
	s.scheme = u.Scheme
	err = s.DB.Ping()
	if err != nil {
		err = convertError(u.Scheme, err)
		_ = s.DB.Close()
		return err
	}
	return nil
}

func convertError(scheme string, err error) error {
	if scheme == schemeMysql {
		return mysql.ConvertError(err)
	}
	if scheme == schemePostgres {
		return postgres.ConvertError(err)
	}
	return err
}

func newSQLClient(uri *url.URL, logMode string) (*sql.DB, error) {
	if uri.Scheme == schemeMysql && strings.Contains(uri.Host, ":") && !strings.Contains(uri.Host, "(") {
		uri.Host = fmt.Sprintf("tcp(%s)", uri.Host)
	}
	passwd, _ := uri.User.Password()
	dsn := fmt.Sprintf("%s:%s@%s%s?%s", uri.User.Username(), passwd, uri.Host, uri.Path, uri.RawQuery)
	if uri.Scheme != schemeMysql {
		dsn = uri.Scheme + "://" + dsn
	}
	db, err := sql.Open(uri.Scheme, dsn)
	if err != nil {
		return nil, err
	}
	if logMode == "" {
		return db, nil
	}
	logLevelMap := map[string]sqldblogger.Level{
		"debug": sqldblogger.LevelDebug,
		"info":  sqldblogger.LevelInfo,
		"error": sqldblogger.LevelError,
		"true":  sqldblogger.LevelInfo,
	}
	logLevel, ok := logLevelMap[logMode]
	if !ok {
		return nil, fmt.Errorf("log mode %s does not supported", logMode)
	}
	return sqldblogger.OpenDriver(dsn, db.Driver(), &sqlLogger{
		Level: logLevel,
	}), nil
}

type sqlLogger struct {
	Level sqldblogger.Level
}

// Log mysql by level
func (s sqlLogger) Log(ctx context.Context, level sqldblogger.Level, msg string, data map[string]interface{}) {
	if level < s.Level {
		return
	}
	var params string
	if len(data) > 0 {
		paramsBytes, _ := json.Marshal(data)
		params = string(paramsBytes)
	}
	log.Extract(ctx).Action("sql." + msg).Info(params)
}

// Close Sql close
func (s *SQL) Close(_ context.Context) error {
	return s.DB.Close()
}

// EnsureIndex ensures index creation
func (s *SQL) EnsureIndex(ctx context.Context, table string, field []string, unique, reverOrder bool) error {
	query := GenerateIndexScheme(table, field, unique, reverOrder)
	_, err := s.ExecContext(ctx, query)
	return err
}

// EnsureDatabase ensures that the database has been created
func (s *SQL) EnsureDatabase(ctx context.Context) error {
	s.uri.Path = "/"
	sqlDB, err := newSQLClient(s.uri, s.logMode)
	if err != nil {
		return err
	}
	_, err = sqlDB.ExecContext(ctx, GenerateDBScheme(s.dbName))
	if err != nil {
		return err
	}
	return err
}

// AutoExpirieData Automatic data expiration
func (s *SQL) AutoExpirieData(ctx context.Context, table, expiriedAtField string) error {
	query := GenerateAutoExpScheme(s.dbName, table, expiriedAtField)
	_, err := s.ExecContext(ctx, query)
	return err
}

// GetDatabase get client instance by project id.
func (s *SQL) GetDatabase(_ context.Context, project string) (database.Database, error) {
	return &SQL{
		DB:          s.DB,
		loc:         s.loc,
		uri:         s.uri,
		compactMode: s.compactMode,
		bustedIndex: s.bustedIndex,
		tx:          s.tx,
		dbName:      s.dbName,
		scheme:      s.scheme,
		project:     project,
	}, nil
}

// TODO: bustedIndex the bustedIndex mode for the mysql 5.6 or lower version
// for exp query the array:
// query providers[*].id = test:qq
// the query is: select _parent_id from _user_providers where the id = 'test:qq' limit 1;
// if the limit is not set: select distict(_parent_id) from _user_providers where the id = 'test:qq';
// query providers[*] = test
// the query is: select _parent_id from _user_providers where the _value = 'test'
// or: select distict(_parent_id) from _user_providers where the _value = 'test'
// for exp query the json map:
// query providers.id = test
// select *from users where the _providers_id = 'test' limit 1;
