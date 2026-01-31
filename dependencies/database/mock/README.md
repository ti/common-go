# Mock Database

Mock Database 是一个内存数据库实现，用于测试和开发环境。它实现了 `database.Database` 接口的所有方法，数据存储在内存中。

## 特性

- ✅ 完整实现 database.Database 接口
- ✅ 内存存储，无需外部依赖
- ✅ 支持所有 CRUD 操作
- ✅ 支持条件查询（Eq, Ne, Gt, Gte, Lt, Lte, In, Nin）
- ✅ 支持排序和限制
- ✅ 支持计数器操作
- ✅ 支持事务（模拟）
- ✅ 线程安全
- ✅ 结构化错误，JSON 格式使用 snake_case（下划线）
- ✅ **自动 Key 标准化**：自动将 camelCase 转换为 snake_case，兼容两种命名风格
- ✅ 适合单元测试

## 安装

Mock database 会自动通过 `database.RegisterImplements` 注册。

```go
import _ "github.com/ti/common-go/dependencies/database/mock"
```

## URL 格式

```
mock://host/database_name
```

示例：
- `mock://local/testdb`
- `mock://memory/myapp`

## 使用示例

### 基本用法

```go
package main

import (
    "context"
    "fmt"
    "github.com/ti/common-go/dependencies/database"
    _ "github.com/ti/common-go/dependencies/database/mock"
)

type User struct {
    ID    int64  `json:"id"`
    Name  string `json:"name"`
    Email string `json:"email"`
    Age   int    `json:"age"`
}

func main() {
    ctx := context.Background()

    // 创建 mock database
    db, err := database.New(ctx, "mock://local/testdb")
    if err != nil {
        panic(err)
    }
    defer db.Close(ctx)

    // 插入数据
    user := &User{
        ID:    1,
        Name:  "Alice",
        Email: "alice@example.com",
        Age:   25,
    }

    err = db.InsertOne(ctx, "users", user)
    if err != nil {
        panic(err)
    }

    // 查询数据
    var result User
    err = db.FindOne(ctx, "users",
        database.C{{Key: "id", Value: int64(1)}},
        &result)
    if err != nil {
        panic(err)
    }

    fmt.Printf("Found user: %s (%s)\n", result.Name, result.Email)
}
```

### 条件查询

```go
// 查找年龄大于 18 的所有用户
var users []User
err := db.Find(ctx, "users",
    database.C{{Key: "age", Value: 18, C: database.Gt}},
    []string{"-age"}, // 按年龄降序
    10, // 限制 10 条
    &users)
```

### 更新操作

```go
// 更新单个文档
count, err := db.UpdateOne(ctx, "users",
    database.C{{Key: "id", Value: int64(1)}},
    database.D{
        {Key: "age", Value: 26},
        {Key: "email", Value: "newemail@example.com"},
    })
```

### 删除操作

```go
// 删除单个文档
count, err := db.DeleteOne(ctx, "users",
    database.C{{Key: "id", Value: int64(1)}})

// 删除多个文档
count, err := db.Delete(ctx, "users",
    database.C{{Key: "age", Value: 18, C: database.Lt}})
```

### 计数操作

```go
// 统计符合条件的文档数
count, err := db.Count(ctx, "users",
    database.C{{Key: "age", Value: 18, C: database.Gte}})
fmt.Printf("Users aged 18+: %d\n", count)

// 检查是否存在
exists, err := db.Exist(ctx, "users",
    database.C{{Key: "email", Value: "alice@example.com"}})
```

### 流式查询

```go
// 适合处理大量数据
var user User
rows, err := db.FindRows(ctx, "users", nil, nil, 0, &user)
if err != nil {
    panic(err)
}
defer rows.Close()

for rows.Next() {
    data, err := rows.Decode()
    if err != nil {
        continue
    }

    // 将 map 转换为 struct
    userMap := data.(map[string]any)
    fmt.Printf("User: %v\n", userMap["name"])
}
```

### 计数器操作

```go
// 初始化计数器并增加
err := db.IncrCounter(ctx, "counters", "page_views", 0, 1)

// 减少计数器
err := db.DecrCounter(ctx, "counters", "page_views", 1)

// 获取计数器值
value, err := db.GetCounter(ctx, "counters", "page_views")
```

### 事务（模拟）

```go
// 开始事务
tx, err := db.StartTransaction(ctx)
if err != nil {
    panic(err)
}

// 使用事务数据库实例
txDB := db.WithTransaction(ctx, tx)

// 执行操作
err = txDB.InsertOne(ctx, "orders", order)
if err != nil {
    tx.Rollback()
    return err
}

_, err = txDB.UpdateOne(ctx, "inventory",
    database.C{{Key: "sku", Value: "ITEM001"}},
    database.D{{Key: "stock", Value: 95}})
if err != nil {
    tx.Rollback()
    return err
}

// 提交事务
err = tx.Commit()
```

## 在单元测试中使用

```go
package myapp_test

import (
    "context"
    "testing"

    "github.com/ti/common-go/dependencies/database"
    _ "github.com/ti/common-go/dependencies/database/mock"
)

func TestUserRepository(t *testing.T) {
    ctx := context.Background()

    // 使用 mock database
    db, err := database.New(ctx, "mock://test/mydb")
    if err != nil {
        t.Fatal(err)
    }
    defer db.Close(ctx)

    // 创建 repository
    repo := NewUserRepository(db)

    // 测试插入
    user := &User{ID: 1, Name: "Test"}
    err = repo.Create(ctx, user)
    if err != nil {
        t.Fatal(err)
    }

    // 测试查询
    found, err := repo.GetByID(ctx, 1)
    if err != nil {
        t.Fatal(err)
    }

    if found.Name != "Test" {
        t.Errorf("Expected name 'Test', got '%s'", found.Name)
    }
}
```

## 支持的条件运算符

| 运算符 | 说明 | 示例 |
|--------|------|------|
| `Eq` | 等于 | `{Key: "age", Value: 18, C: Eq}` |
| `Ne` | 不等于 | `{Key: "status", Value: "deleted", C: Ne}` |
| `Gt` | 大于 | `{Key: "price", Value: 100, C: Gt}` |
| `Gte` | 大于等于 | `{Key: "score", Value: 60, C: Gte}` |
| `Lt` | 小于 | `{Key: "age", Value: 65, C: Lt}` |
| `Lte` | 小于等于 | `{Key: "amount", Value: 1000, C: Lte}` |
| `In` | 包含于 | `{Key: "status", Value: []string{"active", "pending"}, C: In}` |
| `Nin` | 不包含于 | `{Key: "role", Value: []string{"admin"}, C: Nin}` |

## 错误处理

Mock database 的所有错误都使用结构化的 JSON 格式，字段名采用 **snake_case** 格式（下划线）：

### 错误结构

```go
type Error struct {
    ErrorCode        string `json:"error_code"`         // 错误代码
    ErrorMessage     string `json:"error_message"`      // 错误消息
    ErrorDescription string `json:"error_description"`  // 详细描述（可选）
}
```

### JSON 格式示例

```json
{
  "error_code": "not_found",
  "error_message": "record not found",
  "error_description": "no record found in table 'users'"
}
```

### 常见错误类型

| 错误代码 | 说明 | 使用场景 |
|----------|------|----------|
| `not_found` | 记录未找到 | FindOne 未找到匹配记录 |
| `invalid_argument` | 无效参数 | 参数验证失败 |
| `transaction_error` | 事务错误 | 事务操作失败 |
| `database_error` | 数据库错误 | 通用数据库错误 |
| `already_exists` | 记录已存在 | 插入重复记录 |
| `invalid_operation` | 无效操作 | 不支持的操作 |

### 错误处理示例

```go
var user User
err := db.FindOne(ctx, "users",
    database.C{{Key: "id", Value: int64(999)}},
    &user)

if err != nil {
    // 类型断言获取结构化错误
    if mockErr, ok := err.(*mock.Error); ok {
        fmt.Printf("Error code: %s\n", mockErr.ErrorCode)
        fmt.Printf("Error message: %s\n", mockErr.ErrorMessage)

        // 转换为 JSON
        jsonBytes, _ := json.Marshal(mockErr)
        // 输出: {"error_code":"not_found","error_message":"record not found",...}
        fmt.Println(string(jsonBytes))
    }
}
```

**注意**：所有错误字段均使用 `snake_case` 格式（如 `error_code`、`error_message`），不使用 `camelCase` 格式（如 `errorCode`、`errorMessage`）。

## Key 自动标准化

Mock Database 自动将所有字段名标准化为 `snake_case` 格式，这意味着你可以混合使用 `camelCase` 和 `snake_case`：

### 工作原理

所有的字段名（无论是在 struct tag、条件查询、更新操作还是排序字段中）都会自动转换为 `snake_case` 进行存储和匹配：

- `userId` → `user_id`
- `firstName` → `first_name`
- `UserAge` → `user_age`
- `HTTPResponse` → `http_response`
- `user_id` → `user_id` (已经是 snake_case，保持不变)

### 使用示例

#### 1. 混合命名风格的 Struct

```go
// 使用 camelCase JSON tags
type User struct {
    ID        int64  `json:"userId"`
    FirstName string `json:"firstName"`
    Age       int    `json:"userAge"`
}

// 或使用 snake_case JSON tags
type User struct {
    ID        int64  `json:"user_id"`
    FirstName string `json:"first_name"`
    Age       int    `json:"user_age"`
}

// 两种方式都能正常工作！
```

#### 2. 查询条件兼容

```go
// 使用 camelCase 查询
db.FindOne(ctx, "users",
    database.C{{Key: "userId", Value: int64(1)}},
    &user)

// 或使用 snake_case 查询（效果相同）
db.FindOne(ctx, "users",
    database.C{{Key: "user_id", Value: int64(1)}},
    &user)

// 两种方式查询的是同一个字段！
```

#### 3. 更新操作兼容

```go
// 使用 camelCase 更新
db.UpdateOne(ctx, "users",
    database.C{{Key: "userId", Value: int64(1)}},
    database.D{{Key: "userAge", Value: 26}})

// 或使用 snake_case 更新
db.UpdateOne(ctx, "users",
    database.C{{Key: "user_id", Value: int64(1)}},
    database.D{{Key: "user_age", Value: 26}})
```

#### 4. 排序字段兼容

```go
// 使用 camelCase 排序
db.Find(ctx, "users", nil, []string{"userAge"}, 10, &users)

// 或使用 snake_case 排序
db.Find(ctx, "users", nil, []string{"user_age"}, 10, &users)

// 使用 '-' 前缀表示降序
db.Find(ctx, "users", nil, []string{"-userAge"}, 10, &users)
```

#### 5. 跨风格兼容

```go
// 用 camelCase struct 插入
type CamelUser struct {
    ID   int64  `json:"userId"`
    Name string `json:"userName"`
}
camelUser := &CamelUser{ID: 1, Name: "Alice"}
db.InsertOne(ctx, "users", camelUser)

// 用 snake_case struct 查询（依然能找到！）
type SnakeUser struct {
    ID   int64  `json:"user_id"`
    Name string `json:"user_name"`
}
var snakeUser SnakeUser
db.FindOne(ctx, "users",
    database.C{{Key: "user_id", Value: int64(1)}},
    &snakeUser)
// snakeUser.Name == "Alice" ✓
```

### 转换规则

| 输入 | 输出 | 说明 |
|------|------|------|
| `userId` | `user_id` | 标准 camelCase |
| `firstName` | `first_name` | 多个单词 |
| `UserID` | `user_id` | PascalCase |
| `HTTPResponse` | `http_response` | 连续大写 |
| `parseHTMLDoc` | `parse_html_doc` | 混合缩写 |
| `user_id` | `user_id` | 已经是 snake_case |
| `Age` | `age` | 单个大写字母 |

### 优势

1. **灵活性**：前端可以使用 camelCase，后端可以使用 snake_case
2. **兼容性**：可以混用不同命名风格的代码
3. **统一性**：内部存储统一使用 snake_case
4. **简单性**：无需手动转换字段名

### 最佳实践

虽然系统支持混合命名风格，建议在项目中保持一致：

- **推荐**：统一使用 `snake_case` JSON tags（与数据库标准一致）
- **可接受**：统一使用 `camelCase` JSON tags（前端友好）
- **避免**：在同一个项目中混用两种风格

## 注意事项

1. **数据持久性**：Mock database 的数据存储在内存中，程序重启后数据会丢失
2. **事务隔离**：事务是模拟的，不提供真正的隔离级别
3. **性能**：适合测试，不适合生产环境大数据量场景
4. **并发安全**：使用 sync.RWMutex 保证线程安全
5. **字段映射**：优先使用 `json` tag，其次使用 `bson` tag，最后使用字段名
6. **Key 标准化**：所有字段名自动转换为 `snake_case`，支持 camelCase 和 snake_case 混用

## 与其他数据库的兼容性

Mock database 实现了与 MySQL、PostgreSQL 和 MongoDB 相同的接口，因此代码可以在不同数据库之间无缝切换：

```go
// 开发环境使用 mock
db, _ := database.New(ctx, "mock://local/myapp")

// 生产环境使用 MongoDB
db, _ := database.New(ctx, "mongodb://localhost:27017/myapp")

// 生产环境使用 MySQL
db, _ := database.New(ctx, "mysql://user:pass@localhost:3306/myapp")
```

相同的代码可以在所有这些数据库上运行！

## API 参考

完整的 API 参考请查看 [database README](../README.md)。
