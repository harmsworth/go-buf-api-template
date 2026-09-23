package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"go-buf-api-template/internal/conf"
	"go-buf-api-template/internal/platform/database"
	"go-buf-api-template/internal/platform/errorsx"
	"go-buf-api-template/internal/platform/grpcx"
	"go-buf-api-template/internal/platform/httpx"
	"go-buf-api-template/internal/platform/logger"
	"go-buf-api-template/internal/todo"
	"go-buf-api-template/internal/user"

	"github.com/google/wire"
	protovalidatemw "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/protovalidate"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/gorm"
)

// 本文件是**新增/删除业务域时的唯一改动点**。
//
// 为什么把 ProviderSet 集中在 cmd 而不是放进各业务包（kratos-layout 的做法）：
//   - 本仓是 package-by-feature，一个 internal/todo/ 同时承载 GORM PO（model.go）、
//     SQL（repository.go）与业务规则，是完整的纵向切片；让它们和 wire.NewSet 同包，
//     等于让最底层的持久化表示也间接依赖 DI 框架。
//   - 集中之后业务包对 wire 的依赖数为 0，装配知识全部落在这一处文件里，可读性更高。
//
// 注意：本文件**不要加** //go:build wireinject 标签——它必须参与正常构建，
// 才能与带 wireinject 标签的 wire.go（声明）共存。

// PlatformSet 汇聚基础设施 provider：配置切片 → 日志 → 契约校验器 → 数据库 → gRPC Server。
//
// 约定：业务域的增减不应导致本集合变动。
var PlatformSet = wire.NewSet(
	provideLogConfig,
	provideServerConfig,
	provideDatabaseConfig,
	logger.New,
	httpx.NewValidator,
	provideDB,
	provideGRPCServer,
)

// DomainSet 汇聚业务域 provider。
//
// 新增一个业务域 order 时，在这里加四行即可：
//
//	order.NewRepository, order.NewService, order.NewHandler, order.NewServer,
//
// 再把它的 Handler / Server 加进 provideHTTPRegistrars / provideGRPCRegistrars 的形参。
var DomainSet = wire.NewSet(
	todo.NewRepository, todo.NewService, todo.NewHandler, todo.NewServer,
	user.NewRepository, user.NewService, user.NewHandler, user.NewServer,
)

// AppSet 汇聚应用装配本身：注册器聚合 + gateway 构建 + App 构造函数。
var AppSet = wire.NewSet(
	provideHTTPRegistrars,
	provideGRPCRegistrars,
	provideGateway,
	NewApp,
)

// provideHTTPRegistrars 汇总所有业务域的 Gin 路由注册器。
//
// 为什么需要这样一个"返回切片的 provider"：wire **不支持 multibinding**
// （同一输出类型出现两个 provider 会报 "multiple providers"，见 wire 官方
// testdata/MultipleBindings），因此聚合必须显式落在某个 provider 体内。
//
// 代价：新增业务域要在这里加一个形参 + 一行追加（两处编辑，都在本文件）。
// 收益：NewApp 的函数签名被永久冻结，且不会出现"忘了注册某个域"——
// 类型不匹配在编译期就会报错。
func provideHTTPRegistrars(th *todo.Handler, uh *user.Handler) []httpx.Registrar {
	return []httpx.Registrar{th, uh}
}

// provideGRPCRegistrars 汇总 gRPC / grpc-gateway 注册器。
func provideGRPCRegistrars(ts *todo.Server, us *user.Server) []grpcx.Registrar {
	return []grpcx.Registrar{ts, us}
}

// provideGateway 构建 grpc-gateway 反向代理，把 REST 请求转发到 gRPC 服务。
//
// 注册列表来自 provideGRPCRegistrars，因此新增业务域不必再改这里
// （改造前 newGateway 里逐个调用 RegisterXxxHandlerFromEndpoint）。
func provideGateway(cfg *conf.Server, regs []grpcx.Registrar) (http.Handler, error) {
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	return grpcx.NewGateway(context.Background(), dialAddr(cfg.GetGrpcAddr()), opts, regs)
}

// dialAddr 把监听地址（如 ":9090"）转换为可拨号地址（"127.0.0.1:9090"）。
func dialAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}

// provideLogConfig 从 Bootstrap 中取出日志配置。
func provideLogConfig(cfg *conf.Bootstrap) *conf.Log { return cfg.GetLog() }

// provideDatabaseConfig 从 Bootstrap 中取出数据库配置。
func provideDatabaseConfig(cfg *conf.Bootstrap) *conf.Database { return cfg.GetDatabase() }

// provideServerConfig 从 Bootstrap 中取出服务端配置（供 gateway 构建等使用）。
func provideServerConfig(cfg *conf.Bootstrap) *conf.Server { return cfg.GetServer() }

// provideGRPCServer 构建 gRPC Server：拦截器链为 errorsx → recovery → protovalidate。
//
//   - errorsx 在最外层：把业务方法返回的领域错误统一映射为 gRPC status，
//     各业务包的 server.go 因此不需要自己的错误映射 helper；
//   - recovery：panic → Internal；
//   - protovalidate：契约校验，规则来自 .proto 中的 buf.validate，业务代码零手写校验。
//
// validator 由 wire 注入（与 Gin 侧共享同一个实例），不再在此自行构造。
func provideGRPCServer(validate httpx.Validator, log *slog.Logger) (*grpc.Server, error) {
	log.Debug("protovalidate interceptor enabled")
	return grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			errorsx.UnaryServerInterceptor(),
			recovery.UnaryServerInterceptor(),
			protovalidatemw.UnaryServerInterceptor(validate),
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
