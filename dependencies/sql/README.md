# SQL Module

MySQL 和 PostgreSQL 数据库适配器，实现 `database.Database` 接口。

## 特性

- ✅ 自动 Schema 生成
- 📊 JSON 字段支持
- 🔢 数组索引（PostgreSQL）
- 🎯 NULL 值处理
- 📦 批量插入优化
- 🔄 完整事务支持
- 📄 流式查询
- 🚀 连接池管理

## 初始化

### MySQL

```go
import "github.com/ti/common-go/dependencies"

// 方式 1: 使用依赖注入
type Config struct {
    DB *dependencies.SQL `uri:"mysql://user:password@localhost:3306/mydb?charset=utf8mb4&parseTime=true"`
}

// 方式 2: 手动创建
db, err := dependencies.NewSQL(ctx, "mysql://user:password@localhost:3306/mydb?charset=utf8mb4&parseTime=true")
```

### PostgreSQL

```go
db, err := dependencies.NewSQL(ctx, "postgres://user:password@localhost:5432/mydb?sslmode=disable")
```

### 连接参数

**MySQL:**
- `charset=utf8mb4` - 字符集（推荐）
- `parseTime=true` - 解析 TIME/DATETIME
- `loc=Asia%2FShanghai` - 时区
- `maxAllowedPacket=67108864` - 最大包大小

**PostgreSQL:**
- `sslmode=disable` - SSL 模式（require/disable）
- `connect_timeout=10` - 连接超时
- `application_name=myapp` - 应用名称

## Schema 定义

### 结构体标签

```go
type User struct {
    ID        int64     `json:"id" db:"id,primary,auto_increment"`
    Email     string    `json:"email" db:"email,unique,index"`
    Name      string    `json:"name" db:"name,size:100"`
    Age       int       `json:"age" db:"age,default:0"`
    Tags      []string  `json:"tags" db:"tags,json"`
    Settings  Settings  `json:"settings" db:"settings,json"`
    Avatar    *string   `json:"avatar,omitempty" db:"avatar,null"`
    CreatedAt time.Time `json:"created_at" db:"created_at"`
    UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type Settings struct {
    Theme    string `json:"theme"`
    Language string `json:"language"`
}
```

### 标签说明

| 标签 | 说明 | 示例 |
|------|------|------|
| `primary` | 主键 | `db:"id,primary"` |
| `auto_increment` | 自增（MySQL） | `db:"id,primary,auto_increment"` |
| `unique` | 唯一约束 | `db:"email,unique"` |
| `index` | 普通索引 | `db:"city,index"` |
| `size:N` | 字符串长度 | `db:"name,size:100"` |
| `default:X` | 默认值 | `db:"age,default:0"` |
| `json` | JSON 类型 | `db:"settings,json"` |
| `null` | 允许 NULL | `db:"avatar,null"` |
| `omitempty` | 空值忽略 | `db:"avatar,omitempty"` |

### 自动生成 Schema

```go
schema := sql.GenerateScheme("users", User{})
fmt.Println(schema)
```

输出（MySQL）:
```sql
CREATE TABLE IF NOT EXISTS users (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    email VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    age INT DEFAULT 0,
    tags JSON,
    settings JSON,
    avatar VARCHAR(255),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

输出（PostgreSQL）:
```sql
CREATE TABLE IF NOT EXISTS users (
    id BIGSERIAL PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    age INTEGER DEFAULT 0,
    tags JSONB,
    settings JSONB,
    avatar VARCHAR(255),
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
```

## CRUD 操作

### 插入

```go
user := &User{
    Email: "alice@example.com",
    Name:  "Alice",
    Age:   25,
    Tags:  []string{"golang", "developer"},
}

err := db.Insert(ctx, "users", user)
// user.ID 会被自动填充
```

### 查询单条

```go
var user User
err := db.FindOne(ctx, "users", 
    database.C{{Key: "email", Value: "alice@example.com"}}, 
    &user)

if errors.Is(err, database.ErrNotFound) {
    // 用户不存在
}
```

### 查询多条

```go
var users []User
err := db.Find(ctx, "users",
    database.C{
        {Key: "age", Value: 18, C: database.Gte},
        {Key: "status", Value: "active"},
    },
    []string{"-created_at", "name"}, // 排序
    100, // 限制
    &users)
```

### 更新

```go
err := db.Update(ctx, "users",
    database.C{{Key: "id", Value: userId}},
    database.D{
        {Key: "name", Value: "Bob"},
        {Key: "updated_at", Value: time.Now()},
    })
```

### 删除

```go
err := db.Delete(ctx, "users",
    database.C{{Key: "id", Value: userId}})
```

### 计数

```go
count, err := db.Count(ctx, "users",
    database.C{{Key: "status", Value: "active"}})
```

## 高级查询

### 分页查询

```go
req := &database.PageQueryRequest{
    PageIndex: 1,
    PageSize:  20,
    Conditions: database.C{
        {Key: "age", Value: 18, C: database.Gte},
    },
    SortBy: []string{"-created_at"},
}

resp, err := sql.PageQuery[User](ctx, db, "users", req)
fmt.Printf("Total: %d, Data: %d\n", resp.Total, len(resp.Data))
```

### LIKE 查询

```go
// MySQL/PostgreSQL 支持 LIKE
var users []User
db.Find(ctx, "users",
    database.C{
        {Key: "name", Value: "John%", C: database.Like}, // 前缀匹配
        {Key: "email", Value: "%@gmail.com", C: database.Like}, // 后缀匹配
    },
    nil, 100, &users)
```

### IN 查询

```go
userIds := []int64{1, 2, 3, 4, 5}
var users []User
db.Find(ctx, "users",
    database.C{{Key: "id", Value: userIds, C: database.In}},
    nil, 0, &users)
```

### 范围查询

```go
var products []Product
db.Find(ctx, "products",
    database.C{
        {Key: "price", Value: 10, C: database.Gte},  // >= 10
        {Key: "price", Value: 100, C: database.Lte}, // <= 100
    },
    nil, 0, &products)
```

## 流式查询

处理大量数据时使用流式查询避免内存溢出：

```go
var user User
rows, err := db.FindRows(ctx, "users",
    database.C{{Key: "status", Value: "active"}},
    []string{"-id"},
    0, // 无限制
    &user)
if err != nil {
    return err
}
defer rows.Close()

for rows.Next() {
    if err := rows.Scan(&user); err != nil {
        log.Error("Scan error", "err", err)
        continue
    }
    
    // 处理单条记录
    if err := processUser(ctx, &user); err != nil {
        log.Error("Process error", "err", err)
    }
}
```

## 批量操作

### 批量插入

```go
users := []any{
    &User{Name: "Alice", Email: "alice@example.com"},
    &User{Name: "Bob", Email: "bob@example.com"},
    &User{Name: "Charlie", Email: "charlie@example.com"},
}

err := db.BatchInsert(ctx, "users", users)
```

### 批量更新

```go
// 更新所有符合条件的记录
err := db.BatchUpdate(ctx, "users",
    database.C{{Key: "status", Value: "inactive"}},
    database.D{{Key: "deleted_at", Value: time.Now()}})
```

## 事务处理

### 基本事务

```go
tx, err := db.StartTransaction(ctx)
if err != nil {
    return err
}

// 创建事务数据库实例
txDB := db.WithTransaction(ctx, tx)

// 执行操作
if err := txDB.Insert(ctx, "orders", order); err != nil {
    tx.Rollback()
    return err
}

if err := txDB.Update(ctx, "inventory",
    database.C{{Key: "sku", Value: order.SKU}},
    database.D{{Key: "stock", Value: newStock}}); err != nil {
    tx.Rollback()
    return err
}

// 提交事务
return tx.Commit()
```

### 事务辅助函数

```go
func WithTransaction(ctx context.Context, db database.Database, fn func(database.Database) error) error {
    tx, err := db.StartTransaction(ctx)
    if err != nil {
        return err
    }
    
    txDB := db.WithTransaction(ctx, tx)
    
    if err := fn(txDB); err != nil {
        tx.Rollback()
        return err
    }
    
    return tx.Commit()
}

// 使用
err := WithTransaction(ctx, db, func(txDB database.Database) error {
    if err := txDB.Insert(ctx, "orders", order); err != nil {
        return err
    }
    if err := txDB.Update(ctx, "inventory", conds, updates); err != nil {
        return err
    }
    return nil
})
```

## JSON 字段

### 定义 JSON 字段

```go
type User struct {
    ID       int64            `db:"id,primary"`
    Settings UserSettings     `db:"settings,json"`
    Tags     []string         `db:"tags,json"`
    Metadata map[string]any   `db:"metadata,json"`
}

type UserSettings struct {
    Theme      string `json:"theme"`
    Language   string `json:"language"`
    Notify     bool   `json:"notify"`
}
```

### 插入 JSON

```go
user := &User{
    Settings: UserSettings{
        Theme:    "dark",
        Language: "zh-CN",
        Notify:   true,
    },
    Tags: []string{"vip", "premium"},
    Metadata: map[string]any{
        "source": "mobile",
        "version": "2.0.1",
    },
}

db.Insert(ctx, "users", user)
```

### 查询 JSON 字段

**MySQL (JSON_EXTRACT):**
```go
// 查询 settings.theme = 'dark' 的用户
db.Find(ctx, "users",
    database.C{{Key: "settings->theme", Value: "dark"}},
    nil, 0, &users)
```

**PostgreSQL (JSONB):**
```go
// 查询 settings.theme = 'dark' 的用户
db.Find(ctx, "users",
    database.C{{Key: "settings->>'theme'", Value: "dark"}},
    nil, 0, &users)
```

## NULL 值处理

### 使用指针

```go
type User struct {
    Avatar   *string    `db:"avatar,null"`
    DeletedAt *time.Time `db:"deleted_at,null"`
}

user := &User{
    Avatar: nil, // NULL
}

// 设置值
avatar := "https://example.com/avatar.jpg"
user.Avatar = &avatar
```

### 使用 sql.Null* 类型

```go
import "database/sql"

type User struct {
    Avatar sql.NullString `db:"avatar"`
}

user := &User{
    Avatar: sql.NullString{
        String: "https://example.com/avatar.jpg",
        Valid: true,
    },
}
```

## 数据库特定功能

### PostgreSQL 数组

```go
type Article struct {
    ID   int64    `db:"id,primary"`
    Tags []string `db:"tags,array"` // PostgreSQL ARRAY
}

// 查询包含特定标签的文章
db.Find(ctx, "articles",
    database.C{{Key: "tags", Value: "golang", C: database.Contains}},
    nil, 0, &articles)
```

### MySQL 全文索引

```sql
-- 手动创建全文索引
ALTER TABLE articles ADD FULLTEXT INDEX ft_content (content);
```

```go
// 全文搜索
db.Find(ctx, "articles",
    database.C{{Key: "MATCH(content) AGAINST(?)", Value: "golang tutorial"}},
    nil, 0, &articles)
```

## 性能优化

### 1. 使用批量插入

```go
// 不好：逐条插入
for _, user := range users {
    db.Insert(ctx, "users", user) // N 次数据库调用
}

// 好：批量插入
db.BatchInsert(ctx, "users", usersAsAny) // 1 次数据库调用
```

### 2. 使用索引

```go
type User struct {
    Email string `db:"email,unique,index"` // 查询优化
    City  string `db:"city,index"`         // 查询优化
}
```

### 3. 限制返回字段

```go
req := &database.PageQueryRequest{
    PageIndex: 1,
    PageSize:  10,
    Select: []string{"id", "name", "email"}, // 仅返回需要的字段
}
```

### 4. 使用流式查询

```go
// 处理大量数据时使用流式查询
rows, _ := db.FindRows(ctx, "users", nil, nil, 0, &User{})
defer rows.Close()
for rows.Next() {
    // 逐条处理，内存占用低
}
```

### 5. 连接池配置

```go
// 通过 URI 参数配置
uri := "mysql://user:pass@host/db?maxOpenConns=100&maxIdleConns=10&connMaxLifetime=3600"
```

## 错误处理

```go
err := db.FindOne(ctx, "users", conds, &user)
if err != nil {
    if errors.Is(err, database.ErrNotFound) {
        return ErrUserNotFound
    }
    
    if errors.Is(err, context.DeadlineExceeded) {
        return ErrTimeout
    }
    
    // 检查数据库错误
    if strings.Contains(err.Error(), "Duplicate entry") {
        return ErrDuplicateKey
    }
    
    return fmt.Errorf("database error: %w", err)
}
```

## 迁移和维护

### 自动迁移

```go
type User struct {
    // ... 字段定义
}

// 生成建表语句
schema := sql.GenerateScheme("users", User{})

// 执行建表
_, err := db.Exec(ctx, schema)
```

### 手动迁移

```go
migrations := []string{
    "ALTER TABLE users ADD COLUMN phone VARCHAR(20)",
    "CREATE INDEX idx_users_phone ON users(phone)",
    "ALTER TABLE users ADD COLUMN verified BOOLEAN DEFAULT FALSE",
}

for _, migration := range migrations {
    if _, err := db.Exec(ctx, migration); err != nil {
        log.Error("Migration failed", "err", err, "sql", migration)
    }
}
```

## 最佳实践

1. **使用上下文超时**
```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
```

2. **处理 NULL 值**
```go
type User struct {
    DeletedAt *time.Time `db:"deleted_at,null"`
}
```

3. **使用事务保证一致性**
```go
WithTransaction(ctx, db, func(txDB database.Database) error {
    // 所有操作在同一个事务中
})
```

4. **避免 SQL 注入**
```go
// 框架自动处理参数化查询，不要手动拼接 SQL
```

5. **合理使用索引**
```go
type User struct {
    Email string `db:"email,unique,index"` // 频繁查询的字段加索引
}
```

## 参考

- [Database 接口文档](../database/README.md)
- [主 README](../../README.md)
