# Go 分层与类型边界（go-arch）

> 按需加载：当任务涉及跨层类型边界设计/审计、新增分层或转换器、领域包划分、并发与通用 Go 编码规范时，先读本文件再动手。

本规则由两套约束融合并适配本仓现实：**边界原生类型（primitive-at-edges）/ 业务内强类型**的分层纪律，与 Go 工程通用编码规范。本仓不使用 `app/*` / `business/domain/*` / `stores/*db` 目录，而是 Package by Feature（`internal/<domain>/`），下表给出概念层与本仓物理位置的映射。

## 1. 三层映射与类型边界

```text
传输边缘（API）              业务包                        持久边缘（DB）
gen/go DTO（buf 生成）  ──►  internal/<domain>/       ──►  GORM PO（model.go）
userv1.* / todov1.*          service.go 领域类型/规则         原生 SQL 类型
handler.go / server.go  ◄──  model.go ToProto()/applyUpdate ◄──  repository.go
```

- **传输边缘只出现"原生"类型**：即契约生成的 proto DTO 与 Go 基础类型。GORM PO、领域内部类型**禁止**出现在 `handler.go` / `server.go` 的对外签名之外（响应一律 `ToProto()` 产物）。
- **PO 只活在持久边缘**：GORM PO 不跨过 `service.go` 对外暴露；出业务包必经 `ToProto()`。
- **每次跨界必须有具名转换函数，禁止直接赋值跨边界**：`ToProto()`（PO→DTO）、`applyUpdate()`（DTO 三态→PO）、Repository 返回 PO 由 Service 显式消化。转换必须纯原生、逐字段显式赋值，**禁用反射**。
- 校验在边缘完成：契约 `buf.validate` 由 protovalidate 拦截器（gRPC）/ `httpx`（Gin）执行，业务层零手写校验。

## 2. 包依赖纪律（MUST）

- **业务包之间禁止横向 import**：`internal/user` 不得 import `internal/todo`，反之亦然。跨域组合只发生在 `cmd/server`（App 层）。
- `internal/platform/*` 是共享叶子包：任何层可 import；**platform 禁止反向依赖业务包**。
- `internal/conf` / `gen/go` 同为叶子：谁需要谁 import，不得被反向依赖。
- 业务包不 import `github.com/google/wire`（接线知识集中在 `cmd/server/providers.go`）。

## 3. 指针与可空（克制原则）

- 优先值类型；指针仅在"必须区分未设置与零值"时使用——本仓唯一合法场景是契约 `optional` 三态（生成 Go 指针）与 DB 可空列（`*time.Time` / `sql.Null*`）。
- `nil` 语义必须保真：PO 可空字段为 `nil` 时，`ToProto()` 保持对应 `optional` 字段为 `nil`，**禁止落零值**（Zero Value Trap）。
- 不为"表达可选"而新增指针参数/返回值/字段；能用零值、显式标志或接口窄化解决的，不用指针。

## 4. 接口与构造器

- 接口定义在**消费方**（`service.go` 声明 `Repository`），实现返回处（`NewRepository`）返回接口——本仓唯一的刻意偏离，理由见 `go-service-integrator.md` §1。
- 强类型（若未来引入领域 ID/枚举包装类型）：构造器一律 `Parse<Type>` / `MustParse<Type>`，**禁止** `New<Type>` / `MustNew<Type>`；`Must*` 只用于测试与已知常量，禁止用于请求派生数据。
- 单测注入唯一替换点是 `NewService(repo Repository, ...)` 形参；禁止绕过构造函数写未导出字段。

## 5. 通用 Go 编码规范（MUST / MUST NOT）

MUST：

- 所有阻塞操作首参 `context.Context`，并尊重取消；goroutine 必须有明确生命周期（ctx 可取消、channel 有收尾）。
- 错误显式处理：`fmt.Errorf("...: %w", err)` 包装传播；领域错误在声明处用 `errorsx.New` 固化映射。
- 并发代码的测试跑 `-race`；新代码过 `gofmt` 与 `go vet ./...`。
- 导出的类型/函数/包有文档注释；测试 table-driven + 子测试。
- 泛型按需使用（如 `aipgorm.List[T]`），约束用 `X | Y` 联合类型表达。

MUST NOT：

- 忽略错误（无理由的 `_` 赋值）、裸 `return`、用 `panic` 做正常流程错误处理。
- 无性能依据的反射；硬编码配置（走 `internal/conf`）。
- 混用同步/异步模式无清理；在业务包内 import 数据库驱动类型做错误判断（驱动错误翻译归仓储层）。

## 6. 边界确认（kennedy 纪律）

在涉及**新的跨层类型**（新字段类型、新可空表达、新包装类型）落地前，先向用户陈述三类边界的具体选型并确认：① 请求/响应字段用什么类型；② PO 列用什么原生类型与 NULL 表达；③ 转换函数签名与返回。用户选择与本规则冲突时，**以用户选择为准**并相应调整转换器。

## 7. 一致性自查清单

- [ ] 没有领域内部类型/PO 出现在 handler/server 的对外结构中。
- [ ] 每个跨界都有具名转换函数（`ToProto` / `applyUpdate` / PO 构造），零反射。
- [ ] 业务包之间零横向 import；platform 未反向依赖业务包。
- [ ] 指针仅用于 `optional` 三态与 DB 可空；nil 语义未被零值稀释。
- [ ] 新强类型构造器命名为 `Parse*` / `MustParse*`。
- [ ] context / `%w` 包装 / 显式错误处理 / 文档注释齐全；`go vet` 通过。
- [ ] 新跨层类型选型已经用户确认。
