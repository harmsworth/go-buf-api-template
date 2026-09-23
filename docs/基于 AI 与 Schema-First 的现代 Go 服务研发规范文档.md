# 基于 AI 与 Schema-First 的现代 Go 服务研发规范文档

## 1. 概述与核心哲学

在传统的后端开发范式中，开发者往往面临以下痛点：

* **重复代码泛滥**：手写冗余的 DTO 结构体、JSON 序列化逻辑与参数校验判定（`if err != nil`）。
* **契约与实现脱节**：前端/微服务间的 API 文档（Swagger/Postman）与实际代码不一致，维护成本高昂。
* **ORM 隐患与性能损耗**：大面积依赖运行时反射（如传统 ORM），不仅牺牲了极致性能，也容易将类型错误拼写延续到运行时才暴露。

本文档定义了一套**基于 AI 与 Schema-First（契约驱动）** 的标准 Go 语言微服务工程范式。

### 核心架构哲学

1. **API 契约唯一真理源（API Source of Truth）**：以 Protobuf 定义全站接口与数据结构。
2. **持久层契约唯一真理源（DB Source of Truth）**：以版本化 SQL (DDL) 描述数据库表结构。
3. **极度自动化与编译期安全**：全链路采用代码生成器（`buf` 与 `sqlc`），剔除绝大多数手写结构体；任何字段修改如果在代码中未同步更新，直接导致 **Go 编译失败**。
4. **AI 辅助加速**：利用 AI 辅助生成初始 Protobuf 契约、转化 SQL 以及补全双向映射函数，将精力集中于核心业务逻辑。

---

## 2. 整体工程流水线 (Pipeline)

整条研发流水线按照明确的上下游依赖顺序流动，每个环节产出固定的结构化代码：

```
                    ┌─────────────────────────┐
                    │  1. 自然语言/业务需求    │
                    └────────────┬────────────┘
                                 │ AI 辅助编写
                                 ▼
                    ┌─────────────────────────┐
                    │  2. Protobuf (.proto)   │  ── API 契约源头
                    └────────────┬────────────┘
                                 │
           ┌─────────────────────┴─────────────────────┐
           │ buf generate                              │ AI / 人工微调
           ▼                                           ▼
┌──────────────────────┐                    ┌──────────────────────┐
│  gen/go/user/v1/     │                    │  migrations/*.sql    │
│  - *.pb.go (DTO)     │                    │  (DDL 建表/变更)      │
│  - *.validate.go     │                    └──────────┬───────────┘
│  - *_grpc.pb.go      │                               │
└──────────────────────┘                               │ golang-migrate / Atlas
                                                       ▼
                                            ┌──────────────────────┐
                                            │  MySQL / PostgreSQL  │
                                            └──────────┬───────────┘
                                                       │
                                                       │ sqlc generate
                                                       ▼
                                            ┌──────────────────────┐
                                            │  internal/db/        │
                                            │  - models.go         │
                                            │  - db.go / query.sql │
                                            └──────────────────────┘

```

---

## 3. 详细分阶段规范与操作指南

### 阶段一：AI 辅助编写 API 契约 (`.proto`)

以业务需求为输入，通过 IDE AI 插件生成标准规范的 Protobuf 文件。文件需包含结构定义与校验约束。

**规范要求**：

* 统一引入 `buf.validate` 进行字段级约束。
* 使用 Google API Design 指南命名规范（蛇形命名 `snake_case` 表示字段）。

#### 示例文件：`proto/user/v1/user.proto`

```protobuf
syntax = "proto3";

package user.v1;

option go_package = "go-buf-api-template/gen/go/user/v1;userv1";

import "buf/validate/validate.proto";
import "google/protobuf/timestamp.proto";

service UserService {
  rpc CreateUser(CreateUserRequest) returns (CreateUserResponse);
  rpc GetUser(GetUserRequest) returns (GetUserResponse);
}

message User {
  string id = 1 [(buf.validate.field).string.uuid = true];
  string username = 2 [(buf.validate.field).string = {min_len: 3, max_len: 32}];
  string email = 3 [(buf.validate.field).string.email = true];
  int32 status = 4;
  google.protobuf.Timestamp created_at = 5;
  google.protobuf.Timestamp updated_at = 6;
}

message CreateUserRequest {
  string username = 1 [(buf.validate.field).string = {min_len: 3, max_len: 32}];
  string email = 2 [(buf.validate.field).string.email = true];
  string password = 3 [(buf.validate.field).string = {min_len: 8, max_len: 64}];
}

message CreateUserResponse {
  User user = 1;
}

message GetUserRequest {
  string id = 1 [(buf.validate.field).string.uuid = true];
}

message GetUserResponse {
  User user = 1;
}

```

---

### 阶段二：编译传输层代码 (DTO & Stubs)

借助 `buf` CLI 执行预编译，自动输出 Go 传输层结构体及校验能力。

在工程根目录执行：

```bash
buf generate

```

**产物分布 (`gen/go/user/v1/`)**：

* `user.pb.go`：对应的 Request/Response/Entity 传输结构体。
* `user.pb.validate.go`：根据 `buf.validate` 注解自动生成的强类型运行时校验代码。
* `user_grpc.pb.go`：gRPC 服务端/客户端接口定义。

---

### 阶段三：SQL DDL 转化为版本化 Migration

通过 AI 结合 `.proto` 提取实体结构，转化为生产级 SQL DDL。

**规则与校验（DBA / 开发者把关）**：

* **补充物理属性**：`.proto` 中表达不完的字段物理长度（如 `VARCHAR(32)`）、主键递增策略/UUID、默认值、字符集（`utf8mb4`）。
* **索引设计**：添加主键 `PRIMARY KEY`、唯一索引 `UNIQUE KEY` 以及业务查询索引 `KEY`。
* **物理隔离**：不包含密码哈希（`password_hash`）等隐私字段的 `.proto` 契约，需要在 SQL DDL 中补充。

#### Migration 文件：`migrations/000001_create_users_table.up.sql`

```sql
CREATE TABLE IF NOT EXISTS `users` (
  `id` VARCHAR(36) NOT NULL COMMENT '用户UUID',
  `username` VARCHAR(32) NOT NULL COMMENT '用户名',
  `email` VARCHAR(254) NOT NULL COMMENT '邮箱',
  `password_hash` VARCHAR(72) NOT NULL COMMENT '密码哈希',
  `status` TINYINT NOT NULL DEFAULT 1 COMMENT '用户状态: 1-正常 2-冻结',
  `created_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  `deleted_at` DATETIME(3) NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_username` (`username`),
  UNIQUE KEY `uk_email` (`email`),
  KEY `idx_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

```

**执行迁移**：使用 `golang-migrate` 或 `Atlas` 工具将结构变动应用到开发/测试数据库：

```bash
migrate -path migrations/ -database "mysql://root:secret@tcp(localhost:3306)/app_db" up

```

---

### 阶段四：利用 `sqlc` 生成模型与 DAO 层

写好纯 SQL 语句，使用 `sqlc` 直接编译 SQL 文件并生成高性能 Go 持久层代码。

#### 1. 定义查询语句：`queries/users.sql`

```sql
-- name: CreateUser :exec
INSERT INTO users (id, username, email, password_hash, status)
VALUES (?, ?, ?, ?, ?);

-- name: GetUserByID :one
SELECT id, username, email, status, created_at, updated_at
FROM users
WHERE id = ? AND deleted_at IS NULL LIMIT 1;

-- name: GetUserByEmail :one
SELECT id, username, email, password_hash, status, created_at, updated_at
FROM users
WHERE email = ? AND deleted_at IS NULL LIMIT 1;

```

#### 2. 配置 `sqlc.yaml`

```yaml
version: "2"
sql:
  - schema: "migrations"
    queries: "queries"
    gen:
      go:
        package: "db"
        out: "internal/db"
        sql_package: "database/sql"
        emit_json_tags: true
        emit_prepared_queries: false

```

#### 3. 执行代码生成

```bash
sqlc generate

```

**生成产物 (`internal/db/`)**：

* `models.go`：与数据库表完全对齐的 Go 结构体 (`db.User`)。
* `users.sql.go`：包含 `CreateUser`、`GetUserByID` 等强类型方法的代码，实现零反射、零手写 CRUD。

---

### 阶段五：业务层组装与结构体转换 (Service & Converter)

在 Service 层将 `internal/db`（持久层 Model）与 `gen/go/user/v1`（传输层 Proto）进行组合。结构体双向赋值可通过 **AI Copilot** 或 **编译期转换工具（如 `goverter`）** 完成。

#### 示例代码：`internal/service/user.go`

```go
package service

import (
	"context"
	"database/sql"

	userv1 "go-buf-api-template/gen/go/user/v1"
	"go-buf-api-template/internal/db"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type UserService struct {
	queries *db.Queries
	db      *sql.DB
}

func NewUserService(database *sql.DB) *UserService {
	return &UserService{
		queries: db.New(database),
		db:      database,
	}
}

func (s *UserService) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	// 1. 业务逻辑计算（如密码加盐 Hash）
	hashedPassword, err := hashPassword(req.GetPassword())
	if err != nil {
		return nil, err
	}

	userID := uuid.New().String()

	// 2. 调用 sqlc 生成的强类型 DAO 方法落库
	err = s.queries.CreateUser(ctx, db.CreateUserParams{
		ID:           userID,
		Username:     req.GetUsername(),
		Email:        req.GetEmail(),
		PasswordHash: hashedPassword,
		Status:       1,
	})
	if err != nil {
		return nil, err
	}

	// 3. 查询新落库的 User
	dbUser, err := s.queries.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	// 4. 将 db.User 转换为契约 DTO (userv1.User) 并返回
	return &userv1.CreateUserResponse{
		User: dbUserToProto(dbUser),
	}, nil
}

// dbUserToProto 映射转换函数（可写提示词由 AI 自动生成）
func dbUserToProto(u db.GetUserByIDRow) *userv1.User {
	return &userv1.User{
		Id:        u.ID,
		Username:  u.Username,
		Email:     u.Email,
		Status:    int32(u.Status),
		CreatedAt: timestamppb.New(u.CreatedAt),
		UpdatedAt: timestamppb.New(u.UpdatedAt),
	}
}

```

---

### 阶段六：Handler 接入与自动参数校验

网络接入层（Handler/Controller）只需接收请求并透传，中间件负责自动解析拦截与验证。

#### 示例代码：`internal/handler/user.go`

```go
package handler

import (
	"context"

	userv1 "go-buf-api-template/gen/go/user/v1"
	"go-buf-api-template/internal/service"
)

type UserHandler struct {
	userv1.UnimplementedUserServiceServer
	userSvc *service.UserService
}

func NewUserHandler(userSvc *service.UserService) *UserHandler {
	return &UserHandler{userSvc: userSvc}
}

func (h *UserHandler) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	// 注意：buf.validate 的校验在 gRPC Interceptor 或 Connect 中间件中已自动触发并拦截非法请求
	// 此处直接调用 Service 层
	return h.userSvc.CreateUser(ctx, req)
}

```

---

## 4. 目录结构规范

标准化工程目录树展示如下：

```text
├── api/
│   └── proto/
│       └── user/
│           └── v1/
│               └── user.proto         # API 契约源头
├── buf.yaml                           # Buf 配置
├── buf.gen.yaml                       # Buf 代码生成规则
├── gen/                               # [自动生成] 不要手写修改此目录
│   └── go/
│       └── user/
│           └── v1/
│               ├── user.pb.go
│               ├── user.pb.validate.go
│               └── user_grpc.pb.go
├── migrations/                        # 版本化 DDL 建表文件
│   └── 000001_create_users_table.up.sql
├── queries/                           # sqlc 原始 SQL 逻辑文件
│   └── users.sql
├── sqlc.yaml                          # sqlc 配置
├── internal/
│   ├── db/                            # [自动生成] sqlc 导出的 DAO/Model
│   │   ├── db.go
│   │   ├── models.go
│   │   └── users.sql.go
│   ├── service/                       # 核心业务逻辑层 (处理模型转换与事务)
│   │   └── user.go
│   └── handler/                       # 入口接入层 (极薄)
│       └── user.go
└── main.go

```

---

## 5. 常见问题与工程对齐 (FAQ)

### Q1：`sqlc.User` 和 `userv1.User` 冗余吗？为什么要两套结构体？

不冗余。它们服务于不同的边界：

* **`userv1.User`（API 契约）**：对前端/外部微服务暴露。需要考虑字段隐藏（如去除 `password_hash`）、协议版本兼容性、时间戳格式转换（`google.protobuf.Timestamp`）。
* **`db.User`（持久层 Model）**：对数据库暴露。关注数据库物理字段类型（如 `sql.NullString`）、索引映射、数据库内部标记。
两者的拆分保障了存储架构重构时不破损外部 API 契约。

### Q2：修改了一个字段，完整的迭代流是怎样的？

得益于工具链，修改字段极其敏捷：

1. 修改 `user.proto`（修改或新增字段）。
2. 执行 `buf generate` 更新传输层 Go 代码。
3. 修改 `migrations/*.sql` 并更新数据库，在 `queries/*.sql` 中调整查询。
4. 执行 `sqlc generate` 更新持久层 Go 代码。
5. 执行 `go build ./...`，**利用 Go 编译器定位所有需要修正的赋值点**，AI 协助一键修复，完成迭代。
