# go-buf-api-template

基于 **Protobuf + Buf 生态**的 Schema-First / IDL-First API 工程模板：契约即代码、入参校验前移到 IDL（protovalidate）、破坏性变更机器可判、Handler 层零手写校验。

`.proto` 是接口的**唯一事实来源**：数据库表结构、Go 传输层 DTO、OpenAPI 文档全部由契约推导，不允许反向手写。

## 铁律（违反一律打回）

1. 先改契约再改代码：新接口一律先发**只含 `.proto` 变更**的 MR。
2. 校验只写在契约里：`buf.validate` / CEL 声明约束，Handler 只装配 protovalidate 拦截器，**禁止手写入参校验**。
3. 持久层统一用 GORM（本项目不使用 sqlc）：**禁用 AutoMigrate**，表结构走 `db/migrations/*.sql` 由 golang-migrate 执行；转换函数必须纯原生、零反射；`password_hash` 等敏感字段严禁映射进 Proto 响应。

## 目录地图

```text
├── buf.yaml                  # Buf Workspace v2：modules / deps / lint(STANDARD) / breaking(FILE)
├── buf.lock                  # 依赖锁定（buf dep update 产出，必须提交）
├── buf.gen.yaml              # 生成配置：Go / gRPC / gateway / openapiv2（合并单文件）
├── buf.gen.config.yaml       # 生成配置：internal/conf/conf.pb.go
├── api/<domain>/v1/*.proto   # API 契约本体（user/v1、todo/v1）
├── gen/go/                   # 生成：Go 结构体 / gRPC Stub / grpc-gateway（必须提交）
├── gen/openapi/              # 生成：合并后的 openapi.swagger.yaml（必须提交）
├── cmd/server/               # 服务入口：main.go + wire 依赖注入（Gin + gRPC + grpc-gateway）
├── configs/config.yaml       # 运行配置（MySQL / Gin / slog）
├── db/migrations/            # golang-migrate SQL 迁移（嵌入二进制）
├── internal/platform/        # 基础设施：config / database / logger
├── internal/todo/            # Todo 业务包（Package by Feature）
├── docs/                     # 规范与范式文档（入口 docs/README.md）
├── .codebuddy/rules/         # CodeBuddy / WorkBuddy 项目规则（必须提交）
└── CODEBUDDY.md              # 本文件：AI 全局上下文
```

## 日常命令速查

```bash
buf dep update                              # 更新依赖锁定（改了 deps 后必跑）
buf lint                                    # 契约风格/结构/命名检查
buf format --diff --exit-code               # 格式检查
buf generate                                # 生成 Go / OpenAPI 代码
buf breaking --against '.git#branch=main'   # 破坏性变更检查
buf build                                   # 编译契约（IDE 大量报错时先跑它）
```

## AI 规则（CodeBuddy / WorkBuddy）

规则位于 `.codebuddy/rules/<规则名>/RULE.mdc`，随仓库版本控制，团队共享同一套约束。

| 规则 | 加载方式 | 触发场景 |
|---|---|---|
| `.codebuddy/rules/schema-first-overview/RULE.mdc` | 总是加载 | 总纲：仓库结构、六阶段闭环、命令四件套、全局禁令、文档索引 |
| `.codebuddy/rules/proto-expert/RULE.mdc` | 按需加载 | 写/改 `.proto`、设计 API、添加 `buf.validate` 校验、处理 optional 三态 |
| `.codebuddy/rules/db-schema-architect/RULE.mdc` | 按需加载 | 由 `.proto` 推导建表 DDL、`up.sql` 迁移、索引与软删除设计 |
| `.codebuddy/rules/go-service-integrator/RULE.mdc` | 按需加载 | 实现 Service 方法、PO→Proto 转换、GORM 查询、gRPC 服务实现 |

## 参考文档

| 文档 | 内容 |
|---|---|
| `docs/README.md` | 入口导航：阅读顺序、快速开始、新项目迁移清单、FAQ |
| `docs/SCHEMA_FIRST_GUIDE.md` | 规范主文档：协作闭环、Zero Value Trap、CI/CD 卡点、版本纪律 |
| `docs/EXAMPLES.md` | 代码层范式：拦截器装配、optional 三态读写、测试、常见坑 |
| `proto/user/v1/user.proto` | 契约模板本体（含逐条规范注释），写新契约前先读它 |

## 约定与版本

- package 与目录强一致：`proto/<domain>/<version>/` ↔ `<domain>.<version>`；`go_package` 必填。
- 新项目复用需替换占位符 `github.com/yourorg/new-td`（全局搜索 `yourorg` 验证为空）。
- 已验证版本：Buf CLI 1.72.0 · protoc-gen-go v1.36.12 · protoc-gen-go-grpc v1.6.2 · grpc-gateway v2.30.0 · go-grpc-middleware/v2 v2.3.4。
