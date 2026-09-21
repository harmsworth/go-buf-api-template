# 项目规则 — go-buf-api-template（Schema-First / Buf 生态）

## 项目定位

本仓库是 Protobuf + Buf 生态的 Schema-First / IDL-First API 工程模板：契约即代码、校验前移到 IDL（protovalidate）、破坏性变更机器可判。权威规范在 `docs/`（入口 [docs/README.md](docs/README.md)），本文件只列**跨任务硬约束**与**任务索引**，不重复展开。

## 硬约束（任何涉及 API / DB / Service 的改动都适用）

- `.proto`：package `<domain>.v1` 与目录 `proto/<domain>/<version>/` 强一致；Service 名 `<Domain>Service`；RPC 方法驼峰；消息字段 snake_case。
- 入参校验一律声明在契约中：使用 `buf.validate`（禁用第三方过期校验库）；邮箱 `string.email = true`；UUID `string.uuid = true`；字符串长度显式 `min_len` / `max_len`。
- 时间字段一律 `google.protobuf.Timestamp`，禁止用 string / int64 表示时间。
- PATCH 三态语义遵循 [docs/SCHEMA_FIRST_GUIDE.md](docs/SCHEMA_FIRST_GUIDE.md) §5：optional 指针区分 Absent / Explicit Value / Explicit Clear；支持显式清空的字段禁用 `min_len` / `email` 等拒绝空串的规则。
- `password_hash` 等服务端独占敏感字段**严禁**映射进任何 Proto 响应结构体（Converter 中必须显式置空）。
- 持久层使用 sqlc 生成的代码 + 纯原生零反射 Converter（如 `dbUserToProto`）；禁止引入 GORM 或反射依赖。
- 时间映射使用 `timestamppb.New(u.CreatedAt)`；`sql.Null*` 可空字段须正确转换。
- DDL：`ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`；所有字段带 COMMENT；软删除字段 `deleted_at DATETIME(3) NULL` 并建索引；补业务唯一键与常用索引。
- `gen/` 由 `buf generate` 产出且**必须提交**（CI drift 检查依赖）；修改 `.proto` 后必须跑 `buf lint && buf generate`。

## 任务 → 提示词索引（命中时先读对应文件，严格按其中规则执行）

| 任务 | 先读 |
|---|---|
| 生成 / 修改 `.proto` 契约 | [docs/Proto Expert.md](docs/Proto%20Expert.md) |
| 由 `.proto` 生成建表 DDL（up.sql） | [docs/DB Schema Architect.md](docs/DB%20Schema%20Architect.md) |
| 实现 Service 层方法 / db↔proto 转换函数 | [docs/Go Service Integrator.md](docs/Go%20Service%20Integrator.md) |
