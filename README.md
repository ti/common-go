# Common-go

[![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Common-go** 是一个全面的 Go 微服务开发库，提供 gRPC/HTTP 服务构建、依赖注入、数据库抽象、中间件和监控等功能。

---

## 🎯 核心设计理念

### Protocol Buffers First（Protobuf 优先）

**核心思想**: 使用 Protocol Buffers 定义一切 —— API、数据模型、实体对象。

```
┌─────────────────────────────────────────────────────────────┐
│                    Proto 定义 (.proto)                       │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │  API 接口    │  │  数据模型    │  │  验证规则    │      │
│  │  (Service)   │  │  (Message)   │  │  (Validate)  │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
└─────────────────────────────────────────────────────────────┘
                            │
                    buf generate (编译)
                            │
        ┌───────────────────┼───────────────────┐
        │                   │                   │
        ▼                   ▼                   ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│  Go 代码     │    │  Python 代码  │    │  TypeScript  │
│  .pb.go      │    │  _pb2.py     │    │  .pb.ts      │
└──────────────┘    └──────────────┘    └──────────────┘
        │                   │                   │
        ▼                   ▼                   ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│  Go 服务     │    │  Python 客户端│    │  Web 前端    │
└──────────────┘    └──────────────┘    └──────────────┘
```

**核心优势**:
- 🌍 **跨语言一致性** - 一次定义，多语言使用
- 🔄 **API 版本兼容** - 向前向后兼容
- 📊 **统一数据模型** - API、数据库、消息队列统一定义
- 🛠️ **自动化工具链** - 自动生成代码、文档、验证

---

## 📑 目录

- [核心设计理念](#核心设计理念)
- [快速开始](#快速开始)
- [完整示例](#完整示例)
- [模块概览](#模块概览)
- [最佳实践](#最佳实践)

---

## 🚀 快速开始

### 安装

```bash
go get github.com/ti/common-go@latest
go install github.com/bufbuild/buf/cmd/buf@latest
```

### Hello World

#### 1. 定义 Proto

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

#### 2. 生成代码并实现服务

```go
// 生成代码
// buf generate

// 实现服务
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
    
    server.Start() // 支持 gRPC 和 HTTP
}
```

#### 3. 测试

```bash
# HTTP 调用
curl -X POST http://localhost:8080/v1/hello/World

# gRPC 调用
grpcurl -plaintext -d '{"name":"World"}' localhost:8080 hello.Greeter/SayHello
```

---

## 🏗️ 完整示例

### Proto 定义（API + 数据模型）

```protobuf
// proto/user.proto
syntax = "proto3";
package user;

import "google/api/annotations.proto";
import "google/protobuf/timestamp.proto";
import "validate/validate.proto";

// API 定义
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

// 数据模型（对应数据库表）
message User {
  int64 id = 1;
  string email = 2;
  string name = 3;
  UserStatus status = 4;
  repeated string tags = 5;                        // JSON 字段
  google.protobuf.Timestamp created_at = 6;
  google.protobuf.Timestamp updated_at = 7;
}

enum UserStatus {
  USER_STATUS_UNSPECIFIED = 0;
  USER_STATUS_ACTIVE = 1;
  USER_STATUS_INACTIVE = 2;
}

// 请求消息
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

### 数据库设计

```go
// model/user.go
package model

import (
    "time"
    pb "yourproject/pkg/go/proto"
)

// User 数据库实体（基于 Proto 定义）
type User struct {
    ID        int64      `db:"id,primary,auto_increment"`
    Email     string     `db:"email,unique,index"`
    Name      string     `db:"name,size:50"`
    Status    int32      `db:"status,default:1"`
    Tags      []string   `db:"tags,json"`           // JSON 字段
    CreatedAt time.Time  `db:"created_at"`
    UpdatedAt time.Time  `db:"updated_at"`
}

// ToProto 转换为 Proto 消息
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

### CRUD 实现

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
    db database.Database
}

// Create 创建用户
func (r *UserRepository) Create(ctx context.Context, user *model.User) error {
    log.Extract(ctx).Action("CreateUser").Info("Creating user", "email", user.Email)
    
    user.CreatedAt = time.Now()
    user.UpdatedAt = time.Now()
    
    return r.db.Insert(ctx, "users", user)
}

// GetByID 查询用户
func (r *UserRepository) GetByID(ctx context.Context, id int64) (*model.User, error) {
    var user model.User
    err := r.db.FindOne(ctx, "users",
        database.C{{Key: "id", Value: id}},
        &user)
    return &user, err
}

// StreamQuery 流式查询（推荐：处理大量数据）
// 优点：逐条处理，内存占用低，适合数据导出、批量处理等场景
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
        []string{"-created_at"}, // 排序
        0,                       // 无限制
        &user)
    if err != nil {
        return err
    }
    defer rows.Close()
    
    // 逐条处理，内存友好
    for rows.Next() {
        if err := rows.Scan(&user); err != nil {
            log.Extract(ctx).Action("StreamQuery").Error("Scan error", "err", err)
            continue
        }
        
        // 处理单条记录
        if err := handler(&user); err != nil {
            log.Extract(ctx).Action("StreamQuery").Error("Handler error", "err", err)
            return err
        }
    }
    
    return nil
}

// List 分页查询（小数据量场景）
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

### RESTful API 实现

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

// CreateUser 创建用户
func (s *UserService) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.User, error) {
    ctx = log.NewContext(ctx, map[string]any{"action": "CreateUser", "email": req.Email})
    
    // 检查邮箱唯一性
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

// GetUser 获取用户
func (s *UserService) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.User, error) {
    user, err := s.repo.GetByID(ctx, req.Id)
    if err != nil {
        return nil, status.Error(codes.NotFound, "user not found")
    }
    return user.ToProto(), nil
}

// ListUsers 列出用户
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

### 主程序

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
    
    // 1. 初始化配置
    if err := config.Init(ctx, "file://config.yaml", &cfg); err != nil {
        log.Action("Init").Fatal("Failed to init config", "err", err)
    }
    
    // 2. 初始化依赖
    var dep Dependencies
    if err := dependencies.Init(ctx, &dep, cfg.Dependencies); err != nil {
        log.Action("Init").Fatal("Failed to init dependencies", "err", err)
    }
    
    log.Action("Init").Info("Service initialized",
        "grpcAddr", cfg.Apis.GrpcAddr,
        "httpAddr", cfg.Apis.HTTPAddr)
    
    // 3. 创建服务
    userRepo := repository.NewUserRepository(dep.DB)
    userService := service.NewUserService(userRepo)
    
    // 4. 启动服务器
    server := grpcmux.NewServer(
        grpcmux.WithConfig(&cfg.Apis),
    )
    
    pb.RegisterUserServiceServer(server, userService)
    pb.RegisterUserServiceHandlerServer(ctx, server.ServeMux(), userService)
    
    log.Action("Start").Info("Server starting")
    server.Start()
}

// Config 配置结构
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

// Dependencies 依赖结构（通过 dependencies.Init 初始化）
type Dependencies struct {
    DB    *dependencies.Database `dependency:"db"`
    Redis *dependencies.Redis    `dependency:"redis"`
    Cache *dependencies.Lru      `dependency:"cache"`
}
```

### 配置文件

```yaml
# config.yaml
apis:
  grpcAddr: :8081      # gRPC 端口
  httpAddr: :8080      # HTTP 端口
  metricsAddr: :9090   # Metrics 端口
  logBody: false       # 是否记录请求体

dependencies:
  db: mongodb://user:pass@localhost:27017/mydb?authSource=admin
  redis: redis://:pass@localhost:6379/0
  cache: memory://
  # 支持的协议:
  # - mysql://user:pass@host:3306/db?charset=utf8mb4
  # - postgres://user:pass@host:5432/db?sslmode=disable
  # - mongodb://user:pass@host:27017/db
  # - redis://[:pass]@host:6379/db
```

**配置说明**：
- `apis`: 服务端口配置
- `dependencies`: 依赖的 URI 配置（键值对形式）
- 依赖会通过 `dependencies.Init()` 自动解析和初始化
- 支持环境变量覆盖，如：`DB_URI=mysql://...`

### 测试 API

```bash
# 创建用户
curl -X POST http://localhost:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"email": "alice@example.com", "name": "Alice"}'

# 获取用户
curl http://localhost:8080/v1/users/1

# 列出用户（分页）
curl "http://localhost:8080/v1/users?page_index=1&page_size=20"

# 流式导出用户（适合大数据量）
# 实现时使用 StreamQuery 方法处理
```

---

## 📦 模块概览

### 核心模块

| 模块 | 功能 | 使用场景 |
|------|------|---------|
| **grpcmux** | 统一 gRPC/HTTP 服务器 | 单端口同时提供两种协议 |
| **dependencies** | 依赖注入框架 | URI 驱动的依赖管理 |
| **database** | 统一数据库接口 | 跨 SQL/NoSQL 的 CRUD |
| **log** | 结构化日志 | JSON 日志和上下文传播 |
| **config** | 配置管理 | 多源配置加载 |

### 数据库

```go
// 统一接口，支持 MySQL, PostgreSQL, MongoDB
type Database interface {
    Insert(ctx, table string, data any) error
    FindOne(ctx, table string, conds C, result any) error
    Update(ctx, table string, conds C, updates D) error
    Delete(ctx, table string, conds C) error
    FindRows(ctx, table string, conds C, sortBy []string, limit int, data any) (Row, error)
    // ... 更多方法
}

// 条件构造
conds := database.C{
    {Key: "age", Value: 18, C: database.Gt},
    {Key: "status", Value: "active"},
}

// 流式查询（推荐：处理大量数据）
var user User
rows, _ := db.FindRows(ctx, "users", conds, []string{"-created_at"}, 0, &user)
defer rows.Close()

for rows.Next() {
    rows.Scan(&user)
    // 逐条处理，内存占用低
    processUser(&user)
}

// 分页查询（小数据量场景）
resp, _ := sql.PageQuery[User](ctx, db, "users", &database.PageQueryRequest{
    PageIndex: 1,
    PageSize: 20,
    Conditions: conds,
    SortBy: []string{"-created_at"},
})
```

### 依赖配置

```go
// 1. 定义配置结构
type Config struct {
    Apis         grpcmux.Config    `json:"apis"`
    Dependencies map[string]string `json:"dependencies"` // 依赖 URI 映射
}

// 2. 定义依赖结构
type Dependencies struct {
    DB    *dependencies.Database `dependency:"db"`    // 数据库
    Redis *dependencies.Redis    `dependency:"redis"` // 缓存
    MQ    *dependencies.Broker   `dependency:"mq"`    // 消息队列
}

// 3. 初始化流程
func main() {
    var cfg Config
    
    // 步骤1: 加载配置文件
    config.Init(ctx, "file://config.yaml", &cfg)
    // cfg.Dependencies = map[string]string{
    //     "db": "mongodb://localhost:27017/mydb",
    //     "redis": "redis://localhost:6379/0",
    // }
    
    // 步骤2: 初始化依赖（根据 map 中的 URI）
    var dep Dependencies
    dependencies.Init(ctx, &dep, cfg.Dependencies)
    // dep.DB, dep.Redis 已自动连接并可使用
    
    // 步骤3: 使用依赖
    userRepo := repository.NewUserRepository(dep.DB)
}
```

**支持的依赖类型**：

| 键名 | URI 格式 | 说明 |
|------|----------|------|
| `db` | `mysql://user:pass@host:3306/db` | MySQL |
| `db` | `postgres://user:pass@host:5432/db` | PostgreSQL |
| `db` | `mongodb://user:pass@host:27017/db` | MongoDB |
| `redis` | `redis://[:pass]@host:6379/db` | Redis |
| `mq` | `kafka://broker1:9092,broker2:9092` | Kafka |
| `http` | `http://api.example.com` | HTTP 客户端 |

### 日志

```go
// 简单日志
log.Action("CreateUser").Info("User created", "userId", userId)

// 上下文日志
ctx = log.NewContext(ctx, map[string]any{"requestId": uuid.New()})
log.Extract(ctx).Action("ProcessOrder").Warn("Low inventory")
```

---

## 🎨 最佳实践

### 1. 配置和依赖初始化

```go
// ✅ 推荐的初始化流程
func main() {
    ctx := context.Background()
    
    // 步骤1: 加载配置
    var cfg Config
    if err := config.Init(ctx, "file://config.yaml", &cfg); err != nil {
        log.Fatal("Config init failed", "err", err)
    }
    
    // 步骤2: 初始化依赖
    var dep Dependencies
    if err := dependencies.Init(ctx, &dep, cfg.Dependencies); err != nil {
        log.Fatal("Dependencies init failed", "err", err)
    }
    
    // 步骤3: 创建服务
    service := NewService(&dep)
    
    // 步骤4: 启动服务器
    server := grpcmux.NewServer(grpcmux.WithConfig(&cfg.Apis))
    server.Start()
}
```

### 2. 配置结构设计

```go
// ✅ 使用 map[string]string 管理依赖 URI
type Config struct {
    Apis         grpcmux.Config    `json:"apis"`
    Dependencies map[string]string `json:"dependencies"`
}

// ✅ 依赖结构使用 dependency 标签
type Dependencies struct {
    DB    *dependencies.Database `dependency:"db"`
    Redis *dependencies.Redis    `dependency:"redis"`
}

// ❌ 避免在 Config 中直接定义依赖实例
// 原因：依赖需要通过 dependencies.Init() 统一初始化
```

### 3. Proto-First 开发流程

```
Proto 定义 → 代码生成 → 数据库映射 → Repository → Service → API
```

### 4. 优先使用流式查询

```go
// ✅ 推荐：流式查询（大数据量）
var user User
rows, _ := db.FindRows(ctx, "users", conditions, sortBy, 0, &user)
defer rows.Close()

for rows.Next() {
    rows.Scan(&user)
    processUser(&user) // 逐条处理，内存友好
}

// ⚠️ 仅小数据量：分页查询
resp, _ := sql.PageQuery[User](ctx, db, "users", pageReq)
```

**场景选择**：
- **流式查询**：数据导出、批量处理、报表生成、大数据量查询（推荐）
- **分页查询**：API 列表接口、小数据量展示（<1000 条）

### 5. Repository 模式

```go
type UserRepository struct {
    db database.Database
}

func (r *UserRepository) Create(ctx, user) error {
    return r.db.Insert(ctx, "users", user)
}
```

### 6. 统一错误处理

```go
if err != nil {
    if errors.Is(err, database.ErrNotFound) {
        return nil, status.Error(codes.NotFound, "user not found")
    }
    return nil, status.Error(codes.Internal, "internal error")
}
```

### 7. 上下文传播

```go
ctx = log.NewContext(ctx, map[string]any{"action": "CreateUser"})
log.Extract(ctx).Info("Processing...")
```

---

## 📚 更多文档

- [Buf 编译指南](docs/BUF_GUIDE.md) - Proto 编译工具
- [数据库接口](dependencies/database/README.md) - Database 接口详解
- [SQL 适配器](dependencies/sql/README.md) - MySQL/PostgreSQL 使用
- [优化记录](docs/OPTIMIZATION_SUMMARY.md) - 项目优化历史

---

## 🛠️ Proto 编译

```bash
# 安装 buf
go install github.com/bufbuild/buf/cmd/buf@latest

# 编译 proto
buf generate
```

详见 [Buf 编译指南](docs/BUF_GUIDE.md)

---

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

---

## 📄 许可证

MIT License

---

## 🙏 致谢

- [gRPC](https://grpc.io/)
- [gRPC-Gateway](https://github.com/grpc-ecosystem/grpc-gateway)
- [Buf](https://buf.build/)
- [protoc-gen-validate](https://github.com/bufbuild/protoc-gen-validate)

---

**Happy Coding! 🚀**
