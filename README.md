# go-buf-api-template

基于 **Protobuf + Buf 生态**的 Schema-First / IDL-First API 工程模板：契约即代码、入参校验前移到 IDL（protovalidate）、破坏性变更机器可判、Handler 层零手写校验。

以用户域 `user.v1` 为示例契约（8 个 RPC + `buf.validate` 全规则族），可直接照抄改造为新项目的 API 契约。

## 仓库结构

```text
├── buf.yaml                  # Buf Workspace：Module / BSR 依赖 / lint(STANDARD) / breaking(FILE)
├── buf.lock                  # 依赖锁定（buf dep update 产出，必须提交）
├── buf.gen.yaml              # 代码生成配置：remote plugins 版本锁定，无需本地装 protoc
├── proto/
│   └── user/v1/user.proto    # 契约模板：UserService + protovalidate 校验规则
├── gen/
│   ├── go/user/v1/           # 生成：Go 结构体 / gRPC Stub / grpc-gateway 反向代理
│   └── openapi/user/v1/      # 生成：OpenAPI v3 契约文档（前端/网关交付物）
└── docs/                     # 规范与范式文档（入口见 docs/README.md）
```

> `gen/` 是 `buf generate` 的产物，**需提交进 git**（CI drift 检查依赖它），不要加入 `.gitignore`。

## 文档导航

| 文档 | 内容 |
|---|---|
| [docs/README.md](docs/README.md) | 入口导航：阅读顺序、10 分钟快速开始、新项目迁移清单、FAQ |
| [docs/SCHEMA_FIRST_GUIDE.md](docs/SCHEMA_FIRST_GUIDE.md) | 规范主文档：协作闭环、Zero Value Trap、CI/CD 卡点、版本纪律 |
| [docs/EXAMPLES.md](docs/EXAMPLES.md) | 代码层范式：拦截器零侵入校验、optional 指针三态处理、测试、常见坑 |
| [proto/user/v1/user.proto](proto/user/v1/user.proto) | 契约模板本体（含逐条规范注释） |

## 快速开始

前置：Go 1.26+；Buf CLI ≥ 1.72（`go install github.com/bufbuild/buf/cmd/buf@latest`）。

```bash
# 1) 解析 BSR 依赖，产出/更新 buf.lock（首次必做）
buf dep update

# 2) 契约质量检查
buf lint
buf format --diff --exit-code

# 3) 生成代码 → gen/go/user/v1/ 与 gen/openapi/user/v1/
buf generate
```

## 日常命令速查

| 命令 | 用途 |
|---|---|
| `buf dep update` | 更新依赖锁定（改了 `deps` 后必跑） |
| `buf lint` | 契约风格/结构/校验规则检查 |
| `buf breaking --against '.git#branch=main'` | 破坏性变更检查（对 main） |
| `buf generate` | 生成 Go / OpenAPI 代码 |
| `buf build` | 编译契约（IDE 报错时先跑它定位问题） |

## 新项目复用

复制 `buf.yaml`、`buf.gen.yaml`、`proto/<domain>/<version>/*.proto` 到新仓库，替换 `go_package` 中的 `github.com/yourorg/new-td` 占位符，然后执行 `buf dep update && buf lint && buf generate`。完整迁移清单见 [docs/README.md §5](docs/README.md)。

## 已验证版本

Buf CLI 1.72.0 · protoc-gen-go v1.36.12 · protoc-gen-go-grpc v1.6.2 · grpc-gateway v2.30.0（详见 [docs/README.md §7](docs/README.md)）
