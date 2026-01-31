# Database Configuration Guide

本文档说明如何配置不同类型的数据库连接。

## 配置步骤

### 1. 在 main.go 中导入数据库驱动

根据你使用的数据库类型，在 `main.go` 中导入相应的驱动：

```go
import (
    // Mock Database (测试用)
    _ "github.com/ti/common-go/dependencies/database/mock"

    // MongoDB
    // _ "github.com/ti/common-go/dependencies/mongodb"

    // MySQL/PostgreSQL
    // _ "github.com/ti/common-go/dependencies/sql"
)
```

### 2. 在 config.yaml 中配置连接字符串

在 `configs/config.yaml` 的 `dependencies.db` 字段中配置数据库连接字符串。

## 数据库类型和配置示例

### Mock Database（内存数据库，用于测试）

**特点**：
- 纯内存存储，无需外部依赖
- 完全支持 CRUD 操作
- 支持分页查询（PageQuery 和 StreamQuery）
- 自动字段名标准化（camelCase ↔ snake_case）
- 适合单元测试和本地开发

**导入**：
```go
_ "github.com/ti/common-go/dependencies/database/mock"
```

**配置示例**：
```yaml
dependencies:
  db: "mock://local/myapp"
  # 或
  db: "mock://local/project_name"
```

**使用场景**：
- 单元测试
- 集成测试
- 本地开发（无需安装数据库）
- CI/CD 环境

---

### MongoDB

**特点**：
- NoSQL 文档数据库
- 支持复杂查询和聚合
- 水平扩展能力强
- 支持副本集（Replica Set）
- 灵活的文档结构

**导入**：
```go
_ "github.com/ti/common-go/dependencies/mongodb"
```

**配置示例**：

#### 基本配置（本地单机）
```yaml
dependencies:
  db: "mongodb://localhost:27017/myapp"
```

#### 带认证
```yaml
dependencies:
  db: "mongodb://username:password@localhost:27017/myapp?authSource=admin&timeout=5s"
```

#### 副本集（高可用）
```yaml
dependencies:
  db: "mongodb://mongo1:27017,mongo2:27017,mongo3:27017/myapp?replicaSet=rs0&timeout=5s"
```

#### 完整配置示例
```yaml
dependencies:
  db: "mongodb://user:pass@host1:27017,host2:27017/mydb?replicaSet=rs0&authSource=admin&ssl=true&timeout=10s&maxPoolSize=100"
```

**常用查询参数**：
- `timeout`: 连接超时时间（例如：5s, 10s）
- `authSource`: 认证数据库名称（通常为 "admin"）
- `replicaSet`: 副本集名称
- `ssl`: 启用 SSL/TLS（true/false）
- `maxPoolSize`: 连接池最大连接数
- `minPoolSize`: 连接池最小连接数
- `retryWrites`: 自动重试写操作（true/false）

---

### MySQL

**特点**：
- 关系型数据库
- 强 ACID 支持
- 广泛使用，生态成熟
- 支持复杂事务
- 适合结构化数据

**导入**：
```go
_ "github.com/ti/common-go/dependencies/sql"
```

**配置示例**：

#### 基本配置
```yaml
dependencies:
  db: "mysql://root:password@tcp(localhost:3306)/myapp"
```

#### 推荐配置（生产环境）
```yaml
dependencies:
  db: "mysql://username:password@tcp(localhost:3306)/myapp?charset=utf8mb4&parseTime=True&loc=Local&timeout=5s"
```

#### 完整配置示例
```yaml
dependencies:
  db: "mysql://user:pass@tcp(host:3306)/dbname?charset=utf8mb4&parseTime=True&loc=Local&timeout=10s&readTimeout=30s&writeTimeout=30s&maxAllowedPacket=16777216"
```

**常用查询参数**：
- `charset`: 字符集（推荐：utf8mb4，支持完整 Unicode）
- `parseTime`: 将数据库 TIME/DATETIME 类型解析为 Go time.Time（推荐：True）
- `loc`: 时区位置（Local=本地时区, UTC=UTC时区）
- `timeout`: 连接超时时间
- `readTimeout`: 读取超时时间
- `writeTimeout`: 写入超时时间
- `maxAllowedPacket`: 最大数据包大小（字节）

---

### PostgreSQL

**特点**：
- 先进的开源关系型数据库
- 强大的查询优化器
- 支持 JSONB、数组等复杂类型
- 严格的数据完整性
- 扩展性强（支持自定义类型和函数）

**导入**：
```go
_ "github.com/ti/common-go/dependencies/sql"
```

**配置示例**：

#### 基本配置
```yaml
dependencies:
  db: "postgres://username:password@localhost:5432/myapp"
```

#### 禁用 SSL（本地开发）
```yaml
dependencies:
  db: "postgres://username:password@localhost:5432/myapp?sslmode=disable"
```

#### 启用 SSL（生产环境）
```yaml
dependencies:
  db: "postgres://username:password@localhost:5432/myapp?sslmode=require&connect_timeout=10"
```

#### 完整配置示例
```yaml
dependencies:
  db: "postgres://user:pass@host:5432/dbname?sslmode=require&connect_timeout=10&pool_max_conns=20&pool_min_conns=5&application_name=myapp"
```

**常用查询参数**：
- `sslmode`: SSL 模式
  - `disable`: 禁用 SSL
  - `require`: 需要 SSL，但不验证证书
  - `verify-ca`: 验证 CA 证书
  - `verify-full`: 完全验证（包括主机名）
- `connect_timeout`: 连接超时时间（秒）
- `application_name`: 应用名称（用于日志和监控）
- `pool_max_conns`: 连接池最大连接数
- `pool_min_conns`: 连接池最小连接数

---

## 配置文件完整示例

```yaml
# configs/config.yaml
apis:
    grpcAddr: :8081
    httpAddr: :8080
    metricsAddr: :9090
    logBody: true

log:
    level: "debug"

dependencies:
    # 选择一个数据库配置（取消对应行的注释）：

    # Mock Database (默认，用于测试)
    db: "mock://local/restful_tutorial"

    # MongoDB
    # db: "mongodb://localhost:27017/myapp?timeout=5s"
    # db: "mongodb://user:pass@localhost:27017/myapp?authSource=admin"

    # MySQL
    # db: "mysql://root:password@tcp(localhost:3306)/myapp?charset=utf8mb4&parseTime=True&loc=Local"

    # PostgreSQL
    # db: "postgres://user:pass@localhost:5432/myapp?sslmode=disable"

    demoHTTP: "http://127.0.0.1:8080?log=true"

service:
    test: test
```

## 切换数据库类型

要切换数据库类型，只需要：

1. **更新 main.go 的导入**（取消相应驱动的注释）
2. **更新 config.yaml 的连接字符串**（切换到对应数据库的配置）
3. **重新编译并运行**

示例：从 Mock 切换到 MySQL

```go
// main.go
import (
    // _ "github.com/ti/common-go/dependencies/database/mock"  // 注释掉
    _ "github.com/ti/common-go/dependencies/sql"               // 取消注释
)
```

```yaml
# config.yaml
dependencies:
  # db: "mock://local/restful_tutorial"  # 注释掉
  db: "mysql://root:password@tcp(localhost:3306)/myapp?charset=utf8mb4&parseTime=True&loc=Local"  # 取消注释
```

## 数据库接口兼容性

所有数据库类型都实现了相同的 `database.Database` 接口，因此应用代码无需修改即可在不同数据库之间切换。

支持的操作：
- ✅ Insert / InsertOne
- ✅ Update / UpdateOne
- ✅ Delete / DeleteOne
- ✅ Find / FindOne
- ✅ Count / Exist
- ✅ PageQuery (分页查询)
- ✅ StreamQuery (流式查询)
- ✅ Transaction (事务支持)

## 常见问题

### Q: 可以同时使用多个数据库吗？

A: 可以。你可以在 Dependencies 结构体中定义多个数据库连接：

```go
type Dependencies struct {
    dependencies.Dependency
    MainDB   database.Database `required:"false"`  // 主数据库
    CacheDB  database.Database `required:"false"`  // 缓存数据库
    LogDB    database.Database `required:"false"`  // 日志数据库
}
```

在 config.yaml 中配置：
```yaml
dependencies:
  mainDB: "mysql://localhost:3306/main"
  cacheDB: "mongodb://localhost:27017/cache"
  logDB: "postgres://localhost:5432/logs"
```

### Q: Mock Database 和真实数据库的数据能互相迁移吗？

A: 不能直接迁移，因为 Mock Database 只存在于内存中。但由于接口兼容，你可以编写迁移脚本从一个数据库读取并写入另一个数据库。

### Q: 如何调试数据库连接问题？

A:
1. 启用日志：设置 `log.level: "debug"` 查看详细日志
2. 检查连接字符串格式是否正确
3. 确认数据库服务正在运行
4. 验证用户名密码正确
5. 检查防火墙和网络配置

---

**更新日期**: 2026-01-31
**版本**: 1.0
