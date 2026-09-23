# docs/planning — 规划与执行看板

> 本目录放**待办事项与执行方案**（what / how），与 `docs/` 下另外三份"规范文档"（`README.md` / `SCHEMA_FIRST_GUIDE.md` / `EXAMPLES.md`，讲"该怎么做"）职责区分开。
> 规范文档一旦落定就应当稳定；本目录的文档**会随进度变化**，因此每条都带状态。

| 文档 | 类型 | 说明 |
|---|---|---|
| [2026-09-22-优化清单.md](./2026-09-22-优化清单.md) | what | 全量代码走查问题清单：P0（上线前必修）/ P1（架构可维护性）/ P2（细节打磨），每条含「现状 → 风险 → 改法 → 工作量」 |
| [2026-09-23-wire-接线方案.md](./2026-09-23-wire-接线方案.md) | how | 对照 kratos-layout 的 wire 加载方式做机制拆解与取舍，给出 S0–S6 分阶段执行方案 |

图例：✅ 已完成 ｜ 🟡 部分完成 ｜ ⬜ 未开始

---

## 一、wire / 装配重构（方案 S0–S6）—— 全部 ✅

> 依据：2026-09-23 实测（`go build ./...` / `go vet ./...` / `go test ./... -count=1` 全绿）。

| 阶段 | 内容 | 状态 | 验收证据 |
|---|---|---|---|
| S0 | 工具链与 CI 地基 | ✅ | `Makefile` 有 `test` / `wire-drift` 且已进 `check`；wire 版本钉死（不再用 `@latest`）；CI 增加 `Unit test` + `Wire drift check` |
| S1 | 接口边界改造 | ✅ | `NewRepository(db) Repository` + `var _ Repository = (*repository)(nil)`；`NewService(repo, log)`；测试统一 `NewService(fake, logger.Nop())`（19 处，`testService` 跳板已删） |
| S2 | 装配收敛 `providers.go` | ✅ | `providers.go` 158 行；`main.go` 271 → **57 行**；`wire.go` 20 行；`logger.New` 返回 `(*slog.Logger, func())`，另加 `Nop()` / `Bootstrap()` |
| S3a | `platform/errorsx` | ✅ | `errorsx.go` + `interceptor.go` + `errorsx_test.go`；gRPC 拦截器链 `errorsx → recovery → protovalidate` |
| S3b | `platform/httpx` + 合并 router | ✅ | `httpx.go`；`var _ httpx.Registrar = (*Handler)(nil)`；`router.go` 已删（两域各少 1 文件）；**`protovalidate.New()` 全仓收敛为 1 处**（`httpx.NewValidator`） |
| S3c | `aipgorm` 抽取 List 骨架 | ✅ | `aipgorm_test.go` 已建；SQL 断言（`ORDER BY` / `LIKE` 参数绑定 / 枚举翻译）有回归网 |
| S4 | 注册自动化 + App 生命周期 | ✅ | `platform/grpcx`；`var _ grpcx.Registrar = (*Server)(nil)`；`cmd/server/app.go` 154 行承载三 server 与优雅退出；`main.go` 已无 `todo.` / `user.` 业务类型引用 |
| S5 | 持久层测试（GORM DryRun + ToSQL） | ✅ | `todo/repository_test.go` 200 行、`user/repository_test.go` 195 行；零新增依赖 |
| S6 | 文档与 AI 规则同步 | ✅ | `RULE.mdc` 16 处、`CODEBUDDY.md` 4 处、`EXAMPLES.md` 4 处已含 httpx / errorsx / grpcx / wire-drift |

**已知遗留（不阻塞）**

- ⬜ `platform/httpx`、`platform/grpcx` 自身**无单测**（`aipgorm`、`errorsx` 有）。
- ⬜ `.gitattributes` 的 `* text=auto eol=lf` 会让工作区换行归一化，建议单独跑一次 `git add --renormalize .` 并单独提交，避免污染后续 PR diff。

---

## 二、优化清单进度（P0 / P1 / P2）

### P0：上线前必修

| # | 条目 | 状态 | 备注 |
|---|---|---|---|
| 1 | 零测试 | 🟡 | 8 个测试文件（`aipgorm` / `errorsx` / `todo` / `user`）；**Handler 层 `httptest` 未补** |
| 2 | 明文密钥 + 配置无环境变量覆盖 | ⬜ | `configs/config.yaml` 仍是 `jwt.secret: "1221233232"`、DSN 含 `root:123456`；`conf.proto` 无 env overlay |
| 3 | 启动即执行迁移 | ⬜ | `conf.Database` 仍无 `auto_migrate` 开关，生产与本地行为一致 |
| 4 | 错误映射分裂 + 语义丢失 | ✅ | 已收敛到 `errorsx`（声明处定义，消费方零 switch） |
| 5 | Gin 裸跑（CORS / 安全头 / 限流 / Body 限制 / 超时） | ⬜ | `app.go` 仍只有 `gin.Recovery()` + 访问日志 + `/healthz` |
| 6 | 认证是留桩 + 无登录风控 | ⬜ | `service.go` 仍返回 `stub-access-token`；`USER_STATUS_LOCKED` 仍未使用 |

### P1：架构与可维护性

| # | 条目 | 状态 | 备注 |
|---|---|---|---|
| 7 | 缺仓储接口（依赖倒置缺失） | ✅ | `Repository` 接口 + GORM 实现 + fake 单测 |
| 8 | 无 request_id / trace_id | ⬜ | `httpx` 目前只有 `RequestLogger`，无 request_id 注入 |
| 9 | 健康检查不完整 | ⬜ | 只有 `/healthz`，无 `/readyz`（DB Ping）/ `/version` / `/metrics` |
| 10 | 双 REST 入口语义重复 | 🟡 | **入参语义已统一**（`httpx.BindQuery` 复用 grpc-gateway 解析器；Gin 前缀统一为 `/api/v1`，枚举名/bool/lowerCamel 两边一致）。**入口收敛（方案 A）仍待决策**：错误体与成功码仍是两套 |
| 11 | 分页是 offset 游标 | ⬜ | 仍为 offset；深翻页性能未处理 |
| 12 | 缺 Dockerfile / 编排 / LICENSE / CODEOWNERS | ⬜ | 四项均无（实测 `Test-Path` 全部 False） |
| 13 | 生成物未标记为 generated | ✅ | `.gitattributes` 已覆盖 `*.pb.go` / `*_grpc.pb.go` / `*.pb.gw.go` / `wire_gen.go` / `gen/**` |

### P2：细节打磨

| # | 条目 | 状态 | 备注 |
|---|---|---|---|
| 14 | 排序默认键不稳定 | ⬜ | fallback 仍是 `id ASC`（UUIDv4 无序），未改 `created_at DESC, id ASC` |
| 15 | Update 先读后写，无并发保护 | ⬜ | 无 `version` 乐观锁 |
| 16 | 兼容字段未标记废弃 | ⬜ | `ListTodosRequest.status` 未加 `deprecated = true` |
| 17 | aipgorm 时间与函数覆盖 | ⬜ | timestamp 仍以字符串下推；无 `IN` / `duration` |
| 18 | 日志组件副作用 | ✅ | `New` 返回 cleanup（纳入优雅退出）；另加 `Nop` / `Bootstrap` |
| 19 | 路由注册需改 main.go | ✅ | `httpx.Registrar` + `providers.go` 聚合，`NewApp` 签名冻结 |
| 20 | Makefile / CI 小项 | 🟡 | `test` / `wire-drift` 已加；仍缺 `golangci-lint` 与 `-race` |

---

## 三、建议的推进顺序（按依赖与收益）

1. **P0-2 / P0-3（配置安全 + 迁移开关）** —— 半天，直接消除"密钥进 git"与"多副本并发迁移"两个生产风险。
2. **P0-5（Gin 加固）** —— 半天，公网暴露前提。
3. **P0-1 收尾**：补 Handler 层 `httptest` + `httpx`/`grpcx` 单测，CI 加 `-race`。
4. **P1-8 / P1-9（request_id + readyz）** —— 半天，线上排障与就绪探针。
5. **P0-6（认证）** —— 1–2 天，取决于是否引入现成认证库。
6. **P1-12（Dockerfile / LICENSE / CODEOWNERS）** —— 半天，交付配套。
7. 其余 P1-10/11 与 P2 按迭代穿插。
