# JSON 格式配置指南

grpcmux 支持两种 JSON 格式输出：**下划线格式（snake_case）** 和 **驼峰格式（camelCase）**。

## 默认格式

默认情况下，grpcmux 使用 **下划线格式（snake_case）**，这是 Protocol Buffers 的标准格式。

```json
{
  "user_id": 123,
  "user_name": "Alice",
  "email_address": "alice@example.com",
  "created_at": "2024-01-01T00:00:00Z"
}
```

错误响应也使用下划线格式：
```json
{
  "error": "invalid_argument",
  "error_code": 3,
  "error_description": "Invalid user input"
}
```

## 启用驼峰格式

如果你的前端需要驼峰格式（camelCase），可以通过以下几种方式启用：

### 方式一：使用配置选项（推荐）

在配置文件中添加 `useCamelCase` 字段：

```yaml
# config.yaml
apis:
  grpcAddr: :8081
  httpAddr: :8080
  metricsAddr: :9090
  logBody: false
  useCamelCase: true  # 启用驼峰格式
```

在代码中使用：

```go
package main

import (
    "context"
    "github.com/ti/common-go/config"
    "github.com/ti/common-go/grpcmux"
    pb "yourproject/pkg/go/proto"
)

func main() {
    var cfg Config
    config.Init(context.Background(), "file://config.yaml", &cfg)

    // 配置会自动应用 useCamelCase 设置
    server := grpcmux.NewServer(
        grpcmux.WithConfig(&cfg.Apis),
    )

    pb.RegisterYourServiceServer(server, yourService)
    pb.RegisterYourServiceHandlerServer(context.Background(), server.ServeMux(), yourService)

    server.Start()
}

type Config struct {
    Apis grpcmux.Config
}
```

### 方式二：使用函数选项

直接在代码中使用 `WithUseCamelCase()` 选项：

```go
package main

import (
    "context"
    "github.com/ti/common-go/grpcmux"
    pb "yourproject/pkg/go/proto"
)

func main() {
    server := grpcmux.NewServer(
        grpcmux.WithHTTPAddr(":8080"),
        grpcmux.WithGrpcAddr(":8081"),
        grpcmux.WithUseCamelCase(),  // 启用驼峰格式
    )

    pb.RegisterYourServiceServer(server, yourService)
    pb.RegisterYourServiceHandlerServer(context.Background(), server.ServeMux(), yourService)

    server.Start()
}
```

## 驼峰格式输出示例

启用驼峰格式后，JSON 输出将变为：

```json
{
  "userId": 123,
  "userName": "Alice",
  "emailAddress": "alice@example.com",
  "createdAt": "2024-01-01T00:00:00Z"
}
```

错误响应也会使用驼峰格式：
```json
{
  "error": "invalid_argument",
  "errorCode": 3,
  "errorDescription": "Invalid user input"
}
```

## 格式对比

| Proto 字段名 | 下划线格式 (默认) | 驼峰格式 |
|-------------|------------------|----------|
| user_id | user_id | userId |
| user_name | user_name | userName |
| email_address | email_address | emailAddress |
| created_at | created_at | createdAt |
| is_active | is_active | isActive |

### 错误响应字段对比

| Proto 字段名 | 下划线格式 (默认) | 驼峰格式 |
|-------------|------------------|----------|
| error | error | error |
| error_code | error_code | errorCode |
| error_description | error_description | errorDescription |

## 注意事项

1. **一致性**：建议在整个项目中使用统一的格式，要么全部使用下划线，要么全部使用驼峰。

2. **Proto 定义不变**：无论使用哪种 JSON 格式，Proto 文件中的字段名定义保持不变（始终使用 snake_case）。

3. **gRPC 不受影响**：此设置仅影响 HTTP JSON 输出，gRPC 协议不受影响。

4. **前后端协调**：如果修改了 JSON 格式，确保前端代码也做相应调整。

## 完整示例

参考 `docs/tutorial/restful` 目录下的完整示例：

```bash
cd docs/tutorial/restful
go run main.go
```

测试 API：

```bash
# 默认格式（下划线）
curl http://localhost:8080/v1/hello/test
# 返回: {"msg":"hello test"}

# 驼峰格式（需要在配置中启用）
curl http://localhost:8080/v1/hello/test
# 返回: {"msg":"hello test"}  # 这个例子中字段恰好相同
```

## 常见问题

**Q: 为什么默认使用下划线格式？**

A: 这是 Protocol Buffers 的官方标准，符合跨语言一致性的设计理念。

**Q: 修改格式会影响性能吗？**

A: 不会。两种格式的序列化性能基本相同。

**Q: 可以动态切换格式吗？**

A: 不建议。格式应该在服务启动时确定，并在整个服务生命周期内保持不变。
