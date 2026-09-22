//go:build wireinject

// 本文件是 wire 的依赖注入图定义，由 `wire ./cmd/server` 生成 wire_gen.go。
// 带 wireinject 构建标签，不会参与正常编译。
package main

import (
	"go-buf-api-template/internal/conf"
	"go-buf-api-template/internal/platform/logger"
	"go-buf-api-template/internal/todo"
	"go-buf-api-template/internal/user"

	"github.com/google/wire"
)

// InitApp 构建应用：返回 App（Gin Engine + gRPC Server）与清理函数。
func InitApp(cfg *conf.Bootstrap) (*App, func(), error) {
	wire.Build(
		provideLogConfig,
		provideDatabaseConfig,
		logger.New,
		provideDB,
		provideGRPCServer,
		todo.NewService,
		todo.NewHandler,
		todo.NewServer,
		user.NewService,
		user.NewHandler,
		user.NewServer,
		NewApp,
	)
	return nil, nil, nil
}
