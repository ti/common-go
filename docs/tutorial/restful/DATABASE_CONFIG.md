# Database Configuration Guide

This document explains how to configure different types of database connections.

## Configuration Steps

### 1. Import the Database Driver in main.go

Import the appropriate driver in `main.go` based on the database type you are using:

```go
import (
    // Mock Database (for testing)
    _ "github.com/ti/common-go/dependencies/database/mock"

    // MongoDB
    // _ "github.com/ti/common-go/dependencies/mongodb"

    // MySQL/PostgreSQL
    // _ "github.com/ti/common-go/dependencies/sql"
)
```

### 2. Configure the Connection String in config.yaml

Configure the database connection string in the `dependencies.db` field of `configs/config.yaml`.

## Database Types and Configuration Examples

### Mock Database (In-Memory Database for Testing)

**Features**:
- Pure in-memory storage, no external dependencies required
- Full CRUD operation support
- Supports paginated queries (PageQuery and StreamQuery)
- Automatic field name normalization (camelCase <-> snake_case)
- Suitable for unit testing and local development

**Import**:
```go
_ "github.com/ti/common-go/dependencies/database/mock"
```

**Configuration Example**:
```yaml
dependencies:
  db: "mock://local/myapp"
  # or
  db: "mock://local/project_name"
```

**Use Cases**:
- Unit testing
- Integration testing
- Local development (no database installation required)
- CI/CD environments

---

### MongoDB

**Features**:
- NoSQL document database
- Supports complex queries and aggregation
- Strong horizontal scaling capabilities
- Supports Replica Sets
- Flexible document structure

**Import**:
```go
_ "github.com/ti/common-go/dependencies/mongodb"
```

**Configuration Examples**:

#### Basic Configuration (Local Single Instance)
```yaml
dependencies:
  db: "mongodb://localhost:27017/myapp"
```

#### With Authentication
```yaml
dependencies:
  db: "mongodb://username:password@localhost:27017/myapp?authSource=admin&timeout=5s"
```

#### Replica Set (High Availability)
```yaml
dependencies:
  db: "mongodb://mongo1:27017,mongo2:27017,mongo3:27017/myapp?replicaSet=rs0&timeout=5s"
```

#### Full Configuration Example
```yaml
dependencies:
  db: "mongodb://user:pass@host1:27017,host2:27017/mydb?replicaSet=rs0&authSource=admin&ssl=true&timeout=10s&maxPoolSize=100"
```

**Common Query Parameters**:
- `timeout`: Connection timeout (e.g., 5s, 10s)
- `authSource`: Authentication database name (usually "admin")
- `replicaSet`: Replica set name
- `ssl`: Enable SSL/TLS (true/false)
- `maxPoolSize`: Maximum number of connections in the pool
- `minPoolSize`: Minimum number of connections in the pool
- `retryWrites`: Automatically retry write operations (true/false)

---

### MySQL

**Features**:
- Relational database
- Strong ACID support
- Widely used, mature ecosystem
- Supports complex transactions
- Suitable for structured data

**Import**:
```go
_ "github.com/ti/common-go/dependencies/sql"
```

**Configuration Examples**:

#### Basic Configuration
```yaml
dependencies:
  db: "mysql://root:password@tcp(localhost:3306)/myapp"
```

#### Recommended Configuration (Production)
```yaml
dependencies:
  db: "mysql://username:password@tcp(localhost:3306)/myapp?charset=utf8mb4&parseTime=True&loc=Local&timeout=5s"
```

#### Full Configuration Example
```yaml
dependencies:
  db: "mysql://user:pass@tcp(host:3306)/dbname?charset=utf8mb4&parseTime=True&loc=Local&timeout=10s&readTimeout=30s&writeTimeout=30s&maxAllowedPacket=16777216"
```

**Common Query Parameters**:
- `charset`: Character set (recommended: utf8mb4, supports full Unicode)
- `parseTime`: Parse database TIME/DATETIME types to Go time.Time (recommended: True)
- `loc`: Timezone location (Local=local timezone, UTC=UTC timezone)
- `timeout`: Connection timeout
- `readTimeout`: Read timeout
- `writeTimeout`: Write timeout
- `maxAllowedPacket`: Maximum packet size (bytes)

---

### PostgreSQL

**Features**:
- Advanced open-source relational database
- Powerful query optimizer
- Supports complex types like JSONB, arrays
- Strict data integrity
- Highly extensible (supports custom types and functions)

**Import**:
```go
_ "github.com/ti/common-go/dependencies/sql"
```

**Configuration Examples**:

#### Basic Configuration
```yaml
dependencies:
  db: "postgres://username:password@localhost:5432/myapp"
```

#### Disable SSL (Local Development)
```yaml
dependencies:
  db: "postgres://username:password@localhost:5432/myapp?sslmode=disable"
```

#### Enable SSL (Production)
```yaml
dependencies:
  db: "postgres://username:password@localhost:5432/myapp?sslmode=require&connect_timeout=10"
```

#### Full Configuration Example
```yaml
dependencies:
  db: "postgres://user:pass@host:5432/dbname?sslmode=require&connect_timeout=10&pool_max_conns=20&pool_min_conns=5&application_name=myapp"
```

**Common Query Parameters**:
- `sslmode`: SSL mode
  - `disable`: Disable SSL
  - `require`: Require SSL but do not verify certificate
  - `verify-ca`: Verify CA certificate
  - `verify-full`: Full verification (including hostname)
- `connect_timeout`: Connection timeout (seconds)
- `application_name`: Application name (used for logging and monitoring)
- `pool_max_conns`: Maximum number of connections in the pool
- `pool_min_conns`: Minimum number of connections in the pool

---

## Complete Configuration File Example

```yaml
# configs/config.yaml
apis:
    grpcAddr: :8081
    httpAddr: :8080
    metricsAddr: :9090
    logBody: true

log:
    level: "debug"

dependencies:
    # Choose a database configuration (uncomment the corresponding line):

    # Mock Database (default, for testing)
    db: "mock://local/restful_tutorial"

    # MongoDB
    # db: "mongodb://localhost:27017/myapp?timeout=5s"
    # db: "mongodb://user:pass@localhost:27017/myapp?authSource=admin"

    # MySQL
    # db: "mysql://root:password@tcp(localhost:3306)/myapp?charset=utf8mb4&parseTime=True&loc=Local"

    # PostgreSQL
    # db: "postgres://user:pass@localhost:5432/myapp?sslmode=disable"

    demoHTTP: "http://127.0.0.1:8080?log=true"

service:
    test: test
```

## Switching Database Types

To switch database types, you only need to:

1. **Update the import in main.go** (uncomment the corresponding driver)
2. **Update the connection string in config.yaml** (switch to the corresponding database configuration)
3. **Recompile and run**

Example: Switching from Mock to MySQL

```go
// main.go
import (
    // _ "github.com/ti/common-go/dependencies/database/mock"  // Comment out
    _ "github.com/ti/common-go/dependencies/sql"               // Uncomment
)
```

```yaml
# config.yaml
dependencies:
  # db: "mock://local/restful_tutorial"  # Comment out
  db: "mysql://root:password@tcp(localhost:3306)/myapp?charset=utf8mb4&parseTime=True&loc=Local"  # Uncomment
```

## Database Interface Compatibility

All database types implement the same `database.Database` interface, so application code can switch between different databases without modification.

Supported operations:
- Insert / InsertOne
- Update / UpdateOne
- Delete / DeleteOne
- Find / FindOne
- Count / Exist
- PageQuery (paginated queries)
- StreamQuery (streaming queries)
- Transaction (transaction support)

## FAQ

### Q: Can multiple databases be used simultaneously?

A: Yes. You can define multiple database connections in the Dependencies struct:

```go
type Dependencies struct {
    dependencies.Dependency
    MainDB   database.Database `required:"false"`  // Primary database
    CacheDB  database.Database `required:"false"`  // Cache database
    LogDB    database.Database `required:"false"`  // Log database
}
```

Configure in config.yaml:
```yaml
dependencies:
  mainDB: "mysql://localhost:3306/main"
  cacheDB: "mongodb://localhost:27017/cache"
  logDB: "postgres://localhost:5432/logs"
```

### Q: Can data be migrated between Mock Database and real databases?

A: Not directly, because Mock Database only exists in memory. However, since the interfaces are compatible, you can write migration scripts to read from one database and write to another.

### Q: How to debug database connection issues?

A:
1. Enable logging: Set `log.level: "debug"` to view detailed logs
2. Check that the connection string format is correct
3. Confirm the database service is running
4. Verify that the username and password are correct
5. Check firewall and network configuration

---

**Updated**: 2026-01-31
**Version**: 1.0
