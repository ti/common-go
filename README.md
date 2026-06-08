# Common-go

[![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Common-go** is a comprehensive Go microservice development library, providing gRPC/HTTP service building, dependency injection, database abstraction, middleware, and monitoring capabilities.

---

## Core Design Philosophy

### Protocol Buffers First

**Core concept**: Use Protocol Buffers to define everything - APIs, data models, entity objects.

```
┌─────────────────────────────────────────────────────────────┐
│                    Proto Definition (.proto)                 │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │ API Interface │  │  Data Model  │  │  Validation  │      │
│  │  (Service)   │  │  (Message)   │  │  (Validate)  │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
└─────────────────────────────────────────────────────────────┘
                            │
                    buf generate (compile)
                            │
        ┌───────────────────┼───────────────────┐
        │                   │                   │
        ▼                   ▼                   ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│  Go Code     │    │  Python Code │    │  TypeScript  │
│  .pb.go      │    │  _pb2.py     │    │  .pb.ts      │
└──────────────┘    └──────────────┘    └──────────────┘
        │                   │                   │
        ▼                   ▼                   ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│  Go Service  │    │ Python Client│    │ Web Frontend │
└──────────────┘    └──────────────┘    └──────────────┘
```

**Core Advantages**:
- **Cross-language consistency** - Define once, use across multiple languages
- **API version compatibility** - Forward and backward compatible
- **Unified data model** - Unified definition for APIs, databases, and message queues
- **Automated toolchain** - Auto-generate code, documentation, and validation

---

## Table of Contents

- [Core Design Philosophy](#core-design-philosophy)
- [Quick Start](#quick-start)
- [Complete Example](#complete-example)
- [Module Overview](#module-overview)
- [Best Practices](#best-practices)

---

## Quick Start

### Installation

```bash
go get github.com/ti/common-go@latest
go install github.com/bufbuild/buf/cmd/buf@latest
```

### Hello World

#### 1. Define Proto

```protobuf
// proto/hello.proto
syntax = "proto3";
package hello;

import "google/api/annotations.proto";
import "validate/validate.proto";

service Greeter {
  rpc SayHello (HelloRequest) returns (HelloResponse) {
    option (google.api.http) = {
      post: "/v1/hello/{name}"
    };
  }
}

message HelloRequest {
  string name = 1 [(validate.rules).string = {min_len: 1, max_len: 50}];
}

message HelloResponse {
  string message = 1;
}
```

#### 2. Generate code and implement service

```go
// Generate code
// buf generate

// Implement service
package main

import (
    "context"
    "github.com/ti/common-go/grpcmux"
    pb "yourproject/pkg/go/proto"
)

type GreeterService struct {
    pb.UnimplementedGreeterServer
}

func (s *GreeterService) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloResponse, error) {
    return &pb.HelloResponse{
        Message: "Hello, " + req.Name + "!",
    }, nil
}

func main() {
    server := grpcmux.NewServer(grpcmux.WithAddr(":8080"))
    
    greeter := &GreeterService{}
    pb.RegisterGreeterServer(server, greeter)
    pb.RegisterGreeterHandlerServer(context.Background(), server.ServeMux(), greeter)
    
    server.Start() // Supports gRPC and HTTP
}
```

#### 3. Test

```bash
# HTTP call
curl -X POST http://localhost:8080/v1/hello/World

# gRPC call
grpcurl -plaintext -d '{"name":"World"}' localhost:8080 hello.Greeter/SayHello
```

---

## Complete Example

### Proto Definition (API + Data Model)

```protobuf
// proto/user.proto
syntax = "proto3";
package user;

import "google/api/annotations.proto";
import "google/protobuf/timestamp.proto";
import "validate/validate.proto";

// API definition
service UserService {
  rpc CreateUser (CreateUserRequest) returns (User) {
    option (google.api.http) = {
      post: "/v1/users"
      body: "*"
    };
  }
  
  rpc GetUser (GetUserRequest) returns (User) {
    option (google.api.http) = {
      get: "/v1/users/{id}"
    };
  }
  
  rpc ListUsers (ListUsersRequest) returns (ListUsersResponse) {
    option (google.api.http) = {
      get: "/v1/users"
    };
  }
}

// Data model (corresponds to database table)
message User {
  int64 id = 1;
  string email = 2;
  string name = 3;
  UserStatus status = 4;
  repeated string tags = 5;                        // JSON field
  google.protobuf.Timestamp created_at = 6;
  google.protobuf.Timestamp updated_at = 7;
}

enum UserStatus {
  USER_STATUS_UNSPECIFIED = 0;
  USER_STATUS_ACTIVE = 1;
  USER_STATUS_INACTIVE = 2;
}

// Request messages
message CreateUserRequest {
  string email = 1 [(validate.rules).string.email = true];
  string name = 2 [(validate.rules).string = {min_len: 2, max_len: 50}];
}

message GetUserRequest {
  int64 id = 1 [(validate.rules).int64.gt = 0];
}

message ListUsersRequest {
  int32 page_index = 1 [(validate.rules).int32.gte = 1];
  int32 page_size = 2 [(validate.rules).int32 = {gte: 1, lte: 100}];
  optional UserStatus status = 3;
}

message ListUsersResponse {
  repeated User users = 1;
  int64 total = 2;
  int32 total_pages = 3;
}
```

### Database Design

```go
// model/user.go
package model

import (
    "time"
    pb "yourproject/pkg/go/proto"
)

// User database entity (based on Proto definition)
type User struct {
    ID        int64      `db:"id,primary,auto_increment"`
    Email     string     `db:"email,unique,index"`
    Name      string     `db:"name,size:50"`
    Status    int32      `db:"status,default:1"`
    Tags      []string   `db:"tags,json"`           // JSON field
    CreatedAt time.Time  `db:"created_at"`
    UpdatedAt time.Time  `db:"updated_at"`
}

// ToProto converts to Proto message
func (u *User) ToProto() *pb.User {
    return &pb.User{
        Id:        u.ID,
        Email:     u.Email,
        Name:      u.Name,
        Status:    pb.UserStatus(u.Status),
        Tags:      u.Tags,
        CreatedAt: timestamppb.New(u.CreatedAt),
        UpdatedAt: timestamppb.New(u.UpdatedAt),
    }
}
```

### CRUD Implementation

```go
// repository/user_repository.go
package repository

import (
    "context"
    "github.com/ti/common-go/dependencies/database"
    "github.com/ti/common-go/log"
    "yourproject/model"
)

type UserRepository struct {
    db *database.DB
}

// Create creates a user
func (r *UserRepository) Create(ctx context.Context, user *model.User) error {
    log.Extract(ctx).Action("CreateUser").Info("Creating user", "email", user.Email)
    
    user.CreatedAt = time.Now()
    user.UpdatedAt = time.Now()
    
    return r.db.Insert(ctx, "users", user)
}

// GetByID queries a user by ID
func (r *UserRepository) GetByID(ctx context.Context, id int64) (*model.User, error) {
    var user model.User
    err := r.db.FindOne(ctx, "users",
        database.C{{Key: "id", Value: id}},
        &user)
    return &user, err
}

// StreamQuery performs a stream query (recommended for large datasets)
// Advantage: process records one by one, low memory footprint, suitable for data export, batch processing, etc.
func (r *UserRepository) StreamQuery(ctx context.Context, status *pb.UserStatus, handler func(*model.User) error) error {
    log.Extract(ctx).Action("StreamQuery").Info("Starting stream query")
    
    conditions := database.C{}
    if status != nil {
        conditions = append(conditions, database.Condition{
            Key: "status", Value: int32(*status),
        })
    }
    
    var user model.User
    rows, err := r.db.FindRows(ctx, "users",
        conditions,
        []string{"-created_at"}, // Sort
        0,                       // No limit
        &user)
    if err != nil {
        return err
    }
    defer rows.Close()
    
    // Process one by one, memory-friendly
    for rows.Next() {
        if err := rows.Scan(&user); err != nil {
            log.Extract(ctx).Action("StreamQuery").Error("Scan error", "err", err)
            continue
        }
        
        // Process single record
        if err := handler(&user); err != nil {
            log.Extract(ctx).Action("StreamQuery").Error("Handler error", "err", err)
            return err
        }
    }
    
    return nil
}

// List performs a pagination query (for small datasets)
func (r *UserRepository) List(ctx context.Context, req *pb.ListUsersRequest) ([]*model.User, int64, error) {
    conditions := database.C{}
    if req.Status != nil {
        conditions = append(conditions, database.Condition{
            Key: "status", Value: int32(*req.Status),
        })
    }
    
    pageReq := &database.PageQueryRequest{
        PageIndex:  int(req.PageIndex),
        PageSize:   int(req.PageSize),
        Conditions: conditions,
        SortBy:     []string{"-created_at"},
    }
    
    resp, err := sql.PageQuery[model.User](ctx, r.db, "users", pageReq)
    if err != nil {
        return nil, 0, err
    }
    
    users := make([]*model.User, len(resp.Data))
    for i := range resp.Data {
        users[i] = &resp.Data[i]
    }
    
    return users, resp.Total, nil
}
```

### RESTful API Implementation

```go
// service/user_service.go
package service

import (
    "context"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
    "github.com/ti/common-go/log"
    "yourproject/repository"
    pb "yourproject/pkg/go/proto"
)

type UserService struct {
    pb.UnimplementedUserServiceServer
    repo *repository.UserRepository
}

// CreateUser creates a user
func (s *UserService) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.User, error) {
    ctx = log.NewContext(ctx, map[string]any{"action": "CreateUser", "email": req.Email})
    
    // Check email uniqueness
    if existing, _ := s.repo.GetByEmail(ctx, req.Email); existing != nil {
        return nil, status.Error(codes.AlreadyExists, "email already exists")
    }
    
    user := &model.User{
        Email:  req.Email,
        Name:   req.Name,
        Status: int32(pb.UserStatus_USER_STATUS_ACTIVE),
    }
    
    if err := s.repo.Create(ctx, user); err != nil {
        return nil, status.Error(codes.Internal, "failed to create user")
    }
    
    log.Extract(ctx).Action("CreateUser").Info("User created", "userId", user.ID)
    return user.ToProto(), nil
}

// GetUser retrieves a user
func (s *UserService) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.User, error) {
    user, err := s.repo.GetByID(ctx, req.Id)
    if err != nil {
        return nil, status.Error(codes.NotFound, "user not found")
    }
    return user.ToProto(), nil
}

// ListUsers lists users
func (s *UserService) ListUsers(ctx context.Context, req *pb.ListUsersRequest) (*pb.ListUsersResponse, error) {
    users, total, err := s.repo.List(ctx, req)
    if err != nil {
        return nil, status.Error(codes.Internal, "failed to list users")
    }
    
    pbUsers := make([]*pb.User, len(users))
    for i, user := range users {
        pbUsers[i] = user.ToProto()
    }
    
    totalPages := int32(total) / req.PageSize
    if int32(total)%req.PageSize != 0 {
        totalPages++
    }
    
    return &pb.ListUsersResponse{
        Users:      pbUsers,
        Total:      total,
        TotalPages: totalPages,
    }, nil
}
```

### Main Program

```go
// main.go
package main

import (
    "context"
    "github.com/ti/common-go/config"
    "github.com/ti/common-go/dependencies"
    "github.com/ti/common-go/grpcmux"
    "github.com/ti/common-go/log"
    "yourproject/repository"
    "yourproject/service"
    pb "yourproject/pkg/go/proto"
)

func main() {
    ctx := context.Background()
    
    // 1. Initialize config
    if err := config.Init(ctx, "file://config.yaml", &cfg); err != nil {
        log.Action("Init").Fatal("Failed to init config", "err", err)
    }
    
    // 2. Initialize dependencies
    var dep Dependencies
    if err := dependencies.Init(ctx, &dep, cfg.Dependencies); err != nil {
        log.Action("Init").Fatal("Failed to init dependencies", "err", err)
    }
    
    log.Action("Init").Info("Service initialized",
        "grpcAddr", cfg.Apis.GrpcAddr,
        "httpAddr", cfg.Apis.HTTPAddr)
    
    // 3. Create service
    userRepo := repository.NewUserRepository(dep.DB)
    userService := service.NewUserService(userRepo)
    
    // 4. Start server
    server := grpcmux.NewServer(
        grpcmux.WithConfig(&cfg.Apis),
    )
    
    pb.RegisterUserServiceServer(server, userService)
    pb.RegisterUserServiceHandlerServer(ctx, server.ServeMux(), userService)
    
    log.Action("Start").Info("Server starting")
    server.Start()
}

// Config structure
var cfg = Config{
    Apis: grpcmux.Config{
        GrpcAddr:    ":8081",
        HTTPAddr:    ":8080",
        MetricsAddr: ":9090",
        LogBody:     false,
    },
    Dependencies: map[string]string{},
}

type Config struct {
    Apis         grpcmux.Config    `json:"apis"`
    Dependencies map[string]string `json:"dependencies"`
}

// Dependencies structure (initialized via dependencies.Init)
type Dependencies struct {
    DB    *dependencies.Database `dependency:"db"`
    Redis *dependencies.Redis    `dependency:"redis"`
    Cache *dependencies.Lru      `dependency:"cache"`
}
```

### Configuration file

```yaml
# config.yaml
apis:
  grpcAddr: :8081      # gRPC port
  httpAddr: :8080      # HTTP port
  metricsAddr: :9090   # Metrics port
  logBody: false       # Whether to log request body
  useCamelCase: false  # JSON format: false=snake_case(default), true=camelCase

dependencies:
  db: mongodb://user:pass@localhost:27017/mydb?authSource=admin
  redis: redis://:pass@localhost:6379/0
  cache: memory://
  # Supported protocols:
  # - mysql://user:pass@host:3306/db?charset=utf8mb4
  # - postgres://user:pass@host:5432/db?sslmode=disable
  # - mongodb://user:pass@host:27017/db
  # - redis://[:pass]@host:6379/db
```

**Configuration notes**:
- `apis`: Service port configuration
  - `useCamelCase`: Controls HTTP JSON format (false=snake_case like `user_id`, true=camelCase like `userId`)
- `dependencies`: Dependency URI configuration (key-value pairs)
- Dependencies are automatically parsed and initialized via `dependencies.Init()`
- Supports environment variable overrides, e.g.: `DB_URI=mysql://...`

### Test API

```bash
# Create user
curl -X POST http://localhost:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"email": "alice@example.com", "name": "Alice"}'

# Get user
curl http://localhost:8080/v1/users/1

# List users (paginated)
curl "http://localhost:8080/v1/users?page_index=1&page_size=20"

# Stream export users (suitable for large datasets)
# Use StreamQuery method when implementing
```

---

## Module Overview

### Core Modules

| Module | Function | Use Case |
|--------|----------|----------|
| **grpcmux** | Unified gRPC/HTTP server | Single port serving both protocols |
| **dependencies** | Dependency injection framework | URI-driven dependency management |
| **database** | Unified database interface | Cross SQL/NoSQL CRUD |
| **log** | Structured logging | JSON logging and context propagation |
| **config** | Configuration management | Multi-source config loading |

### Database

```go
// Unified interface, supports MySQL, PostgreSQL, MongoDB
type Database interface {
    Insert(ctx, table string, data any) error
    FindOne(ctx, table string, conds C, result any) error
    Update(ctx, table string, conds C, updates D) error
    Delete(ctx, table string, conds C) error
    FindRows(ctx, table string, conds C, sortBy []string, limit int, data any) (Row, error)
    // ... more methods
}

// Condition construction
conds := database.C{
    {Key: "age", Value: 18, C: database.Gt},
    {Key: "status", Value: "active"},
}

// Stream query (recommended for large datasets)
var user User
rows, _ := db.FindRows(ctx, "users", conds, []string{"-created_at"}, 0, &user)
defer rows.Close()

for rows.Next() {
    rows.Scan(&user)
    // Process one by one, low memory footprint
    processUser(&user)
}

// Pagination query (for small datasets)
resp, _ := sql.PageQuery[User](ctx, db, "users", &database.PageQueryRequest{
    PageIndex: 1,
    PageSize: 20,
    Conditions: conds,
    SortBy: []string{"-created_at"},
})
```

### Dependency Configuration

```go
// 1. Define config structure
type Config struct {
    Apis         grpcmux.Config    `json:"apis"`
    Dependencies map[string]string `json:"dependencies"` // Dependency URI mapping
}

// 2. Define dependencies structure
type Dependencies struct {
    DB    *dependencies.Database `dependency:"db"`    // Database
    Redis *dependencies.Redis    `dependency:"redis"` // Cache
    MQ    *dependencies.Broker   `dependency:"mq"`    // Message queue
}

// 3. Initialization flow
func main() {
    var cfg Config
    
    // Step 1: Load config file
    config.Init(ctx, "file://config.yaml", &cfg)
    // cfg.Dependencies = map[string]string{
    //     "db": "mongodb://localhost:27017/mydb",
    //     "redis": "redis://localhost:6379/0",
    // }
    
    // Step 2: Initialize dependencies (based on URIs in the map)
    var dep Dependencies
    dependencies.Init(ctx, &dep, cfg.Dependencies)
    // dep.DB, dep.Redis are automatically connected and ready to use
    
    // Step 3: Use dependencies
    userRepo := repository.NewUserRepository(dep.DB)
}
```

**Supported dependency types**:

| Key | URI Format | Description |
|-----|-----------|-------------|
| `db` | `mysql://user:pass@host:3306/db` | MySQL |
| `db` | `postgres://user:pass@host:5432/db` | PostgreSQL |
| `db` | `mongodb://user:pass@host:27017/db` | MongoDB |
| `redis` | `redis://[:pass]@host:6379/db` | Redis |
| `mq` | `kafka://broker1:9092,broker2:9092` | Kafka |
| `http` | `http://api.example.com` | HTTP Client |

### Logging

```go
// Simple logging
log.Action("CreateUser").Info("User created", "userId", userId)

// Context logging
ctx = log.NewContext(ctx, map[string]any{"requestId": uuid.New()})
log.Extract(ctx).Action("ProcessOrder").Warn("Low inventory")
```

---

## Best Practices

### 1. Configuration and Dependency Initialization

```go
// Recommended initialization flow
func main() {
    ctx := context.Background()
    
    // Step 1: Load config
    var cfg Config
    if err := config.Init(ctx, "file://config.yaml", &cfg); err != nil {
        log.Fatal("Config init failed", "err", err)
    }
    
    // Step 2: Initialize dependencies
    var dep Dependencies
    if err := dependencies.Init(ctx, &dep, cfg.Dependencies); err != nil {
        log.Fatal("Dependencies init failed", "err", err)
    }
    
    // Step 3: Create service
    service := NewService(&dep)
    
    // Step 4: Start server
    server := grpcmux.NewServer(grpcmux.WithConfig(&cfg.Apis))
    server.Start()
}
```

### 2. Configuration Structure Design

```go
// Use map[string]string for dependency URIs
type Config struct {
    Apis         grpcmux.Config    `json:"apis"`
    Dependencies map[string]string `json:"dependencies"`
}

// Use dependency tags for dependency structure
type Dependencies struct {
    DB    *dependencies.Database `dependency:"db"`
    Redis *dependencies.Redis    `dependency:"redis"`
}

// Avoid defining dependency instances directly in Config
// Reason: Dependencies need to be uniformly initialized via dependencies.Init()
```

### 3. Proto-First Development Flow

```
Proto Definition -> Code Generation -> Database Mapping -> Repository -> Service -> API
```

### 4. Prefer Stream Queries

```go
// Recommended: Stream query (large datasets)
var user User
rows, _ := db.FindRows(ctx, "users", conditions, sortBy, 0, &user)
defer rows.Close()

for rows.Next() {
    rows.Scan(&user)
    processUser(&user) // Process one by one, memory-friendly
}

// Small datasets only: Pagination query
resp, _ := sql.PageQuery[User](ctx, db, "users", pageReq)
```

**Use case selection**:
- **Stream query**: Data export, batch processing, report generation, large dataset queries (recommended)
- **Pagination query**: API list endpoints, small dataset display (<1000 records)

### 5. Repository Pattern

```go
type UserRepository struct {
    db *database.DB
}

func (r *UserRepository) Create(ctx, user) error {
    return r.db.Insert(ctx, "users", user)
}
```

### 6. Unified Error Handling

```go
if err != nil {
    if errors.Is(err, database.ErrNotFound) {
        return nil, status.Error(codes.NotFound, "user not found")
    }
    return nil, status.Error(codes.Internal, "internal error")
}
```

### 7. Context Propagation

```go
ctx = log.NewContext(ctx, map[string]any{"action": "CreateUser"})
log.Extract(ctx).Info("Processing...")
```

---

## More Documentation

- [JSON Format Configuration](docs/JSON_FORMAT.md) - HTTP JSON response format configuration (snake_case/camelCase)
- [Buf Compilation Guide](docs/BUF_GUIDE.md) - Proto compilation tool
- [Database Interface](dependencies/database/README.md) - Database interface details
- [SQL Adapter](dependencies/sql/README.md) - MySQL/PostgreSQL usage
- [Optimization Records](docs/OPTIMIZATION_SUMMARY.md) - Project optimization history

---

## Proto Compilation

```bash
# Install buf
go install github.com/bufbuild/buf/cmd/buf@latest

# Compile proto
buf generate
```

See [Buf Compilation Guide](docs/BUF_GUIDE.md) for details.

---

## Contributing

Contributions via Issues and Pull Requests are welcome!

---

## License

MIT License

---

## Acknowledgments

- [gRPC](https://grpc.io/)
- [gRPC-Gateway](https://github.com/grpc-ecosystem/grpc-gateway)
- [Buf](https://buf.build/)
- [protoc-gen-validate](https://github.com/bufbuild/protoc-gen-validate)

---

**Happy Coding!**
