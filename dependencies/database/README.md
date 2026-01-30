# Database Module

数据库抽象层，提供统一的 CRUD 接口，支持 MySQL、PostgreSQL 和 MongoDB。

## 核心接口

### Database Interface

```go
type Database interface {
    // 基础 CRUD
    Insert(ctx context.Context, table string, data any) error
    Update(ctx context.Context, table string, conds C, updates D) error
    Delete(ctx context.Context, table string, conds C) error
    FindOne(ctx context.Context, table string, conds C, result any) error
    Find(ctx context.Context, table string, conds C, sortBy []string, limit int, results any) error
    
    // 计数和聚合
    Count(ctx context.Context, table string, conds C) (int64, error)
    Aggregate(ctx context.Context, table string, pipeline any, result any) error
    
    // 事务支持
    StartTransaction(ctx context.Context) (Transaction, error)
    WithTransaction(ctx context.Context, tx Transaction) Database
    
    // 流式查询
    FindRows(ctx context.Context, table string, conds C, sortBy []string, limit int, data any) (Row, error)
    
    // 批量操作
    BatchInsert(ctx context.Context, table string, documents []any) error
    BatchUpdate(ctx context.Context, table string, conds C, updates D) error
}
```

## 条件构造器 (Conditions DSL)

### Condition 类型

```go
type Condition struct {
    Key   string      // 字段名
    Value any         // 值
    C     ConditionOp // 运算符
}

type C []Condition // 条件列表
```

### 支持的运算符

| 运算符 | 说明 | 示例 |
|--------|------|------|
| `Eq` | 等于 | `{Key: "age", Value: 18, C: Eq}` |
| `Ne` | 不等于 | `{Key: "status", Value: "deleted", C: Ne}` |
| `Gt` | 大于 | `{Key: "price", Value: 100, C: Gt}` |
| `Gte` | 大于等于 | `{Key: "score", Value: 60, C: Gte}` |
| `Lt` | 小于 | `{Key: "age", Value: 65, C: Lt}` |
| `Lte` | 小于等于 | `{Key: "amount", Value: 1000, C: Lte}` |
| `In` | 包含于 | `{Key: "status", Value: []string{"active", "pending"}, C: In}` |
| `Nin` | 不包含于 | `{Key: "role", Value: []string{"admin", "root"}, C: Nin}` |
| `Like` | 模糊匹配 (SQL) | `{Key: "name", Value: "John%", C: Like}` |
| `Regex` | 正则表达式 (Mongo) | `{Key: "email", Value: ".*@example\\.com", C: Regex}` |
| `Exists` | 字段存在 | `{Key: "optional_field", Value: true, C: Exists}` |

### 使用示例

```go
// 简单条件
conds := database.C{
    {Key: "age", Value: 18, C: database.Gt},
}

// 复合条件（AND 关系）
conds := database.C{
    {Key: "age", Value: 18, C: database.Gt},
    {Key: "status", Value: "active", C: database.Eq},
    {Key: "city", Value: []string{"Beijing", "Shanghai"}, C: database.In},
}

// 查询
var users []User
db.Find(ctx, "users", conds, []string{"-created_at"}, 100, &users)
```

## 更新构造器 (Updates DSL)

```go
type Element struct {
    Key   string
    Value any
}

type D []Element // 更新列表
```

### 使用示例

```go
updates := database.D{
    {Key: "status", Value: "active"},
    {Key: "updated_at", Value: time.Now()},
    {Key: "login_count", Value: 1}, // 递增需要特殊处理
}

db.Update(ctx, "users", 
    database.C{{Key: "id", Value: userId}}, 
    updates)
```

## 分页查询

### PageQueryRequest

```go
type PageQueryRequest struct {
    PageIndex  int         `json:"pageIndex"`  // 页码（从 1 开始）
    PageSize   int         `json:"pageSize"`   // 每页大小
    Conditions C           `json:"conditions"` // 查询条件
    SortBy     []string    `json:"sortBy"`     // 排序字段
    Select     []string    `json:"select"`     // 选择字段（可选）
}
```

### PageQueryResponse

```go
type PageQueryResponse[T any] struct {
    Data       []T   `json:"data"`       // 数据列表
    Total      int64 `json:"total"`      // 总记录数
    PageIndex  int   `json:"pageIndex"`  // 当前页码
    PageSize   int   `json:"pageSize"`   // 每页大小
    TotalPages int   `json:"totalPages"` // 总页数
}
```

### 使用示例

```go
req := &database.PageQueryRequest{
    PageIndex: 1,
    PageSize:  20,
    Conditions: database.C{
        {Key: "status", Value: "active"},
        {Key: "age", Value: 18, C: database.Gte},
    },
    SortBy: []string{"-created_at", "name"}, // - 表示降序
}

// SQL
resp, err := sql.PageQuery[User](ctx, db, "users", req)

// Mongo
resp, err := mongo.PageQuery[User](ctx, db, "users", req)

fmt.Printf("Total: %d, Pages: %d\n", resp.Total, resp.TotalPages)
for _, user := range resp.Data {
    fmt.Printf("User: %s\n", user.Name)
}
```

## 流式查询

用于处理大量数据，避免内存溢出。

```go
type Row interface {
    Next() bool
    Scan(dest any) error
    Close() error
}
```

### 使用示例

```go
var user User
rows, err := db.FindRows(ctx, "users", 
    database.C{{Key: "status", Value: "active"}},
    []string{"-id"},
    0, // 无限制
    &user)
defer rows.Close()

for rows.Next() {
    if err := rows.Scan(&user); err != nil {
        log.Error("Scan error", "err", err)
        continue
    }
    
    // 处理单个用户
    processUser(&user)
}
```

## 事务处理

### Transaction Interface

```go
type Transaction interface {
    Commit() error
    Rollback() error
}
```

### 使用示例

```go
// 1. 开启事务
tx, err := db.StartTransaction(ctx)
if err != nil {
    return err
}

// 2. 创建事务数据库实例
txDB := db.WithTransaction(ctx, tx)

// 3. 执行操作
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

// 4. 提交事务
if err := tx.Commit(); err != nil {
    return err
}
```

## 排序规则

使用 `sortBy` 参数指定排序：

```go
sortBy := []string{
    "-created_at",  // 降序（前缀 -）
    "name",         // 升序
    "-priority",    // 降序
}
```

## 字段选择

仅查询指定字段（可选优化）：

```go
req := &database.PageQueryRequest{
    PageIndex: 1,
    PageSize:  10,
    Select: []string{"id", "name", "email"}, // 仅返回这些字段
}
```

## 最佳实践

### 1. 使用上下文超时

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

db.FindOne(ctx, "users", conds, &user)
```

### 2. 批量操作

```go
// 批量插入（性能更好）
users := []any{&user1, &user2, &user3}
db.BatchInsert(ctx, "users", users)
```

### 3. 索引优化

```go
// 确保查询字段有索引
type User struct {
    Email string `db:"email,unique,index"` // 唯一索引
    City  string `db:"city,index"`         // 普通索引
}
```

### 4. 避免 N+1 查询

```go
// 不好：循环查询
for _, orderId := range orderIds {
    db.FindOne(ctx, "orders", database.C{{Key: "id", Value: orderId}}, &order)
}

// 好：使用 In
db.Find(ctx, "orders", database.C{{Key: "id", Value: orderIds, C: database.In}}, nil, 0, &orders)
```

### 5. 使用流式查询处理大数据

```go
// 不好：一次性加载所有数据
var allUsers []User
db.Find(ctx, "users", nil, nil, 0, &allUsers) // 可能 OOM

// 好：流式处理
rows, _ := db.FindRows(ctx, "users", nil, nil, 0, &User{})
defer rows.Close()
for rows.Next() {
    // 逐条处理
}
```

## 数据库差异处理

### SQL vs Mongo

| 特性 | SQL | MongoDB |
|------|-----|---------|
| 条件运算符 | 支持全部 | 支持全部 |
| Like | 支持 `%` 通配符 | 使用 Regex |
| 事务 | ✅ 完整支持 | ✅ 支持（需副本集） |
| Schema | 需要预定义 | 灵活 Schema |
| 聚合 | SQL 语句 | Aggregation Pipeline |

### 跨数据库兼容代码

```go
// 这段代码可以在 MySQL/PostgreSQL/MongoDB 上运行
func FindActiveUsers(ctx context.Context, db database.Database) ([]User, error) {
    var users []User
    err := db.Find(ctx, "users", 
        database.C{{Key: "status", Value: "active"}},
        []string{"-created_at"},
        100,
        &users)
    return users, err
}
```

## 错误处理

```go
err := db.FindOne(ctx, "users", conds, &user)
if err != nil {
    if errors.Is(err, database.ErrNotFound) {
        // 记录不存在
        return nil, ErrUserNotFound
    }
    // 其他数据库错误
    return nil, fmt.Errorf("database error: %w", err)
}
```

## 参考

- [SQL 适配器文档](../sql/README.md)
- [MongoDB 适配器文档](../mongo/README.md)
- [主 README](../../README.md)
