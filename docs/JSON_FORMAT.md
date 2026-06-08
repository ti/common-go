# JSON Format Configuration Guide

grpcmux supports two JSON format outputs: **snake_case** and **camelCase**.

## Default Format

By default, grpcmux uses **snake_case**, which is the standard Protocol Buffers format.

```json
{
  "user_id": 123,
  "user_name": "Alice",
  "email_address": "alice@example.com",
  "created_at": "2024-01-01T00:00:00Z"
}
```

Error responses also use snake_case format:
```json
{
  "error": "invalid_argument",
  "error_code": 3,
  "error_description": "Invalid user input"
}
```

## Enabling CamelCase Format

If your frontend requires camelCase format, you can enable it in the following ways:

### Method 1: Using Configuration Options (Recommended)

Add the `useCamelCase` field in the configuration file:

```yaml
# config.yaml
apis:
  grpcAddr: :8081
  httpAddr: :8080
  metricsAddr: :9090
  logBody: false
  useCamelCase: true  # Enable camelCase format
```

Usage in code:

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

    // The configuration automatically applies the useCamelCase setting
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

### Method 2: Using Function Options

Use the `WithUseCamelCase()` option directly in code:

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
        grpcmux.WithUseCamelCase(),  // Enable camelCase format
    )

    pb.RegisterYourServiceServer(server, yourService)
    pb.RegisterYourServiceHandlerServer(context.Background(), server.ServeMux(), yourService)

    server.Start()
}
```

## CamelCase Format Output Example

After enabling camelCase format, the JSON output becomes:

```json
{
  "userId": 123,
  "userName": "Alice",
  "emailAddress": "alice@example.com",
  "createdAt": "2024-01-01T00:00:00Z"
}
```

Error responses also use camelCase format:
```json
{
  "error": "invalid_argument",
  "errorCode": 3,
  "errorDescription": "Invalid user input"
}
```

## Format Comparison

| Proto Field Name | snake_case (Default) | camelCase |
|-----------------|---------------------|-----------|
| user_id | user_id | userId |
| user_name | user_name | userName |
| email_address | email_address | emailAddress |
| created_at | created_at | createdAt |
| is_active | is_active | isActive |

### Error Response Field Comparison

| Proto Field Name | snake_case (Default) | camelCase |
|-----------------|---------------------|-----------|
| error | error | error |
| error_code | error_code | errorCode |
| error_description | error_description | errorDescription |

## Notes

1. **Consistency**: It is recommended to use a uniform format throughout the entire project, either all snake_case or all camelCase.

2. **Proto definitions unchanged**: Regardless of the JSON format used, field name definitions in Proto files remain unchanged (always use snake_case).

3. **gRPC unaffected**: This setting only affects HTTP JSON output; the gRPC protocol is not affected.

4. **Frontend coordination**: If you change the JSON format, ensure the frontend code is updated accordingly.

## Complete Example

Refer to the complete example in the `docs/tutorial/restful` directory:

```bash
cd docs/tutorial/restful
go run main.go
```

Test the API:

```bash
# Default format (snake_case)
curl http://localhost:8080/v1/hello/test
# Returns: {"msg":"hello test"}

# CamelCase format (needs to be enabled in configuration)
curl http://localhost:8080/v1/hello/test
# Returns: {"msg":"hello test"}  # In this example the field happens to be the same
```

## FAQ

**Q: Why is snake_case the default format?**

A: This is the official Protocol Buffers standard, consistent with the cross-language design philosophy.

**Q: Does changing the format affect performance?**

A: No. The serialization performance of both formats is essentially the same.

**Q: Can the format be switched dynamically?**

A: Not recommended. The format should be determined at service startup and remain unchanged throughout the service lifecycle.
