# JSON Format Comparison Test Report

This document compares the return results of camelCase and snake_case JSON formats in CRUD operations.

## Test Environment

- **camelCase server**: Port 8080 (HTTP), 8081 (gRPC)
- **snakeCase server**: Port 8082 (HTTP), 8083 (gRPC)
- **Database**: Mock Database (in-memory database)

## Key Differences

### camelCase Format
- Uses the `grpcmux.WithUseCamelCase()` option
- Field names use camelCase: `userId`, `createdAt`, `updatedAt`, `pageSize`

### snake_case Format (Default)
- Does not use the `WithUseCamelCase()` option
- Field names use snake_case: `user_id`, `created_at`, `updated_at`, `page_size`

---

## Test Results Comparison

### 1. CreateUser

#### camelCase Request and Response
```bash
# Request
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"TestUser","email":"test@example.com","age":30}'

# Response
{
    "user": {
        "userId": "1769868266642494615",
        "name": "TestUser",
        "email": "test@example.com",
        "age": 30,
        "createdAt": "2026-01-31T14:04:26.642494530Z",
        "updatedAt": "2026-01-31T14:04:26.642494530Z"
    }
}
```

#### snake_case Request and Response
```bash
# Request
curl -X POST http://127.0.0.1:8082/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"TestUser","email":"test@example.com","age":30}'

# Response
{
    "user": {
        "user_id": "1769874547784032306",
        "name": "TestUser",
        "email": "test@example.com",
        "age": 30,
        "created_at": "2026-01-31T15:49:07.784032167Z",
        "updated_at": "2026-01-31T15:49:07.784032167Z"
    }
}
```

**Field Comparison**:
| camelCase | snake_case |
|-----------|------------|
| userId | user_id |
| createdAt | created_at |
| updatedAt | updated_at |

---

### 2. GetUser

#### camelCase Request and Response
```bash
# Request
curl -X GET "http://127.0.0.1:8080/v1/users/1769868266642494615"

# Response
{
    "user": {
        "userId": "1769868266642494615",
        "name": "TestUser",
        "email": "test@example.com",
        "age": 30,
        "createdAt": "2026-01-31T14:04:26.642494530Z",
        "updatedAt": "2026-01-31T14:04:26.642494530Z"
    }
}
```

#### snake_case Request and Response
```bash
# Request
curl -X GET "http://127.0.0.1:8082/v1/users/1769874547784032306"

# Response
{
    "user": {
        "user_id": "1769874547784032306",
        "name": "TestUser",
        "email": "test@example.com",
        "age": 30,
        "created_at": "2026-01-31T15:49:07.784032167Z",
        "updated_at": "2026-01-31T15:49:07.784032167Z"
    }
}
```

---

### 3. UpdateUser

#### camelCase Request and Response
```bash
# Request (note: request body uses userId)
curl -X PUT "http://127.0.0.1:8080/v1/users/1769868266642494615" \
  -H "Content-Type: application/json" \
  -d '{"userId":"1769868266642494615","name":"UpdatedUser","email":"updated@example.com","age":31}'

# Response
{
    "user": {
        "userId": "1769868266642494615",
        "name": "UpdatedUser",
        "email": "updated@example.com",
        "age": 31,
        "createdAt": "2026-01-31T14:04:26.642494530Z",
        "updatedAt": "2026-01-31T14:04:36.162857516Z"
    }
}
```

#### snake_case Request and Response
```bash
# Request (note: request body uses user_id)
curl -X PUT "http://127.0.0.1:8082/v1/users/1769874547784032306" \
  -H "Content-Type: application/json" \
  -d '{"user_id":"1769874547784032306","name":"UpdatedUser","email":"updated@example.com","age":31}'

# Response
{
    "user": {
        "user_id": "1769874547784032306",
        "name": "UpdatedUser",
        "email": "updated@example.com",
        "age": 31,
        "created_at": "2026-01-31T15:49:07.784032167Z",
        "updated_at": "2026-01-31T15:49:15.959740573Z"
    }
}
```

**Key point**: `updatedAt` / `updated_at` timestamp is automatically updated

---

### 4. ListUsers (Paginated)

#### camelCase Request and Response
```bash
# Request (note: query parameter uses pageSize)
curl -X GET "http://127.0.0.1:8080/v1/users?page=1&pageSize=10"

# Response
{
    "users": [
        {
            "userId": "1769868266642494615",
            "name": "UpdatedUser",
            "email": "updated@example.com",
            "age": 31,
            "createdAt": "2026-01-31T14:04:26.642494530Z",
            "updatedAt": "2026-01-31T14:04:36.162857516Z"
        },
        {
            "userId": "1769868281066952197",
            "name": "User2",
            "email": "user2@example.com",
            "age": 25,
            "createdAt": "2026-01-31T14:04:41.066952124Z",
            "updatedAt": "2026-01-31T14:04:41.066952124Z"
        }
    ],
    "total": "2",
    "page": 1,
    "pageSize": 10
}
```

#### snake_case Request and Response
```bash
# Request (note: query parameter uses page_size)
curl -X GET "http://127.0.0.1:8082/v1/users?page=1&page_size=10"

# Response
{
    "users": [
        {
            "user_id": "1769874547784032306",
            "name": "UpdatedUser",
            "email": "updated@example.com",
            "age": 31,
            "created_at": "2026-01-31T15:49:07.784032167Z",
            "updated_at": "2026-01-31T15:49:15.959740573Z"
        },
        {
            "user_id": "1769874560151784593",
            "name": "User2",
            "email": "user2@example.com",
            "age": 25,
            "created_at": "2026-01-31T15:49:20.151784504Z",
            "updated_at": "2026-01-31T15:49:20.151784504Z"
        }
    ],
    "total": "2",
    "page": 1,
    "page_size": 10
}
```

**Query Parameter Comparison**:
| camelCase | snake_case |
|-----------|------------|
| pageSize | page_size |

**Response Field Comparison**:
| camelCase | snake_case |
|-----------|------------|
| pageSize | page_size |

---

### 5. DeleteUser

#### camelCase Request and Response
```bash
# Request
curl -X DELETE "http://127.0.0.1:8080/v1/users/1769868281066952197"

# Response
{
    "success": true,
    "message": "User 1769868281066952197 deleted successfully"
}
```

#### snake_case Request and Response
```bash
# Request
curl -X DELETE "http://127.0.0.1:8082/v1/users/1769874560151784593"

# Response
{
    "success": true,
    "message": "User 1769874560151784593 deleted successfully"
}
```

**Note**: The DeleteUser response fields (success, message) are the same in both formats because these fields do not contain underscores.

---

## Summary

### Test Results

| Operation | camelCase Fields | snake_case Fields | Test Status |
|-----------|-----------------|-------------------|-------------|
| CreateUser | userId, createdAt, updatedAt | user_id, created_at, updated_at | Passed |
| GetUser | userId, createdAt, updatedAt | user_id, created_at, updated_at | Passed |
| UpdateUser | userId, updatedAt | user_id, updated_at | Passed |
| ListUsers | userId, createdAt, updatedAt, pageSize | user_id, created_at, updated_at, page_size | Passed |
| DeleteUser | success, message | success, message | Passed |

### Key Findings

1. **Field name conversion is correct**:
   - `user_id` <-> `userId`
   - `created_at` <-> `createdAt`
   - `updated_at` <-> `updatedAt`
   - `page_size` <-> `pageSize`

2. **Request body format**: Clients should use the appropriate field name format based on the server configuration

3. **Query parameters**:
   - camelCase server accepts `?pageSize=10`
   - snake_case server accepts `?page_size=10`

4. **Compatibility**: Mock Database's automatic field normalization feature ensures both formats work correctly

### Usage Recommendations

#### Choose camelCase format:
- JavaScript/TypeScript frontend projects (matches JS naming conventions)
- Maintaining consistency with existing camelCase APIs
- Mobile applications (iOS/Android)

#### Choose snake_case format:
- Python backend projects (matches Python naming conventions)
- Direct database field mapping
- RESTful API standard (most use snake_case)

### How to Switch Formats

**Enable camelCase**:
```go
gs := grpcmux.NewServer(
    grpcmux.WithUseCamelCase(), // Add this option
)
```

**Use snake_case (default)**:
```go
gs := grpcmux.NewServer(
    // Do not add WithUseCamelCase() option
)
```

---

**Test completed**: 2026-01-31
**Test environment**: Mock Database
**Test conclusion**: Both JSON formats work correctly, field conversion is accurate
