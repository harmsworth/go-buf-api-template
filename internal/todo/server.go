package todo

import (
	"context"

	todov1 "go-buf-api-template/gen/go/todo/v1"
	"go-buf-api-template/internal/platform/grpcx"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
)

// Server 实现 todo.v1.TodoServiceServer（gRPC）。
//
// 入参校验不在此手写：由 protovalidate 拦截器统一执行（见 cmd/server 的拦截器链）。
// 错误映射同样不在此手写：业务方法直接返回领域错误，由 errorsx.UnaryServerInterceptor
// 统一转换成 gRPC status —— 改造前这里有一份 gRPCError switch，与 user 包那份连签名都不一致。
type Server struct {
	todov1.UnimplementedTodoServiceServer
	svc *Service
}

// NewServer 构造函数，供 wire 注入。
func NewServer(svc *Service) *Server {
	return &Server{svc: svc}
}

// 编译期断言：确保 Server 始终满足宿主的注册契约。
// 加了它之后，proto 重新生成导致接口变化时，构建期就会报错，
// 而不是等到运行时某个 RPC 返回 Unimplemented。
var _ grpcx.Registrar = (*Server)(nil)

// RegisterGRPC 把 todo.v1.TodoServiceServer 注册到给定的 gRPC Server。
func (s *Server) RegisterGRPC(reg grpc.ServiceRegistrar) {
	todov1.RegisterTodoServiceServer(reg, s)
}

// RegisterGateway 把同一份实现注册到 grpc-gateway（REST 路由由 .proto 的 google.api.http 注解决定）。
func (s *Server) RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error {
	return todov1.RegisterTodoServiceHandlerFromEndpoint(ctx, mux, endpoint, opts)
}

// CreateTodo 创建待办。
func (s *Server) CreateTodo(ctx context.Context, req *todov1.CreateTodoRequest) (*todov1.CreateTodoResponse, error) {
	t, err := s.svc.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	return &todov1.CreateTodoResponse{Todo: t}, nil
}

// GetTodo 查询单个待办。
func (s *Server) GetTodo(ctx context.Context, req *todov1.GetTodoRequest) (*todov1.GetTodoResponse, error) {
	t, err := s.svc.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &todov1.GetTodoResponse{Todo: t}, nil
}

// ListTodos 分页列出待办。
func (s *Server) ListTodos(ctx context.Context, req *todov1.ListTodosRequest) (*todov1.ListTodosResponse, error) {
	todos, next, err := s.svc.List(ctx, req)
	if err != nil {
		return nil, err
	}
	return &todov1.ListTodosResponse{Todos: todos, NextPageToken: next}, nil
}

// UpdateTodo 按 PATCH 三态语义部分更新待办。
func (s *Server) UpdateTodo(ctx context.Context, req *todov1.UpdateTodoRequest) (*todov1.UpdateTodoResponse, error) {
	t, err := s.svc.Update(ctx, req)
	if err != nil {
		return nil, err
	}
	return &todov1.UpdateTodoResponse{Todo: t}, nil
}

// DeleteTodo 软删除待办。
func (s *Server) DeleteTodo(ctx context.Context, req *todov1.DeleteTodoRequest) (*todov1.DeleteTodoResponse, error) {
	if err := s.svc.Delete(ctx, req.GetId()); err != nil {
		return nil, err
	}
	return &todov1.DeleteTodoResponse{Id: req.GetId()}, nil
}
