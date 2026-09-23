//go:build wireinject

// 本文件是 wire 的依赖注入图定义，由 `make wire` 生成 wire_gen.go。
// 带 wireinject 构建标签，不会参与正常编译。
//
// 这里只按语义列出三个 ProviderSet（定义见 providers.go），**不逐个列举 provider**：
// 新增业务域只需改 providers.go 的 DomainSet，本文件永不改动。
package main

import (
	"go-buf-api-template/internal/conf"

	"github.com/google/wire"
)

// InitApp 构建应用：返回 App（Gin Engine + gRPC Server）与清理函数。
func InitApp(cfg *conf.Bootstrap) (*App, func(), error) {
	wire.Build(PlatformSet, DomainSet, AppSet)
	return nil, nil, nil
}
