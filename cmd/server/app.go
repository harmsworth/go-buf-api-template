package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-buf-api-template/internal/conf"
	"go-buf-api-template/internal/platform/grpcx"
	"go-buf-api-template/internal/platform/httpx"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

// shutdownTimeout 是优雅退出等待在途请求的最长时间。
const shutdownTimeout = 5 * time.Second

// App 聚合本进程对外暴露的三个服务入口与生命周期。
//
// 同一进程、一份业务实现（位于 internal/todo、internal/user）：
//   - Gin HTTP      : cfg.server.addr          → /api/v1/todos
//   - gRPC          : cfg.server.grpc_addr     → todo.v1.TodoService
//   - grpc-gateway  : cfg.server.gateway_addr  → /v1/todos（反向代理到上面的 gRPC）
//
// 业务域以**切片**注入（见 NewApp），因此新增/删除业务域不需要改动本文件。
type App struct {
	// Log 是进程内唯一的应用 logger，由 wire 产出。
	Log *slog.Logger
	// HTTP 是 Gin 引擎。
	HTTP *gin.Engine
	// GRPC 是 gRPC Server。
	GRPC *grpc.Server
	// GW 是 grpc-gateway 反向代理。
	GW http.Handler

	cfg *conf.Server

	// 以下三个在 Run 中创建，供 Shutdown 使用。
	httpSrv *http.Server
	gwSrv   *http.Server
	grpcLis net.Listener
}

// NewApp 构建应用：注册 gRPC 服务实现，并搭建 Gin Engine。
//
// 业务域以切片注入（httpRegs / grpcRegs），因此新增或删除业务域**不需要**
// 改动本函数的签名与函数体——注册循环是固定的。对比 kratos-layout 的
// NewHTTPServer/NewGRPCServer：那里每加一个资源都要改函数形参列表。
func NewApp(
	cfg *conf.Bootstrap,
	log *slog.Logger,
	httpRegs []httpx.Registrar,
	grpcRegs []grpcx.Registrar,
	gw http.Handler,
	grpcSrv *grpc.Server,
) *App {
	// gRPC 服务实现
	for _, r := range grpcRegs {
		r.RegisterGRPC(grpcSrv)
	}

	// Gin HTTP 路由
	gin.SetMode(cfg.GetServer().GetMode())
	r := gin.New()
	r.Use(gin.Recovery(), httpx.RequestLogger(log))
	// 健康检查必须在业务路由之前注册，避免被通配路径吞掉。
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	for _, h := range httpRegs {
		h.RegisterRoutes(r)
	}

	return &App{Log: log, HTTP: r, GRPC: grpcSrv, GW: gw, cfg: cfg.GetServer()}
}

// Run 启动三个 server，阻塞直到收到 SIGINT/SIGTERM，然后按 shutdownTimeout 优雅退出。
//
// 退出顺序与改造前逐条一致：gateway → http → gRPC（GracefulStop，超时后强制 Stop）。
func (a *App) Run() error {
	a.httpSrv = &http.Server{Addr: a.cfg.GetAddr(), Handler: a.HTTP}
	a.gwSrv = &http.Server{Addr: a.cfg.GetGatewayAddr(), Handler: a.GW}

	lis, err := net.Listen("tcp", a.cfg.GetGrpcAddr())
	if err != nil {
		return fmt.Errorf("listen grpc %q: %w", a.cfg.GetGrpcAddr(), err)
	}
	a.grpcLis = lis

	go func() {
		a.Log.Info("http server started", "addr", a.httpSrv.Addr, "mode", a.cfg.GetMode())
		if err := a.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.Log.Error("http server serve failed", "error", err)
		}
	}()

	go func() {
		a.Log.Info("grpc server started", "addr", a.cfg.GetGrpcAddr())
		if err := a.GRPC.Serve(lis); err != nil {
			a.Log.Error("grpc server serve failed", "error", err)
		}
	}()

	go func() {
		a.Log.Info("grpc-gateway started", "addr", a.gwSrv.Addr)
		if err := a.gwSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.Log.Error("grpc-gateway serve failed", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	a.Log.Info("shutting down servers")
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	a.Shutdown(ctx)
	a.Log.Info("servers exited")
	return nil
}

// Shutdown 优雅停止三个 server。
func (a *App) Shutdown(ctx context.Context) {
	if a.httpSrv != nil {
		if err := a.httpSrv.Shutdown(ctx); err != nil {
			a.Log.Error("http server shutdown failed", "error", err)
		}
	}
	if a.gwSrv != nil {
		if err := a.gwSrv.Shutdown(ctx); err != nil {
			a.Log.Error("grpc-gateway shutdown failed", "error", err)
		}
	}
	// GracefulStop 会等待在途 RPC 结束，超时后强制 Stop。
	stopped := make(chan struct{})
	go func() {
		a.GRPC.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-ctx.Done():
		a.GRPC.Stop()
	}
}
