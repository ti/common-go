package main

import (
	"context"
	"time"

	"github.com/ti/common-go/dependencies/database"
	_ "github.com/ti/common-go/dependencies/mongo"
	_ "github.com/ti/common-go/dependencies/sql"

	_ "github.com/lib/pq"
	pb "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"
)

func main() {
	addr := "postgres://postgres:passwd@127.0.0.1:5432/postgres?sslmode=disable"
	ctx := context.TODO()
	cli, err := database.New(ctx, addr)
	if err != nil {
		panic(err)
	}
	_ = cli

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data := &pb.Request{
		Name: "test",
	}
	const table = "test"
	db, _ := cli.GetDatabase(ctx, "xbase")
	err = db.InsertOne(ctx, table, data)
	if err != nil {
		panic(err)
	}
	var newData pb.Request
	err = db.FindOne(ctx, table, database.C{
		{
			Key: "name",
		},
	}, &newData)
	if err != nil {
		panic(err)
	}
}
