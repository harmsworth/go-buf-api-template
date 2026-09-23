# Schema-First / IDL-First 研发流程与工程规范

> 适用范围：本仓库全部 RPC / REST API 契约。
> 契约目录：`api/<domain>/<version>/*.proto`（Buf Workspace 含 `api` 与 `internal` 两个 module，规范文档在 `docs/`）。
> 配套文档：[EXAMPLES.md](./EXAMPLES.md)（代码层范式） · [契约：api/user/v1/user.proto](../api/user/v1/user.proto) · [api/todo/v1/todo.proto](../api/todo/v1/todo.proto)。

| 元数据 | 值 |
|---|---|
| 文档版本 | v1.1.0 |
| 目标语言 | Go 1.26+ |
| 工具链 | Buf CLI（config v2）/ protoc-gen-go / gRPC-Go / grpc-gateway v2 |
| 服务端栈 | Gin + GORM(MySQL) + golang-migrate + Google wire + `log/slog` |
| 校验引擎 | protovalidate（官方 Go 运行时：`buf.build/go/protovalidate`） |
| AIP 能力 | `go.einride.tech/aip`（分页/过滤/排序/字段掩码） |

---

## 1. 核心原则：为什么是 Schema-First

1. **契约即代码（Contract as Code）**：`.proto` 文件是前后端、服务间交互的**唯一事实来源（SSOT）**。所有请求/响应结构、错误语义、校验规则均以 IDL 为准，禁止在文档、IM、口头上另立契约。
2. **校验前移到契约**：参数约束（长度、格式、范围、必填）声明在 `.proto` 中，由 protovalidate 引擎在运行时统一执行。**业务代码中严禁出现手写参数校验样板代码**（详见 [EXAMPLES.md §6](./EXAMPLES.md)）。
3. **破坏性变更机器可判**：兼容性不靠人肉 Review，由 `buf breaking` 在 MR 阶段机器判定并阻断。
4. **生成而非手写**：Go 结构体、gRPC Stub、REST Gateway、Swagger 文档、配置结构体全部由 `buf generate` 产出，人工禁止修改 `gen/` 与 `internal/conf/conf.pb.go`。
5. **字段语义显式化**：所有"可更新/可空/三态"字段必须使用 `optional` 声明（见 §5 Zero Value Trap），禁止依赖标量零值做语义判断。

**反模式黑名单**：

| 反模式 | 后果 | 替代方案 |
|---|---|---|
| 先写代码，后补文档/契约 | 契约与实现漂移 | 契约提案 PR 先行 |
| 在 Handler 中手写 `if x == ""` 校验 | 校验规则分散、不可审计 | `buf.validate` + 拦截器 |
| 用标量零值表达"未传/清空" | 三态坍缩，PATCH 语义丢失 | `optional` 指针（§5） |
| 手动改 `gen/` 或 `conf.pb.go` | 下次 generate 被覆盖 | 修改 `.proto` 重新生成 |
| 用 `AutoMigrate` 建表 | 表结构脱离版本控制 | `db/migrations/*.sql`（golang-migrate） |
| 复用已删除字段的编号 | 读写串线，线上事故 | `reserved`（§7） |

---

## 2. 仓库布局

```text
.
├── buf.yaml                      # Buf Workspace v2：modules api / internal，lint/breaking 规则
├── buf.gen.yaml                  # 生成 Go/gRPC/gateway/Swagger，输入 api 目录 → gen/
├── buf.gen.config.yaml           # 生成 internal/conf/conf.pb.go（用 --template 指定）
├── buf.lock                      # 依赖锁定（buf dep update 产出，必须提交）
├── Makefile                      # 命令别名（make help）
├── .github/workflows/            # CI 质量卡点
├── api/                          # ← Buf Module：API 契约的家
│   ├── user/v1/user.proto        #   package user.v1（8 个 RPC）
│   └── todo/v1/todo.proto        #   package todo.v1（5 个 RPC + google.api.http）
├── gen/                          # 生成产物（禁止手改；CI drift 检查覆盖）
│   ├── go/user/v1/               #   user.pb.go / user_grpc.pb.go / user.pb.gw.go
│   ├── go/todo/v1/               #   todo.pb.go / todo_grpc.pb.go / todo.pb.gw.go
│   └── openapi/openapi.swagger.yaml   # Swagger 2.0，allow_merge 合并单文件
├── cmd/server/                   # 服务入口：main.go + wire.go + wire_gen.go
├── configs/config.yaml           # 运行配置
├── db/migrations/                # SQL 迁移（embed 进二进制，启动时执行）
├── internal/                     # ← Buf Module：conf.proto（另有平台与业务包）
│   ├── conf/conf.proto           #   配置契约 → conf.pb.go
│   ├── platform/                 #   config / database(GORM+migrate) / logger(slog) / aipgorm
│   ├── todo/                     #   Todo 业务包（Package by Feature）
│   └── user/                     #   User 业务包（Package by Feature）
└── docs/                         # 规范与导航文档
    ├── README.md                 # 导航 + 快速开始 + 迁移清单
    ├── SCHEMA_FIRST_GUIDE.md     # 本文档
    └── EXAMPLES.md               # 代码层最佳实践
```

> **目录与包名强约束**：buf lint 规则 `PACKAGE_DIRECTORY_MATCH` 要求 package 路径与文件目录严格一致，即 `package user.v1` 只能放在 `api/user/v1/` 下。
> **例外**：`internal` module 在 `buf.yaml` 中已 `except` 掉 `PACKAGE_DIRECTORY_MATCH` 与 `PACKAGE_VERSION_SUFFIX`，因此 `internal/conf/conf.proto` 使用无版本后缀的 `package conf`。

---

## 3. 协作闭环：从 API 设计到 Handler 落地

```mermaid
flowchart LR
    A["① API 设计<br/>.proto 提案 PR"] --> B["② 本地质量门禁<br/>lint / format / breaking / generate"]
    B --> C["③ MR 质量卡点<br/>CI: lint + breaking + drift"]
    C --> D{CI 全绿?}
    D -- 否 --> A
    D -- 是 --> E["④ 合入 main<br/>CI: buf generate 产出 gen/"]
    E --> F["⑤ 业务实现<br/>零手写校验，只写业务"]
    F --> G["⑥ 联调与分发<br/>gateway / BSR / buf curl"]
```

### 3.1 阶段一：API 设计（提案 PR 只含 `.proto`）

- 新接口以 **只包含 `.proto` 变更的 MR** 发起，后端、前端、调用方在同一份契约上评审。
- 设计必须遵守：
  - 命名规范由 buf lint `STANDARD` 规则集自动强制（`SERVICE_SUFFIX`、`RPC_REQUEST_STANDARD_NAME`、`ENUM_VALUE_PREFIX` 等，速查见 §9.2）。
  - **同 package 新增第二个 `.proto` 文件时**，`go_package` / `java_package` / `java_multiple_files` 等 file options 必须与既有文件**逐字节一致**（lint `PACKAGE_SAME_*` 强制）；最稳妥做法是整体复制既有文件的 option 块。
  - 所有入参字段挂 `buf.validate` 校验规则；规则设计原则见 §5.3。
  - 涉及"部分更新"的请求按 §5.5 提供 `update_mask`（AIP-134）。
  - REST 映射用 `google.api.http` 注解（grpc-gateway 依据它生成反向代理）。

### 3.2 阶段二：本地质量门禁

提交前在本地执行（已固化为 `make check`）：

```bash
buf dep update                              # 解析/更新依赖，产出 buf.lock
buf lint                                    # 风格与结构规范（STANDARD 规则集）
buf format --exit-code                      # 格式检查（需 diff 命令；Windows cmd 可跳过，CI 必跑）
buf breaking --against '.git#branch=main'   # 兼容性检查（对 main 的 merge-base）
buf generate                                # → gen/go/**、gen/openapi/openapi.swagger.yaml
buf generate --template buf.gen.config.yaml # → internal/conf/conf.pb.go
go build ./...                              # 编译验证
```

### 3.3 阶段三：MR 质量卡点（自动化）

MR 中由 CI 重新执行 lint / format / breaking / drift 四项检查并**阻断合并**（详见 §6）。

### 3.4 阶段四：代码生成

合入后 CI 执行 `buf generate` 产出 `gen/`。配置使用 **Buf Remote Plugins**（见 §4.2），保证全团队与 CI 的生成结果逐字节一致。

### 3.5 阶段五：业务实现

- Service / Handler / gRPC Server **只写业务逻辑**；参数校验已由契约声明 + 全局拦截器完成。
- `optional` 字段按 [EXAMPLES.md §4](./EXAMPLES.md) 的指针范式处理。
- List 接口按 [EXAMPLES.md §5](./EXAMPLES.md) 接入 AIP 分页/过滤/排序；Update 按 §5.5 处理 `update_mask`。
- 需要新增/修改校验规则时，回到阶段一修改 `.proto`，而非在 Go 代码里加 if。

### 3.6 阶段六：联调与分发

| 产物 | 生成方式 | 消费方 |
|---|---|---|
| Go Stub | `buf generate` | 本仓库服务端/客户端 |
| Swagger 2.0 | `buf generate`（`buf.build/grpc-ecosystem/openapiv2` 插件，`allow_merge` 合并单文件） | 前端、外部 API 文档 |
| REST 网关 | `buf generate`（gateway 插件 → `*.pb.gw.go`）+ `cmd/server` 装配 | 前端（`:8081`） |
| 多语言 SDK | Buf Schema Registry（BSR）托管 Module | 跨团队调用方 |

> 产物位于 `gen/openapi/openapi.swagger.yaml`。注意是 **Swagger 2.0**（openapiv2 插件），不是 OpenAPI v3。

---

## 4. 工程配置基准

### 4.1 `buf.yaml`（仓库根，当前真实内容）

```yaml
version: v2
modules:
  - path: api
  - path: internal
    lint:
      use:
        - STANDARD
      except:
        - PACKAGE_DIRECTORY_MATCH
        - PACKAGE_VERSION_SUFFIX
deps:
  - buf.build/bufbuild/protovalidate   # buf/validate/validate.proto
  - buf.build/googleapis/googleapis    # google/api/annotations.proto 等
lint:
  use:
    - STANDARD                         # 全局默认采用标准规则集
breaking:
  use:
    - FILE                             # 以"文件"粒度判定破坏性变更
```

### 4.2 `buf.gen.yaml`（仓库根，当前真实内容）

```yaml
version: v2
inputs:
  - directory: api                       # 只生成 API 契约目录
clean: true                              # 每次生成前清理 out 目录
plugins:
  - remote: buf.build/protocolbuffers/go:v1.36.12
    out: gen/go
    opt: paths=source_relative
  - remote: buf.build/grpc/go:v1.6.2
    out: gen/go
    opt: paths=source_relative
  - remote: buf.build/grpc-ecosystem/gateway:v2.30.0
    out: gen/go
    opt:
      - paths=source_relative
      - generate_unbound_methods=false
  # Swagger 2.0：allow_merge 把所有 proto 合并成单个交付文件
  - remote: buf.build/grpc-ecosystem/openapiv2:v2.30.0
    out: gen/openapi
    opt:
      - allow_merge=true
      - merge_file_name=openapi          # → openapi.swagger.yaml
      - json_names_for_fields=true
      - output_format=yaml
```

`buf.gen.config.yaml`（生成配置结构体）：

```yaml
version: v2
inputs:
  - directory: internal
plugins:
  - remote: buf.build/protocolbuffers/go:v1.36.12
    out: internal
    opt: paths=source_relative
```

> ① 为什么不启用 `managed` 模式：本仓库在各 `.proto` 中**显式声明** `option go_package = "go-buf-api-template/gen/go/user/v1;userv1";`，通过别名精确控制 Go 包名，而不依赖自动推导。
> ② 已实测：`buf lint` 全绿；`buf generate` 产出 `gen/go/{user,todo}/v1/*.pb.go|*_grpc.pb.go|*.pb.gw.go` 与 `gen/openapi/openapi.swagger.yaml`；`buf generate --template buf.gen.config.yaml` 产出 `internal/conf/conf.pb.go`。

### 4.3 Makefile

命令已固化在仓库根 `Makefile`，`make help` 查看全部。契约相关目标（节选）：

```makefile
BUF ?= buf

.PHONY: proto-update proto-lint proto-format proto-breaking proto-generate proto-check

proto-update:      ## 解析/更新 BSR 依赖并锁定 buf.lock
	$(BUF) dep update

proto-lint:        ## 契约风格与结构检查
	$(BUF) lint

proto-format:      ## 格式检查（不自动改写，与 CI 行为一致）
	$(BUF) format --exit-code

proto-breaking:    ## 对 main 的兼容性检查
	$(BUF) breaking --against '.git#branch=main'

proto-generate:    ## 生成 Go / Swagger 代码（含配置契约）
	$(BUF) generate
	$(BUF) generate --template buf.gen.config.yaml

proto-check: proto-update proto-format proto-lint proto-breaking proto-generate  ## 提交前全量自检
```

> 完整目标还包含 `deps` / `wire` / `build` / `run` / `vet` / `fmt` / `gen` / `check`，见根目录 `Makefile`。

### 4.4 首次初始化 SOP

```bash
# 1. 确认 buf.yaml / buf.gen.yaml / buf.gen.config.yaml 已就位
# 2. 解析依赖，生成 buf.lock（必须提交到仓库）
buf dep update
# 3. 首次生成
buf generate
buf generate --template buf.gen.config.yaml
# 4. 拉取依赖并编译
go mod download && go build ./...
# 5. 全量自检
make check
```

> 本仓库 module 名为 **`go-buf-api-template`**（不带 `github.com/` 前缀），所有 `go_package` 与 Go import 均以此开头。
> 迁移到新项目时全局替换该名，并替换 `api/user/v1/user.proto` 中的 `java_package = "com.example.gobuf.user.v1"`（同样为占位符，改为你的 Java 包名或直接删除 `java_*` 选项）。

---

## 5. 字段与零值陷阱规范（Zero Value Trap）

### 5.1 问题本质：proto3 标量零值会让"三态"坍缩成"两态"

proto3 中**未声明 `optional` 的标量字段没有 presence（存在性）语义**：`""`、`0`、`false` 既可能是"调用方没传"，也可能是"调用方明确传了零值"。落到 Go 就是同一个值，**PATCH 语义无法表达**：

```text
wire 上 {"nickname": ""}        →  Go: Nickname = ""      ─┐
wire 上 {}（字段缺失）          →  Go: Nickname = ""      ─┴─ 两种意图不可区分 ❌
```

典型事故：用户只想"更新头像"，后端把没传的 `phone: ""` 理解为"清空手机号"，数据被静默抹掉。

### 5.2 规范一：可三态字段必须使用 `optional`，映射为 Go 指针

| Protobuf 声明 | Go 生成字段 | 语义 |
|---|---|---|
| `string nickname = 1;` | `Nickname string` | 无 presence：三态坍缩 ❌ |
| `optional string nickname = 1;` | `Nickname *string` | `nil` / 指向 `""` / 指向有效值，三态完备 ✅ |
| `optional int32 age = 2;` | `Age *int32` | 同上 |
| `optional UserStatus status = 3;` | `Status *UserStatus` | 枚举同样适用 |
| `google.protobuf.Timestamp updated_at` | `*timestamppb.Timestamp` | message 引用类型天然 nullable |
| `repeated string tags` | `Tags []string` | `nil` 与 `[]` 可区分（一般无需 optional） |

**生成的访问器（protoc-gen-go）**：

| 生成 API | 普通标量 | `optional` 标量 |
|---|---|---|
| 结构体字段 | `Name string` | `Name *string` |
| `GetName() string` | ✅ nil 不适用 | ✅ nil 安全，返回 `""` |
| `HasName() bool` | ❌ 不生成 | ✅ 仅 `optional` 字段生成，三态判定唯一手段 |

**protojson 线上行为**（REST / HTTP 调试必须理解）：

| 请求 JSON | 反序列化到 Go | 三态语义 |
|---|---|---|
| `{"nickname": "neo"}` | `Nickname` 指向 `"neo"` | Explicit Value（更新） |
| `{"nickname": ""}` | `Nickname` 指向 `""` | Explicit Clear（显式清空） |
| `{}`（不含该键） | `Nickname = nil` | Absent（保持不变） |

> ⚠️ `protojson.MarshalOptions{EmitUnpopulated: true}` 会把 `nil` 的 optional 字段序列化成 `""`，**会破坏三态**；同时会让 `user.v1.User.password_hash` 这类服务端独占字段出现在响应里。
> 本仓库 Gin 侧使用默认（关闭）配置，且 grpc-gateway 已在 `cmd/server` 中显式关闭 `EmitUnpopulated`。

### 5.3 规范二：PATCH 部分更新的三态契约

`UpdateUserRequest` / `UpdateTodoRequest` 类接口的字段语义（服务端实现见 `internal/*/model.go` 的 `applyUpdate`）：

| 状态 | 请求表现 | Go 判定 | 服务端行为 |
|---|---|---|---|
| **Absent**（未传递） | JSON 无该键 / gRPC 字段未设置 | `req.Nickname == nil` / `!req.HasNickname()` | 保持原值，不修改 |
| **Explicit Value**（传有效值） | 传递合法值 | `req.HasNickname() && *req.Nickname != ""` | 更新为该值 |
| **Explicit Clear**（显式清空） | 传递零值（字符串 `""`） | `req.HasNickname() && *req.Nickname == ""` | 清空该字段 |

**契约层三条铁律**：

1. **支持"显式清空"的 `optional` 字段，禁止使用 `min_len`、`email` 等会拒绝空串的规则**；必须改用"允许空串"的 `pattern`（空分支 + 正常分支）：

   ```protobuf
   // ❌ 错误：空串（显式清空）会被 min_len 拒绝，三态残废
   optional string phone = 3 [(buf.validate.field).string.min_len = 8];

   // ✅ 正确：pattern 放行空串，非空时校验手机号格式
   optional string phone = 3 [(buf.validate.field).string.pattern = "^(|1[3-9][0-9]{9})$"];
   ```

2. **业务代码判定三态必须使用 `Has*()`，禁止零值比较**（`Get*() == ""` 无法区分 Absent 与 Explicit Clear）。
3. **枚举与 message 字段不适用 Explicit Clear**（枚举无法表达"空"）。需要"取消/清空"语义时：单独 RPC、引入业务态枚举值，或使用 `update_mask`（§5.5）。

### 5.4 规范三：Go 侧处理范式（摘要）

完整代码见 [EXAMPLES.md](./EXAMPLES.md)。核心口诀：**读用 `Get*`（nil 安全），判用 `Has*`（三态），写用 `proto.String(...)` / `proto.Int32(...)` 构造指针**。

```go
req := &userv1.UpdateUserRequest{
    UserId:   id,
    Nickname: proto.String("neo"), // Explicit Value
    Phone:    proto.String(""),    // Explicit Clear
}
```

### 5.5 方案 B：FieldMask 白名单更新（AIP-134，**已落地**）

当"可更新字段集合"需要**动态控制**（多端权限差异、批量接口）时，在请求中追加 `google.protobuf.FieldMask update_mask`：`user.proto` 的 `UpdateUserRequest.update_mask = 6`、`todo.proto` 的 `UpdateTodoRequest.update_mask = 5`。

- 语义切换为：mask 中**声明了路径的字段才被更新**；未声明的字段连零值都被忽略（彻底解决零值覆盖）。
- "显式清空"由 mask 路径 + 零值组合表达。
- 服务端**校验 mask 路径白名单**（`updatablePaths`），配合 `fieldmask.Validate` 拒绝未声明路径，防止越权字段写入；`*` 表示全量替换。

两种方案选型（二者在本仓库**按是否传 mask 自动切换**，不混用于同一字段）：

| 维度 | 方案 A：optional 三态 | 方案 B：FieldMask |
|---|---|---|
| 表达力 | 每字段独立三态 | 动态路径白名单 |
| 实现复杂度 | 低（指针判空） | 中（路径校验、mask 规范化） |
| 适用场景 | 常规资源 PATCH | 多端权限差异、批量/代理更新 |
| 触发条件 | 未传 `update_mask` | 传了 `update_mask` |

---

## 6. CI/CD 质量卡点

### 6.1 卡点矩阵

| 卡点 | 工具 | 触发时机 | 失败动作 |
|---|---|---|---|
| 契约风格/结构 | `buf lint` | MR + push | ❌ 阻断合并 |
| 兼容性 | `buf breaking` | MR（对 merge-base） | ❌ 阻断合并 |
| 格式一致性 | `buf format --diff --exit-code` | MR | ❌ 阻断合并 |
| 依赖锁定漂移 | `buf dep update` + git diff | MR | ❌ 阻断合并 |
| 生成代码漂移 | `buf generate` + git diff（含 `--template` 的 `internal/conf`） | MR | ❌ 阻断合并 |
| 编译 | `go build ./...` / `go vet ./...` | MR | ❌ 阻断合并 |
| 契约评审 | CODEOWNERS（可选） | 契约路径变更 | 要求架构组 Approve |

### 6.2 GitHub Actions（`.github/workflows/quality-gate.yml`，**本仓库已落地**）

```yaml
name: quality-gate

on:
  pull_request:
    paths:
      - "api/**/*.proto"                 # ← 契约目录是 api/，不是 docs/
      - "internal/conf/conf.proto"
      - "buf.yaml"
      - "buf.gen.yaml"
      - "buf.gen.config.yaml"
      - "buf.lock"
      - "gen/**"
      - "go.mod"
      - "go.sum"
      - "cmd/**"
      - "internal/**"
      - ".github/workflows/quality-gate.yml"
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  buf:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0                 # merge-base 对比需要完整提交历史

      - uses: bufbuild/buf-setup-action@v1
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Lint
        run: buf lint

      - name: Format check
        run: buf format --exit-code

      - name: Breaking change check (vs merge-base)
        if: github.event_name == 'pull_request'
        run: buf breaking --against '.git#commit=${{ github.event.pull_request.base.sha }}'

      - name: Dependency lock drift check
        run: |
          buf dep update
          git diff --exit-code -- buf.lock \
            || (echo "::error::buf.lock 已过期，请执行 buf dep update 并提交" && exit 1)

      - name: Generated code drift check
        run: |
          buf generate
          buf generate --template buf.gen.config.yaml
          git diff --exit-code -- gen internal/conf \
            || (echo "::error::生成代码与 .proto 不同步，请执行 buf generate 并提交" && exit 1)

      - name: Build
        run: go build ./... && go vet ./...
```

> Remote plugins 仅需网络即可下载，CI 镜像无需预装 protoc / Go 插件。

### 6.3 GitLab CI（`.gitlab-ci.yml` 片段，**模板，本仓库未接入**）

```yaml
proto-quality-gate:
  stage: verify
  image: bufbuild/buf:latest
  variables:
    GIT_DEPTH: 0
  rules:
    - if: '$CI_PIPELINE_SOURCE == "merge_request_event"'
      changes:
        - "api/**/*.proto"                # ← 同理，不是 docs/
        - "internal/conf/conf.proto"
        - "buf.yaml"
        - "buf.gen.yaml"
        - "buf.gen.config.yaml"
  script:
    - buf lint
    - buf format --exit-code
    - buf breaking --against ".git#commit=${CI_MERGE_REQUEST_DIFF_BASE_SHA}"
    - buf generate && buf generate --template buf.gen.config.yaml
    - git diff --exit-code -- gen internal/conf
```

### 6.4 分支保护与 MR 策略

| 配置 | 位置 | 效果 |
|---|---|---|
| Required status check：`buf` | GitHub → Settings → Branches → Protection | lint/breaking 未过，禁止合并 |
| Pipelines must succeed | GitLab → Settings → Merge requests | 同上 |
| CODEOWNERS 强制评审 | 仓库 CODEOWNERS | 契约变更必须架构组 Approve |
| `gen/` 目录禁手改 | CODEOWNERS + drift 检查 | 生成代码仅允许机器人/CI 提交 |

```text
# CODEOWNERS 示例（GitHub: .github/CODEOWNERS / GitLab: CODEOWNERS）
/api/            @org/backend-architecture
/gen/            @org/backend-architecture
/internal/conf/  @org/backend-architecture
/db/migrations/  @org/backend-architecture
```

> 本仓库**尚未配置 CODEOWNERS**，以上为示例。

---

## 7. 版本演进与兼容性纪律

### 7.1 变更兼容性对照表

| 变更 | 兼容性 | 约束 |
|---|---|---|
| 新增字段（新编号） | ✅ 非破坏 | 必须使用全新编号，不得复用已删除编号 |
| 新增 `optional` 字段 | ✅ 非破坏 | 读取方必须容忍字段缺失 |
| 新增 RPC | ✅ 非破坏 | — |
| 新增枚举值 | ✅（`FILE` 模式下放行） | 服务端必须能处理未知枚举值 |
| 修改字段编号 / 类型 | ❌ 破坏 | `buf breaking` 拦截；禁止 |
| 删除 / 改名字段、RPC | ❌ 破坏 | 走 §7.3 下线流程 |
| 删除 / 复用枚举编号 | ❌ 破坏 | 先 `reserved` |

### 7.2 字段下线：`deprecated` + `reserved` 两步走

```protobuf
message User {
  // 第一步：标记废弃（非破坏，生成代码产生告警注释）
  string fax = 9 [deprecated = true];

  // 第二步：所有调用方迁移完成后，物理删除字段并锁死编号与名字（破坏性，
  // 必须在下一个版本窗口执行，删除前先确认 buf breaking 报告）
  reserved 9;
  reserved "fax";
}
```

### 7.3 不可兼容变更 SOP（v2 并行）

1. 新建 `api/user/v2/`，package `user.v2`，v1 保持冻结。
2. v1 中被替代的 RPC 打上 `deprecated = true`，响应头/文档标注迁移窗口。
3. 调用方迁移至 v2，观测 v1 流量归零。
4. 在约定的版本窗口内删除 v1（`buf breaking` 对 v1 目录的删除告警需架构组显式豁免记录）。

---

## 8. 与公司遗留 proto 体系（sdmsx）的差异对照

公司内并存两套契约风格：本规范管理的 Schema-First 体系（`docs/`），与 protoc + git submodule + 后处理脚本驱动的遗留体系（`bsi-proto/sdmsx` 组织仓库，其切片曾暂存于本仓库，当时目录名为 `proto/`，现已更名为 `api/`）。**遗留体系不纳入本仓库 buf 工具链**（接入将触发 STANDARD 规则全量报错）。差异对照如下：

| 维度 | 本规范（docs/） | 遗留体系（bsi-proto/sdmsx） | 说明 |
|---|---|---|---|
| 字段命名 | lower_snake_case（proto 社区标准） | PascalCase + Python 脚本后处理转换 | snake_case 生成 Go/JSON 更符合惯例 |
| 枚举 | proto enum + `defined_only` 校验 | `int32` + 注释魔数（如 IsEnable 1/2） | 枚举自文档、可校验、可演进 |
| 可空/三态 | proto3 `optional` → Go 指针 | `google.protobuf.wrappers.*` | optional 映射更干净 |
| 响应消息 | 每个 RPC 独立 Request/Response | 通用 `OperationResponse` 复用 | buf lint `RPC_REQUEST_RESPONSE_UNIQUE` 禁止复用 |
| service 命名 | `XxxService` + 资源化 RPC（AIP 风格） | `XxxManager` + 动词后置（`UserAdd`） | buf lint `SERVICE_SUFFIX` 自动强制 |
| 版本化 | package 以 `vN` 结尾 + `buf breaking` 机器卡点 | package 无版本 | 遗留体系兼容性靠约定 |
| 工具链 | buf lint/format/breaking/generate + remote plugins | protoc + git submodule + Python/PowerShell 后处理 | 前者声明式、可复现、CI 可卡点 |
| 契约交付物 | `buf generate` 产出 Swagger 2.0（`gen/openapi/openapi.swagger.yaml`） | protoc openapiv2 插件产出 swagger.json | 同一目标，前者纳入同一套 generate 流水线 |
| 参数校验 | `buf.validate` 契约级规则 + 引擎统一执行 | 无（服务端手写） | 零手写校验的前提 |

**两点有意保留的设计**：

- `User.user_id` 不改为 AIP-122 建议的 `id`：`user_id` 在 JSON 与日志中自解释，调用方调试成本更低；
- 分页用字段级 `page_size`/`page_token`（AIP-158 标准字段），不引入共享的 `PageRequest` 消息：避免跨域共享消息引发耦合。

**迁移建议**：本仓库新域一律走本规范；如需从遗留体系迁移某个域，按域逐个进行（先以 lint 豁免清单接入，再逐步清零豁免）。

---

## 9. 附录

### 9.1 契约命令速查

| 命令 | 用途 |
|---|---|
| `buf dep update` | 解析/更新 BSR 依赖，产出 `buf.lock` |
| `buf lint` | 契约风格与结构检查 |
| `buf format --diff --exit-code` | 格式检查（CI 同款） |
| `buf breaking --against '.git#branch=main'` | 兼容性检查 |
| `buf generate` | 生成 Go / gateway / Swagger |
| `buf generate --template buf.gen.config.yaml` | 生成 `internal/conf/conf.pb.go` |
| `buf build` | 编译校验契约，产出 Image |
| `buf curl` | 对 gRPC 服务直接调试 |

服务端相关命令（`make gen` / `make wire` / `make build` / `make run`）见根 [`README.md`](../README.md)。

### 9.2 常见 lint 违规速查（STANDARD 规则集）

| 规则 | 触发条件 | 修复 |
|---|---|---|
| `PACKAGE_DIRECTORY_MATCH` | package 与目录不一致 | `package user.v1` ↔ `api/user/v1/`（`internal` module 已 except） |
| `PACKAGE_SAME_*`（GO_PACKAGE/JAVA_PACKAGE 等） | 同 package 多个文件的 file options 不一致 | 新文件整体复制既有文件的 option 块 |
| `PACKAGE_VERSION_SUFFIX` | package 未以 `v<N>` 结尾 | `user.v1` 而非 `user`（`internal` module 已 except） |
| `SERVICE_SUFFIX` | Service 名未以 `Service` 结尾 | `UserService` / `TodoService` |
| `RPC_REQUEST_STANDARD_NAME` | 请求/响应消息命名不规范 | `MethodNameRequest` / `MethodNameResponse` |
| `RPC_REQUEST_RESPONSE_UNIQUE` | 请求/响应消息被多个 RPC 复用 | 每个 RPC 独立消息 |
| `ENUM_ZERO_VALUE_SUFFIX` | 枚举首值名未以 `_UNSPECIFIED` 结尾 | `USER_STATUS_UNSPECIFIED = 0;` |
| `ENUM_VALUE_PREFIX` | 枚举值缺大写枚举名前缀 | `USER_ROLE_ADMIN` 而非 `ADMIN` |
| `FIELD_LOWER_SNAKE_CASE` | 字段名非 lower_snake_case | `user_id` 而非 `userId` |

### 9.3 参考资料

- Buf 官方文档：<https://buf.build/docs>
- protovalidate 规则参考：<https://protovalidate.com/schemas/standard-rules/>
- gRPC-Go middleware：<https://github.com/grpc-ecosystem/go-grpc-middleware>
- Google AIP：<https://google.aip.dev/132>（List/排序）、[134](https://google.aip.dev/134)（FieldMask）、[158](https://google.aip.dev/158)（分页）、[160](https://google.aip.dev/160)（过滤）
- go.einride.tech/aip：<https://pkg.go.dev/go.einride.tech/aip>
