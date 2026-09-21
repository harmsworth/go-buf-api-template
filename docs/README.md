# docs — Schema-First / IDL-First 工具集导航

> 一套基于 **Protobuf + Buf 生态**的 API 研发规范与契约模板：契约即代码、校验前移到 IDL、破坏性变更机器可判、Handler 零手写校验。
> 本文档是入口。读完后你会知道：每个文件是什么、按什么顺序读、日常怎么用、如何搬到新项目。

---

## 1. 文件地图（每个文件是什么）

| 文件 | 位置 | 状态 | 一句话说明 |
|---|---|---|---|
| `README.md`（本文档） | `docs/` | ✅ 已落地 | 导航：阅读顺序、使用步骤、新项目迁移指南 |
| `SCHEMA_FIRST_GUIDE.md` | `docs/` | ✅ 已落地 | **规范主文档**：协作闭环、Zero Value Trap、CI/CD 卡点、版本纪律 |
| `EXAMPLES.md` | `docs/` | ✅ 已落地 | **代码层范式**：拦截器零侵入校验、optional 指针三态处理、测试、FAQ |
| `user/v1/user.proto` | `proto/` | ✅ 已落地 | **契约模板**：用户域 6 个 RPC + `buf.validate` 全规则族，照抄改造 |
| `buf.yaml` | 仓库根 | ✅ 已落地 | Buf Workspace：Module / BSR 依赖 / lint(STANDARD) / breaking(FILE) |
| `buf.lock` | 仓库根 | ✅ 已落地 | 依赖锁定（`buf dep update` 产出），**必须提交**，可复现构建的基石 |
| `buf.gen.yaml` | 仓库根 | ✅ 已落地 | 代码生成配置：remote plugins 版本锁定，产出 `gen/go/` |

> 生成产物 `gen/go/` 由 `buf generate` 产出，**要提交进 git**（CI 的 drift 检查依赖它），不要加进 `.gitignore`。

---

## 2. 阅读顺序

### 路径 A：业务开发者（约 15 分钟上手）

1. **本文档**（3 分钟）— 全景图 + 快速开始
2. **[proto/user/v1/user.proto](../proto/user/v1/user.proto)**（5 分钟）— 先看契约长什么样。重点读：文件头的规范要点注释、`User` 消息的字段规则、`UpdateUserRequest` 顶部的 PATCH 三态注释表
3. **[EXAMPLES.md](./EXAMPLES.md) §3 + §5**（7 分钟）— gRPC 拦截器怎么装配、`optional` 指针字段怎么读写

### 路径 B：架构 / 技术负责人（1–2 小时深度掌握）

1. **本文档**
2. **[SCHEMA_FIRST_GUIDE.md](./SCHEMA_FIRST_GUIDE.md) 全文** — §1 原则 → §2 仓库布局 → §3 协作闭环 → §4 工程配置 → §5 零值陷阱（核心） → §6 CI/CD → §7 版本纪律
3. **[EXAMPLES.md](./EXAMPLES.md) 全文** — 错误映射、HTTP 中间件、测试范式、八条常见坑（§8）
4. [protovalidate 标准规则手册](https://protovalidate.com/schemas/standard-rules/) — 写校验规则时查阅

```mermaid
flowchart TD
    R["README<br/>（全景导航）"] --> P["user.proto<br/>契约长什么样"]
    R --> G["SCHEMA_FIRST_GUIDE<br/>为什么 / 怎么管"]
    P --> E["EXAMPLES<br/>代码怎么写"]
    G --> E
    E --> X["protovalidate 手册<br/>规则怎么写"]
```

---

## 3. 快速开始（10 分钟在本仓库跑通）

> **执行位置**：以下命令必须在**仓库根目录**运行，且根目录需已存在 `buf.yaml`、`buf.gen.yaml`、`proto/user/v1/user.proto`——本仓库开箱即有。
> **新项目**不能直接照跑：请先完成 [§5 迁移清单](#5-如何在新项目中复用这一套迁移清单) 第 1～2 步（复制 `buf.yaml` / `buf.gen.yaml` / 契约文件），再回来执行本节。

**前置**：Go 1.26+；Buf CLI ≥ 1.72（安装：`go install github.com/bufbuild/buf/cmd/buf@latest` 或 scoop/choco）；IDE 安装 **Buf 插件**（`bufbuild.vscode-buf`，负责契约诊断）。

```bash
# 0) 确认工具链
buf --version

# 1) 解析 BSR 依赖，产出/更新 buf.lock（首次必做）
buf dep update

# 2) 契约质量检查（应全绿、无输出）
buf lint
buf format --diff --exit-code

# 3) 生成 Go 代码 → gen/go/user/v1/
buf generate

# 4) 初始化 Go 模块并拉取运行时依赖
go mod init github.com/yourorg/new-td          # ← 替换为实际模块路径
go get buf.build/go/protovalidate@latest
go get github.com/grpc-ecosystem/go-grpc-middleware/v2@v2.3.4
go get google.golang.org/grpc google.golang.org/protobuf
go get github.com/grpc-ecosystem/grpc-gateway/v2@v2.30.0
go mod tidy

# 5) 写第一个 Handler：按 EXAMPLES.md §3 装配 gRPC Server + 校验拦截器
```

**验证成功的标志**：`buf lint` 退出码 0；`gen/go/user/v1/` 下出现 `user.pb.go`、`user_grpc.pb.go`、`user.pb.gw.go` 三个文件，且 `gen/openapi/user/v1/` 下出现 `user.openapi.json`（OpenAPI v3 契约文档）。

---

## 4. 日常研发流程（每个需求怎么用）

六阶段闭环（详见 [SCHEMA_FIRST_GUIDE.md §3](./SCHEMA_FIRST_GUIDE.md)）：

| 阶段 | 你要做什么 | 命令 / 产物 |
|---|---|---|
| ① API 设计 | 发起**只含 `.proto` 变更**的 MR，校验规则写进契约 | 照抄 `user.proto` 的风格与注释 |
| ② 本地自检 | 提交前跑四件套 | `buf dep update` / `buf lint` / `buf format --diff --exit-code` / `buf generate` |
| ③ MR 卡点 | CI 自动执行 lint + format + breaking + drift | 全绿才可合并（配置见规范 §6） |
| ④ 代码生成 | 合入 main 后产出 `gen/` | `buf generate` |
| ⑤ Handler 实现 | 只写业务逻辑，零手写校验 | 按 [EXAMPLES.md](./EXAMPLES.md) 范式 |
| ⑥ 联调 / 分发 | REST 走 grpc-gateway（已注解）；可选发布 BSR | `buf curl` 调试 |

**常用命令速查**：

| 命令 | 用途 |
|---|---|
| `buf dep update` | 更新依赖锁定（改了 `deps` 后必跑） |
| `buf lint` | 契约风格/结构/规则检查 |
| `buf breaking --against '.git#branch=main'` | 兼容性检查（对 main） |
| `buf generate` | 生成 Go 代码 |
| `buf build` | 编译契约；**IDE 报错时先跑它定位问题** |

---

## 5. 如何在新项目中复用这一套（迁移清单）

### 第 1 步：复制文件

```text
新仓库/
├── buf.yaml              ← 原样复制（仓库根）
├── buf.gen.yaml          ← 原样复制（仓库根）
├── proto/
│   └── user/v1/user.proto ← 作为契约模板改造
└── docs/
    └── README.md / SCHEMA_FIRST_GUIDE.md / EXAMPLES.md   ← 按需保留
```

### 第 2 步：替换占位符（全局搜索替换）

| 占位符 | 替换为 |
|---|---|
| `github.com/yourorg/new-td` | 实际仓库模块路径（在 `user.proto` 的 `go_package`、`EXAMPLES.md` 的 import 中出现） |
| `;userv1` | 按域改名，如 `;orderv1` |
| `yourorg/new-td`（buf.yaml 无此项；BSR 托管时才需要） | 可选：接入 Buf Schema Registry 时填写 |

**新增业务域的目录约定**（buf lint 会强制 package 与目录一致；对齐 Buf 官方 quickstart 示例，域作顶层 package）：

| 说明 | 目录 | package |
|---|---|---|
| 通用约定 | `proto/<domain>/<version>/` | `<domain>.<version>` |
| 现有用户域 | `proto/user/v1/` | `user.v1` |
| 示例：新增任务域 | `proto/task/v1/task.proto` | `task.v1` |

### 第 3 步：初始化 Go 模块

```bash
go mod init <你的模块路径>
buf dep update && buf lint && buf generate   # 首次三连
go get buf.build/go/protovalidate@latest
go get github.com/grpc-ecosystem/go-grpc-middleware/v2@v2.3.4
go get google.golang.org/grpc google.golang.org/protobuf
go get github.com/grpc-ecosystem/grpc-gateway/v2@v2.30.0
go mod tidy && go build ./...
```

### 第 4 步：接入 CI 质量卡点

1. 从 [SCHEMA_FIRST_GUIDE.md §6.2（GitHub Actions）或 §6.3（GitLab CI）](./SCHEMA_FIRST_GUIDE.md) 复制 workflow，原样落地。
2. 配置分支保护：将 `proto-quality-gate` 设为 **Required status check**（GitLab 勾选 "Pipelines must succeed"）。
3. 添加 CODEOWNERS：`docs/` 与 `gen/` 目录强制架构组评审。

### 第 5 步：团队约定

- 评审卡点：[EXAMPLES.md §6 禁止事项](./EXAMPLES.md)（手写校验、零值伪装三态、裸解引用指针一律打回）。
- 新接口一律先发"只含 `.proto` 的 MR"。

### 迁移检查清单

- [ ] `buf.yaml` / `buf.gen.yaml` 已复制，插件版本号未被改动
- [ ] `buf dep update` 成功，`buf.lock` 已提交
- [ ] `buf lint` 全绿
- [ ] `buf generate` 产出 `gen/`
- [ ] 占位模块路径已全部替换（`grep -r "yourorg"` 验证为空）
- [ ] CI workflow 已合入，required check 生效
- [ ] `gen/` 已提交进版本控制（未进 `.gitignore`）
- [ ] CODEOWNERS 已配置

---

## 6. FAQ（常见报错速查）

| 现象 | 原因与解决 |
|---|---|
| IDE 大量 `imported file does not exist` / `cannot find buf.validate.field` | `buf.lock` 缺失或过期 → 跑 `buf dep update`；确认已装 Buf 插件（bufbuild.vscode-buf） |
| `buf generate` 报 `plugin references must be specified as "<name>:<version>"` | remote 插件必须固定版本号（`buf.gen.yaml` 已固定，勿删版本号） |
| 正则报 `invalid repeat count` | RE2 重复计数上限为 1000（如 `{1,2048}` 非法）→ 长度限制交给 `string.max_len` |
| 密码复杂度写不进 `pattern` | RE2 不支持 lookaround 断言 → 用 message 级 CEL 规则（`user.proto` 的 `CreateUserRequest` 有现成示例） |
| `buf format --diff` 在 Windows 报找不到 diff | Windows 缺 `diff.exe`，属环境问题；本地可跳过，CI（Linux）正常 |
| 更多坑（protojson 三态、oneof vs optional、validator 单例等） | [EXAMPLES.md §8](./EXAMPLES.md) |

---

## 7. 已验证的版本组合

| 组件 | 版本 | 验证结果 |
|---|---|---|
| Buf CLI | 1.72.0 | `buf lint` / `buf build` / `buf generate` 全绿 |
| protoc-gen-go（remote plugin） | v1.36.12 | 生成 `user.pb.go`，optional → 指针 |
| protoc-gen-go-grpc（remote plugin） | v1.6.2 | 生成 `user_grpc.pb.go` |
| grpc-gateway（remote plugin） | v2.30.0 | 生成 `user.pb.gw.go` |
| openapiv3（remote plugin） | v2.30.0 | 生成 `gen/openapi/user/v1/user.openapi.json`（OpenAPI v3 契约文档） |
| go-grpc-middleware/v2 | v2.3.4（最新稳定版，Go proxy 已核实） | protovalidate 拦截器可用 |
| grpc-gateway/v2 | v2.30.0 | REST 网关运行时库（`user.pb.gw.go` 依赖，与生成插件版本对齐） |
| protovalidate Go 运行时 | `buf.build/go/protovalidate@latest` | 契约规则编译与执行正常 |
