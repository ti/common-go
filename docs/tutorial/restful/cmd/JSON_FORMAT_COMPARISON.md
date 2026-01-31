# JSON 格式对比测试报告

本文档对比了 camelCase 和 snake_case 两种 JSON 格式在 CRUD 操作中的返回结果。

## 测试环境

- **camelCase 服务器**: 端口 8080 (HTTP), 8081 (gRPC)
- **snakeCase 服务器**: 端口 8082 (HTTP), 8083 (gRPC)
- **数据库**: Mock Database（内存数据库）

## 关键差异

### camelCase 格式
- 使用 `grpcmux.WithUseCamelCase()` 选项
- 字段名使用驼峰命名：`userId`, `createdAt`, `updatedAt`, `pageSize`

### snake_case 格式（默认）
- 不使用 `WithUseCamelCase()` 选项
- 字段名使用下划线命名：`user_id`, `created_at`, `updated_at`, `page_size`

---

## 测试结果对比

### 1. CreateUser - 创建用户

#### camelCase 请求和响应
```bash
# 请求
curl -X POST http://127.0.0.1:8080/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"TestUser","email":"test@example.com","age":30}'

# 响应
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

#### snake_case 请求和响应
```bash
# 请求
curl -X POST http://127.0.0.1:8082/v1/users \
  -H "Content-Type: application/json" \
  -d '{"name":"TestUser","email":"test@example.com","age":30}'

# 响应
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

**字段对比**:
| camelCase | snake_case |
|-----------|------------|
| userId | user_id |
| createdAt | created_at |
| updatedAt | updated_at |

---

### 2. GetUser - 获取用户

#### camelCase 请求和响应
```bash
# 请求
curl -X GET "http://127.0.0.1:8080/v1/users/1769868266642494615"

# 响应
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

#### snake_case 请求和响应
```bash
# 请求
curl -X GET "http://127.0.0.1:8082/v1/users/1769874547784032306"

# 响应
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

### 3. UpdateUser - 更新用户

#### camelCase 请求和响应
```bash
# 请求（注意：请求体使用 userId）
curl -X PUT "http://127.0.0.1:8080/v1/users/1769868266642494615" \
  -H "Content-Type: application/json" \
  -d '{"userId":"1769868266642494615","name":"UpdatedUser","email":"updated@example.com","age":31}'

# 响应
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

#### snake_case 请求和响应
```bash
# 请求（注意：请求体使用 user_id）
curl -X PUT "http://127.0.0.1:8082/v1/users/1769874547784032306" \
  -H "Content-Type: application/json" \
  -d '{"user_id":"1769874547784032306","name":"UpdatedUser","email":"updated@example.com","age":31}'

# 响应
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

**重点**: `updatedAt` / `updated_at` 时间戳自动更新

---

### 4. ListUsers - 列出用户（分页）

#### camelCase 请求和响应
```bash
# 请求（注意：查询参数使用 pageSize）
curl -X GET "http://127.0.0.1:8080/v1/users?page=1&pageSize=10"

# 响应
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

#### snake_case 请求和响应
```bash
# 请求（注意：查询参数使用 page_size）
curl -X GET "http://127.0.0.1:8082/v1/users?page=1&page_size=10"

# 响应
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

**查询参数对比**:
| camelCase | snake_case |
|-----------|------------|
| pageSize | page_size |

**响应字段对比**:
| camelCase | snake_case |
|-----------|------------|
| pageSize | page_size |

---

### 5. DeleteUser - 删除用户

#### camelCase 请求和响应
```bash
# 请求
curl -X DELETE "http://127.0.0.1:8080/v1/users/1769868281066952197"

# 响应
{
    "success": true,
    "message": "User 1769868281066952197 deleted successfully"
}
```

#### snake_case 请求和响应
```bash
# 请求
curl -X DELETE "http://127.0.0.1:8082/v1/users/1769874560151784593"

# 响应
{
    "success": true,
    "message": "User 1769874560151784593 deleted successfully"
}
```

**注意**: DeleteUser 的响应字段（success, message）在两种格式下都相同，因为这些字段没有下划线。

---

## 总结

### ✅ 测试结果

| 操作 | camelCase 字段 | snake_case 字段 | 测试状态 |
|------|----------------|-----------------|----------|
| CreateUser | userId, createdAt, updatedAt | user_id, created_at, updated_at | ✅ 通过 |
| GetUser | userId, createdAt, updatedAt | user_id, created_at, updated_at | ✅ 通过 |
| UpdateUser | userId, updatedAt | user_id, updated_at | ✅ 通过 |
| ListUsers | userId, createdAt, updatedAt, pageSize | user_id, created_at, updated_at, page_size | ✅ 通过 |
| DeleteUser | success, message | success, message | ✅ 通过 |

### 关键发现

1. **字段命名转换正确**:
   - `user_id` ↔ `userId`
   - `created_at` ↔ `createdAt`
   - `updated_at` ↔ `updatedAt`
   - `page_size` ↔ `pageSize`

2. **请求体格式**: 客户端应该根据服务器配置使用相应的字段名格式

3. **查询参数**:
   - camelCase 服务器接受 `?pageSize=10`
   - snake_case 服务器接受 `?page_size=10`

4. **兼容性**: Mock Database 的自动字段标准化功能确保了两种格式都能正常工作

### 使用建议

#### 选择 camelCase 格式：
- ✅ JavaScript/TypeScript 前端项目（符合 JS 命名规范）
- ✅ 与现有 camelCase API 保持一致
- ✅ 移动端应用（iOS/Android）

#### 选择 snake_case 格式：
- ✅ Python 后端项目（符合 Python 命名规范）
- ✅ 数据库字段直接映射
- ✅ RESTful API 标准（多数采用 snake_case）

### 如何切换格式

**启用 camelCase**:
```go
gs := grpcmux.NewServer(
    grpcmux.WithUseCamelCase(), // 添加此选项
)
```

**使用 snake_case（默认）**:
```go
gs := grpcmux.NewServer(
    // 不添加 WithUseCamelCase() 选项
)
```

---

**测试完成时间**: 2026-01-31
**测试环境**: Mock Database
**测试结论**: ✅ 两种 JSON 格式都正常工作，字段转换准确无误
