package service

import (
	"github.com/ti/common-go/dependencies"
	"github.com/ti/common-go/dependencies/database"
	dephttp "github.com/ti/common-go/dependencies/http"
	depgrpc "github.com/ti/common-go/docs/tutorial/restful/pkg/go/proto"
)

// Dependencies depend on the main structure
//
// Database Configuration Examples:
//
// The DB field accepts a database.Database interface which supports multiple database types.
// Configure the database connection string in config.yaml under dependencies.db
//
// 1. Mock Database (for testing):
//    Import: _ "github.com/ti/common-go/dependencies/database/mock"
//    Config: db: "mock://local/myapp"
//    Usage:  Perfect for unit tests and local development without external dependencies
//
// 2. MongoDB:
//    Import: _ "github.com/ti/common-go/dependencies/mongodb"
//    Config examples:
//      - Simple:     db: "mongodb://localhost:27017/myapp"
//      - With auth:  db: "mongodb://user:pass@localhost:27017/myapp?authSource=admin"
//      - Replica set: db: "mongodb://mongo1:27017,mongo2:27017/myapp?replicaSet=rs0"
//    Common query parameters:
//      - timeout: Connection timeout (e.g., timeout=5s)
//      - authSource: Authentication database (usually "admin")
//      - replicaSet: Replica set name for high availability
//      - ssl: Enable SSL (ssl=true)
//
// 3. MySQL:
//    Import: _ "github.com/ti/common-go/dependencies/sql"
//    Config examples:
//      - Simple:    db: "mysql://root:password@tcp(localhost:3306)/myapp"
//      - Full:      db: "mysql://user:pass@tcp(host:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"
//    Common query parameters:
//      - charset: Character set (recommended: utf8mb4)
//      - parseTime: Parse time values to time.Time (recommended: True)
//      - loc: Timezone location (e.g., Local, UTC)
//      - timeout: Connection timeout (e.g., timeout=5s)
//
// 4. PostgreSQL:
//    Import: _ "github.com/ti/common-go/dependencies/sql"
//    Config examples:
//      - Simple:    db: "postgres://user:pass@localhost:5432/myapp"
//      - With SSL:  db: "postgres://user:pass@localhost:5432/db?sslmode=require"
//    Common query parameters:
//      - sslmode: SSL mode (disable, require, verify-ca, verify-full)
//      - connect_timeout: Connection timeout in seconds
//      - application_name: Application name for logging
//
// Note: Remember to import the appropriate database driver with blank import (_)
// in your main.go file. The database.New() function will automatically detect
// the database type from the connection string scheme (mock://, mongodb://, mysql://, postgres://)
type Dependencies struct {
	dependencies.Dependency
	DB       database.Database `required:"false"`
	DemoHTTP *dephttp.HTTP     `required:"false"`
	DemoGRPC depgrpc.SayClient `required:"false"`
}
