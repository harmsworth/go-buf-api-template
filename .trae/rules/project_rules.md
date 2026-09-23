# Schema-First 研发总纲（go-buf-api-template · Trae 项目规则）

本仓库是 **Protobuf + Buf 生态的 Schema-First / IDL-First API 工程模板**。核心信条：

> **契约即代码（SSOT）· 校验前移到 IDL · 破坏性变更机器可判 · Handler 层零手写校验。**

`.proto` 是接口的唯一事实来源。数据库表结构、Go 传输层 DTO、OpenAPI 文档全部由契约推导，不允许反向手写。

> 本文件由 Trae 每轮对话自动加载（always-on）。按需专家规则见文末「规则索引」，命中任务时**先读对应规则文件再动手**。

## 1. 仓库地图

| 路径 | 作用 | 是否入库 |
|---|---|---|
| `buf.yaml` | Buf Workspace v2：`modules: api + internal`（conf 模块豁免 `PACKAGE_DIRECTORY_MATCH` / `PACKAGE_VERSION_SUFFIX`）、`deps`（protovalidate / googleapis）、`lint: STANDARD`、`breaking: FILE` | 必须 |
| `buf.lock` | 依赖锁定，由 `buf dep update` 产出 | **必须提交** |
| `buf.gen.yaml` | 生成配置 v2：4 个 remote 插件（版本固定）、`clean: true` → `gen/go` 与 `gen/openapi` | 必须 |
| `buf.gen.config.yaml` | 生成配置：`internal/conf/conf.pb.go` | 必须 |
| `api/<domain>/<version>/*.proto` | API 契约本体（现有 `api/user/v1/user.proto`、`api/todo/v1/todo.proto`） | 必须 |
| `gen/go/`、`gen/openapi/` | `buf generate` 产物（CI drift 检查依赖） | **必须提交，禁止 .gitignore** |
| `internal/conf/conf.proto` | 启动配置契约，生成 `internal/conf/conf.pb.go` | 必须 |
| `cmd/server/` | 服务入口：`main.go` + wire 依赖注入（`wire.go` / `providers.go` / `app.go` / `wire_gen.go`） | 必须 |
| `configs/config.yaml` | 运行配置（MySQL / Gin / slog），由 `internal/platform/config` 解析为 `*conf.Bootstrap` | 必须 |
| `db/migrations/` | golang-migrate 的 `.sql` 迁移文件，嵌入二进制启动时执行 | 必须 |
| `internal/platform/` | 基础设施：config / database / logger / aipgorm / errorsx / httpx / grpcx | 必须 |
| `internal/todo/`、`internal/user/` | 业务包（Package by Feature 五件套：`model.go` + `repository.go` + `service.go` + `handler.go` + `server.go`） | 必须 |
| `docs/` | 规范与范式文档（入口 `docs/README.md`） | 必须 |
| `.trae/rules/` | Trae 项目规则（本文件所属） | 必须 |
| `.codebuddy/rules/` | CodeBuddy / WorkBuddy 项目规则（与本目录内容对等） | 必须 |

> Go module 名为 `go-buf-api-template`（无组织前缀），所有 `go_package` 必须以此开头，例如 `go-buf-api-template/gen/go/user/v1;userv1`。

## 2. 目录与 package 约定（buf lint 强制）

| 说明 | 目录 | package |
|---|---|---|
| 通用约定 | `api/<domain>/<version>/` | `<domain>.<version>` |
| 现有用户域 | `api/user/v1/` | `user.v1` |
| 新增任务域示例 | `api/task/v1/task.proto` | `task.v1` |

- package 与目录必须**强一致**（`PACKAGE_DIRECTORY_MATCH`），违反会被 `buf lint` 直接拒绝。
- `go_package` 必填：`go-buf-api-template/gen/go/<domain>/<version>;<domain><version>`。

## 3. 六阶段协作闭环

| 阶段 | 动作 | 产物 / 命令 |
|---|---|---|
| ① API 设计 | 发起**只含 `.proto` 变更**的 MR，校验规则写进契约 | `.proto` diff |
| ② 本地自检 | 提交前跑命令四件套 | 见 §4 |
| ③ MR 卡点 | CI 执行 lint + format + breaking + drift，全绿才可合并 | `.github/workflows/quality-gate.yml` |
| ④ 代码生成 | 合入 main 后生成产物 | `buf generate` |
| ⑤ Handler 实现 | 只写业务逻辑，**零手写校验** | 见 `docs/EXAMPLES.md` |
| ⑥ 联调 / 分发 | REST 走 grpc-gateway（已注解），可选发布 BSR | `buf curl` |

## 4. 常用命令速查

```bash
# Buf 四件套（改契约后缺一不可）
buf dep update                                  # 改了 deps 后必跑，产出/更新 buf.lock
buf lint                                        # 契约风格/结构/命名检查（退出码必须为 0）
buf format --diff --exit-code                   # 格式检查（Windows 缺 diff.exe 可跳过，CI 在 Linux 跑）
buf generate                                    # 生成 gen/go 与 gen/openapi
buf breaking --against '.git#branch=main'       # 破坏性变更检查

# Make 快捷方式
make wire          # 重新生成 wire_gen.go（wire 版本由 go.mod tool 指令锁定）
make wire-drift    # 校验 wire_gen.go 与 wire.go 同步（CI 卡点）
make build         # 编译全部包
make test          # 单元测试（含持久层 DryRun 测试）
make vet           # go vet 静态检查
make check         # 提交前全量自检
make run           # 启动服务（自动建库 + 执行迁移）
```

补充：`buf build`（编译契约；**IDE 大量报错时先跑它定位问题**）、`buf curl`（调试 REST）。

## 5. 全局禁令（评审一律打回）

1. **禁止手写入参校验**：校验全部声明在 `.proto`（`buf.validate` / CEL），Handler 只装配 protovalidate 拦截器。
2. **持久层统一使用 GORM**（本项目不使用 sqlc），业务包内固定五件套：
   - `model.go`：GORM PO + `ToProto()` + `applyUpdate()`（三态 / FieldMask）。
   - `repository.go`：GORM 查询实现（**只写结构体与方法，不定义接口**；导出 `NewRepository(db) Repository` 返回接口）。
   - `service.go`：业务规则；**在此声明 `Repository` 接口**（消费方视角，符合 "accept interfaces, return structs"），方法签名与 `repository.go` 一一对应。领域错误用 `errorsx.New` 在此声明，HTTP 状态码与 gRPC code 一次写清。
   - `handler.go`：Gin 绑定、路由注册（`RegisterRoutes`），编解码与校验一律走 `internal/platform/httpx`。
   - `server.go`：gRPC 实现 + 实现 `grpcx.Registrar`（`RegisterGRPC` / `RegisterGateway`）；**不写错误映射**，由 `errorsx.UnaryServerInterceptor` 统一转换。
   - **禁用 `AutoMigrate`**：表结构一律由 `db/migrations/*.sql` 经 golang-migrate 维护。
   - **禁止用反射**做结构体转换：转换函数必须纯原生、显式赋值。
   - **禁止**在业务包内重复实现 platform 已有能力（protojson 读写、错误映射、AIP 翻译、路由/服务注册契约）。
   - **不为抽象而抽象**：只有当需要"单测替换"或"第二种存储实现"时才引入接口；不要预先铺跨包层。
3. **禁止零值伪装三态**：`optional` 字段的 Absent / 显式值 / 显式清空三态必须靠指针判空区分，不准用零值代替"未设置"。
4. **禁止敏感字段下发**：`password_hash` 等服务端独占字段严禁出现在任何 Proto 响应与前端交付物。
5. **禁止改动 `buf.gen.yaml` 插件版本号**：remote 插件必须固定 `<name>:<version>`。
6. **禁止把 `gen/` 与 `buf.lock` 加入 `.gitignore`**：CI drift 检查依赖它们。
7. **新增/删除业务域的唯一接线点是 `cmd/server/providers.go`**：`DomainSet` 加 provider，`provideHTTPRegistrars` / `provideGRPCRegistrars` 各加形参，然后 `make wire`。`wire.go` / `main.go` / `app.go` 永不改动。

## 6. 三条易踩的坑

- **Zero Value Trap**：`optional string nickname` 的"未设置"与"空串"语义不同。支持**显式清空**的字段**禁用** `min_len` / `email` 等拒绝空串的规则，必须改用放行空串的 pattern，如 `^(|.{2,32})$`。
- **RE2 限制**：重复计数上限 1000（`{1,2048}` 无法编译 → 长度交给 `max_len`）；不支持 lookaround 断言（密码复杂度必须走 message 级 CEL，见 `api/user/v1/user.proto` 的 `CreateUserRequest`）。
- **枚举无法表达"清空"**：枚举字段只有两态（Absent = 不变 / 显式值 = 变更），恢复默认请显式传业务态。

## 7. Go 编码硬约束（任何 Go 文件都适用）

- 所有阻塞操作携带 `context.Context`；错误用 `fmt.Errorf("...: %w", err)` 包装，**无裸 `return`**、不忽略错误。
- 禁止用 `panic` 做正常错误处理；禁止无生命周期管理的 goroutine（必须可被 ctx 取消）。
- 禁止无性能依据的反射；禁止硬编码配置（走 `internal/conf`）。
- 导出的类型/函数/包必须有文档注释；新代码 `gofmt` + `go vet ./...` 通过。
- 测试用 table-driven + 子测试；并发代码跑 `-race`；Service 单测一律 `NewService(fakeRepo, logger)` 构造，禁止绕过构造函数写未导出字段。

## 8. 规则索引（命中任务时先读对应文件，严格按其中规则执行）

| 规则文件 | 触发场景 |
|---|---|
| `.trae/rules/proto-expert.md` | 写/改 `.proto`、设计新 API、添加 `buf.validate` 校验、optional 三态 |
| `.trae/rules/db-schema-architect.md` | 由 `.proto` 推导建表 DDL / 迁移 SQL / 索引与软删除设计 |
| `.trae/rules/go-service-integrator.md` | 实现 Service 方法、PO→Proto 转换、GORM 查询、gRPC/Gin 实现 |
| `.trae/rules/go-arch.md` | 跨层类型边界审计、新增分层/转换器、领域包划分 |

## 9. 参考文档与版本

- `docs/SCHEMA_FIRST_GUIDE.md` — 规范主文档：协作闭环、Zero Value Trap、CI/CD 卡点、版本纪律
- `docs/EXAMPLES.md` — 代码层范式：拦截器装配、`optional` 三态读写、测试、常见坑
- `docs/README.md` — 导航、快速开始、新项目迁移清单、FAQ
- `api/user/v1/user.proto` — 契约模板本体（含逐条规范注释），写新契约前先读它

| 组件 | 版本 |
|---|---|
| Go | 1.26.2 |
| Buf CLI | 1.72.0 |
| protoc-gen-go（remote） | v1.36.12 |
| protoc-gen-go-grpc（remote） | v1.6.2 |
| grpc-gateway 生成插件 / 运行时 | v2.30.0 |
| openapiv2（remote，Swagger 2.0 合并单文件） | v2.30.0 |
| go-grpc-middleware/v2 | v2.3.4 |
| GORM | v1.31.2 |
| golang-migrate | v4.20.1 |
| wire | v0.7.0（go.mod tool 指令锁定） |
