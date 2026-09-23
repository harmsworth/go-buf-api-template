# go-buf-api-template

基于 **Protobuf + Buf 生态**的 Schema-First / IDL-First API 工程模板：契约即代码、入参校验前移到 IDL（protovalidate）、破坏性变更机器可判、Handler 层零手写校验。

以用户域 `user.v1` 为示例契约（8 个 RPC + `buf.validate` 全规则族），可直接照抄改造为新项目的 API 契约。

## 仓库结构

```text
├── buf.yaml                  # Buf Workspace：Module / BSR 依赖 / lint(STANDARD) / breaking(FILE)
├── buf.lock                  # 依赖锁定（buf dep update 产出，必须提交）
├── buf.gen.yaml              # 代码生成配置：remote plugins 版本锁定，无需本地装 protoc
├── api/
│   ├── user/v1/user.proto    # 用户域契约：8 个 RPC + protovalidate 校验 + google.api.http 注解
│   └── todo/v1/todo.proto    # 待办域契约：5 个 RPC + protovalidate 校验 + google.api.http 注解
├── gen/
│   ├── go/user/v1/           # 生成：Go 结构体 / gRPC Stub / grpc-gateway 反向代理
│   ├── go/todo/v1/           # 生成：Todo 域同构产物
│   └── openapi/              # 生成：openapi.swagger.yaml（Swagger 2.0，openapiv2 合并单文件）
├── .codebuddy/rules/         # CodeBuddy / WorkBuddy 项目规则（随仓库提交，团队共享）
├── CODEBUDDY.md              # AI 全局上下文（CodeBuddy / WorkBuddy 默认加载）
└── docs/                     # 规范与范式文档（入口见 docs/README.md）
```

> `gen/` 是 `buf generate` 的产物，**需提交进 git**（CI drift 检查依赖它），不要加入 `.gitignore`。

## 文档导航

| 文档 | 内容 |
|---|---|
| [docs/README.md](docs/README.md) | 入口导航：阅读顺序、10 分钟快速开始、新项目迁移清单、FAQ |
| [docs/SCHEMA_FIRST_GUIDE.md](docs/SCHEMA_FIRST_GUIDE.md) | 规范主文档：协作闭环、Zero Value Trap、CI/CD 卡点、版本纪律 |
| [docs/EXAMPLES.md](docs/EXAMPLES.md) | 代码层范式：拦截器零侵入校验、optional 指针三态处理、测试、常见坑 |
| [api/user/v1/user.proto](api/user/v1/user.proto) | 契约模板本体（含逐条规范注释） |
| [api/todo/v1/todo.proto](api/todo/v1/todo.proto) | 待办域契约（含 google.api.http 与 update_mask） |
| [.codebuddy/rules/](.codebuddy/rules/) | AI 规则（CodeBuddy / WorkBuddy 通用）：总纲 + Proto/DBA/Go 三个专家角色 |
| [CODEBUDDY.md](CODEBUDDY.md) | AI 全局上下文：铁律、目录地图、命令速查、规则索引 |

## 快速开始

前置：Go 1.26+；Buf CLI ≥ 1.72（`go install github.com/bufbuild/buf/cmd/buf@latest`）。

```bash
# 1) 解析 BSR 依赖，产出/更新 buf.lock（首次必做）
buf dep update

# 2) 契约质量检查
buf lint
buf format --exit-code

# 3) 生成代码 → gen/go/{user,todo}/v1/ 与 gen/openapi/openapi.swagger.yaml
buf generate

# 4) 生成配置结构体 → internal/conf/conf.pb.go
buf generate --template buf.gen.config.yaml
```

等价的 `make` 别名：`make deps` / `make proto-lint` / `make gen` / `make build` / `make run` / `make check`，`make help` 查看全部。

## 日常命令速查

| 命令 | 用途 |
|---|---|
| `buf dep update` | 更新依赖锁定（改了 `deps` 后必跑） |
| `buf lint` | 契约风格/结构/校验规则检查 |
| `buf format --exit-code` | 契约格式检查（未格式化则非零退出） |
| `buf breaking --against '.git#branch=main'` | 破坏性变更检查（对 main） |
| `buf generate` | 生成 Go / gateway / Swagger 代码 |
| `buf generate --template buf.gen.config.yaml` | 生成 `internal/conf/conf.pb.go` |
| `buf build` | 编译契约（IDE 报错时先跑它定位问题） |
| `make check` | 提交前全量自检（format + lint + breaking + generate + vet + build） |

## 运行服务（Todo 模块）

技术栈：Gin + GORM(MySQL) + golang-migrate + wire + `log/slog` + `go.uber.org/automaxprocs`；配置由 `internal/conf/conf.proto` 生成的结构体承载（不使用 Viper）。

> **automaxprocs**：进程启动时按 cgroup / 容器的 CPU quota 自动设置 `GOMAXPROCS`（日志接入 slog）。
> 无 CPU 限额时保持原值并打印 `maxprocs: Leaving GOMAXPROCS=...: CPU quota undefined`——该日志出现在 slog 初始化**之前**，因此格式是 slog 默认 logger 的样式并输出到 stderr，属正常现象。

```bash
# 启动：首次会自动建库并执行 db/migrations 下的迁移（禁用 GORM AutoMigrate）
go run ./cmd/server -conf configs/config.yaml

# 冒烟
curl -X POST localhost:8080/api/v1/todos -H "Content-Type: application/json" -d '{"title":"buy milk"}'
curl "localhost:8080/api/v1/todos?page_size=10"
curl -X PATCH localhost:8080/api/v1/todos/<id> -H "Content-Type: application/json" -d '{"status":3}'
curl -X DELETE localhost:8080/api/v1/todos/<id>
```

一个进程同时暴露三种入口（业务实现只有一份，位于 `internal/todo`）：

| 入口 | 配置 | 地址 | 说明 |
|---|---|---|---|
| Gin HTTP | `server.addr` | `:8080` | `/api/v1/todos`、`/healthz` |
| gRPC | `server.grpc_addr` | `:9090` | `todo.v1.TodoService` |
| grpc-gateway | `server.gateway_addr` | `:8081` | REST `/v1/todos` 反代到 gRPC |

```bash
# 经 grpc-gateway 调用（走真实 gRPC 链路）
curl -X POST localhost:8081/v1/todos -H "Content-Type: application/json" -d '{"title":"buy milk"}'
curl "localhost:8081/v1/todos?pageSize=10"
curl -X PATCH localhost:8081/v1/todos/<id> -H "Content-Type: application/json" -d '{"status":3}'
```

- 请求与响应均为 `todo.v1` / `user.v1` 的 Proto JSON（`protojson` 解析，兼容 snake_case 与枚举名）。

### User 模块（`internal/user`）

与 Todo 同构：PO + Service + Gin Handler + gRPC Server 收拢在一个包内，表结构由 `db/migrations/000002_create_users_table.up.sql` 维护。

| 方法 | Gin（:8080，前缀 `/api/v1`） | grpc-gateway（:8081，前缀 `/v1`） |
|---|---|---|
| Login | `POST /api/v1/auth/login` | `POST /v1/auth/login` |
| CreateUser | `POST /api/v1/users` | `POST /v1/users` |
| ListUsers | `GET /api/v1/users` | `GET /v1/users` |
| GetUser | `GET /api/v1/users/:user_id` | `GET /v1/users/{user_id}` |
| UpdateUser | `PATCH /api/v1/users/:user_id` | `PATCH /v1/users/{user_id}` |
| DeleteUser | `DELETE /api/v1/users/:user_id` | `DELETE /v1/users/{user_id}` |
| ChangePassword | `POST /api/v1/users/:user_id/change-password` | `POST /v1/users/{user_id}:changePassword` |
| ResetPassword | `POST /api/v1/users/:user_id/reset-password` | `POST /v1/users/{user_id}:resetPassword` |

> - Gin 侧统一用 `/api/v1` 前缀（与 `/api/v1/todos` 对齐）；gateway 侧沿用 proto 注解里的 `/v1`。
> - Gin 不支持单路径段内的冒号（一个段只能有一个通配符），故自定义方法在 Gin 侧用 `-password` 后缀；proto 原生 `:changePassword` 形态由 gateway 提供。
> - **两个入口的 query 绑定语义已统一**：Gin 侧经 `httpx.BindQuery` 复用 grpc-gateway 的解析器，因此枚举名（`status=USER_STATUS_ACTIVE`）、bool 标准写法（`show_deleted=TRUE`）、lowerCamel 字段名（`pageSize`）两边一致；已知字段的非法值返回 400，未知参数被忽略（与 gateway 同源行为）。

- `password_hash` 是服务端独占字段：`ToProto()` 不映射，且网关 marshaler 已关闭 `EmitUnpopulated`，响应中不会出现该字段。
- JWT 签发为留桩（`Login` 返回 `stub-access-token`），接入认证模块时替换 `Service.Login` 即可。
- 入参校验来自契约中的 `buf.validate` 规则：gRPC 侧由 protovalidate 拦截器执行（recovery → validation），Gin 侧由 Handler 调用同一引擎；业务代码零手写校验。
- 重新生成依赖注入代码：`go run github.com/google/wire/cmd/wire@latest ./cmd/server`。

### AIP 标准化能力（`go.einride.tech/aip`）

`todo` 与 `user` 两个模块的 List / Update 均按 Google AIP 实现：

| AIP | 能力 | 用法示例 |
|---|---|---|
| AIP-158 | 游标分页 | `?page_size=2&page_token=<nextPageToken>`，token 由 `pagination.ParsePageToken/Next` 生成（含请求校验和，跨页改条件会被拒） |
| AIP-160 | 通用过滤 | `?filter=status = "DONE" AND title : "meet"`、支持 `= != < <= > >= :` 与 `AND/OR/NOT` |
| AIP-132 | 多字段排序 | `?order_by=created_at desc, title asc`（字段走白名单校验） |
| AIP-134 | 增量更新 | `PATCH` 时传 `update_mask`（如 `"title,status"`），仅 mask 内字段被写入 |

- 过滤/排序的"表达式 → SQL"翻译收在 `internal/platform/aipgorm`，两个模块只声明自己的字段 Schema。
- 可过滤字段是显式的：`aipgorm.Schema` 未声明的字段在 `ParseFilter` 类型检查阶段就被拒绝（400）。
- 枚举按字符串匹配（`status = "DONE"`），由翻译层解析成枚举号再与整型列比较。
- 不传 `update_mask` 时仍走原有 PATCH 三态语义（Absent / 传值 / 空串清空），向后兼容。

## 新项目复用

复制 `buf.yaml`、`buf.gen.yaml`、`buf.gen.config.yaml`、`api/<domain>/<version>/*.proto` 到新仓库，把 `go_package` 中的 `go-buf-api-template` 替换为你的 module 名，然后执行 `buf dep update && buf lint && buf generate`。完整迁移清单见 [docs/README.md §5](docs/README.md)。

## 已验证版本

Buf CLI 1.72.0 · protoc-gen-go v1.36.12 · protoc-gen-go-grpc v1.6.2 · grpc-gateway v2.30.0（详见 [docs/README.md §7](docs/README.md)）
