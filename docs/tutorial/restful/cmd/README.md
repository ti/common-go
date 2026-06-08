# RESTful API Command Line Programs

This directory contains three example programs demonstrating how to start API servers with different JSON naming formats and protocols.

## Directory Structure

```
cmd/
├── camelCase/          # camelCase JSON format server
│   └── main.go
├── snakeCase/          # snake_case JSON format server (default)
│   └── main.go
├── connectrpc/         # ConnectRPC + gRPC-Gateway + gRPC multi-protocol server
│   ├── main.go
│   ├── handler.go
│   └── README.md
├── README.md           # This document
└── JSON_FORMAT_COMPARISON.md  # Format comparison test report
```

## Program Descriptions

### 1. camelCase Server

**Location**: `cmd/camelCase/main.go`

**Features**:
- Uses camelCase JSON format
- HTTP port: `8080`
- gRPC port: `8081`
- Metrics port: `9090`
- Enables `grpcmux.WithUseCamelCase()` option

**JSON Format Example**:
```json
{
    "user": {
        "userId": "123",
        "userName": "Alice",
        "createdAt": "2026-01-31T10:00:00Z"
    }
}
```

**Use Cases**:
- JavaScript/TypeScript frontend projects
- Mobile applications (iOS/Android)
- Projects that need to maintain consistency with camelCase APIs

---

### 2. snakeCase Server

**Location**: `cmd/snakeCase/main.go`

**Features**:
- Uses snake_case JSON format (underscore naming)
- HTTP port: `8082`
- gRPC port: `8083`
- Metrics port: `9091`
- Default format, no additional configuration needed

**JSON Format Example**:
```json
{
    "user": {
        "user_id": "123",
        "user_name": "Alice",
        "created_at": "2026-01-31T10:00:00Z"
    }
}
```

**Use Cases**:
- Python backend projects
- Direct database field mapping
- Traditional RESTful API standards

---

### 3. ConnectRPC Server

**Location**: `cmd/connectrpc/`

**Features**:
- Adds ConnectRPC protocol support on top of camelCase
- HTTP port: `8080` (REST + ConnectRPC coexist)
- gRPC port: `8081`
- Metrics port: `9090`
- Supports Connect, gRPC, and gRPC-Web protocols
- TLS configured via config.yaml (default h2c)

**Routes**:
- `/v1/users/*` - gRPC-Gateway REST
- `/pb.UserService/*` - ConnectRPC
- `:8081` - native gRPC

See [connectrpc/README.md](./connectrpc/README.md) for details.

---

## Build and Run

### Build Programs

Run from the project root directory:

```bash
# Build camelCase server
go build -o bin/server_camelCase ./docs/tutorial/restful/cmd/camelCase

# Build snakeCase server
go build -o bin/server_snakeCase ./docs/tutorial/restful/cmd/snakeCase

# Or build both simultaneously
go build -o bin/server_camelCase ./docs/tutorial/restful/cmd/camelCase && \
go build -o bin/server_snakeCase ./docs/tutorial/restful/cmd/snakeCase
```

### Run Servers

#### Run camelCase Server

```bash
# Run from project root directory
cd docs/tutorial/restful
./bin/server_camelCase

# Or run directly
./bin/server_camelCase -config=docs/tutorial/restful/configs/config.yaml
```

After startup, the server displays:
```
INFO Starting server with camelCase JSON format
INFO Server ready with camelCase JSON format httpAddr=:8080 grpcAddr=:8081 format=camelCase
```

#### Run snakeCase Server

```bash
# Run from project root directory
cd docs/tutorial/restful
./bin/server_snakeCase

# Or run directly
./bin/server_snakeCase -config=docs/tutorial/restful/configs/config.yaml
```

After startup, the server displays:
```
INFO Starting server with snake_case JSON format (default)
INFO Server ready with snake_case JSON format (default) httpAddr=:8082 grpcAddr=:8083 format=snake_case
```

---

## Test API

### camelCase Format Tests

#### Create User
```bash
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Alice","email":"alice@example.com","age":25}'
```

**Response**:
```json
{
    "user": {
        "userId": "1769868266642494615",
        "name": "Alice",
        "email": "alice@example.com",
        "age": 25,
        "createdAt": "2026-01-31T14:04:26.642494530Z",
        "updatedAt": "2026-01-31T14:04:26.642494530Z"
    }
}
```

#### Get User
```bash
curl -X GET "http://127.0.0.1:8080/v1/users/1769868266642494615"
```

#### Update User
```bash
curl -X PUT "http://127.0.0.1:8080/v1/users/1769868266642494615" \
  -H "Content-Type: application/json" \
  -d '{"userId":"1769868266642494615","name":"Alice Updated","email":"alice.new@example.com","age":26}'
```

#### List Users (note: uses pageSize)
```bash
curl -X GET "http://127.0.0.1:8080/v1/users?page=1&pageSize=10"
```

#### Delete User
```bash
curl -X DELETE "http://127.0.0.1:8080/v1/users/1769868266642494615"
```

---

### snake_case Format Tests

#### Create User
```bash
curl -X POST http://127.0.0.1:8082/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Bob","email":"bob@example.com","age":30}'
```

**Response**:
```json
{
    "user": {
        "user_id": "1769874547784032306",
        "name": "Bob",
        "email": "bob@example.com",
        "age": 30,
        "created_at": "2026-01-31T15:49:07.784032167Z",
        "updated_at": "2026-01-31T15:49:07.784032167Z"
    }
}
```

#### Get User
```bash
curl -X GET "http://127.0.0.1:8082/v1/users/1769874547784032306"
```

#### Update User
```bash
curl -X PUT "http://127.0.0.1:8082/v1/users/1769874547784032306" \
  -H "Content-Type: application/json" \
  -d '{"user_id":"1769874547784032306","name":"Bob Updated","email":"bob.new@example.com","age":31}'
```

#### List Users (note: uses page_size)
```bash
curl -X GET "http://127.0.0.1:8082/v1/users?page=1&page_size=10"
```

#### Delete User
```bash
curl -X DELETE "http://127.0.0.1:8082/v1/users/1769874547784032306"
```

---

## Running Both Servers Simultaneously

Since the two servers use different ports, you can run them simultaneously for comparison testing:

**Terminal 1 - camelCase server**:
```bash
cd docs/tutorial/restful
./bin/server_camelCase
```

**Terminal 2 - snakeCase server**:
```bash
cd docs/tutorial/restful
./bin/server_snakeCase
```

**Terminal 3 - Testing**:
```bash
# Test camelCase server (port 8080)
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Alice","email":"alice@example.com","age":25}'

# Test snakeCase server (port 8082)
curl -X POST http://127.0.0.1:8082/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Bob","email":"bob@example.com","age":30}'
```

---

## Configuration File

Both programs share the same configuration file: `docs/tutorial/restful/configs/config.yaml`

```yaml
dependencies:
    # Mock Database for testing
    db: "mock://local/restful_tutorial"

    # Optional: Switch to other databases
    # db: "mongodb://localhost:27017/myapp"
    # db: "mysql://root:password@tcp(localhost:3306)/myapp?charset=utf8mb4&parseTime=True"
    # db: "postgres://user:pass@localhost:5432/myapp?sslmode=disable"
```

---

## Key Code Differences

### camelCase Server

```go
// Enable camelCase format
gs := grpcmux.NewServer(
    grpcmux.WithHTTPAddr(":8080"),
    grpcmux.WithGrpcAddr(":8081"),
    grpcmux.WithMetricsAddr(":9090"),
    grpcmux.WithUseCamelCase(), // Key: enable camelCase
)
```

### snakeCase Server

```go
// Use default snake_case format
gs := grpcmux.NewServer(
    grpcmux.WithHTTPAddr(":8082"),
    grpcmux.WithGrpcAddr(":8083"),
    grpcmux.WithMetricsAddr(":9091"),
    // No WithUseCamelCase() - uses default snake_case
)
```

---

## Notes

1. **Port conflicts**: Ensure ports are not in use
   - camelCase: 8080 (HTTP), 8081 (gRPC), 9090 (Metrics)
   - snakeCase: 8082 (HTTP), 8083 (gRPC), 9091 (Metrics)

2. **Field naming consistency**: Request body field names should match the server format
   - camelCase server: use `userId`, `createdAt`
   - snakeCase server: use `user_id`, `created_at`

3. **Query parameters**: URL query parameters follow the same naming rules
   - camelCase: `?page=1&pageSize=10`
   - snakeCase: `?page=1&page_size=10`

4. **Database compatibility**: Mock Database's automatic field normalization feature ensures both formats work correctly

---

## Related Documentation

- [JSON_FORMAT_COMPARISON.md](./JSON_FORMAT_COMPARISON.md) - Detailed format comparison test report
- [DATABASE_CONFIG.md](../DATABASE_CONFIG.md) - Database configuration guide
- [README.md](../README.md) - Project main README

---

**Updated**: 2026-01-31
**Test status**: All CRUD operations passed testing
