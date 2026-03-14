# RESTful Tutorial

该教程讲解用 gRPC 编写 HTTP RESTful API 的功能，并演示 UserService CRUD 操作。

## 使用这种方法，构建应用，您将获得：

1. **Proto-First 设计**: 使用 proto + implement 模式，协议和实现解耦的现代开发模式
2. **自动参数校验**: 利用 proto + validate 实现入参校验，免去业务逻辑中大量验证过程
3. **双协议支持**: API 可同时通过 HTTP 和 gRPC 两种方式访问
4. **自动文档生成**: 自动生成 Swagger 文档
5. **类型安全**: 使用 protobuf wrapper types，支持所有 MongoDB codecs 类型
6. **灵活的 JSON 格式**: 支持 camelCase 和 snake_case 两种 JSON 命名风格

## 快速运行

### 运行主服务器（使用配置文件）

```bash
cd docs/tutorial/restful
go run main.go
```

### 运行 camelCase 格式服务器（端口 8080）

```bash
cd docs/tutorial/restful
go run cmd/camelCase/main.go
```

### 运行 snake_case 格式服务器（端口 8082）

```bash
cd docs/tutorial/restful
go run cmd/snakeCase/main.go
```

## API 测试示例

本教程提供完整的 UserService CRUD 操作示例，展示所有 protobuf wrapper 类型的使用。

### 1. 创建用户 (CreateUser)

**camelCase 格式 (端口 8080):**

```bash
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{
    "name": "John Doe",
    "email": "john@example.com",
    "age": 30,
    "isPremium": true,
    "phoneNumber": "+1234567890",
    "address": "123 Main St",
    "bio": "Software Engineer",
    "accountBalance": 1000.50,
    "discountRate": 0.15
  }'
```

**响应:**
```json
{
  "user": {
    "userId": "1769969638553951322",
    "name": "John Doe",
    "email": "john@example.com",
    "createdAt": "2026-02-01T18:13:58.553951225Z",
    "updatedAt": "2026-02-01T18:13:58.553951225Z",
    "age": 30,
    "isPremium": true,
    "phoneNumber": "+1234567890",
    "address": "123 Main St",
    "bio": "Software Engineer",
    "accountBalance": 1000.5,
    "discountRate": 0.15
  }
}
```

**snake_case 格式 (端口 8082):**

```bash
curl -X POST http://127.0.0.1:8082/v1/users \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Jane Smith",
    "email": "jane@example.com",
    "age": 28,
    "is_premium": false,
    "phone_number": "+0987654321",
    "address": "456 Oak Ave",
    "referrer_id": 123456789,
    "login_count": 5,
    "account_balance": 2500.75
  }'
```

**响应:**
```json
{
  "user": {
    "user_id": "1769971219958902103",
    "name": "Jane Smith",
    "email": "jane@example.com",
    "created_at": "2026-02-01T18:40:19.958902011Z",
    "updated_at": "2026-02-01T18:40:19.958902011Z",
    "age": 28,
    "is_premium": false,
    "phone_number": "+0987654321",
    "address": "456 Oak Ave",
    "referrer_id": "123456789",
    "account_balance": 2500.75
  }
}
```

### 2. 获取用户 (GetUser)

```bash
# camelCase 格式
curl -X GET http://127.0.0.1:8080/v1/users/1769969638553951322 \
  -H "Content-Type: application/json"

# snake_case 格式
curl -X GET http://127.0.0.1:8082/v1/users/1769971219958902103 \
  -H "Content-Type: application/json"
```

### 3. 更新用户 (UpdateUser)

支持部分更新，只需传入要修改的字段：

```bash
curl -X PUT http://127.0.0.1:8080/v1/users/1769969638553951322 \
  -H "Content-Type: application/json" \
  -d '{
    "isActive": true,
    "isVerified": true,
    "rating": 4.8
  }'
```

**响应:**
```json
{
  "user": {
    "userId": "1769969638553951322",
    "name": "John Doe",
    "email": "john@example.com",
    "createdAt": "2026-02-01T18:13:58.553951225Z",
    "updatedAt": "2026-02-01T18:14:07.126156045Z",
    "age": 30,
    "isActive": true,
    "isVerified": true,
    "isPremium": true,
    "phoneNumber": "+1234567890",
    "address": "123 Main St",
    "bio": "Software Engineer",
    "accountBalance": 1000.5,
    "rating": 4.8,
    "discountRate": 0.15
  }
}
```

### 4. 列出用户 (ListUsers - PageQuery)

使用 `PageQueryRequest` 实现基于页码的分页查询，适合传统的分页场景：

**基本分页查询:**

```bash
# 第1页，每页10条
curl -X GET "http://127.0.0.1:8080/v1/users?page=1&limit=10" \
  -H "Content-Type: application/json"

# 第2页，每页2条
curl -X GET "http://127.0.0.1:8080/v1/users?page=2&limit=2" \
  -H "Content-Type: application/json"
```

**带排序的查询:**

```bash
# 按年龄降序排序
curl -X GET "http://127.0.0.1:8080/v1/users?page=1&limit=10&sort=-age" \
  -H "Content-Type: application/json"

# 多字段排序：按年龄降序，姓名升序
curl -X GET "http://127.0.0.1:8080/v1/users?page=1&limit=10&sort=-age&sort=name" \
  -H "Content-Type: application/json"
```

**响应:**
```json
{
  "data": [
    {
      "userId": "1769969638553951322",
      "name": "John Doe",
      "email": "john@example.com",
      "createdAt": "2026-02-01T18:13:58.553951225Z",
      "updatedAt": "2026-02-01T18:14:07.126156045Z",
      "age": 30,
      "isActive": true,
      "isVerified": true,
      "isPremium": true,
      "phoneNumber": "+1234567890",
      "address": "123 Main St",
      "bio": "Software Engineer",
      "accountBalance": 1000.5,
      "rating": 4.8,
      "discountRate": 0.15
    }
  ],
  "total": "2"
}
```

**PageQueryRequest 支持的查询参数:**
- `page`: 页码（从 1 开始，默认: 1）
- `limit`: 每页数量（默认: 10）
- `sort`: 排序字段，支持多个。使用 `-` 前缀表示降序，例如 `-age`
- `select`: 选择返回的字段（可选）

### 5. 流式查询用户 (StreamUsers - StreamQuery)

使用 `StreamQueryRequest` 实现基于游标的分页，适合大数据集的高效遍历：

**首次查询（获取前2条）:**

```bash
curl -X GET "http://127.0.0.1:8080/v1/users/stream?limit=2" \
  -H "Content-Type: application/json"
```

**响应:**
```json
{
  "pageToken": "1769974891318458409",
  "data": [
    {
      "userId": "1769974891321156043",
      "name": "Charlie",
      "email": "charlie@example.com",
      "createdAt": "2026-02-01T19:41:31.321155954Z",
      "updatedAt": "2026-02-01T19:41:31.321155954Z",
      "age": 35
    },
    {
      "userId": "1769974891318458409",
      "name": "Bob",
      "email": "bob@example.com",
      "createdAt": "2026-02-01T19:41:31.318458319Z",
      "updatedAt": "2026-02-01T19:41:31.318458319Z",
      "age": 30
    }
  ],
  "total": "3"
}
```

**使用 pageToken 获取下一页:**

```bash
curl -X GET "http://127.0.0.1:8080/v1/users/stream?limit=2&pageToken=1769974891318458409" \
  -H "Content-Type: application/json"
```

**响应:**
```json
{
  "data": [
    {
      "userId": "1769974886273632397",
      "name": "Alice",
      "email": "alice@example.com",
      "createdAt": "2026-02-01T19:41:26.273632304Z",
      "updatedAt": "2026-02-01T19:41:26.273632304Z",
      "age": 25
    }
  ],
  "total": "3"
}
```

**StreamQueryRequest 支持的查询参数:**
- `pageToken`: 游标令牌（从上一次响应的 pageToken 获取）
- `limit`: 每页数量（默认: 10）
- `ascending`: 排序方向（true: 升序，false: 降序，默认: false）
- `select`: 选择返回的字段（可选）

**PageQuery vs StreamQuery:**
- **PageQuery**: 基于页码的分页，适合需要跳页的场景（如 UI 分页器）
- **StreamQuery**: 基于游标的分页，适合顺序遍历大数据集，性能更好，不会有深分页问题

### 6. 删除用户 (DeleteUser)

```bash
curl -X DELETE http://127.0.0.1:8080/v1/users/1769969638553951322 \
  -H "Content-Type: application/json"
```

**响应:**
```json
{
  "success": true,
  "message": "User 1769969638553951322 deleted successfully"
}
```

## 编写步骤

1. **定义 Proto**: 在 `proto/main.proto` 中定义你的 API 和数据结构
   - 使用 protobuf wrapper types (Int32Value, BoolValue, StringValue, etc.) 表示可选字段
   - 避免使用 `any` 或 `struct` 等模糊类型，保持类型安全

2. **编译 Proto**: 执行 `make build` 命令生成 Go 代码
   ```bash
   make build
   ```

3. **实现服务**: 参考 `service/user_service.go` 实现你的业务逻辑
   - 实现 proto 定义的 service 接口
   - 使用 Mock Database 进行本地测试

4. **注册服务**: 在 `main.go` 中注册你的服务
   ```go
   userSrv := service.NewUserServiceServer(&cfg.Dependencies, &cfg.Service)
   pb.RegisterUserServiceServer(gs, userSrv)
   pb.RegisterUserServiceHandlerServer(context.Background(), gs.ServeMux(), userSrv)
   ```

5. **运行服务**:
   ```bash
   go run main.go
   ```

## 查询支持

本项目使用 `dependencies/database/query` 包提供的高效查询功能，支持两种分页模式。

**重要提示 - 查询类型命名规范**:

定义 API 时，**强烈建议直接使用标准的查询请求类型**：
- **PageQueryRequest** - 用于基于页码的分页查询（通用，可复用）
- **StreamQueryRequest** - 用于基于游标的流式查询（通用，可复用）

响应类型则**根据具体业务资源命名**：
- **PageUsersResponse** - 用户分页查询响应
- **StreamUsersResponse** - 用户流式查询响应
- **PageOrdersResponse** - 订单分页查询响应（示例）
- **StreamOrdersResponse** - 订单流式查询响应（示例）

**优点**:
- 请求类型统一，减少重复定义
- API 接口更加规范和一致
- 易于理解和维护
- 响应类型明确区分不同业务资源

### PageQuery - 基于页码的分页

使用 `query.PageQuery` 函数，适合传统的页码分页场景：

```go
resp, err := query.PageQuery[User](ctx, s.dep.DB, "users", &database.PageQueryRequest{
    Page:  1,
    Limit: 10,
    Sort:  []string{"-created_at"},
})
```

**特点:**
- 支持跳转到任意页
- 适合 UI 分页器
- 每次查询返回总数

**请求结构 (PageQueryRequest - 通用标准):**
```protobuf
message PageQueryRequest {
    int32 page = 1;              // 页码（从 1 开始）
    int32 limit = 2;             // 每页数量
    repeated string select = 3;  // 选择返回的字段
    repeated string sort = 4;    // 排序（- 前缀表示降序）
}
```

**注意**: `PageQueryRequest` 和 `StreamQueryRequest` 是**通用的标准请求类型**，建议在所有分页 API 中直接使用，无需为每个资源重新定义。

**响应结构 (PageUsersResponse - 业务定制):**
```protobuf
message PageUsersResponse {
    repeated User data = 1;  // 用户数据
    int64 total = 2;         // 总记录数
}
```

**注意**: 响应类型名称应根据业务场景命名（如 `PageUsersResponse`、`PageOrdersResponse`），以区分不同的资源类型。

### StreamQuery - 基于游标的分页

使用 `query.StreamQuery` 函数，适合大数据集的高效遍历：

```go
resp, err := query.StreamQuery[User](ctx, s.dep.DB, "users", &database.StreamQueryRequest{
    PageToken: "",  // 空表示首次查询
    PageField: "user_id",
    Limit:     10,
    Ascending: false,
})
```

**特点:**
- 使用游标（page_token）而非页码
- 避免深分页性能问题
- 适合大数据集顺序遍历
- 性能稳定，不受数据量影响

**请求结构 (StreamQueryRequest - 通用标准):**
```protobuf
message StreamQueryRequest {
    string page_token = 1;       // 游标令牌
    int32 limit = 2;             // 每页数量
    repeated string select = 3;  // 选择返回的字段
    bool ascending = 4;          // 排序方向
}
```

**响应结构 (StreamUsersResponse - 业务定制):**
```protobuf
message StreamUsersResponse {
    string page_token = 1;   // 下一页游标
    repeated User data = 2;  // 用户数据
    int64 total = 3;         // 总记录数
}
```

**注意**: 响应类型名称应根据业务场景命名（如 `StreamUsersResponse`、`StreamOrdersResponse`），以区分不同的资源类型。

### 使用场景对比

| 场景 | 推荐方式 | 原因 |
|------|----------|------|
| UI 分页器（需要跳页） | PageQuery | 支持直接跳转到任意页 |
| 导出数据 | StreamQuery | 性能稳定，适合大数据集 |
| 无限滚动 | StreamQuery | 顺序加载，性能更好 |
| 数据同步 | StreamQuery | 游标保证不丢失数据 |
| 搜索结果（<1000条） | PageQuery | 简单直观 |
| 日志查询 | StreamQuery | 数据量大，顺序访问 |

## Proto 类型支持

本教程展示了所有 MongoDB codecs 支持的 protobuf 类型：

### Wrapper Types (可选字段)
- `google.protobuf.Int32Value` - 32位整数 (如: age)
- `google.protobuf.Int64Value` - 64位整数 (如: referrerId, loginCount)
- `google.protobuf.BoolValue` - 布尔值 (如: isActive, isVerified, isPremium)
- `google.protobuf.StringValue` - 字符串 (如: phoneNumber, address, bio)
- `google.protobuf.DoubleValue` - 双精度浮点 (如: accountBalance, rating)
- `google.protobuf.FloatValue` - 单精度浮点 (如: discountRate)
- `google.protobuf.UInt32Value` - 32位无符号整数 (如: failedLoginAttempts)
- `google.protobuf.UInt64Value` - 64位无符号整数 (如: totalSpent)
- `google.protobuf.BytesValue` - 字节数组 (如: profilePicture, publicKey)

### Timestamp Types
- `google.protobuf.Timestamp` - 时间戳 (如: createdAt, updatedAt, lastLoginAt)

使用 wrapper types 的好处：
- 可以区分 "未设置" 和 "设置为零值"
- 支持部分更新（只更新提供的字段）
- 类型安全，避免使用 `any` 或 `struct`

## 数据库支持

项目支持多种数据库类型，通过导入相应的驱动和配置连接字符串即可：

### Mock Database (测试用)
```go
import _ "github.com/ti/common-go/dependencies/database/mock"
```
配置: `db: "mock://local/myapp"`

### MongoDB
```go
import _ "github.com/ti/common-go/dependencies/mongodb"
```
配置: `db: "mongodb://localhost:27017/myapp"`

### MySQL
```go
import _ "github.com/ti/common-go/dependencies/sql"
```
配置: `db: "mysql://root:password@tcp(localhost:3306)/myapp"`

### PostgreSQL
```go
import _ "github.com/ti/common-go/dependencies/sql"
```
配置: `db: "postgres://user:pass@localhost:5432/myapp"`

详细的数据库配置说明请参考 `service/dependencies.go` 中的注释。

## CORS 配置

grpcmux 内置了 CORS 支持，默认允许所有跨域请求。可通过 `WithCORS` 选项自定义：

### 默认配置（允许所有 Origin）

```go
gs := grpcmux.NewServer(
    grpcmux.WithCORS(grpcmux.CORSConfig{
        AllowedOrigins: []string{"*"},
    }),
)
```

### 限制特定域名

```go
gs := grpcmux.NewServer(
    grpcmux.WithCORS(grpcmux.CORSConfig{
        AllowedOrigins: []string{"https://example.com", "https://app.example.com"},
        ExposeHeaders:  []string{"X-Request-Id"},
    }),
)
```

### 添加额外请求头

不填写 `AllowedHeaders` 时，默认允许以下请求头：
`Authorization, Content-Type, Accept, X-Project-Id, X-Device-Id, X-Request-Id, X-Request-Timestamp, Connect-Protocol-Version, Connect-Timeout-Ms, Grpc-Timeout`

`AllowedHeaders` 字段用于在默认基础上**追加**额外的头：

```go
gs := grpcmux.NewServer(
    grpcmux.WithCORS(grpcmux.CORSConfig{
        AllowedOrigins: []string{"*"},
        AllowedHeaders: []string{"X-Custom-Header", "X-Organization-Id"},
        ExposeHeaders:  []string{"X-Request-Id", "X-Trace-Id"},
    }),
)
```

### 禁用 CORS（由反向代理处理）

```go
gs := grpcmux.NewServer(
    grpcmux.WithCORS(grpcmux.CORSConfig{Disabled: true}),
)
```

### 通过配置文件设置

```yaml
apis:
  cors:
    allowedOrigins:
      - "https://example.com"
    allowedHeaders:
      - "X-Custom-Header"
    exposeHeaders:
      - "X-Request-Id"
```

### CORSConfig 字段说明

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `Disabled` | `bool` | `false` | 完全禁用 CORS 头注入 |
| `AllowedOrigins` | `[]string` | `["*"]` | 允许的 Origin 列表，`*` 表示允许全部 |
| `AllowedHeaders` | `[]string` | `[]` | 在默认允许头基础上追加的额外请求头 |
| `ExposeHeaders` | `[]string` | `[]` | 允许前端 JS 读取的响应头 |

### CORS 覆盖范围

| 路径类型 | 是否受 CORS 保护 |
|----------|:---:|
| gRPC-Gateway REST 路由 | ✅ |
| ConnectRPC 路由 | ✅ |
| 自定义 HTTP handler (`s.Handle`) | ✅ |
| WebSocket (`s.HandleWebSocket`) | ❌ |
| Metrics 端点 | ❌ |

## JWT Auth 鉴权

grpcmux 通过 `WithAuthFunc` 提供统一的鉴权入口，**一次配置即可覆盖 gRPC、HTTP（gRPC-Gateway）和 ConnectRPC 三种接口**。

### 鉴权覆盖原理

`WithAuthFunc` 注册的函数会同时传递到两个层面：

1. **gRPC interceptor 链** — 覆盖原生 gRPC 请求（`:8081`）
2. **HTTP mux 中间件** — 覆盖所有 HTTP 路由（gRPC-Gateway + ConnectRPC + 自定义 Handle）

```
                     WithAuthFunc(jwtAuthFunc)
                            │
           ┌────────────────┼────────────────┐
           ▼                ▼                ▼
      gRPC interceptor   mux.authFunc    mux.authFunc
           │                │                │
           ▼                ▼                ▼
      原生 gRPC :8081    gRPC-Gateway    ConnectRPC + 自定义 HTTP
```

### 基本用法

```go
import (
    "context"
    "strings"

    "github.com/grpc-ecosystem/go-grpc-middleware/v2/metadata"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

func jwtAuthFunc(ctx context.Context) (context.Context, error) {
    // 从 gRPC metadata 中取 Authorization header
    // (HTTP 请求经 mux 转换后，HTTP header 已自动注入 metadata)
    token := metadata.ExtractIncoming(ctx).Get("authorization")
    if token == "" {
        return ctx, status.Error(codes.Unauthenticated, "missing authorization")
    }
    token = strings.TrimPrefix(token, "Bearer ")

    // 校验 JWT token（替换为你的实际验证逻辑）
    claims, err := verifyJWT(token)
    if err != nil {
        return ctx, status.Error(codes.Unauthenticated, "invalid token")
    }

    // 将用户信息注入 context
    ctx = mux.NewContextWithAuthInfo(ctx, claims)
    return ctx, nil
}

func main() {
    gs := grpcmux.NewServer(
        grpcmux.WithAuthFunc(jwtAuthFunc),
        grpcmux.WithNoAuthPrefixes("/healthz", "/v1/auth/login"),
    )
    // ...
    gs.Start()
}
```

### 跳过鉴权的路径

使用 `WithNoAuthPrefixes` 设置不需要鉴权的路径前缀：

```go
gs := grpcmux.NewServer(
    grpcmux.WithAuthFunc(jwtAuthFunc),
    grpcmux.WithNoAuthPrefixes(
        "/healthz",           // 健康检查
        "/v1/auth/",          // 登录、注册等
        "/v1/public/",        // 公开接口
    ),
)
```

该配置在两侧都生效：
- **gRPC 侧**：按 `fullMethod` 前缀匹配
- **HTTP 侧**：按 `r.URL.Path` 前缀匹配

### 各接口的鉴权覆盖情况

| 接口类型 | 鉴权方式 | 覆盖情况 |
|----------|----------|:---:|
| 原生 gRPC (`:8081`) | gRPC interceptor | ✅ |
| gRPC-Gateway REST | mux 中间件 | ✅ |
| ConnectRPC | mux 中间件（经 `mux.Middleware` 包裹） | ✅ |
| 自定义 HTTP (`s.Handle`) | mux 中间件 | ✅ |
| WebSocket (`s.HandleWebSocket`) | 需在 handler 内自行处理 | ❌ |
| Metrics (`:9090`) | 独立 HTTP Server | ❌ |

## JSON 格式控制

项目支持两种 JSON 命名格式：

### camelCase 格式（默认启用）
```go
gs := grpcmux.NewServer(
    grpcmux.WithUseCamelCase(), // 启用 camelCase
)
```
字段示例: `userId`, `isPremium`, `phoneNumber`

### snake_case 格式（默认）
```go
gs := grpcmux.NewServer(
    // 不传 WithUseCamelCase() 即为 snake_case
)
```
字段示例: `user_id`, `is_premium`, `phone_number`

参考 `cmd/camelCase/main.go` 和 `cmd/snakeCase/main.go` 查看完整示例。

## 错误处理

本教程展示了完整的自定义错误码系统，遵循 gRPC 和 HTTP 错误码规范。

### 错误码规范

错误码定义在 `proto/error.proto` 中，遵循以下规范：
- **4xxx**: 客户端错误（无效输入、未授权等）
- **5xxx**: 服务端错误（内部错误、服务不可用等）

### 注册错误码

在服务初始化时注册自定义错误码，使 grpcmux 框架能够正确映射到 HTTP 状态码：

```go
func NewUserServiceServer(dep *Dependencies, cfg *Config) *UserServiceServer {
    // 注册自定义错误码
    mux.RegisterErrorCodes(pb.ErrorCode_name)

    return &UserServiceServer{
        dep: dep,
        cfg: cfg,
    }
}
```

### 使用错误码

在业务逻辑中使用自定义错误码：

```go
// 用户不存在
return nil, status.Error(codes.Code(pb.ErrorCode_user_not_found),
    fmt.Sprintf("user with ID %d not found", req.UserId))

// 邮箱已被使用
return nil, status.Error(codes.Code(pb.ErrorCode_email_already_in_use),
    fmt.Sprintf("email %s is already in use", req.Email))

// 年龄超出范围
return nil, status.Error(codes.Code(pb.ErrorCode_age_out_of_range),
    fmt.Sprintf("age %d is out of valid range (0-150)", age))
```

### 常见错误示例

#### 1. 用户不存在 (user_not_found - 4004)

```bash
curl -X GET http://127.0.0.1:8080/v1/users/999999999 \
  -H "Content-Type: application/json"
```

**错误响应:**
```json
{
  "code": 4004,
  "message": "user with ID 999999999 not found"
}
```

#### 2. 邮箱已被使用 (email_already_in_use - 4010)

```bash
# 先创建一个用户
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Test User", "email": "test@example.com"}'

# 尝试使用相同邮箱创建另一个用户
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Another User", "email": "test@example.com"}'
```

**错误响应:**
```json
{
  "code": 4010,
  "message": "email test@example.com is already in use"
}
```

#### 3. 年龄超出范围 (age_out_of_range - 4031)

```bash
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Invalid Age", "email": "invalid@example.com", "age": 200}'
```

**错误响应:**
```json
{
  "code": 4031,
  "message": "age 200 is out of valid range (0-150)"
}
```

#### 4. 用户已被删除 (user_deleted - 4011)

当尝试访问已删除（`isActive = false`）的用户时：

```bash
curl -X GET http://127.0.0.1:8080/v1/users/{deleted_user_id} \
  -H "Content-Type: application/json"
```

**错误响应:**
```json
{
  "code": 4011,
  "message": "user with ID {deleted_user_id} has been deleted"
}
```

#### 5. 数据库错误 (database_error - 5027)

当数据库不可用或操作失败时：

```json
{
  "code": 5027,
  "message": "database not available"
}
```

### 完整错误码列表

| 错误码 | 名称 | 说明 |
|-------|------|------|
| 0 | OK | 成功 |
| 4001 | captcha_required | 需要验证码 |
| 4002 | captcha_invalid | 验证码无效 |
| 4004 | user_not_found | 用户不存在 |
| 4009 | user_already_exists | 用户已存在 |
| 4010 | email_already_in_use | 邮箱已被使用 |
| 4011 | user_deleted | 用户已被删除 |
| 4012 | user_not_activated | 用户未激活 |
| 4020 | invalid_user_data | 用户数据无效 |
| 4021 | invalid_request | OAuth2: 无效请求 |
| 4022 | unauthorized_client | OAuth2: 未授权的客户端 |
| 4023 | access_denied | OAuth2: 访问被拒绝 |
| 4024 | unsupported_response_type | OAuth2: 不支持的响应类型 |
| 4025 | invalid_scope | OAuth2: 无效范围 |
| 4026 | invalid_grant | OAuth2: 无效授权 |
| 4030 | payment_required | 需要支付 |
| 4031 | age_out_of_range | 年龄超出范围 |
| 4032 | insufficient_balance | 余额不足 |
| 4033 | premium_required | 需要高级会员 |
| 5026 | server_error | 服务器错误 |
| 5027 | database_error | 数据库错误 |
| 5028 | service_unavailable | 服务不可用 |

### 实现的错误处理场景

#### CreateUser 方法
- 数据库不可用检查 → `database_error`
- 邮箱唯一性检查 → `email_already_in_use`
- 年龄范围验证 → `age_out_of_range`
- 数据库插入失败 → `database_error`

#### GetUser 方法
- 数据库不可用检查 → `database_error`
- 用户不存在 → `user_not_found`
- 用户已删除检查 → `user_deleted`

#### UpdateUser 方法
- 数据库不可用检查 → `database_error`
- 用户不存在 → `user_not_found`
- 年龄范围验证 → `age_out_of_range`
- 邮箱唯一性检查 → `email_already_in_use`
- 数据库更新失败 → `database_error`

#### DeleteUser 方法
- 数据库不可用检查 → `database_error`
- 用户不存在 → `user_not_found`
- 用户已删除检查 → `user_deleted`
- 数据库删除失败 → `database_error`

#### ListUsers & StreamUsers 方法
- 数据库不可用检查 → `database_error`
- 查询失败 → `database_error`

### 客户端错误处理建议

```javascript
// JavaScript 示例
async function getUser(userId) {
  try {
    const response = await fetch(`/v1/users/${userId}`);
    const data = await response.json();

    if (!response.ok) {
      switch (data.code) {
        case 4004:
          console.log('用户不存在');
          break;
        case 4011:
          console.log('用户已被删除');
          break;
        case 5027:
          console.log('数据库错误，请稍后重试');
          break;
        default:
          console.log('未知错误:', data.message);
      }
      return null;
    }

    return data.user;
  } catch (error) {
    console.error('网络错误:', error);
    return null;
  }
}
```

### 添加自定义错误码

要添加新的错误码，按以下步骤操作：

1. 在 `proto/error.proto` 中添加错误码定义：
```protobuf
enum ErrorCode {
  // ... 现有错误码 ...

  // 添加新错误码，使用 4xxx (客户端) 或 5xxx (服务端)
  custom_error = 4050;
}
```

2. 重新生成 proto 代码：
```bash
make build
```

3. 在业务逻辑中使用新错误码：
```go
return nil, status.Error(codes.Code(pb.ErrorCode_custom_error),
    "custom error message")
```

## Dependencies 依赖管理

项目中所有的**外部依赖**——包括数据库、缓存、消息队列、HTTP/gRPC 下游服务、AI 模型服务等——都建议通过 **URI** 的形式定义，并统一通过 `Dependencies` 结构体接入。

这样做的好处：
- **配置即连接**：一个 URI 字符串包含协议、地址、认证和参数，无需为每种依赖单独编写初始化逻辑
- **统一生命周期**：框架自动并发初始化所有依赖，并在进程退出时自动调用 `Close` 进行优雅关闭
- **环境无关**：开发/测试/生产只需切换配置文件中的 URI，代码零修改

### 核心原理

`config.Init()` 在加载配置后，自动扫描 Config 中嵌入了 `dependencies.Dependency` 的 struct 字段，将 YAML 中对应的 key-value（字段名→URI）传入 `dependencies.Init()` 进行初始化。

对每个字段，框架按以下优先级尝试初始化：

1. **指针类型** (`*T`)：自动 `reflect.New(T)`，然后查找并调用 `Init(context.Context, *url.URL) error` 方法
2. **接口类型** (`interface`)：通过 `WithNewFns()` 注册的工厂函数创建实例
3. 如果都没有，返回错误并提示注册方式

### 定义 Dependencies 结构体

```go
// service/dependencies.go
package service

import (
    "github.com/ti/common-go/dependencies"
    "github.com/ti/common-go/dependencies/database"
    "github.com/ti/common-go/dependencies/redis"
    "github.com/ti/common-go/dependencies/broker"
    dephttp "github.com/ti/common-go/dependencies/http"
    "github.com/ti/common-go/dependencies/mqlru"
    pb "your/project/proto"
)

type Dependencies struct {
    dependencies.Dependency                           // 必须嵌入，作为第一个匿名字段

    // ——— 存储类 ———
    DB    *database.DB   `required:"false"`           // 数据库（mock/mongo/mysql/postgres）
    Redis *redis.Redis   `required:"false"`           // 缓存

    // ——— 消息队列 ———
    Broker *broker.Broker `required:"false"`           // MQ（kafka 等）
    Cache  *mqlru.Lru     `required:"false"`           // 带 MQ 同步的 LRU 缓存

    // ——— 下游服务 ———
    PaymentAPI *dephttp.HTTP            `required:"false"` // HTTP 下游
    UserSvc    pb.UserServiceClient     `required:"false"` // gRPC 下游（接口类型）

    // ——— AI 模型 ———
    LLM     *dephttp.HTTP `required:"false"`           // AI 模型 API
}
```

**规则：**
- **第一个字段**必须是匿名嵌入的 `dependencies.Dependency`
- 字段类型必须是**指针** (`*T`) 或**接口** (`interface`)
- 字段名（小写化后）对应 YAML 配置中的 key
- 默认 `required:"true"`，加 `required:"false"` 标记为可选（URI 为空时跳过，不报错）

### YAML 配置

```yaml
dependencies:
    # 存储
    db: "mock://local/myapp"
    redis: "redis://:password@127.0.0.1:6379?db=0"

    # 消息队列
    broker: "kafka://127.0.0.1:9092/events"
    cache: "cache://memory?ttl=5m&capacity=1000"

    # 下游 HTTP 服务
    paymentAPI: "http://payment.internal:8080?try=3&timeout=5s&log=true"

    # 下游 gRPC 服务
    userSvc: "dns://user-service.ns.svc:8081?log=true&metrics=true"

    # AI 模型服务
    llm: "http://llm-gateway.internal:8080/v1/chat/completions?timeout=30s&try=2&log=true"
```

### main.go 初始化

```go
type Config struct {
    Dependencies service.Dependencies   // 框架自动发现并初始化
    Service      service.Config
    Apis         grpcmux.Config
}

func main() {
    var cfg Config
    err := config.Init(context.Background(), "", &cfg,
        // 接口类型的字段需要注册工厂函数
        dependencies.WithNewFns(
            database.New,              // database.Database 接口
            pb.NewUserServiceClient,   // gRPC 客户端接口
        ),
    )
    if err != nil {
        log.Action("InitConfig").Fatal(err.Error())
    }
    // cfg.Dependencies.DB、cfg.Dependencies.Redis 等已自动初始化完成
}
```

### 内置依赖类型

| 类型 | 字段类型 | URI 格式 | 初始化方式 |
|------|---------|----------|-----------|
| Mock DB | `*database.DB` | `mock://local/dbname` | 自动 Init |
| MongoDB | `*database.DB` | `mongodb://user:pass@host/db` | 自动 Init |
| MySQL | `*database.DB` | `mysql://user:pass@tcp(host:3306)/db` | 自动 Init |
| PostgreSQL | `*database.DB` | `postgres://user:pass@host:5432/db` | 自动 Init |
| Redis | `*redis.Redis` | `redis://:pass@host:6379?db=0` | 自动 Init |
| Redis (TLS) | `*redis.Redis` | `rediss://:pass@host:6379` | 自动 Init |
| HTTP 客户端 | `*dephttp.HTTP` | `http://host?try=3&timeout=5s&log=true` | 自动 Init |
| Broker (Kafka) | `*broker.Broker` | `kafka://host:9092/topic` | 自动 Init |
| LRU 缓存 | `*mqlru.Lru` | `cache://memory?ttl=5m&capacity=1000` | 自动 Init |
| gRPC 客户端 | `pb.XxxClient` (接口) | `dns://svc:8081?log=true` | WithNewFns |

### 自定义依赖

当你需要接入一个框架未内置的外部服务（如第三方 SDK、AI 平台等），只需让结构体实现 `Init` 方法：

```go
type MySDK struct {
    client *somepackage.Client
}

// Init 实现 Init(context.Context, *url.URL) error 接口
// 框架自动调用，无需手动注册
func (s *MySDK) Init(ctx context.Context, u *url.URL) error {
    apiKey := u.User.Username()
    secret, _ := u.User.Password()
    region := u.Query().Get("region")
    s.client = somepackage.NewClient(u.Host, apiKey, secret, region)
    return s.client.Ping(ctx)
}

// Close 可选实现，框架在 graceful shutdown 时自动调用
func (s *MySDK) Close(ctx context.Context) error {
    return s.client.Close()
}
```

在 Dependencies 中直接使用：

```go
type Dependencies struct {
    dependencies.Dependency
    MySDK *MySDK                      // 配置: mySDK: "custom://apiKey:secret@host:9090?region=us-east-1"
}
```

### URI 参数约定

各内置依赖支持的通用查询参数：

**HTTP 客户端 (`dephttp.HTTP`):**
| 参数 | 说明 | 示例 |
|------|------|------|
| `timeout` | 请求超时 | `timeout=5s` |
| `try` | 重试次数 | `try=3` |
| `log` | 启用日志 | `log=true` |
| `logBody` | 记录请求/响应体 | `logBody=true` |
| `tracing` | 启用 OpenTelemetry 追踪 | `tracing=true` |
| `metrics` | 启用 Prometheus 指标 | `metrics=true` |
| `proxy` | HTTP 代理 | `proxy=http://proxy:1080` |

**Redis:**
| 参数 | 说明 | 示例 |
|------|------|------|
| `db` | 数据库编号 | `db=1` |
| `master` | Sentinel master 名称 | `master=mymaster` |
| `cache` | 启用客户端缓存 | `cache=true` |
| `shuffle` | 随机化初始连接顺序 | `shuffle=true` |

**LRU 缓存 (`mqlru.Lru`):**
| 参数 | 说明 | 示例 |
|------|------|------|
| `ttl` | 缓存过期时间 | `ttl=5m` |
| `capacity` | 最大缓存条目数 | `capacity=1000` |
| `touch` | 访问时刷新 TTL | `touch=false` |
| `mq` | 启用 MQ 同步 | `mq=false` |

### 多层依赖（分组）

当依赖数量较多时，可以用嵌套 struct 分组管理：

```go
type Dependencies struct {
    dependencies.Dependency
    Storage  StorageDeps
    Services ServiceDeps
}

type StorageDeps struct {
    DB    *database.DB
    Redis *redis.Redis
}

type ServiceDeps struct {
    UserSvc pb.UserServiceClient
}
```

```yaml
dependencies:
    storage:
        db: "mongodb://localhost/myapp"
        redis: "redis://localhost:6379"
    services:
        userSvc: "dns://user-svc:8081?log=true"
```

框架自动检测嵌套结构，切换为 `InitMulti` 模式，按分组**并发**初始化。

### 最佳实践

1. **所有外部依赖都通过 URI 定义**：无论是数据库、缓存、消息队列、下游微服务还是 AI 模型 API，统一用 URI 描述连接信息，让配置文件成为唯一的环境差异来源
2. **用 `required:"false"` 标记可选依赖**：避免因某个非核心服务不可用而阻塞启动
3. **自定义依赖实现 `Init` + `Close`**：融入框架的自动初始化和优雅关闭机制
4. **善用 URI 查询参数**：超时、重试、日志、追踪等行为通过 URI 参数控制，不侵入业务代码