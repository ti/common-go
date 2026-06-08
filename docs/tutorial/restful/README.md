# RESTful Tutorial

This tutorial explains how to build HTTP RESTful APIs using gRPC, and demonstrates UserService CRUD operations.

## By using this approach to build applications, you will get:

1. **Proto-First Design**: Using the proto + implement pattern, a modern development approach with decoupled protocol and implementation
2. **Automatic Parameter Validation**: Leveraging proto + validate for input validation, eliminating extensive validation logic in business code
3. **Dual Protocol Support**: APIs accessible via both HTTP and gRPC
4. **Automatic Documentation Generation**: Auto-generated Swagger documentation
5. **Type Safety**: Using protobuf wrapper types, supporting all MongoDB codecs types
6. **Flexible JSON Format**: Supporting both camelCase and snake_case JSON naming styles

## Quick Start

### Run the Main Server (Using Configuration File)

```bash
cd docs/tutorial/restful
go run main.go
```

### Run the camelCase Format Server (Port 8080)

```bash
cd docs/tutorial/restful
go run cmd/camelCase/main.go
```

### Run the snake_case Format Server (Port 8082)

```bash
cd docs/tutorial/restful
go run cmd/snakeCase/main.go
```

## API Test Examples

This tutorial provides complete UserService CRUD operation examples, demonstrating the use of all protobuf wrapper types.

### 1. Create User (CreateUser)

**camelCase format (Port 8080):**

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

**Response:**
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

**snake_case format (Port 8082):**

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

**Response:**
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

### 2. Get User (GetUser)

```bash
# camelCase format
curl -X GET http://127.0.0.1:8080/v1/users/1769969638553951322 \
  -H "Content-Type: application/json"

# snake_case format
curl -X GET http://127.0.0.1:8082/v1/users/1769971219958902103 \
  -H "Content-Type: application/json"
```

### 3. Update User (UpdateUser)

Supports partial updates, only the fields to be modified need to be provided:

```bash
curl -X PUT http://127.0.0.1:8080/v1/users/1769969638553951322 \
  -H "Content-Type: application/json" \
  -d '{
    "isActive": true,
    "isVerified": true,
    "rating": 4.8
  }'
```

**Response:**
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

### 4. List Users (ListUsers - PageQuery)

Uses `PageQueryRequest` to implement page-based pagination, suitable for traditional pagination scenarios:

**Basic Paginated Query:**

```bash
# Page 1, 10 items per page
curl -X GET "http://127.0.0.1:8080/v1/users?page=1&limit=10" \
  -H "Content-Type: application/json"

# Page 2, 2 items per page
curl -X GET "http://127.0.0.1:8080/v1/users?page=2&limit=2" \
  -H "Content-Type: application/json"
```

**Query with Sorting:**

```bash
# Sort by age descending
curl -X GET "http://127.0.0.1:8080/v1/users?page=1&limit=10&sort=-age" \
  -H "Content-Type: application/json"

# Multi-field sort: by age descending, then name ascending
curl -X GET "http://127.0.0.1:8080/v1/users?page=1&limit=10&sort=-age&sort=name" \
  -H "Content-Type: application/json"
```

**Response:**
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

**PageQueryRequest Supported Query Parameters:**
- `page`: Page number (starting from 1, default: 1)
- `limit`: Number of items per page (default: 10)
- `sort`: Sort fields, supports multiple. Use `-` prefix for descending order, e.g., `-age`
- `select`: Select fields to return (optional)

### 5. Stream Users (StreamUsers - StreamQuery)

Uses `StreamQueryRequest` to implement cursor-based pagination, suitable for efficient traversal of large datasets:

**First Query (Get first 2 items):**

```bash
curl -X GET "http://127.0.0.1:8080/v1/users/stream?limit=2" \
  -H "Content-Type: application/json"
```

**Response:**
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

**Use pageToken to Get the Next Page:**

```bash
curl -X GET "http://127.0.0.1:8080/v1/users/stream?limit=2&pageToken=1769974891318458409" \
  -H "Content-Type: application/json"
```

**Response:**
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

**StreamQueryRequest Supported Query Parameters:**
- `pageToken`: Cursor token (obtained from the previous response's pageToken)
- `limit`: Number of items per page (default: 10)
- `ascending`: Sort direction (true: ascending, false: descending, default: false)
- `select`: Select fields to return (optional)

**PageQuery vs StreamQuery:**
- **PageQuery**: Page-based pagination, suitable for scenarios requiring page jumping (e.g., UI paginators)
- **StreamQuery**: Cursor-based pagination, suitable for sequential traversal of large datasets, better performance, no deep pagination issues

### 6. Delete User (DeleteUser)

```bash
curl -X DELETE http://127.0.0.1:8080/v1/users/1769969638553951322 \
  -H "Content-Type: application/json"
```

**Response:**
```json
{
  "success": true,
  "message": "User 1769969638553951322 deleted successfully"
}
```

## Development Steps

1. **Define Proto**: Define your API and data structures in `proto/main.proto`
   - Use protobuf wrapper types (Int32Value, BoolValue, StringValue, etc.) for optional fields
   - Avoid using ambiguous types like `any` or `struct`, maintain type safety

2. **Compile Proto**: Run `make build` to generate Go code
   ```bash
   make build
   ```

3. **Implement Service**: Refer to `service/user_service.go` to implement your business logic
   - Implement the service interface defined in proto
   - Use Mock Database for local testing

4. **Register Service**: Register your service in `main.go`
   ```go
   userSrv := service.NewUserServiceServer(&cfg.Dependencies, &cfg.Service)
   pb.RegisterUserServiceServer(gs, userSrv)
   pb.RegisterUserServiceHandlerServer(context.Background(), gs.ServeMux(), userSrv)
   ```

5. **Run Service**:
   ```bash
   go run main.go
   ```

## Query Support

This project uses the `dependencies/database/query` package to provide efficient query functionality, supporting two pagination modes.

**Important - Query Type Naming Conventions**:

When defining APIs, it is **strongly recommended to directly use standard query request types**:
- **PageQueryRequest** - For page-based paginated queries (generic, reusable)
- **StreamQueryRequest** - For cursor-based streaming queries (generic, reusable)

Response types should be **named according to specific business resources**:
- **PageUsersResponse** - User paginated query response
- **StreamUsersResponse** - User streaming query response
- **PageOrdersResponse** - Order paginated query response (example)
- **StreamOrdersResponse** - Order streaming query response (example)

**Advantages**:
- Unified request types, reducing duplicate definitions
- More standardized and consistent API interfaces
- Easy to understand and maintain
- Response types clearly distinguish different business resources

### PageQuery - Page-Based Pagination

Uses the `query.PageQuery` function, suitable for traditional page-based pagination scenarios:

```go
resp, err := query.PageQuery[User](ctx, s.dep.DB, "users", &database.PageQueryRequest{
    Page:  1,
    Limit: 10,
    Sort:  []string{"-created_at"},
})
```

**Features:**
- Supports jumping to any page
- Suitable for UI paginators
- Returns total count with each query

**Request Structure (PageQueryRequest - Generic Standard):**
```protobuf
message PageQueryRequest {
    int32 page = 1;              // Page number (starting from 1)
    int32 limit = 2;             // Items per page
    repeated string select = 3;  // Select fields to return
    repeated string sort = 4;    // Sort (- prefix for descending)
}
```

**Note**: `PageQueryRequest` and `StreamQueryRequest` are **generic standard request types** and are recommended for direct use in all paginated APIs without redefining for each resource.

**Response Structure (PageUsersResponse - Business Specific):**
```protobuf
message PageUsersResponse {
    repeated User data = 1;  // User data
    int64 total = 2;         // Total record count
}
```

**Note**: Response type names should be named according to business scenarios (e.g., `PageUsersResponse`, `PageOrdersResponse`) to distinguish different resource types.

### StreamQuery - Cursor-Based Pagination

Uses the `query.StreamQuery` function, suitable for efficient traversal of large datasets:

```go
resp, err := query.StreamQuery[User](ctx, s.dep.DB, "users", &database.StreamQueryRequest{
    PageToken: "",  // Empty for first query
    PageField: "user_id",
    Limit:     10,
    Ascending: false,
})
```

**Features:**
- Uses cursor (page_token) instead of page numbers
- Avoids deep pagination performance issues
- Suitable for sequential traversal of large datasets
- Stable performance, unaffected by data volume

**Request Structure (StreamQueryRequest - Generic Standard):**
```protobuf
message StreamQueryRequest {
    string page_token = 1;       // Cursor token
    int32 limit = 2;             // Items per page
    repeated string select = 3;  // Select fields to return
    bool ascending = 4;          // Sort direction
}
```

**Response Structure (StreamUsersResponse - Business Specific):**
```protobuf
message StreamUsersResponse {
    string page_token = 1;   // Next page cursor
    repeated User data = 2;  // User data
    int64 total = 3;         // Total record count
}
```

**Note**: Response type names should be named according to business scenarios (e.g., `StreamUsersResponse`, `StreamOrdersResponse`) to distinguish different resource types.

### Use Case Comparison

| Scenario | Recommended Method | Reason |
|----------|-------------------|--------|
| UI paginator (needs page jumping) | PageQuery | Supports direct jump to any page |
| Data export | StreamQuery | Stable performance, suitable for large datasets |
| Infinite scrolling | StreamQuery | Sequential loading, better performance |
| Data synchronization | StreamQuery | Cursor guarantees no data loss |
| Search results (<1000 items) | PageQuery | Simple and intuitive |
| Log queries | StreamQuery | Large data volume, sequential access |

## Proto Type Support

This tutorial demonstrates all protobuf types supported by MongoDB codecs:

### Wrapper Types (Optional Fields)
- `google.protobuf.Int32Value` - 32-bit integer (e.g., age)
- `google.protobuf.Int64Value` - 64-bit integer (e.g., referrerId, loginCount)
- `google.protobuf.BoolValue` - Boolean (e.g., isActive, isVerified, isPremium)
- `google.protobuf.StringValue` - String (e.g., phoneNumber, address, bio)
- `google.protobuf.DoubleValue` - Double-precision float (e.g., accountBalance, rating)
- `google.protobuf.FloatValue` - Single-precision float (e.g., discountRate)
- `google.protobuf.UInt32Value` - 32-bit unsigned integer (e.g., failedLoginAttempts)
- `google.protobuf.UInt64Value` - 64-bit unsigned integer (e.g., totalSpent)
- `google.protobuf.BytesValue` - Byte array (e.g., profilePicture, publicKey)

### Timestamp Types
- `google.protobuf.Timestamp` - Timestamp (e.g., createdAt, updatedAt, lastLoginAt)

Benefits of using wrapper types:
- Can distinguish between "not set" and "set to zero value"
- Support partial updates (only update provided fields)
- Type safe, avoiding the use of `any` or `struct`

## Database Support

The project supports multiple database types by importing the appropriate driver and configuring the connection string:

### Mock Database (For Testing)
```go
import _ "github.com/ti/common-go/dependencies/database/mock"
```
Config: `db: "mock://local/myapp"`

### MongoDB
```go
import _ "github.com/ti/common-go/dependencies/mongodb"
```
Config: `db: "mongodb://localhost:27017/myapp"`

### MySQL
```go
import _ "github.com/ti/common-go/dependencies/sql"
```
Config: `db: "mysql://root:password@tcp(localhost:3306)/myapp"`

### PostgreSQL
```go
import _ "github.com/ti/common-go/dependencies/sql"
```
Config: `db: "postgres://user:pass@localhost:5432/myapp"`

See comments in `service/dependencies.go` for detailed database configuration instructions.

## CORS Configuration

grpcmux has built-in CORS support, allowing all cross-origin requests by default. It can be customized via the `WithCORS` option:

### Default Configuration (Allow All Origins)

```go
gs := grpcmux.NewServer(
    grpcmux.WithCORS(grpcmux.CORSConfig{
        AllowedOrigins: []string{"*"},
    }),
)
```

### Restrict to Specific Domains

```go
gs := grpcmux.NewServer(
    grpcmux.WithCORS(grpcmux.CORSConfig{
        AllowedOrigins: []string{"https://example.com", "https://app.example.com"},
        ExposeHeaders:  []string{"X-Request-Id"},
    }),
)
```

### Add Additional Request Headers

When `AllowedHeaders` is not specified, the following request headers are allowed by default:
`Authorization, Content-Type, Accept, X-Project-Id, X-Device-Id, X-Request-Id, X-Request-Timestamp, Connect-Protocol-Version, Connect-Timeout-Ms, Grpc-Timeout`

The `AllowedHeaders` field is used to **append** additional headers on top of the defaults:

```go
gs := grpcmux.NewServer(
    grpcmux.WithCORS(grpcmux.CORSConfig{
        AllowedOrigins: []string{"*"},
        AllowedHeaders: []string{"X-Custom-Header", "X-Organization-Id"},
        ExposeHeaders:  []string{"X-Request-Id", "X-Trace-Id"},
    }),
)
```

### Disable CORS (Handled by Reverse Proxy)

```go
gs := grpcmux.NewServer(
    grpcmux.WithCORS(grpcmux.CORSConfig{Disabled: true}),
)
```

### Configure via Configuration File

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

### CORSConfig Field Descriptions

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Disabled` | `bool` | `false` | Completely disable CORS header injection |
| `AllowedOrigins` | `[]string` | `["*"]` | List of allowed Origins, `*` allows all |
| `AllowedHeaders` | `[]string` | `[]` | Additional request headers appended to the default allowed headers |
| `ExposeHeaders` | `[]string` | `[]` | Response headers that frontend JS is allowed to read |

### CORS Coverage

| Path Type | Protected by CORS |
|-----------|:-:|
| gRPC-Gateway REST routes | Yes |
| ConnectRPC routes | Yes |
| Custom HTTP handler (`s.Handle`) | Yes |
| WebSocket (`s.HandleWebSocket`) | No |
| Metrics endpoint | No |

## JWT Auth

grpcmux provides a unified authentication entry point through `WithAuthFunc`. **A single configuration covers gRPC, HTTP (gRPC-Gateway), and ConnectRPC interfaces**.

### Authentication Coverage Principle

The function registered with `WithAuthFunc` is passed to two layers simultaneously:

1. **gRPC interceptor chain** - Covers native gRPC requests (`:8081`)
2. **HTTP mux middleware** - Covers all HTTP routes (gRPC-Gateway + ConnectRPC + custom Handle)

```
                     WithAuthFunc(jwtAuthFunc)
                            |
           +----------------+----------------+
           v                v                v
      gRPC interceptor   mux.authFunc    mux.authFunc
           |                |                |
           v                v                v
      native gRPC :8081  gRPC-Gateway    ConnectRPC + custom HTTP
```

### Basic Usage

```go
import (
    "context"
    "strings"

    "github.com/grpc-ecosystem/go-grpc-middleware/v2/metadata"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

func jwtAuthFunc(ctx context.Context) (context.Context, error) {
    // Extract Authorization header from gRPC metadata
    // (HTTP requests are converted by mux, HTTP headers are automatically injected into metadata)
    token := metadata.ExtractIncoming(ctx).Get("authorization")
    if token == "" {
        return ctx, status.Error(codes.Unauthenticated, "missing authorization")
    }
    token = strings.TrimPrefix(token, "Bearer ")

    // Validate JWT token (replace with your actual verification logic)
    claims, err := verifyJWT(token)
    if err != nil {
        return ctx, status.Error(codes.Unauthenticated, "invalid token")
    }

    // Inject user information into context
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

### Paths That Skip Authentication

Use `WithNoAuthPrefixes` to set path prefixes that do not require authentication:

```go
gs := grpcmux.NewServer(
    grpcmux.WithAuthFunc(jwtAuthFunc),
    grpcmux.WithNoAuthPrefixes(
        "/healthz",           // Health check
        "/v1/auth/",          // Login, registration, etc.
        "/v1/public/",        // Public endpoints
    ),
)
```

This configuration takes effect on both sides:
- **gRPC side**: Matches by `fullMethod` prefix
- **HTTP side**: Matches by `r.URL.Path` prefix

### Authentication Coverage for Each Interface Type

| Interface Type | Auth Method | Coverage |
|---------------|-------------|:---:|
| Native gRPC (`:8081`) | gRPC interceptor | Yes |
| gRPC-Gateway REST | mux middleware | Yes |
| ConnectRPC | mux middleware (wrapped by `mux.Middleware`) | Yes |
| Custom HTTP (`s.Handle`) | mux middleware | Yes |
| WebSocket (`s.HandleWebSocket`) | Must be handled within the handler | No |
| Metrics (`:9090`) | Separate HTTP Server | No |

## JSON Format Control

The project supports two JSON naming formats:

### camelCase Format (Enabled Explicitly)
```go
gs := grpcmux.NewServer(
    grpcmux.WithUseCamelCase(), // Enable camelCase
)
```
Field examples: `userId`, `isPremium`, `phoneNumber`

### snake_case Format (Default)
```go
gs := grpcmux.NewServer(
    // Without WithUseCamelCase(), snake_case is used
)
```
Field examples: `user_id`, `is_premium`, `phone_number`

See `cmd/camelCase/main.go` and `cmd/snakeCase/main.go` for complete examples.

## Error Handling

This tutorial demonstrates a complete custom error code system following gRPC and HTTP error code conventions.

### Error Code Conventions

Error codes are defined in `proto/error.proto` and follow these conventions:
- **4xxx**: Client errors (invalid input, unauthorized, etc.)
- **5xxx**: Server errors (internal error, service unavailable, etc.)

### Register Error Codes

Register custom error codes during service initialization so the grpcmux framework can correctly map them to HTTP status codes:

```go
func NewUserServiceServer(dep *Dependencies, cfg *Config) *UserServiceServer {
    // Register custom error codes
    mux.RegisterErrorCodes(pb.ErrorCode_name)

    return &UserServiceServer{
        dep: dep,
        cfg: cfg,
    }
}
```

### Using Error Codes

Use custom error codes in business logic:

```go
// User not found
return nil, status.Error(codes.Code(pb.ErrorCode_user_not_found),
    fmt.Sprintf("user with ID %d not found", req.UserId))

// Email already in use
return nil, status.Error(codes.Code(pb.ErrorCode_email_already_in_use),
    fmt.Sprintf("email %s is already in use", req.Email))

// Age out of range
return nil, status.Error(codes.Code(pb.ErrorCode_age_out_of_range),
    fmt.Sprintf("age %d is out of valid range (0-150)", age))
```

### Common Error Examples

#### 1. User Not Found (user_not_found - 4004)

```bash
curl -X GET http://127.0.0.1:8080/v1/users/999999999 \
  -H "Content-Type: application/json"
```

**Error Response:**
```json
{
  "code": 4004,
  "message": "user with ID 999999999 not found"
}
```

#### 2. Email Already In Use (email_already_in_use - 4010)

```bash
# First create a user
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Test User", "email": "test@example.com"}'

# Try to create another user with the same email
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Another User", "email": "test@example.com"}'
```

**Error Response:**
```json
{
  "code": 4010,
  "message": "email test@example.com is already in use"
}
```

#### 3. Age Out of Range (age_out_of_range - 4031)

```bash
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name": "Invalid Age", "email": "invalid@example.com", "age": 200}'
```

**Error Response:**
```json
{
  "code": 4031,
  "message": "age 200 is out of valid range (0-150)"
}
```

#### 4. User Deleted (user_deleted - 4011)

When attempting to access a deleted user (`isActive = false`):

```bash
curl -X GET http://127.0.0.1:8080/v1/users/{deleted_user_id} \
  -H "Content-Type: application/json"
```

**Error Response:**
```json
{
  "code": 4011,
  "message": "user with ID {deleted_user_id} has been deleted"
}
```

#### 5. Database Error (database_error - 5027)

When the database is unavailable or an operation fails:

```json
{
  "code": 5027,
  "message": "database not available"
}
```

### Complete Error Code List

| Error Code | Name | Description |
|-----------|------|-------------|
| 0 | OK | Success |
| 4001 | captcha_required | Captcha required |
| 4002 | captcha_invalid | Captcha invalid |
| 4004 | user_not_found | User not found |
| 4009 | user_already_exists | User already exists |
| 4010 | email_already_in_use | Email already in use |
| 4011 | user_deleted | User has been deleted |
| 4012 | user_not_activated | User not activated |
| 4020 | invalid_user_data | Invalid user data |
| 4021 | invalid_request | OAuth2: Invalid request |
| 4022 | unauthorized_client | OAuth2: Unauthorized client |
| 4023 | access_denied | OAuth2: Access denied |
| 4024 | unsupported_response_type | OAuth2: Unsupported response type |
| 4025 | invalid_scope | OAuth2: Invalid scope |
| 4026 | invalid_grant | OAuth2: Invalid grant |
| 4030 | payment_required | Payment required |
| 4031 | age_out_of_range | Age out of range |
| 4032 | insufficient_balance | Insufficient balance |
| 4033 | premium_required | Premium membership required |
| 5026 | server_error | Server error |
| 5027 | database_error | Database error |
| 5028 | service_unavailable | Service unavailable |

### Implemented Error Handling Scenarios

#### CreateUser Method
- Database unavailability check -> `database_error`
- Email uniqueness check -> `email_already_in_use`
- Age range validation -> `age_out_of_range`
- Database insert failure -> `database_error`

#### GetUser Method
- Database unavailability check -> `database_error`
- User not found -> `user_not_found`
- User deleted check -> `user_deleted`

#### UpdateUser Method
- Database unavailability check -> `database_error`
- User not found -> `user_not_found`
- Age range validation -> `age_out_of_range`
- Email uniqueness check -> `email_already_in_use`
- Database update failure -> `database_error`

#### DeleteUser Method
- Database unavailability check -> `database_error`
- User not found -> `user_not_found`
- User deleted check -> `user_deleted`
- Database delete failure -> `database_error`

#### ListUsers & StreamUsers Methods
- Database unavailability check -> `database_error`
- Query failure -> `database_error`

### Client-Side Error Handling Suggestions

```javascript
// JavaScript example
async function getUser(userId) {
  try {
    const response = await fetch(`/v1/users/${userId}`);
    const data = await response.json();

    if (!response.ok) {
      switch (data.code) {
        case 4004:
          console.log('User not found');
          break;
        case 4011:
          console.log('User has been deleted');
          break;
        case 5027:
          console.log('Database error, please try again later');
          break;
        default:
          console.log('Unknown error:', data.message);
      }
      return null;
    }

    return data.user;
  } catch (error) {
    console.error('Network error:', error);
    return null;
  }
}
```

### Adding Custom Error Codes

To add new error codes, follow these steps:

1. Add error code definitions in `proto/error.proto`:
```protobuf
enum ErrorCode {
  // ... existing error codes ...

  // Add new error code, use 4xxx (client) or 5xxx (server)
  custom_error = 4050;
}
```

2. Regenerate proto code:
```bash
make build
```

3. Use the new error code in business logic:
```go
return nil, status.Error(codes.Code(pb.ErrorCode_custom_error),
    "custom error message")
```

## Dependencies Management

All **external dependencies** in the project - including databases, caches, message queues, HTTP/gRPC downstream services, AI model services, etc. - are recommended to be defined via **URI** format and accessed uniformly through the `Dependencies` struct.

Benefits of this approach:
- **Configuration as connection**: A single URI string contains protocol, address, authentication, and parameters, eliminating the need to write separate initialization logic for each dependency
- **Unified lifecycle**: The framework automatically initializes all dependencies concurrently and calls `Close` for graceful shutdown on process exit
- **Environment agnostic**: Development/testing/production only requires switching URIs in the configuration file, with zero code changes

### Core Principle

After loading the configuration, `config.Init()` automatically scans Config struct fields that embed `dependencies.Dependency`, passing the corresponding key-value pairs (field name -> URI) from YAML into `dependencies.Init()` for initialization.

For each field, the framework attempts initialization in the following priority:

1. **Pointer type** (`*T`): Automatically `reflect.New(T)`, then finds and calls the `Init(context.Context, *url.URL) error` method
2. **Interface type** (`interface`): Creates instances using factory functions registered via `WithNewFns()`
3. If neither works, returns an error with registration instructions

### Defining the Dependencies Struct

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
    dependencies.Dependency                           // Must be embedded as the first anonymous field

    // --- Storage ---
    DB    *database.DB   `required:"false"`           // Database (mock/mongo/mysql/postgres)
    Redis *redis.Redis   `required:"false"`           // Cache

    // --- Message Queue ---
    Broker *broker.Broker `required:"false"`           // MQ (kafka, etc.)
    Cache  *mqlru.Lru     `required:"false"`           // LRU cache with MQ synchronization

    // --- Downstream Services ---
    PaymentAPI *dephttp.HTTP            `required:"false"` // HTTP downstream
    UserSvc    pb.UserServiceClient     `required:"false"` // gRPC downstream (interface type)

    // --- AI Models ---
    LLM     *dephttp.HTTP `required:"false"`           // AI model API
}
```

**Rules:**
- The **first field** must be an anonymously embedded `dependencies.Dependency`
- Field types must be **pointer** (`*T`) or **interface** (`interface`)
- Field names (lowercased) correspond to keys in the YAML configuration
- Default is `required:"true"`, add `required:"false"` to mark as optional (skipped without error when URI is empty)

### YAML Configuration

```yaml
dependencies:
    # Storage
    db: "mock://local/myapp"
    redis: "redis://:password@127.0.0.1:6379?db=0"

    # Message Queue
    broker: "kafka://127.0.0.1:9092/events"
    cache: "cache://memory?ttl=5m&capacity=1000"

    # Downstream HTTP services
    paymentAPI: "http://payment.internal:8080?try=3&timeout=5s&log=true"

    # Downstream gRPC services
    userSvc: "dns://user-service.ns.svc:8081?log=true&metrics=true"

    # AI model services
    llm: "http://llm-gateway.internal:8080/v1/chat/completions?timeout=30s&try=2&log=true"
```

### main.go Initialization

```go
type Config struct {
    Dependencies service.Dependencies   // Framework auto-discovers and initializes
    Service      service.Config
    Apis         grpcmux.Config
}

func main() {
    var cfg Config
    err := config.Init(context.Background(), "", &cfg,
        // Interface type fields require registered factory functions
        dependencies.WithNewFns(
            database.New,              // database.Database interface
            pb.NewUserServiceClient,   // gRPC client interface
        ),
    )
    if err != nil {
        log.Action("InitConfig").Fatal(err.Error())
    }
    // cfg.Dependencies.DB, cfg.Dependencies.Redis, etc. are already auto-initialized
}
```

### Built-in Dependency Types

| Type | Field Type | URI Format | Initialization |
|------|-----------|------------|----------------|
| Mock DB | `*database.DB` | `mock://local/dbname` | Auto Init |
| MongoDB | `*database.DB` | `mongodb://user:pass@host/db` | Auto Init |
| MySQL | `*database.DB` | `mysql://user:pass@tcp(host:3306)/db` | Auto Init |
| PostgreSQL | `*database.DB` | `postgres://user:pass@host:5432/db` | Auto Init |
| Redis | `*redis.Redis` | `redis://:pass@host:6379?db=0` | Auto Init |
| Redis (TLS) | `*redis.Redis` | `rediss://:pass@host:6379` | Auto Init |
| HTTP Client | `*dephttp.HTTP` | `http://host?try=3&timeout=5s&log=true` | Auto Init |
| Broker (Kafka) | `*broker.Broker` | `kafka://host:9092/topic` | Auto Init |
| LRU Cache | `*mqlru.Lru` | `cache://memory?ttl=5m&capacity=1000` | Auto Init |
| gRPC Client | `pb.XxxClient` (interface) | `dns://svc:8081?log=true` | WithNewFns |

### Custom Dependencies

When you need to integrate an external service not built into the framework (e.g., third-party SDK, AI platform, etc.), simply implement the `Init` method on your struct:

```go
type MySDK struct {
    client *somepackage.Client
}

// Init implements the Init(context.Context, *url.URL) error interface
// The framework calls this automatically, no manual registration needed
func (s *MySDK) Init(ctx context.Context, u *url.URL) error {
    apiKey := u.User.Username()
    secret, _ := u.User.Password()
    region := u.Query().Get("region")
    s.client = somepackage.NewClient(u.Host, apiKey, secret, region)
    return s.client.Ping(ctx)
}

// Close is optionally implemented, the framework calls it automatically during graceful shutdown
func (s *MySDK) Close(ctx context.Context) error {
    return s.client.Close()
}
```

Use it directly in Dependencies:

```go
type Dependencies struct {
    dependencies.Dependency
    MySDK *MySDK                      // Config: mySDK: "custom://apiKey:secret@host:9090?region=us-east-1"
}
```

### URI Parameter Conventions

Common query parameters supported by built-in dependencies:

**HTTP Client (`dephttp.HTTP`):**
| Parameter | Description | Example |
|-----------|-------------|---------|
| `timeout` | Request timeout | `timeout=5s` |
| `try` | Retry count | `try=3` |
| `log` | Enable logging | `log=true` |
| `logBody` | Log request/response body | `logBody=true` |
| `tracing` | Enable OpenTelemetry tracing | `tracing=true` |
| `metrics` | Enable Prometheus metrics | `metrics=true` |
| `proxy` | HTTP proxy | `proxy=http://proxy:1080` |

**Redis:**
| Parameter | Description | Example |
|-----------|-------------|---------|
| `db` | Database number | `db=1` |
| `master` | Sentinel master name | `master=mymaster` |
| `cache` | Enable client-side caching | `cache=true` |
| `shuffle` | Randomize initial connection order | `shuffle=true` |

**LRU Cache (`mqlru.Lru`):**
| Parameter | Description | Example |
|-----------|-------------|---------|
| `ttl` | Cache expiration time | `ttl=5m` |
| `capacity` | Maximum cache entries | `capacity=1000` |
| `touch` | Refresh TTL on access | `touch=false` |
| `mq` | Enable MQ synchronization | `mq=false` |

### Multi-Level Dependencies (Grouping)

When the number of dependencies is large, you can use nested structs for grouped management:

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

The framework automatically detects nested structures and switches to `InitMulti` mode, initializing groups **concurrently**.

### Best Practices

1. **Define all external dependencies via URI**: Whether it's databases, caches, message queues, downstream microservices, or AI model APIs, uniformly describe connection information with URIs, making the configuration file the single source of environment differences
2. **Mark optional dependencies with `required:"false"`**: Avoid blocking startup due to a non-critical service being unavailable
3. **Custom dependencies should implement `Init` + `Close`**: Integrate with the framework's automatic initialization and graceful shutdown mechanism
4. **Use URI query parameters wisely**: Control timeout, retry, logging, tracing, and other behaviors through URI parameters without invading business code
