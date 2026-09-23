// Package logger 基于 Go 原生 log/slog 初始化应用日志。
package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"go-buf-api-template/internal/conf"
)

// Nop 返回一个丢弃所有输出的 slog.Logger，供单元测试构造依赖使用。
//
// 避免每个测试文件各自写一遍 slog.New(slog.NewTextHandler(io.Discard, nil))。
func Nop() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// New 依据配置构建 slog.Logger，并设置为全局默认 logger。
// cfg 为 nil 时回退到 Info 级别 + text 格式 + stdout。
//
// 第二返回值是 wire 的清理函数，负责关闭日志文件句柄（仅当配置了 file_path 时有实际动作）。
// wire 按构造的**逆序**调用 cleanup：logger 是最早构造的依赖之一，因此它的 cleanup
// 最后执行——这正好保证"关闭数据库"等资源的日志能被完整落盘。
//
// 刻意**不返回 error**：打开日志文件失败时沿用既有的降级行为（回退 stdout + 记一条日志），
// 与改造前保持一致，不放行为变更。
func New(cfg *conf.Log) (*slog.Logger, func()) {
	level := parseLevel(cfg.GetLevel())
	opts := &slog.HandlerOptions{Level: level}

	var w = os.Stdout
	var closer func()
	if path := cfg.GetFilePath(); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			slog.Default().Error("open log file failed, fallback to stdout", "path", path, "error", err)
		} else {
			w = f
			closer = func() {
				if err := f.Close(); err != nil {
					slog.Default().Error("close log file failed", "path", path, "error", err)
				}
			}
		}
	}

	var handler slog.Handler
	if strings.EqualFold(cfg.GetFormat(), "json") {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	if closer == nil {
		closer = func() {}
	}
	return logger, closer
}

// Bootstrap 返回进程启动最早阶段（配置加载 / DI 装配完成之前）使用的最小 logger。
//
// 背景：此前 main.go 先手工 logger.New(cfg.GetLog()) 建一个供启动阶段使用，
// wire 图内又构造了一次注入业务——两个实例、两套配置来源，且第一个的文件句柄从不关闭。
// 引入 Bootstrap 后：启动失败前的极少数日志走它，之后进程内一切日志都由 wire 唯一产出的
// app.Log 承载，日志文件句柄的关闭也交给 wire 的 cleanup 聚合。
func Bootstrap() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
