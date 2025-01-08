// Package mongo provide mongo utils
package mongo

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"time"

	"github.com/ti/common-go/dependencies/mongo/codecs"

	"github.com/ti/common-go/dependencies/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Mongo the mongo client
type Mongo struct {
	*mongo.Client
	defaultDatabase string
	project         string
	session         *sessionTransaction
}

func init() {
	database.RegisterImplements("mongodb", func(ctx context.Context, u *url.URL) (database.Database, error) {
		m := &Mongo{}
		return m, m.Init(ctx, u)
	})
}

// New client
func New(ctx context.Context, uri string) (*Mongo, error) {
	m := &Mongo{}
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	return m, m.Init(ctx, u)
}

// Init the mongo client
func (m *Mongo) Init(ctx context.Context, u *url.URL) error {
	uri := u.String()
	opts := options.Client().
		ApplyURI(uri).
		SetConnectTimeout(5 * time.Second).
		SetRegistry(codecs.DefaultRegistry).
		SetBSONOptions(&options.BSONOptions{
			UseJSONStructTags:       true,
			ErrorOnInlineDuplicates: true,
			NilMapAsEmpty:           false,
			NilSliceAsEmpty:         true,
			NilByteSliceAsEmpty:     true,
			OmitZeroStruct:          false,
			StringifyMapKeysWithFmt: false,
		})
	mongoClient, err := mongo.Connect(ctx, opts)
	if err != nil {
		return errors.New("can not dial mongo " + uri + " - " + err.Error())
	}
	if u.Path == "" {
		err = errors.New("default database not set in mongo uri")
		return err
	}
	if len(u.Path) > 1 {
		defaultDatabase := u.Path[1:]
		err = mongoClient.Database(defaultDatabase).RunCommand(ctx, bson.M{"ping": 1}).Err()
		if err != nil {
			return errors.New("can not ping mongo " + uri + " - " + err.Error())
		}
		if defaultDatabase == "" {
			err = errors.New("mongo default database not set in uri")
			return err
		}
		m.defaultDatabase = defaultDatabase
	}
	m.Client = mongoClient
	return nil
}

// transformDocument transform any object to bson documents
func (m *Mongo) transformData(val any, forceDoc bool) any {
	hasAnonymous := reflect.ValueOf(val).Elem().Type().Field(0).Anonymous
	if !forceDoc && !hasAnonymous && m.project == "" {
		return val
	}
	return transformDocument(val, m.project, hasAnonymous)
}

// isFirstFieldAnonymous check if interface has anonymous filed, just support one filed
// for performance considerations.
func isFirstFieldAnonymous(val any) (firstField, newValue reflect.Value, isPointer, ok bool) {
	v := reflect.ValueOf(val).Elem()
	t := v.Type()
	sf := t.Field(0)
	ok = sf.Anonymous
	if !ok {
		return
	}
	firstField = v.Field(0)
	if firstField.Kind() == reflect.Ptr {
		newValue = reflect.New(sf.Type.Elem())
		isPointer = true
	} else {
		newValue = reflect.New(sf.Type)
	}
	return
}

// transformDocument transform any object to bson documents
func transformDocument(val any, project string, hasAnonymous bool) bson.D {
	dis, err := codecs.EncodeToDocument(val)
	if err != nil {
		panic(err)
	}
	if hasAnonymous {
		disOther := dis[1:]
		dis = dis[0].Value.(bson.D)
		dis = append(dis, disOther...)
	}
	if project != "" {
		dis = append(bson.D{{Key: "project", Value: project}}, dis...)
	}
	return dis
}

// Collection get Collection
func (m *Mongo) Collection(colName string) *mongo.Collection {
	return m.Database(m.defaultDatabase).Collection(colName)
}

// DefaultDatabase gets the default database
func (m *Mongo) DefaultDatabase() *mongo.Database {
	return m.Database(m.defaultDatabase)
}

// Close database.
func (m *Mongo) Close(ctx context.Context) error {
	err := m.Client.Disconnect(ctx)
	m.Client = nil
	return err
}

var (
	databaseDocType = reflect.TypeOf(database.D{})
	databaseMapType = reflect.TypeOf(map[string]any{})
)
