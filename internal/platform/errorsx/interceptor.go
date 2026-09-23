package errorsx

import (
	"context"

	"google.golang.org/grpc"
)

// UnaryServerInterceptor 把业务方法返回的领域错误统一转换成 gRPC status。
//
// 收益：各业务包的 server.go 不再需要各自的 gRPCError helper，也不可能存在
// 「新写的 RPC 方法忘记做错误映射」——这正是改造前 todo/user 两份 gRPCError
// 连签名都不统一的根因。
//
// 放在拦截器链的**最外层（第一个）**：
//
//	grpc.ChainUnaryInterceptor(
//	    errorsx.UnaryServerInterceptor(),
//	    recovery.UnaryServerInterceptor(),
//	    protovalidatemw.UnaryServerInterceptor(validator),
//	)
//
// 内层拦截器产生的错误都已经是 gRPC status（recovery → Internal、
// protovalidate → InvalidArgument），Status 会识别并原样透传，不受影响。
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			return nil, Status(err)
		}
		return resp, nil
	}
}
