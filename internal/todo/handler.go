package todo

import (
	"log/slog"
	"net/http"

	todov1 "go-buf-api-template/gen/go/todo/v1"
	"go-buf-api-template/internal/platform/httpx"

	"github.com/gin-gonic/gin"
)

// Handler 是 Todo 模块的 Gin HTTP 处理层：解析请求 → 调用 Service → 输出 Proto JSON。
//
// 入参校验不在此手写：约束声明在 api/todo/v1/todo.proto 的 buf.validate 规则中，
// 由 protovalidate 统一执行，保持 Handler 层零手写校验。
type Handler struct {
	svc      *Service
	log      *slog.Logger
	validate httpx.Validator
}

// NewHandler 构造函数，供 wire 注入。
//
// 校验器由 wire 注入（进程内共享一份），因此本函数不再自己构造 protovalidate，
// 也不再需要返回 error —— wire 图里少一个错误分支。
func NewHandler(svc *Service, validate httpx.Validator, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log, validate: validate}
}

// 编译期断言：确保 Handler 始终满足宿主的路由注册契约。
var _ httpx.Registrar = (*Handler)(nil)

// RegisterRoutes 把 Todo 模块的路由注册到给定的 Gin 路由（Engine 或 RouterGroup）。
//
// 原 router.go 的内容并入此处：它与 Handler 是严格一对一关系，
// 单独成文件只多一个 package 声明与 import block，没有隔离收益。
func (h *Handler) RegisterRoutes(r gin.IRouter) {
	g := r.Group("/api/v1/todos")
	{
		g.POST("", h.Create)
		g.GET("", h.List)
		g.GET("/:id", h.Get)
		g.PATCH("/:id", h.Update)
		g.DELETE("/:id", h.Delete)
	}
}

// Create 处理 POST /api/v1/todos
func (h *Handler) Create(c *gin.Context) {
	var req todov1.CreateTodoRequest
	if !httpx.ReadBody(c, &req) || !httpx.Validate(c, h.validate, &req) {
		return
	}
	h.log.Debug("create todo", "title", req.GetTitle())

	todo, err := h.svc.Create(c.Request.Context(), &req)
	if err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusCreated, &todov1.CreateTodoResponse{Todo: todo})
}

// Get 处理 GET /api/v1/todos/:id
func (h *Handler) Get(c *gin.Context) {
	req := &todov1.GetTodoRequest{Id: c.Param("id")}
	if !httpx.Validate(c, h.validate, req) {
		return
	}

	todo, err := h.svc.Get(c.Request.Context(), req.GetId())
	if err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusOK, &todov1.GetTodoResponse{Todo: todo})
}

// List 处理 GET /api/v1/todos
//
// query 绑定交给 httpx.BindQuery（与 gateway 入口共用 grpc-gateway 的解析器），
// 因此 page_size / status / filter / order_by 无需逐个手写解析，枚举名也可直接使用。
func (h *Handler) List(c *gin.Context) {
	req := &todov1.ListTodosRequest{}
	if !httpx.BindQuery(c, req) || !httpx.Validate(c, h.validate, req) {
		return
	}

	todos, next, err := h.svc.List(c.Request.Context(), req)
	if err != nil {
		// filter / order_by / page_token 非法 → 400，其余 → 错误声明处固化的状态码
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusOK, &todov1.ListTodosResponse{Todos: todos, NextPageToken: next})
}

// Update 处理 PATCH /api/v1/todos/:id
func (h *Handler) Update(c *gin.Context) {
	var req todov1.UpdateTodoRequest
	// 先读 body，再补路径参数，最后统一校验：顺序颠倒会让 id 的 uuid 规则对空值报错。
	if !httpx.ReadBody(c, &req) {
		return
	}
	req.Id = c.Param("id")
	if !httpx.Validate(c, h.validate, &req) {
		return
	}

	todo, err := h.svc.Update(c.Request.Context(), &req)
	if err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusOK, &todov1.UpdateTodoResponse{Todo: todo})
}

// Delete 处理 DELETE /api/v1/todos/:id
func (h *Handler) Delete(c *gin.Context) {
	req := &todov1.DeleteTodoRequest{Id: c.Param("id")}
	if !httpx.Validate(c, h.validate, req) {
		return
	}

	if err := h.svc.Delete(c.Request.Context(), req.GetId()); err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusOK, &todov1.DeleteTodoResponse{Id: req.GetId()})
}
