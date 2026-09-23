// Package grpcx 收敛 gRPC 与 grpc-gateway 的注册契约。
//
// 动机：改造前 cmd/server/main.go 里逐个调用
// RegisterTodoServiceServer / RegisterUserServiceServer 与
// RegisterTodoServiceHandlerFromEndpoint / RegisterUserServiceHandlerFromEndpoint，
// 新增业务域必须记得改 NewApp 与 newGateway 两处——属于"可能忘记改"的清单式接线。
//
// 有了 Registrar 契约，宿主只需聚合一个切片（见 cmd/server/providers.go），
// 新增业务域时 NewApp 的函数签名保持不变。
package grpcx

import (
	"context"
	"fmt"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

// Registrar 是业务包向宿主注册 gRPC 服务与 grpc-gateway 路由的统一契约，
// 由各业务的 *Server 实现。
type Registrar interface {
	// RegisterGRPC 把服务实现挂到 gRPC Server 上。
	RegisterGRPC(grpc.ServiceRegistrar)
	// RegisterGateway 把同一份实现经 REST → gRPC 反向代理暴露出去。
	// 路由由 .proto 中的 google.api.http 注解决定。
	RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error
}

// NewGateway 构建 grpc-gateway 反向代理，把 REST 请求转发到 gRPC 服务。
//
// 关键：覆盖默认 marshaler，关闭 EmitUnpopulated。
// grpc-gateway 默认会输出未填充字段，这会让 user.v1.User 的 password_hash
// 以 "passwordHash":"" 的形式出现在响应里——虽然值恒空，但字段名本身不应外泄。
// 关闭后网关输出与 Gin 侧（protojson 默认行为）保持一致。
//
// 注册列表由调用方聚合传入，本函数不感知具体业务域。
func NewGateway(ctx context.Context, endpoint string, opts []grpc.DialOption, regs []Registrar) (http.Handler, error) {
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{EmitUnpopulated: false},
		}),
	)
	for _, r := range regs {
		if err := r.RegisterGateway(ctx, mux, endpoint, opts); err != nil {
			return nil, fmt.Errorf("register gateway %T: %w", r, err)
		}
	}
	return mux, nil
}
