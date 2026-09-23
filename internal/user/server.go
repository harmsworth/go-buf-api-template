package user

import (
	"context"

	userv1 "go-buf-api-template/gen/go/user/v1"
	"go-buf-api-template/internal/platform/grpcx"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server 实现 user.v1.UserServiceServer（gRPC）。
//
// 入参校验不在此手写：由 protovalidate 拦截器统一执行（见 cmd/server 的拦截器链）。
// 错误映射同样不在此手写：业务方法直接返回领域错误，由 errorsx.UnaryServerInterceptor
// 统一转换成 gRPC status —— 改造前这里有一份 gRPCError switch，外加 ChangePassword
// 的一段特例分支。
//
// 注意：grpc-gateway 依据 .proto 的 google.api.http 注解生成路由，
// 自定义方法的原生路径 /v1/users/{user_id}:changePassword 在此形态下可用。
type Server struct {
	userv1.UnimplementedUserServiceServer
	svc *Service
}

// NewServer 构造函数，供 wire 注入。
func NewServer(svc *Service) *Server {
	return &Server{svc: svc}
}

// 编译期断言：确保 Server 始终满足宿主的注册契约。
var _ grpcx.Registrar = (*Server)(nil)

// RegisterGRPC 把 user.v1.UserServiceServer 注册到给定的 gRPC Server。
func (s *Server) RegisterGRPC(reg grpc.ServiceRegistrar) {
	userv1.RegisterUserServiceServer(reg, s)
}

// RegisterGateway 把同一份实现注册到 grpc-gateway（REST 路由由 .proto 的 google.api.http 注解决定）。
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return userv1.RegisterUserServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// Login 登录并签发令牌。
func (s *Server) Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.LoginResponse, error) {
	resp, err := s.svc.Login(ctx, req, clientIP(ctx))
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// CreateUser 创建用户。
func (s *Server) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	u, err := s.svc.CreateUser(ctx, req)
	if err != nil {
		return nil, err
	}
	return &userv1.CreateUserResponse{User: u}, nil
}

// GetUser 查询单个用户。
func (s *Server) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	u, err := s.svc.GetUser(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &userv1.GetUserResponse{User: u}, nil
}

// ListUsers 分页列出用户。
func (s *Server) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) (*userv1.ListUsersResponse, error) {
	users, next, err := s.svc.ListUsers(ctx, req)
	if err != nil {
		return nil, err
	}
	return &userv1.ListUsersResponse{Users: users, NextPageToken: next}, nil
}

// UpdateUser 按 PATCH 三态语义部分更新用户。
func (s *Server) UpdateUser(ctx context.Context, req *userv1.UpdateUserRequest) (*userv1.UpdateUserResponse, error) {
	u, err := s.svc.UpdateUser(ctx, req)
	if err != nil {
		return nil, err
	}
	return &userv1.UpdateUserResponse{User: u}, nil
}

// DeleteUser 软删除用户。
func (s *Server) DeleteUser(ctx context.Context, req *userv1.DeleteUserRequest) (*userv1.DeleteUserResponse, error) {
	if err := s.svc.DeleteUser(ctx, req.GetUserId()); err != nil {
		return nil, err
	}
	return &userv1.DeleteUserResponse{UserId: req.GetUserId()}, nil
}

// ChangePassword 修改本人密码（需验证旧密码）。
//
// 旧密码不匹配由 Service 返回 ErrPasswordMismatch，其 gRPC code（FailedPrecondition）
// 已在声明处固化，因此这里不再需要特例 switch。
func (s *Server) ChangePassword(ctx context.Context, req *userv1.ChangePasswordRequest) (*userv1.ChangePasswordResponse, error) {
	changedAt, err := s.svc.ChangePassword(ctx, req)
	if err != nil {
		return nil, err
	}
	return &userv1.ChangePasswordResponse{PasswordChangedAt: timestamppb.New(changedAt)}, nil
}

// ResetPassword 管理员重置密码，返回一次性临时密码。
func (s *Server) ResetPassword(ctx context.Context, req *userv1.ResetPasswordRequest) (*userv1.ResetPasswordResponse, error) {
	temp, err := s.svc.ResetPassword(ctx, req)
	if err != nil {
		return nil, err
	}
	return &userv1.ResetPasswordResponse{TemporaryPassword: temp}, nil
}

// clientIP 从 gRPC 上下文中提取对端地址，用于记录最后登录 IP。
func clientIP(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return ""
}
