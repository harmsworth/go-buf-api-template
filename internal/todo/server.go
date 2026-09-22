package todo

import (
	"context"
	"errors"

	todov1 "go-buf-api-template/gen/go/todo/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server 实现 todo.v1.TodoServiceServer（gRPC）。
//
// 入参校验不在此手写：由 protovalidate 拦截器统一执行（见 cmd/server 的拦截器链），
// 因此本文件只承载业务编排与错误映射。
type Server struct {
	todov1.UnimplementedTodoServiceServer
	svc *Service
}

// NewServer 构造函数，供 wire 注入。
func NewServer(svc *Service) *Server {
	return &Server{svc: svc}
}

// CreateTodo 创建待办。
func (s *Server) CreateTodo(ctx context.Context, req *todov1.CreateTodoRequest) (*todov1.CreateTodoResponse, error) {
	t, err := s.svc.Create(ctx, req)
	if err != nil {
		return nil, gRPCError(err, "create todo failed")
	}
	return &todov1.CreateTodoResponse{Todo: t}, nil
}

// GetTodo 查询单个待办。
func (s *Server) GetTodo(ctx context.Context, req *todov1.GetTodoRequest) (*todov1.GetTodoResponse, error) {
	t, err := s.svc.Get(ctx, req.GetId())
	if err != nil {
		return nil, gRPCError(err, "get todo failed")
	}
	return &todov1.GetTodoResponse{Todo: t}, nil
}

// ListTodos 分页列出待办。
func (s *Server) ListTodos(ctx context.Context, req *todov1.ListTodosRequest) (*todov1.ListTodosResponse, error) {
	todos, next, err := s.svc.List(ctx, req)
	if err != nil {
		return nil, gRPCError(err, "list todos failed")
	}
	return &todov1.ListTodosResponse{Todos: todos, NextPageToken: next}, nil
}

// UpdateTodo 按 PATCH 三态语义部分更新待办。
func (s *Server) UpdateTodo(ctx context.Context, req *todov1.UpdateTodoRequest) (*todov1.UpdateTodoResponse, error) {
	t, err := s.svc.Update(ctx, req)
	if err != nil {
		return nil, gRPCError(err, "update todo failed")
	}
	return &todov1.UpdateTodoResponse{Todo: t}, nil
}

// DeleteTodo 软删除待办。
func (s *Server) DeleteTodo(ctx context.Context, req *todov1.DeleteTodoRequest) (*todov1.DeleteTodoResponse, error) {
	if err := s.svc.Delete(ctx, req.GetId()); err != nil {
		return nil, gRPCError(err, "delete todo failed")
	}
	return &todov1.DeleteTodoResponse{Id: req.GetId()}, nil
}

// gRPCError 将领域错误映射为 gRPC 标准状态码。
func gRPCError(err error, internalMsg string) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, internalMsg)
	}
}
