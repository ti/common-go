# CamelCase JSON Format Support Implementation Summary

## Feature Overview

Added JSON format configuration options to grpcmux, supporting switching between **snake_case** and **camelCase** formats.

## Implementation Files

### 1. grpcmux/options.go
- Added `useCamelCase bool` field to the `options` struct
- Added `UseCamelCase bool` field to the `Config` struct
- Added handling of `UseCamelCase` in the `WithConfig()` function
- Added `WithUseCamelCase()` function to enable camelCase format

### 2. grpcmux/server.go
- In the `NewServer()` function, pass the `useCamelCase` option to the mux layer
- Use `mux.WithUseCamelCase()` to pass the configuration

### 3. grpcmux/mux/options.go
- Added `useCamelCase bool` field to the `options` struct
- Implemented `WithUseCamelCase()` function with core logic:
  - Set `UseProtoNames: false` to enable camelCase format
  - Update `bodyMarshaler` for normal responses
  - Update `errorMarshaler` for error responses
  - Ensure response and error formats are consistent

### 4. grpcmux/mux/errorhandler.go
- Added `fallbackCamelCase` constant
- Added `getFallback()` function that returns the appropriate format fallback error message based on configuration
- Updated `httpErrorHandler()` and `routingErrorHandler()` to use `getFallback()`

### 5. grpcmux/mux/middleware.go
- Updated `WriteHTTPErrorResponseWithMarshaler()` to use `getFallback(false)` as the default value

## Usage

### Method 1: Via Configuration File (Recommended)

```yaml
# config.yaml
apis:
  grpcAddr: :8081
  httpAddr: :8080
  metricsAddr: :9090
  useCamelCase: true  # Enable camelCase format
```

```go
server := grpcmux.NewServer(
    grpcmux.WithConfig(&cfg.Apis),
)
```

### Method 2: Via Function Options

```go
server := grpcmux.NewServer(
    grpcmux.WithHTTPAddr(":8080"),
    grpcmux.WithGrpcAddr(":8081"),
    grpcmux.WithUseCamelCase(),  // Enable camelCase format
)
```

## Format Comparison

### Default Format (snake_case)

```json
{
  "user_id": 123,
  "user_name": "Alice",
  "email_address": "alice@example.com"
}
```

Error response:
```json
{
  "error": "invalid_argument",
  "error_code": 3,
  "error_description": "Invalid user input"
}
```

### CamelCase Format

```json
{
  "userId": 123,
  "userName": "Alice",
  "emailAddress": "alice@example.com"
}
```

Error response:
```json
{
  "error": "invalid_argument",
  "errorCode": 3,
  "errorDescription": "Invalid user input"
}
```

## Technical Details

### Key Configuration Options

- `UseProtoNames: true` - Uses Proto field names (snake_case format)
- `UseProtoNames: false` - Uses JSON field names (camelCase format)

### Scope of Impact

1. **Normal API responses**: Controlled by `bodyMarshaler`
2. **Error responses**: Controlled by `errorMarshaler`
3. **Fallback error messages**: Controlled by the `getFallback()` function

### Data Flow

```
User request
    |
grpcmux.NewServer (options.go)
    |
WithUseCamelCase() option
    |
mux.NewServeMux (mux/mux.go)
    |
mux.WithUseCamelCase() (mux/options.go)
    |
Set marshalOptions (UseProtoNames: false)
    |
Update bodyMarshaler and errorMarshaler
    |
HTTP response (camelCase format)
```

## Backward Compatibility

- Fully backward compatible: Default behavior unchanged (snake_case format)
- Opt-in only: CamelCase format is enabled only when explicitly set
- No performance impact: Both formats have essentially the same performance

## Testing Suggestions

### Test snake_case Format (Default)
```bash
curl -X POST http://localhost:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"email_address": "test@example.com", "user_name": "Test"}'
```

### Test camelCase Format
```bash
# After configuring useCamelCase: true
curl -X POST http://localhost:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"emailAddress": "test@example.com", "userName": "Test"}'
```

### Test Error Responses
```bash
# Trigger an error
curl -X POST http://localhost:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"invalid_field": "value"}'

# Observe the error response format
```

## Documentation

- Detailed usage guide: `docs/JSON_FORMAT.md`
- Code example: `docs/examples/json_format_example.go`
- Main README updated with configuration instructions

## Notes

1. **Consistency**: It is recommended to use a uniform format throughout the entire project
2. **Proto definitions unchanged**: Proto files always use snake_case
3. **gRPC unaffected**: This setting only affects HTTP JSON, not the gRPC protocol
4. **Frontend coordination**: After changing the format, ensure the frontend code is updated accordingly

## Implementation Checklist

- [x] Add UseCamelCase option in grpcmux/options.go
- [x] Pass UseCamelCase option in grpcmux/server.go
- [x] Implement UseCamelCase logic in grpcmux/mux/options.go
- [x] Add UseCamelCase configuration item in grpcmux/Config
- [x] Update fallback error messages to support camelCase format
- [x] Create detailed usage documentation
- [x] Create code examples
- [x] Update README.md
- [x] Verify code syntax correctness

## References

- [protojson.MarshalOptions documentation](https://pkg.go.dev/google.golang.org/protobuf/encoding/protojson#MarshalOptions)
- [grpc-gateway documentation](https://github.com/grpc-ecosystem/grpc-gateway)
