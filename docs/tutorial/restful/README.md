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