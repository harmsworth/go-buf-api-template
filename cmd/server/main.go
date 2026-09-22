// Package main 是服务入口：加载配置 → 依赖注入 → 启动 Gin / gRPC / grpc-gateway → 优雅退出。
//
// 同一个进程暴露三种入口（业务实现只有一份，位于 internal/todo）：
//   - Gin HTTP      : cfg.server.addr          → /api/v1/todos
//   - gRPC          : cfg.server.grpc_addr     → todo.v1.TodoService
//   - grpc-gateway  : cfg.server.gateway_addr  → /v1/todos（反向代理到上面的 gRPC）
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	todov1 "go-buf-api-template/gen/go/todo/v1"
	userv1 "go-buf-api-template/gen/go/user/v1"
	"go-buf-api-template/internal/conf"
	"go-buf-api-template/internal/platform/config"
	"go-buf-api-template/internal/platform/database"
	"go-buf-api-template/internal/platform/logger"
	"go-buf-api-template/internal/todo"
	"go-buf-api-template/internal/user"

	"buf.build/go/protovalidate"
	"github.com/gin-gonic/gin"
	protovalidatemw "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/protovalidate"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/automaxprocs/maxprocs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

// shutdownTimeout 是优雅退出等待在途请求的最长时间。
const shutdownTimeout = 5 * time.Second

// App 聚合本进程对外暴露的服务入口。
type App struct {
	// Engine 是 Gin HTTP 引擎（/api/v1/todos）。
	Engine *gin.Engine
	// GRPC 是 gRPC Server（todo.v1.TodoService）。
	GRPC *grpc.Server
}

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

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}

	log := logger.New(cfg.GetLog())
	gin.SetMode(cfg.GetServer().GetMode())

	app, cleanup, err := InitApp(cfg)
	if err != nil {
		log.Error("init app failed", "error", err)
		os.Exit(1)
	}
	defer cleanup()

	// ---------- 1) Gin HTTP ----------
	httpSrv := &http.Server{Addr: cfg.GetServer().GetAddr(), Handler: app.Engine}

	// ---------- 2) gRPC ----------
	grpcAddr := cfg.GetServer().GetGrpcAddr()
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Error("listen grpc failed", "addr", grpcAddr, "error", err)
		os.Exit(1)
	}

	// ---------- 3) grpc-gateway（REST → gRPC 反向代理）----------
	gwMux, err := newGateway(dialAddr(grpcAddr))
	if err != nil {
		log.Error("init grpc-gateway failed", "error", err)
		os.Exit(1)
	}
	gwSrv := &http.Server{Addr: cfg.GetServer().GetGatewayAddr(), Handler: gwMux}

	go func() {
		log.Info("http server started", "addr", httpSrv.Addr, "mode", cfg.GetServer().GetMode())
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server serve failed", "error", err)
		}
	}()

	go func() {
		log.Info("grpc server started", "addr", grpcAddr)
		if err := app.GRPC.Serve(lis); err != nil {
			log.Error("grpc server serve failed", "error", err)
		}
	}()

	go func() {
		log.Info("grpc-gateway started", "addr", gwSrv.Addr)
		if err := gwSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("grpc-gateway serve failed", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down servers")
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Error("http server shutdown failed", "error", err)
	}
	if err := gwSrv.Shutdown(ctx); err != nil {
		log.Error("grpc-gateway shutdown failed", "error", err)
	}
	// GracefulStop 会等待在途 RPC 结束，超时后强制 Stop。
	stopped := make(chan struct{})
	go func() {
		app.GRPC.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-ctx.Done():
		app.GRPC.Stop()
	}

	log.Info("servers exited")
}

// NewApp 构建应用：注册 gRPC 服务实现，并搭建 Gin Engine。
func NewApp(
	cfg *conf.Bootstrap,
	log *slog.Logger,
	th *todo.Handler, ts *todo.Server,
	uh *user.Handler, us *user.Server,
	grpcSrv *grpc.Server,
) *App {
	// gRPC 服务实现
	todov1.RegisterTodoServiceServer(grpcSrv, ts)
	userv1.RegisterUserServiceServer(grpcSrv, us)

	// Gin HTTP 路由
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger(log))
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	todo.RegisterRoutes(r, th)
	user.RegisterRoutes(r, uh)

	return &App{Engine: r, GRPC: grpcSrv}
}

// newGateway 创建 grpc-gateway 反向代理，把 REST 请求转发到 gRPC 服务。
//
// 关键：覆盖默认 marshaler，关闭 EmitUnpopulated。
// grpc-gateway 默认会输出未填充字段，这会让 user.v1.User 的 password_hash
// 以 "passwordHash":"" 的形式出现在响应里——虽然值恒空，但字段名本身不应外泄。
// 关闭后网关输出与 Gin 侧（protojson 默认行为）保持一致。
func newGateway(grpcEndpoint string) (http.Handler, error) {
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{EmitUnpopulated: false},
		}),
	)
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	ctx := context.Background()

	if err := todov1.RegisterTodoServiceHandlerFromEndpoint(ctx, mux, grpcEndpoint, opts); err != nil {
		return nil, err
	}
	if err := userv1.RegisterUserServiceHandlerFromEndpoint(ctx, mux, grpcEndpoint, opts); err != nil {
		return nil, err
	}
	return mux, nil
}

// dialAddr 把监听地址（如 ":9090"）转换为可拨号地址（"127.0.0.1:9090"）。
func dialAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}

// requestLogger 用 slog 记录访问日志。
func requestLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("http request",
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"latency", time.Since(start).String(),
			"client_ip", c.ClientIP(),
		)
	}
}

// provideLogConfig 从 Bootstrap 中取出日志配置。
func provideLogConfig(cfg *conf.Bootstrap) *conf.Log { return cfg.GetLog() }

// provideDatabaseConfig 从 Bootstrap 中取出数据库配置。
func provideDatabaseConfig(cfg *conf.Bootstrap) *conf.Database { return cfg.GetDatabase() }

// provideGRPCServer 构建 gRPC Server：拦截器链为 recovery → protovalidate（契约校验）。
// 校验规则来自 .proto 中的 buf.validate，业务代码零手写校验。
func provideGRPCServer(log *slog.Logger) (*grpc.Server, error) {
	validator, err := protovalidate.New()
	if err != nil {
		return nil, err
	}
	log.Debug("protovalidate interceptor enabled")
	return grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recovery.UnaryServerInterceptor(),
			protovalidatemw.UnaryServerInterceptor(validator),
		),
	), nil
}

// provideDB 建立 GORM 连接并在启动阶段执行 SQL 迁移（禁用 AutoMigrate）。
// 第二返回值是 wire 的清理函数，负责关闭数据库连接。
func provideDB(cfg *conf.Database, log *slog.Logger) (*gorm.DB, func(), error) {
	if err := database.EnsureDatabase(cfg.GetDsn()); err != nil {
		return nil, nil, err
	}

	db, err := database.New(cfg, log)
	if err != nil {
		return nil, nil, err
	}
	if err := database.Migrate(db, log); err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		sqlDB, err := db.DB()
		if err != nil {
			return
		}
		if err := sqlDB.Close(); err != nil {
			log.Error("close database failed", "error", err)
		}
	}
	return db, cleanup, nil
}
