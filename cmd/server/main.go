// Package main 是服务入口：加载配置 → 依赖注入 → 启动 Gin / gRPC / grpc-gateway → 优雅退出。
//
// 本文件只做三件事：maxprocs、加载配置、调用 wire 生成的 InitApp 并 Run。
// 装配规则见 providers.go，服务生命周期见 app.go。
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"go-buf-api-template/internal/platform/config"
	"go-buf-api-template/internal/platform/logger"

	"go.uber.org/automaxprocs/maxprocs"
)

func main() {
	// 依据 cgroup / 容器的 CPU quota 自动设置 GOMAXPROCS（automaxprocs）。
	// 容器内若沿用宿主机核数会创建过多 P，导致调度 overhead 与 GC 线程膨胀。
	// 日志接入 slog，与其余启动日志保持一致。
	if _, err := maxprocs.Set(maxprocs.Logger(func(format string, args ...any) {
		slog.Info(fmt.Sprintf(format, args...))
	})); err != nil {
		slog.Warn("automaxprocs failed, keep default GOMAXPROCS", "error", err)
	}

	cfgPath := flag.String("conf", "configs/config.yaml", "path to config file")
	flag.Parse()

	// 启动最早阶段（配置已加载、DI 未完成）的最小 logger；之后统一使用 app.Log。
	//
	// 此前这里手工 logger.New(cfg.GetLog()) 建了一个完整 logger，而 wire 图内又构造了一次，
	// 导致进程内存在两个 logger 实例、且第一个的文件句柄从不关闭。
	// 现在进程内只有一个真实 logger（由 wire 产出并注入业务），其文件句柄也由 wire 的
	// cleanup 统一关闭。
	std := logger.Bootstrap()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		std.Error("load config failed", "error", err)
		os.Exit(1)
	}

	app, cleanup, err := InitApp(cfg)
	if err != nil {
		std.Error("init app failed", "error", err)
		os.Exit(1)
	}
	defer cleanup()

	if err := app.Run(); err != nil {
		app.Log.Error("app exited with error", "error", err)
		os.Exit(1)
	}
}
