package service

import (
	"github.com/ti/common-go/dependencies"
	dephttp "github.com/ti/common-go/dependencies/http"
	"github.com/ti/common-go/dependencies/sql"
	depgrpc "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"
)

// Dependencies depend on the main structure
type Dependencies struct {
	dependencies.Dependency
	DB       *sql.SQL          `required:"false"`
	DemoHTTP *dephttp.HTTP     `required:"false"`
	DemoGRPC depgrpc.SayClient `required:"false"`
}
