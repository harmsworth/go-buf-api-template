BUF ?= buf
GO  ?= go
CONF ?= configs/config.yaml
# wire 版本由 go.mod 的 tool 指令锁定（`go get -tool github.com/google/wire/cmd/wire`）。
# 此前用 @latest：不同机器会装到不同版本，产出不同格式的 wire_gen.go，导致生成物无谓漂移。
WIRE ?= $(GO) tool wire ./cmd/server

.DEFAULT_GOAL := help

.PHONY: help deps proto-update proto-lint proto-format proto-breaking proto-generate \
        gen wire wire-drift fmt vet build test run check

help: ## 显示所有可用目标
	@echo 可用目标：
	@echo   deps             下载 Go 依赖
	@echo   proto-update     解析/更新 BSR 依赖并锁定 buf.lock
	@echo   proto-lint       契约风格与结构检查（buf lint）
	@echo   proto-format     格式检查（buf format --exit-code，需 diff 命令）
	@echo   proto-breaking   对 main 分支做兼容性检查
	@echo   proto-generate   生成 Go / gateway / Swagger 与配置结构体
	@echo   gen              全量代码生成（契约 + wire）
	@echo   wire             重新生成 wire 依赖注入代码
	@echo   wire-drift       检查 wire 生成物与 wire.go 是否同步
	@echo   fmt              gofmt 格式化（写入）
	@echo   vet              go vet 静态检查
	@echo   build            编译全部包
	@echo   test             运行单元测试
	@echo   run              启动服务（自动建库 + 执行迁移）
	@echo   check            提交前全量自检

deps: ## 下载 Go 依赖
	$(GO) mod download

proto-update: ## 解析/更新 BSR 依赖并锁定 buf.lock
	$(BUF) dep update

proto-lint: ## 契约风格与结构检查
	$(BUF) lint

proto-format: ## 格式检查（需 diff 命令：Linux/macOS/Git Bash 可用，CI 已包含；Windows cmd 无 diff.exe 时可跳过）
	$(BUF) format --exit-code

proto-breaking: ## 对 main 分支做兼容性检查
	$(BUF) breaking --against '.git#branch=main'

proto-generate: ## 生成 Go / gateway / Swagger 与配置结构体
	$(BUF) generate
	$(BUF) generate --template buf.gen.config.yaml

gen: proto-generate wire ## 全量代码生成（契约 + wire）

wire: ## 重新生成 wire 依赖注入代码
	$(WIRE)

wire-drift: ## 检查 wire 生成物是否与 wire.go 同步（CI 卡点）
	$(WIRE)
	git diff --exit-code -- cmd/server/wire_gen.go \
	  || (echo "wire_gen.go 与 wire.go 不同步，请执行 make wire 并提交" && exit 1)

fmt: ## gofmt 格式化（写入）
	$(GO)fmt -w cmd internal db

vet: ## go vet 静态检查
	$(GO) vet ./...

build: ## 编译全部包
	$(GO) build ./...

test: ## 运行单元测试
	$(GO) test ./... -count=1

run: ## 启动服务（自动建库 + 执行迁移）
	$(GO) run ./cmd/server -conf $(CONF)

check: proto-lint proto-breaking proto-generate vet build test wire-drift ## 提交前全量自检（格式检查见 proto-format）
