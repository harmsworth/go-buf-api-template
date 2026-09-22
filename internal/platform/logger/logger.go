// Package logger 基于 Go 原生 log/slog 初始化应用日志。
package logger

import (
	"log/slog"
	"os"
	"strings"

	"go-buf-api-template/internal/conf"
)

// New 依据配置构建 slog.Logger，并设置为全局默认 logger。
// cfg 为 nil 时回退到 Info 级别 + text 格式 + stdout。
func New(cfg *conf.Log) *slog.Logger {
	level := parseLevel(cfg.GetLevel())
	opts := &slog.HandlerOptions{Level: level}

	var w = os.Stdout
	if path := cfg.GetFilePath(); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			slog.Default().Error("open log file failed, fallback to stdout", "path", path, "error", err)
		} else {
			w = f
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
	return logger
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
