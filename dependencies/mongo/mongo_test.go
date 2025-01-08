package mongo

import (
	"context"
	"net/url"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// TestMongo test database json mapping problem
func TestMongo(t *testing.T) {
	type TestModel struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		TestName string `json:"test_name"`
	}
	u, _ := url.Parse("mongodb://127.0.0.1:27017/test")
	mgo := &Mongo{}
	ctx, cc := context.WithTimeout(context.Background(), 3*time.Second)
	defer cc()
	err := mgo.Init(ctx, u)
	if err != nil {
		// The mongodb is not init, just ignore
		return
	}
	value := "test"
	col := mgo.Collection(value)
	_ = col.Drop(ctx)
	data := &TestModel{
		ID:       value,
		Name:     value,
		TestName: value,
	}
	_, err = col.InsertOne(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	ret := col.FindOne(ctx, bson.D{{Key: "id", Value: "test"}})
	if ret.Err() != nil {
		t.Fatal(ret.Err())
	}
	var dataOrg TestModel
	err = ret.Decode(&dataOrg)
	if err != nil {
		t.Fatal(ret.Err())
	}
	if dataOrg.Name != value || dataOrg.TestName != value {
		t.Fatal("test data does not match")
	}
	_ = col.Drop(ctx)
}
