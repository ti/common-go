# Common-go

[![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Common-go** 是一个全面的 Go 微服务开发库，提供 gRPC/HTTP 服务构建、依赖注入、数据库抽象、中间件和监控等功能。

## 🎯 核心设计理念

### Protocol Buffers First（Protobuf 优先）

**核心思想**: 使用 Protocol Buffers 定义一切 —— API、数据模型、实体对象。

#### 为什么选择 Protobuf？

1. **跨语言一致性** 🌍
   - 一次定义，多语言使用（Go, Python, Java, C++, JavaScript...）
   - 自动生成类型安全的代码
   - 避免手动维护多语言数据结构

2. **API 版本兼容** 🔄
   - 向前向后兼容
   - 字段编号保证兼容性
   - 安全的 API 演进

3. **统一数据模型** 📊
   - **RESTful API** - 请求和响应
   - **数据库实体** - 数据库表结构
   - **内部对象** - 业务逻辑对象
   - **消息队列** - 事件和消息

4. **自动化工具链** 🛠️
   - 自动生成 gRPC 服务代码
   - 自动生成 HTTP/REST 反向代理
   - 自动生成参数验证代码
   - 自动生成 Swagger/OpenAPI 文档

#### 架构图

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
│  _grpc.pb.go │    │  _pb2_grpc.py│    │  _grpc.pb.ts │
└──────────────┘    └──────────────┘    └──────────────┘
        │                   │                   │
        ▼                   ▼                   ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│  Go 服务     │    │  Python 客户端│    │  Web 前端    │
│  (Server)    │◄───│  (Client)    │◄───│  (UI)        │
└──────────────┘    └──────────────┘    └──────────────┘
```

---

## 📑 目录

- [核心设计理念](#核心设计理念)
- [快速开始](#快速开始)
- [完整示例：用户管理系统](#完整示例用户管理系统)
  - [1. Proto 定义](#1-proto-定义)
  - [2. 数据库设计](#2-数据库设计)
  - [3. CRUD 实现](#3-crud-实现)
  - [4. RESTful API](#4-restful-api)
  - [5. 日志集成](#5-日志集成)
- [模块概览](#模块概览)
- [最佳实践](#最佳实践)

---

## 🚀 快速开始

### 安装

```bash
go get github.com/ti/common-go@latest
```

### 安装 Buf（推荐的 Proto 编译工具）

```bash
go install github.com/bufbuild/buf/cmd/buf@latest
```

---

## 🏗️ 完整示例：用户管理系统

本示例展示如何使用 common-go 构建一个完整的用户管理系统，包括：
- ✅ Proto 定义（API + 数据模型）
- ✅ 数据库 CRUD 操作
- ✅ RESTful API 实现
- ✅ 结构化日志记录
- ✅ 参数验证
- ✅ 错误处理

### 1. Proto 定义

**核心理念**: 使用 Proto 定义 API 接口和数据模型

```protobuf
// proto/user.proto
syntax = "proto3";
package user;

option go_package = "yourproject/pkg/go/proto;pb";

import "google/api/annotations.proto";
import "google/protobuf/timestamp.proto";
import "validate/validate.proto";

// ============================================================================
// Service Definition (API 接口定义)
// ============================================================================

service UserService {
  // 创建用户
  rpc CreateUser (CreateUserRequest) returns (User) {
    option (google.api.http) = {
      post: "/v1/users"
      body: "*"
    };
  }
  
  // 获取用户
  rpc GetUser (GetUserRequest) returns (User) {
    option (google.api.http) = {
      get: "/v1/users/{id}"
    };
  }
  
  // 更新用户
  rpc UpdateUser (UpdateUserRequest) returns (User) {
    option (google.api.http) = {
      put: "/v1/users/{id}"
      body: "*"
    };
  }
  
  // 删除用户
  rpc DeleteUser (DeleteUserRequest) returns (DeleteUserResponse) {
    option (google.api.http) = {
      delete: "/v1/users/{id}"
    };
  }
  
  // 列出用户（分页）
  rpc ListUsers (ListUsersRequest) returns (ListUsersResponse) {
    option (google.api.http) = {
      get: "/v1/users"
    };
  }
}

// ============================================================================
// Data Models (数据模型 - 对应数据库表)
// ============================================================================

// User 用户实体（可直接映射到数据库）
message User {
  int64 id = 1;                                    // 主键
  string email = 2;                                // 邮箱（唯一）
  string name = 3;                                 // 用户名
  string phone = 4;                                // 手机号
  UserStatus status = 5;                           // 状态
  repeated string tags = 6;                        // 标签（JSON 字段）
  UserProfile profile = 7;                         // 用户配置（JSON 字段）
  google.protobuf.Timestamp created_at = 8;        // 创建时间
  google.protobuf.Timestamp updated_at = 9;        // 更新时间
}

// UserProfile 用户配置（嵌套对象，存储为 JSON）
message UserProfile {
  string avatar = 1;                               // 头像 URL
  string bio = 2;                                  // 个人简介
  string location = 3;                             // 位置
  map<string, string> settings = 4;                // 用户设置
}

// UserStatus 用户状态枚举
enum UserStatus {
  USER_STATUS_UNSPECIFIED = 0;
  USER_STATUS_ACTIVE = 1;                          // 激活
  USER_STATUS_INACTIVE = 2;                        // 未激活
  USER_STATUS_BANNED = 3;                          // 封禁
}

// ============================================================================
// Request/Response Messages (API 消息)
// ============================================================================

// CreateUserRequest 创建用户请求
message CreateUserRequest {
  string email = 1 [(validate.rules).string = {
    email: true,
    max_bytes: 255
  }];
  string name = 2 [(validate.rules).string = {
    min_len: 2,
    max_len: 50
  }];
  string phone = 3 [(validate.rules).string = {
    pattern: "^1[3-9]\\d{9}$"  // 中国手机号
  }];
  repeated string tags = 4;
  UserProfile profile = 5;
}

// GetUserRequest 获取用户请求
message GetUserRequest {
  int64 id = 1 [(validate.rules).int64.gt = 0];
}

// UpdateUserRequest 更新用户请求
message UpdateUserRequest {
  int64 id = 1 [(validate.rules).int64.gt = 0];
  optional string name = 2 [(validate.rules).string = {max_len: 50}];
  optional string phone = 3;
  optional UserStatus status = 4;
  repeated string tags = 5;
  optional UserProfile profile = 6;
}

// DeleteUserRequest 删除用户请求
message DeleteUserRequest {
  int64 id = 1 [(validate.rules).int64.gt = 0];
}

// DeleteUserResponse 删除用户响应
message DeleteUserResponse {
  bool success = 1;
}

// ListUsersRequest 列表用户请求
message ListUsersRequest {
  int32 page_index = 1 [(validate.rules).int32 = {gte: 1}];     // 页码
  int32 page_size = 2 [(validate.rules).int32 = {gte: 1, lte: 100}]; // 每页大小
  optional UserStatus status = 3;                                // 状态过滤
  optional string keyword = 4;                                   // 关键词搜索
}

// ListUsersResponse 列表用户响应
message ListUsersResponse {
  repeated User users = 1;                         // 用户列表
  int64 total = 2;                                 // 总数
  int32 page_index = 3;                            // 当前页
  int32 page_size = 4;                             // 每页大小
  int32 total_pages = 5;                           // 总页数
}
```

### 2. 数据库设计

**核心理念**: Proto 消息可以直接映射到数据库表

```go
// model/user.go
package model

import (
    "time"
    pb "yourproject/pkg/go/proto"
)

// User 数据库实体（基于 Proto 定义）
type User struct {
    ID        int64           `db:"id,primary,auto_increment" json:"id"`
    Email     string          `db:"email,unique,index" json:"email"`
    Name      string          `db:"name,size:50" json:"name"`
    Phone     string          `db:"phone,size:20,index" json:"phone"`
    Status    int32           `db:"status,default:1" json:"status"`
    Tags      []string        `db:"tags,json" json:"tags"`                  // JSON 字段
    Profile   *pb.UserProfile `db:"profile,json" json:"profile"`            // JSON 字段
    CreatedAt time.Time       `db:"created_at" json:"created_at"`
    UpdatedAt time.Time       `db:"updated_at" json:"updated_at"`
}

// TableName 指定表名
func (User) TableName() string {
    return "users"
}

// ToProto 转换为 Proto 消息
func (u *User) ToProto() *pb.User {
    return &pb.User{
        Id:        u.ID,
        Email:     u.Email,
        Name:      u.Name,
        Phone:     u.Phone,
        Status:    pb.UserStatus(u.Status),
        Tags:      u.Tags,
        Profile:   u.Profile,
        CreatedAt: timestamppb.New(u.CreatedAt),
        UpdatedAt: timestamppb.New(u.UpdatedAt),
    }
}

// FromProto 从 Proto 消息创建
func (u *User) FromProto(pb *pb.User) {
    u.ID = pb.Id
    u.Email = pb.Email
    u.Name = pb.Name
    u.Phone = pb.Phone
    u.Status = int32(pb.Status)
    u.Tags = pb.Tags
    u.Profile = pb.Profile
    if pb.CreatedAt != nil {
        u.CreatedAt = pb.CreatedAt.AsTime()
    }
    if pb.UpdatedAt != nil {
        u.UpdatedAt = pb.UpdatedAt.AsTime()
    }
}
```

### 3. CRUD 实现

**核心理念**: 使用统一的 Database 接口，支持多种数据库

```go
// repository/user_repository.go
package repository

import (
    "context"
    "time"
    
    "github.com/ti/common-go/dependencies/database"
    "github.com/ti/common-go/log"
    "yourproject/model"
    pb "yourproject/pkg/go/proto"
)

// UserRepository 用户仓储
type UserRepository struct {
    db database.Database
}

// NewUserRepository 创建用户仓储
func NewUserRepository(db database.Database) *UserRepository {
    return &UserRepository{db: db}
}

// Create 创建用户
func (r *UserRepository) Create(ctx context.Context, user *model.User) error {
    user.CreatedAt = time.Now()
    user.UpdatedAt = time.Now()
    
    log.Extract(ctx).Action("CreateUser").Info("Creating user", "email", user.Email)
    
    if err := r.db.Insert(ctx, "users", user); err != nil {
        log.Extract(ctx).Action("CreateUser").Error("Failed to create user", "err", err)
        return err
    }
    
    log.Extract(ctx).Action("CreateUser").Info("User created successfully", "userId", user.ID)
    return nil
}

// GetByID 根据 ID 获取用户
func (r *UserRepository) GetByID(ctx context.Context, id int64) (*model.User, error) {
    log.Extract(ctx).Action("GetUser").Info("Getting user", "userId", id)
    
    var user model.User
    err := r.db.FindOne(ctx, "users",
        database.C{{Key: "id", Value: id}},
        &user)
    
    if err != nil {
        log.Extract(ctx).Action("GetUser").Error("User not found", "userId", id, "err", err)
        return nil, err
    }
    
    return &user, nil
}

// GetByEmail 根据邮箱获取用户
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*model.User, error) {
    log.Extract(ctx).Action("GetUserByEmail").Info("Getting user by email", "email", email)
    
    var user model.User
    err := r.db.FindOne(ctx, "users",
        database.C{{Key: "email", Value: email}},
        &user)
    
    if err != nil {
        return nil, err
    }
    
    return &user, nil
}

// Update 更新用户
func (r *UserRepository) Update(ctx context.Context, id int64, updates map[string]any) error {
    log.Extract(ctx).Action("UpdateUser").Info("Updating user", "userId", id, "updates", updates)
    
    updates["updated_at"] = time.Now()
    
    // 构造更新数据
    var d database.D
    for k, v := range updates {
        d = append(d, database.Element{Key: k, Value: v})
    }
    
    err := r.db.Update(ctx, "users",
        database.C{{Key: "id", Value: id}},
        d)
    
    if err != nil {
        log.Extract(ctx).Action("UpdateUser").Error("Failed to update user", "userId", id, "err", err)
        return err
    }
    
    return nil
}

// Delete 删除用户（软删除）
func (r *UserRepository) Delete(ctx context.Context, id int64) error {
    log.Extract(ctx).Action("DeleteUser").Info("Deleting user", "userId", id)
    
    // 软删除：更新状态
    return r.Update(ctx, id, map[string]any{
        "status": pb.UserStatus_USER_STATUS_BANNED,
    })
}

// List 列出用户（分页）
func (r *UserRepository) List(ctx context.Context, req *pb.ListUsersRequest) ([]*model.User, int64, error) {
    log.Extract(ctx).Action("ListUsers").Info("Listing users", 
        "page", req.PageIndex, 
        "size", req.PageSize)
    
    // 构造查询条件
    var conditions database.C
    
    // 状态过滤
    if req.Status != nil && *req.Status != pb.UserStatus_USER_STATUS_UNSPECIFIED {
        conditions = append(conditions, database.Condition{
            Key:   "status",
            Value: int32(*req.Status),
        })
    }
    
    // 关键词搜索（模糊匹配名称或邮箱）
    if req.Keyword != nil && *req.Keyword != "" {
        // 这里简化处理，实际可以使用更复杂的搜索逻辑
        conditions = append(conditions, database.Condition{
            Key:   "name",
            Value: "%" + *req.Keyword + "%",
            C:     database.Like,
        })
    }
    
    // 构造分页请求
    pageReq := &database.PageQueryRequest{
        PageIndex:  int(req.PageIndex),
        PageSize:   int(req.PageSize),
        Conditions: conditions,
        SortBy:     []string{"-created_at"}, // 按创建时间降序
    }
    
    // 执行分页查询
    resp, err := sql.PageQuery[model.User](ctx, r.db, "users", pageReq)
    if err != nil {
        log.Extract(ctx).Action("ListUsers").Error("Failed to list users", "err", err)
        return nil, 0, err
    }
    
    // 转换为指针切片
    users := make([]*model.User, len(resp.Data))
    for i := range resp.Data {
        users[i] = &resp.Data[i]
    }
    
    return users, resp.Total, nil
}
```

### 4. RESTful API

**核心理念**: 实现 Proto 定义的服务接口

```go
// service/user_service.go
package service

import (
    "context"
    "fmt"
    
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
    
    "github.com/ti/common-go/log"
    "yourproject/model"
    "yourproject/repository"
    pb "yourproject/pkg/go/proto"
)

// UserService 用户服务实现
type UserService struct {
    pb.UnimplementedUserServiceServer
    repo *repository.UserRepository
}

// NewUserService 创建用户服务
func NewUserService(repo *repository.UserRepository) *UserService {
    return &UserService{
        repo: repo,
    }
}

// CreateUser 创建用户
func (s *UserService) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.User, error) {
    // 添加日志上下文
    ctx = log.NewContext(ctx, map[string]any{
        "action": "CreateUser",
        "email":  req.Email,
    })
    
    log.Extract(ctx).Action("CreateUser").Info("Received create user request")
    
    // 检查邮箱是否已存在
    existing, _ := s.repo.GetByEmail(ctx, req.Email)
    if existing != nil {
        log.Extract(ctx).Action("CreateUser").Warn("Email already exists")
        return nil, status.Error(codes.AlreadyExists, "email already exists")
    }
    
    // 创建用户模型
    user := &model.User{
        Email:   req.Email,
        Name:    req.Name,
        Phone:   req.Phone,
        Status:  int32(pb.UserStatus_USER_STATUS_ACTIVE),
        Tags:    req.Tags,
        Profile: req.Profile,
    }
    
    // 保存到数据库
    if err := s.repo.Create(ctx, user); err != nil {
        return nil, status.Error(codes.Internal, "failed to create user")
    }
    
    log.Extract(ctx).Action("CreateUser").Info("User created successfully", "userId", user.ID)
    
    // 返回 Proto 消息
    return user.ToProto(), nil
}

// GetUser 获取用户
func (s *UserService) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.User, error) {
    ctx = log.NewContext(ctx, map[string]any{
        "action": "GetUser",
        "userId": req.Id,
    })
    
    log.Extract(ctx).Action("GetUser").Info("Received get user request")
    
    user, err := s.repo.GetByID(ctx, req.Id)
    if err != nil {
        if errors.Is(err, database.ErrNotFound) {
            return nil, status.Error(codes.NotFound, "user not found")
        }
        return nil, status.Error(codes.Internal, "failed to get user")
    }
    
    return user.ToProto(), nil
}

// UpdateUser 更新用户
func (s *UserService) UpdateUser(ctx context.Context, req *pb.UpdateUserRequest) (*pb.User, error) {
    ctx = log.NewContext(ctx, map[string]any{
        "action": "UpdateUser",
        "userId": req.Id,
    })
    
    log.Extract(ctx).Action("UpdateUser").Info("Received update user request")
    
    // 检查用户是否存在
    user, err := s.repo.GetByID(ctx, req.Id)
    if err != nil {
        if errors.Is(err, database.ErrNotFound) {
            return nil, status.Error(codes.NotFound, "user not found")
        }
        return nil, status.Error(codes.Internal, "failed to get user")
    }
    
    // 构造更新字段
    updates := make(map[string]any)
    if req.Name != nil {
        updates["name"] = *req.Name
    }
    if req.Phone != nil {
        updates["phone"] = *req.Phone
    }
    if req.Status != nil {
        updates["status"] = int32(*req.Status)
    }
    if req.Tags != nil {
        updates["tags"] = req.Tags
    }
    if req.Profile != nil {
        updates["profile"] = req.Profile
    }
    
    // 执行更新
    if err := s.repo.Update(ctx, req.Id, updates); err != nil {
        return nil, status.Error(codes.Internal, "failed to update user")
    }
    
    // 重新获取用户
    user, _ = s.repo.GetByID(ctx, req.Id)
    
    log.Extract(ctx).Action("UpdateUser").Info("User updated successfully")
    
    return user.ToProto(), nil
}

// DeleteUser 删除用户
func (s *UserService) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*pb.DeleteUserResponse, error) {
    ctx = log.NewContext(ctx, map[string]any{
        "action": "DeleteUser",
        "userId": req.Id,
    })
    
    log.Extract(ctx).Action("DeleteUser").Info("Received delete user request")
    
    if err := s.repo.Delete(ctx, req.Id); err != nil {
        return nil, status.Error(codes.Internal, "failed to delete user")
    }
    
    log.Extract(ctx).Action("DeleteUser").Info("User deleted successfully")
    
    return &pb.DeleteUserResponse{Success: true}, nil
}

// ListUsers 列出用户
func (s *UserService) ListUsers(ctx context.Context, req *pb.ListUsersRequest) (*pb.ListUsersResponse, error) {
    ctx = log.NewContext(ctx, map[string]any{
        "action":    "ListUsers",
        "pageIndex": req.PageIndex,
        "pageSize":  req.PageSize,
    })
    
    log.Extract(ctx).Action("ListUsers").Info("Received list users request")
    
    users, total, err := s.repo.List(ctx, req)
    if err != nil {
        return nil, status.Error(codes.Internal, "failed to list users")
    }
    
    // 转换为 Proto 消息
    pbUsers := make([]*pb.User, len(users))
    for i, user := range users {
        pbUsers[i] = user.ToProto()
    }
    
    // 计算总页数
    totalPages := int32(total) / req.PageSize
    if int32(total)%req.PageSize != 0 {
        totalPages++
    }
    
    return &pb.ListUsersResponse{
        Users:      pbUsers,
        Total:      total,
        PageIndex:  req.PageIndex,
        PageSize:   req.PageSize,
        TotalPages: totalPages,
    }, nil
}
```

### 5. 日志集成

**核心理念**: 使用结构化日志记录所有关键操作

```go
// main.go
package main

import (
    "context"
    
    "github.com/ti/common-go/config"
    "github.com/ti/common-go/grpcmux"
    "github.com/ti/common-go/log"
    "yourproject/repository"
    "yourproject/service"
    pb "yourproject/pkg/go/proto"
)

func main() {
    ctx := context.Background()
    
    // 1. 初始化配置
    var cfg Config
    if err := config.Init(ctx, "file://config.yaml", &cfg); err != nil {
        log.Action("InitConfig").Fatal("Failed to init config", "err", err)
    }
    
    log.Action("InitConfig").Info("Config initialized", 
        "dbHost", cfg.Database.Host,
        "serverPort", cfg.Server.Port)
    
    // 2. 初始化数据库
    db := cfg.Database // 自动通过依赖注入初始化
    log.Action("InitDatabase").Info("Database connected")
    
    // 3. 创建仓储和服务
    userRepo := repository.NewUserRepository(db)
    userService := service.NewUserService(userRepo)
    
    // 4. 创建服务器
    server := grpcmux.NewServer(
        grpcmux.WithAddr(cfg.Server.Addr),
        grpcmux.WithMetrics(true),
    )
    
    // 5. 注册服务
    pb.RegisterUserServiceServer(server, userService)
    pb.RegisterUserServiceHandlerServer(ctx, server.ServeMux(), userService)
    
    log.Action("StartServer").Info("Server starting", "addr", cfg.Server.Addr)
    
    // 6. 启动服务（自动优雅关闭）
    server.Start()
}

// Config 配置结构
type Config struct {
    Server   ServerConfig
    Database *dependencies.SQL `uri:"mysql://user:pass@localhost:3306/mydb?charset=utf8mb4"`
}

type ServerConfig struct {
    Addr string `json:"addr" default:":8080"`
}
```

### 6. 配置文件

```yaml
# config.yaml
server:
  addr: ":8080"

database:
  # 通过 uri 标签自动初始化
```

### 7. 测试 API

```bash
# 创建用户
curl -X POST http://localhost:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{
    "email": "alice@example.com",
    "name": "Alice",
    "phone": "13800138000",
    "tags": ["vip", "premium"],
    "profile": {
      "avatar": "https://example.com/avatar.jpg",
      "bio": "Software Engineer",
      "location": "Beijing"
    }
  }'

# 响应
{
  "id": "1",
  "email": "alice@example.com",
  "name": "Alice",
  "phone": "13800138000",
  "status": "USER_STATUS_ACTIVE",
  "tags": ["vip", "premium"],
  "profile": {
    "avatar": "https://example.com/avatar.jpg",
    "bio": "Software Engineer",
    "location": "Beijing"
  },
  "created_at": "2026-01-30T12:00:00Z",
  "updated_at": "2026-01-30T12:00:00Z"
}

# 获取用户
curl http://localhost:8080/v1/users/1

# 更新用户
curl -X PUT http://localhost:8080/v1/users/1 \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Alice Wang",
    "status": "USER_STATUS_ACTIVE"
  }'

# 删除用户
curl -X DELETE http://localhost:8080/v1/users/1

# 列出用户（分页）
curl "http://localhost:8080/v1/users?page_index=1&page_size=20&status=USER_STATUS_ACTIVE"

# 查看 Swagger 文档
open http://localhost:8080/swagger/
```

### 8. 日志输出示例

```json
{
  "time": "2026-01-30T12:00:00Z",
  "level": "INFO",
  "action": "CreateUser",
  "msg": "Received create user request",
  "email": "alice@example.com"
}
{
  "time": "2026-01-30T12:00:01Z",
  "level": "INFO",
  "action": "CreateUser",
  "msg": "User created successfully",
  "userId": 1,
  "email": "alice@example.com"
}
{
  "time": "2026-01-30T12:00:05Z",
  "level": "INFO",
  "action": "ListUsers",
  "msg": "Listing users",
  "page": 1,
  "size": 20
}
```

---

## 📦 模块概览

### 核心模块

#### 1. **grpcmux** - 统一服务器
**位置**: `grpcmux/`  
**功能**: 在单个端口上同时提供 gRPC 和 HTTP 服务

```go
server := grpcmux.NewServer(
    grpcmux.WithAddr(":8080"),
    grpcmux.WithMetrics(true),
)

// 注册 gRPC 服务
pb.RegisterUserServiceServer(server, userService)

// 注册 HTTP 路由（自动转换）
pb.RegisterUserServiceHandlerServer(ctx, server.ServeMux(), userService)

server.Start()
```

**内置功能**:
- ✅ 健康检查 `/healthz`
- 📊 Prometheus 指标 `/metrics`
- 📖 Swagger UI `/swagger/`
- 🔄 自动重试和负载均衡
- 🛡️ 优雅关闭

---

#### 2. **dependencies** - 依赖注入

**位置**: `dependencies/`  
**功能**: URI 驱动的依赖初始化

```go
type Config struct {
    DB    *dependencies.SQL   `uri:"mysql://user:pass@host/db"`
    Cache *dependencies.Redis `uri:"redis://host:6379/0"`
    MQ    *dependencies.Broker `uri:"kafka://broker1,broker2"`
}

var cfg Config
dependencies.Init(ctx, &cfg)
```

---

#### 3. **database** - 统一数据库接口

**位置**: `dependencies/database/`  
**功能**: 跨 SQL/NoSQL 的统一 CRUD 接口

```go
// 统一接口，支持 MySQL, PostgreSQL, MongoDB
type Database interface {
    Insert(ctx, table string, data any) error
    Update(ctx, table string, conds C, updates D) error
    Delete(ctx, table string, conds C) error
    FindOne(ctx, table string, conds C, result any) error
    Find(ctx, table string, conds C, sortBy []string, limit int, results any) error
    Count(ctx, table string, conds C) (int64, error)
    // ... 更多方法
}
```

**条件构造器**:
```go
conds := database.C{
    {Key: "age", Value: 18, C: database.Gt},           // age > 18
    {Key: "status", Value: "active"},                  // status = 'active'
    {Key: "city", Value: []string{"BJ", "SH"}, C: database.In}, // city IN (...)
}
```

---

#### 4. **log** - 结构化日志

**位置**: `log/`  
**功能**: JSON 结构化日志

```go
// 简单日志
log.Action("CreateUser").Info("User created", "userId", userId)

// 上下文日志
ctx := log.NewContext(ctx, map[string]any{
    "requestId": uuid.New(),
    "userId":    userId,
})
logger := log.Extract(ctx)
logger.Action("ProcessOrder").Warn("Low inventory", "sku", sku)
```

---

#### 5. **config** - 配置管理

**位置**: `config/`  
**功能**: 多源配置加载

```go
type Config struct {
    Server struct {
        Port int `json:"port"`
    } `json:"server"`
    Database dependencies.SQLConfig `json:"database"`
}

var cfg Config
config.Init(ctx, "file://config.yaml", &cfg)
```

---

## 🎨 设计模式总结

### 1. Proto-First 模式

**所有定义从 Proto 开始**:
```
Proto 定义 → 代码生成 → 数据库映射 → API 实现
```

**优势**:
- ✅ 一次定义，到处使用
- ✅ 跨语言一致性
- ✅ API 版本兼容
- ✅ 自动化工具链

---

### 2. Repository 模式

**数据访问层抽象**:
```go
type UserRepository struct {
    db database.Database // 可以是任何数据库
}

func (r *UserRepository) Create(ctx, user) error {
    return r.db.Insert(ctx, "users", user)
}
```

---

### 3. 统一错误处理

**使用 gRPC 状态码**:
```go
if err != nil {
    if errors.Is(err, database.ErrNotFound) {
        return nil, status.Error(codes.NotFound, "user not found")
    }
    return nil, status.Error(codes.Internal, "internal error")
}
```

---

### 4. 上下文传播

**日志和追踪上下文**:
```go
ctx = log.NewContext(ctx, map[string]any{
    "action": "CreateUser",
    "userId": userId,
})

// 自动包含上下文信息
log.Extract(ctx).Info("Processing...")
```

---

## 📚 更多文档

- [Buf 编译指南](docs/BUF_GUIDE.md)
- [数据库接口文档](dependencies/database/README.md)
- [SQL 适配器文档](dependencies/sql/README.md)
- [优化记录](docs/OPTIMIZATION_SUMMARY.md)

---

## 🛠️ Proto 编译

### 使用 Buf（推荐）

```bash
cd your-project
buf generate
```

详细说明请查看 [Buf 编译指南](docs/BUF_GUIDE.md)

---

## 🤝 贡献指南

欢迎贡献！请遵循以下流程：

1. Fork 本仓库
2. 创建特性分支
3. 提交更改
4. 创建 Pull Request

---

## 📄 许可证

本项目采用 MIT 许可证。

---

## 🙏 致谢

感谢以下开源项目：
- [gRPC](https://grpc.io/)
- [gRPC-Gateway](https://github.com/grpc-ecosystem/grpc-gateway)
- [Buf](https://buf.build/)
- [protoc-gen-validate](https://github.com/bufbuild/protoc-gen-validate)

---

**Happy Coding! 🚀**
