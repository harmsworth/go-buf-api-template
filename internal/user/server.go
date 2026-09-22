package user

import (
	"context"
	"errors"

	userv1 "go-buf-api-template/gen/go/user/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server 实现 user.v1.UserServiceServer（gRPC）。
//
// 入参校验不在此手写：由 protovalidate 拦截器统一执行（见 cmd/server 的拦截器链），
// 因此本文件只承载业务编排与错误映射。
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

// Login 登录并签发令牌。
func (s *Server) Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.LoginResponse, error) {
	resp, err := s.svc.Login(ctx, req, clientIP(ctx))
	if err != nil {
		return nil, gRPCError(err)
	}
	return resp, nil
}

// CreateUser 创建用户。
func (s *Server) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	u, err := s.svc.CreateUser(ctx, req)
	if err != nil {
		return nil, gRPCError(err)
	}
	return &userv1.CreateUserResponse{User: u}, nil
}

// GetUser 查询单个用户。
func (s *Server) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	u, err := s.svc.GetUser(ctx, req.GetUserId())
	if err != nil {
		return nil, gRPCError(err)
	}
	return &userv1.GetUserResponse{User: u}, nil
}

// ListUsers 分页列出用户。
func (s *Server) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) (*userv1.ListUsersResponse, error) {
	users, next, err := s.svc.ListUsers(ctx, req)
	if err != nil {
		return nil, gRPCError(err)
	}
	return &userv1.ListUsersResponse{Users: users, NextPageToken: next}, nil
}

// UpdateUser 按 PATCH 三态语义部分更新用户。
func (s *Server) UpdateUser(ctx context.Context, req *userv1.UpdateUserRequest) (*userv1.UpdateUserResponse, error) {
	u, err := s.svc.UpdateUser(ctx, req)
	if err != nil {
		return nil, gRPCError(err)
	}
	return &userv1.UpdateUserResponse{User: u}, nil
}

// DeleteUser 软删除用户。
func (s *Server) DeleteUser(ctx context.Context, req *userv1.DeleteUserRequest) (*userv1.DeleteUserResponse, error) {
	if err := s.svc.DeleteUser(ctx, req.GetUserId()); err != nil {
		return nil, gRPCError(err)
	}
	return &userv1.DeleteUserResponse{UserId: req.GetUserId()}, nil
}

// ChangePassword 修改本人密码（需验证旧密码）。
func (s *Server) ChangePassword(ctx context.Context, req *userv1.ChangePasswordRequest) (*userv1.ChangePasswordResponse, error) {
	changedAt, err := s.svc.ChangePassword(ctx, req)
	if err != nil {
		// 旧密码校验失败按契约约定回 FailedPrecondition。
		if errors.Is(err, ErrInvalidCredentials) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, gRPCError(err)
	}
	return &userv1.ChangePasswordResponse{PasswordChangedAt: timestamppb.New(changedAt)}, nil
}

// ResetPassword 管理员重置密码，返回一次性临时密码。
func (s *Server) ResetPassword(ctx context.Context, req *userv1.ResetPasswordRequest) (*userv1.ResetPasswordResponse, error) {
	temp, err := s.svc.ResetPassword(ctx, req)
	if err != nil {
		return nil, gRPCError(err)
	}
	return &userv1.ResetPasswordResponse{TemporaryPassword: temp}, nil
}

// gRPCError 将领域错误映射为 gRPC 标准状态码。
func gRPCError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ErrInvalidCredentials):
		return status.Error(codes.Unauthenticated, err.Error())
	case errors.Is(err, ErrDuplicateUser):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, ErrSamePassword):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, "internal server error")
	}
}

// clientIP 从 gRPC 上下文中提取对端地址，用于记录最后登录 IP。
func clientIP(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return ""
}
