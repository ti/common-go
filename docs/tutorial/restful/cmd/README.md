# RESTful API 命令行程序

本目录包含两个示例程序，展示如何使用不同的 JSON 命名格式启动 RESTful API 服务器。

## 目录结构

```
cmd/
├── camelCase/          # camelCase JSON 格式服务器
│   └── main.go
├── snakeCase/          # snake_case JSON 格式服务器（默认）
│   └── main.go
├── README.md           # 本文档
└── JSON_FORMAT_COMPARISON.md  # 格式对比测试报告
```

## 程序说明

### 1. camelCase 服务器

**位置**: `cmd/camelCase/main.go`

**特点**:
- 使用 camelCase JSON 格式（驼峰命名）
- HTTP 端口: `8080`
- gRPC 端口: `8081`
- Metrics 端口: `9090`
- 启用 `grpcmux.WithUseCamelCase()` 选项

**JSON 格式示例**:
```json
{
    "user": {
        "userId": "123",
        "userName": "Alice",
        "createdAt": "2026-01-31T10:00:00Z"
    }
}
```

**适用场景**:
- JavaScript/TypeScript 前端项目
- 移动端应用（iOS/Android）
- 需要与 camelCase API 保持一致的项目

---

### 2. snakeCase 服务器

**位置**: `cmd/snakeCase/main.go`

**特点**:
- 使用 snake_case JSON 格式（下划线命名）
- HTTP 端口: `8082`
- gRPC 端口: `8083`
- Metrics 端口: `9091`
- 默认格式，无需额外配置

**JSON 格式示例**:
```json
{
    "user": {
        "user_id": "123",
        "user_name": "Alice",
        "created_at": "2026-01-31T10:00:00Z"
    }
}
```

**适用场景**:
- Python 后端项目
- 数据库字段直接映射
- 传统 RESTful API 标准

---

## 编译和运行

### 编译程序

从项目根目录运行：

```bash
# 编译 camelCase 服务器
go build -o bin/server_camelCase ./docs/tutorial/restful/cmd/camelCase

# 编译 snakeCase 服务器
go build -o bin/server_snakeCase ./docs/tutorial/restful/cmd/snakeCase

# 或者同时编译两个
go build -o bin/server_camelCase ./docs/tutorial/restful/cmd/camelCase && \
go build -o bin/server_snakeCase ./docs/tutorial/restful/cmd/snakeCase
```

### 运行服务器

#### 运行 camelCase 服务器

```bash
# 从项目根目录运行
cd docs/tutorial/restful
./bin/server_camelCase

# 或者直接运行
./bin/server_camelCase -config=docs/tutorial/restful/configs/config.yaml
```

服务器启动后会显示：
```
INFO Starting server with camelCase JSON format
INFO Server ready with camelCase JSON format httpAddr=:8080 grpcAddr=:8081 format=camelCase
```

#### 运行 snakeCase 服务器

```bash
# 从项目根目录运行
cd docs/tutorial/restful
./bin/server_snakeCase

# 或者直接运行
./bin/server_snakeCase -config=docs/tutorial/restful/configs/config.yaml
```

服务器启动后会显示：
```
INFO Starting server with snake_case JSON format (default)
INFO Server ready with snake_case JSON format (default) httpAddr=:8082 grpcAddr=:8083 format=snake_case
```

---

## 测试 API

### camelCase 格式测试

#### 创建用户
```bash
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Alice","email":"alice@example.com","age":25}'
```

**响应**:
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

#### 获取用户
```bash
curl -X GET "http://127.0.0.1:8080/v1/users/1769868266642494615"
```

#### 更新用户
```bash
curl -X PUT "http://127.0.0.1:8080/v1/users/1769868266642494615" \
  -H "Content-Type: application/json" \
  -d '{"userId":"1769868266642494615","name":"Alice Updated","email":"alice.new@example.com","age":26}'
```

#### 列出用户（注意使用 pageSize）
```bash
curl -X GET "http://127.0.0.1:8080/v1/users?page=1&pageSize=10"
```

#### 删除用户
```bash
curl -X DELETE "http://127.0.0.1:8080/v1/users/1769868266642494615"
```

---

### snake_case 格式测试

#### 创建用户
```bash
curl -X POST http://127.0.0.1:8082/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Bob","email":"bob@example.com","age":30}'
```

**响应**:
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

#### 获取用户
```bash
curl -X GET "http://127.0.0.1:8082/v1/users/1769874547784032306"
```

#### 更新用户
```bash
curl -X PUT "http://127.0.0.1:8082/v1/users/1769874547784032306" \
  -H "Content-Type: application/json" \
  -d '{"user_id":"1769874547784032306","name":"Bob Updated","email":"bob.new@example.com","age":31}'
```

#### 列出用户（注意使用 page_size）
```bash
curl -X GET "http://127.0.0.1:8082/v1/users?page=1&page_size=10"
```

#### 删除用户
```bash
curl -X DELETE "http://127.0.0.1:8082/v1/users/1769874547784032306"
```

---

## 同时运行两个服务器

由于两个服务器使用不同的端口，你可以同时运行它们来对比测试：

**终端 1 - camelCase 服务器**:
```bash
cd docs/tutorial/restful
./bin/server_camelCase
```

**终端 2 - snakeCase 服务器**:
```bash
cd docs/tutorial/restful
./bin/server_snakeCase
```

**终端 3 - 测试**:
```bash
# 测试 camelCase 服务器（端口 8080）
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Alice","email":"alice@example.com","age":25}'

# 测试 snakeCase 服务器（端口 8082）
curl -X POST http://127.0.0.1:8082/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Bob","email":"bob@example.com","age":30}'
```

---

## 配置文件

两个程序共享相同的配置文件：`docs/tutorial/restful/configs/config.yaml`

```yaml
dependencies:
    # Mock Database for testing
    db: "mock://local/restful_tutorial"

    # 可选：切换到其他数据库
    # db: "mongodb://localhost:27017/myapp"
    # db: "mysql://root:password@tcp(localhost:3306)/myapp?charset=utf8mb4&parseTime=True"
    # db: "postgres://user:pass@localhost:5432/myapp?sslmode=disable"
```

---

## 关键代码差异

### camelCase 服务器

```go
// 启用 camelCase 格式
gs := grpcmux.NewServer(
    grpcmux.WithHTTPAddr(":8080"),
    grpcmux.WithGrpcAddr(":8081"),
    grpcmux.WithMetricsAddr(":9090"),
    grpcmux.WithUseCamelCase(), // 关键：启用 camelCase
)
```

### snakeCase 服务器

```go
// 使用默认 snake_case 格式
gs := grpcmux.NewServer(
    grpcmux.WithHTTPAddr(":8082"),
    grpcmux.WithGrpcAddr(":8083"),
    grpcmux.WithMetricsAddr(":9091"),
    // 无 WithUseCamelCase() - 使用默认 snake_case
)
```

---

## 注意事项

1. **端口冲突**: 确保端口未被占用
   - camelCase: 8080 (HTTP), 8081 (gRPC), 9090 (Metrics)
   - snakeCase: 8082 (HTTP), 8083 (gRPC), 9091 (Metrics)

2. **字段命名一致性**: 请求体的字段名应该与服务器格式匹配
   - camelCase 服务器: 使用 `userId`, `createdAt`
   - snakeCase 服务器: 使用 `user_id`, `created_at`

3. **查询参数**: URL 查询参数也遵循相同的命名规则
   - camelCase: `?page=1&pageSize=10`
   - snakeCase: `?page=1&page_size=10`

4. **数据库兼容性**: Mock Database 的自动字段标准化功能确保了两种格式都能正常工作

---

## 相关文档

- [JSON_FORMAT_COMPARISON.md](./JSON_FORMAT_COMPARISON.md) - 详细的格式对比测试报告
- [DATABASE_CONFIG.md](../DATABASE_CONFIG.md) - 数据库配置指南
- [README.md](../README.md) - 项目主 README

---

**更新日期**: 2026-01-31
**测试状态**: ✅ 所有 CRUD 操作通过测试
