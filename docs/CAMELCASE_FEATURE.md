# CamelCase JSON 格式支持实现总结

## 功能概述

为 grpcmux 添加了 JSON 格式配置选项，支持在**下划线格式（snake_case）** 和 **驼峰格式（camelCase）** 之间切换。

## 实现文件

### 1. grpcmux/options.go
- 添加 `useCamelCase bool` 字段到 `options` 结构体
- 添加 `UseCamelCase bool` 字段到 `Config` 结构体
- 在 `WithConfig()` 函数中添加对 `UseCamelCase` 的处理
- 新增 `WithUseCamelCase()` 函数，用于启用驼峰格式

### 2. grpcmux/server.go
- 在 `NewServer()` 函数中，将 `useCamelCase` 选项传递给 mux 层
- 使用 `mux.WithUseCamelCase()` 传递配置

### 3. grpcmux/mux/options.go
- 添加 `useCamelCase bool` 字段到 `options` 结构体
- 实现 `WithUseCamelCase()` 函数，核心逻辑：
  - 设置 `UseProtoNames: false` 以启用驼峰格式
  - 更新 `bodyMarshaler` 用于正常响应
  - 更新 `errorMarshaler` 用于错误响应
  - 确保响应和错误格式一致

### 4. grpcmux/mux/errorhandler.go
- 添加 `fallbackCamelCase` 常量
- 新增 `getFallback()` 函数，根据配置返回相应格式的 fallback 错误消息
- 更新 `httpErrorHandler()` 和 `routingErrorHandler()` 使用 `getFallback()`

### 5. grpcmux/mux/middleware.go
- 更新 `WriteHTTPErrorResponseWithMarshaler()` 使用 `getFallback(false)` 作为默认值

## 使用方式

### 方式一：通过配置文件（推荐）

```yaml
# config.yaml
apis:
  grpcAddr: :8081
  httpAddr: :8080
  metricsAddr: :9090
  useCamelCase: true  # 启用驼峰格式
```

```go
server := grpcmux.NewServer(
    grpcmux.WithConfig(&cfg.Apis),
)
```

### 方式二：通过函数选项

```go
server := grpcmux.NewServer(
    grpcmux.WithHTTPAddr(":8080"),
    grpcmux.WithGrpcAddr(":8081"),
    grpcmux.WithUseCamelCase(),  // 启用驼峰格式
)
```

## 格式对比

### 默认格式（下划线）

```json
{
  "user_id": 123,
  "user_name": "Alice",
  "email_address": "alice@example.com"
}
```

错误响应：
```json
{
  "error": "invalid_argument",
  "error_code": 3,
  "error_description": "Invalid user input"
}
```

### 驼峰格式

```json
{
  "userId": 123,
  "userName": "Alice",
  "emailAddress": "alice@example.com"
}
```

错误响应：
```json
{
  "error": "invalid_argument",
  "errorCode": 3,
  "errorDescription": "Invalid user input"
}
```

## 技术细节

### 关键配置项

- `UseProtoNames: true` → 使用 Proto 字段名（下划线格式）
- `UseProtoNames: false` → 使用 JSON 字段名（驼峰格式）

### 影响范围

1. **正常 API 响应**：通过 `bodyMarshaler` 控制
2. **错误响应**：通过 `errorMarshaler` 控制
3. **Fallback 错误消息**：通过 `getFallback()` 函数控制

### 数据流

```
用户请求
    ↓
grpcmux.NewServer (options.go)
    ↓
WithUseCamelCase() 选项
    ↓
mux.NewServeMux (mux/mux.go)
    ↓
mux.WithUseCamelCase() (mux/options.go)
    ↓
设置 marshalOptions (UseProtoNames: false)
    ↓
更新 bodyMarshaler 和 errorMarshaler
    ↓
HTTP 响应 (驼峰格式)
```

## 向后兼容性

- ✅ **完全向后兼容**：默认行为不变（下划线格式）
- ✅ **可选启用**：只有明确设置时才启用驼峰格式
- ✅ **无性能影响**：两种格式性能基本相同

## 测试建议

### 测试下划线格式（默认）
```bash
curl -X POST http://localhost:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"email_address": "test@example.com", "user_name": "Test"}'
```

### 测试驼峰格式
```bash
# 配置 useCamelCase: true 后
curl -X POST http://localhost:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"emailAddress": "test@example.com", "userName": "Test"}'
```

### 测试错误响应
```bash
# 触发一个错误
curl -X POST http://localhost:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"invalid_field": "value"}'

# 观察错误响应格式
```

## 文档

- 详细使用指南：`docs/JSON_FORMAT.md`
- 代码示例：`docs/examples/json_format_example.go`
- 主 README 已更新，添加了配置说明

## 注意事项

1. **一致性**：建议在整个项目中使用统一的格式
2. **Proto 定义不变**：Proto 文件始终使用 snake_case
3. **gRPC 不受影响**：此设置仅影响 HTTP JSON，不影响 gRPC 协议
4. **前后端协调**：修改格式后，确保前端代码也做相应调整

## 实现完成清单

- [x] 在 grpcmux/options.go 中添加 UseCamelCase 选项
- [x] 在 grpcmux/server.go 中传递 UseCamelCase 选项
- [x] 在 grpcmux/mux/options.go 中实现 UseCamelCase 逻辑
- [x] 在 grpcmux/Config 中添加 UseCamelCase 配置项
- [x] 更新 fallback 错误消息支持驼峰格式
- [x] 创建详细的使用文档
- [x] 创建代码示例
- [x] 更新 README.md
- [x] 验证代码语法正确性

## 参考链接

- [protojson.MarshalOptions 文档](https://pkg.go.dev/google.golang.org/protobuf/encoding/protojson#MarshalOptions)
- [grpc-gateway 文档](https://github.com/grpc-ecosystem/grpc-gateway)
