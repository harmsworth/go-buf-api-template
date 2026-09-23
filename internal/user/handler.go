package user

import (
	"log/slog"
	"net/http"

	userv1 "go-buf-api-template/gen/go/user/v1"
	"go-buf-api-template/internal/platform/httpx"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler 是 User 模块的 Gin HTTP 处理层：解析请求 → 调用 Service → 输出 Proto JSON。
//
// 入参校验不在此手写：约束声明在 api/user/v1/user.proto 的 buf.validate 规则中，
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

// RegisterRoutes 把 User 模块的路由注册到给定的 Gin 路由（Engine 或 RouterGroup）。
//
// 路径前缀为 /api/v1（与 internal/todo 的 /api/v1/todos 对齐）；
// gin-gateway 入口走的是 proto 注解里的 /v1/*，两者前缀不同但入参语义一致
// （query 绑定共用 httpx.BindQuery → grpc-gateway 的解析器）。
//
// 另有一处形态差异：proto 的自定义方法路径 /v1/users/{user_id}:changePassword 含段内冒号，
// Gin 路由解析器不支持（一个路径段只能有一个通配符），故改为 /change-password 后缀形式，
// 原生 :changePassword 形态由 grpc-gateway 提供（见 server.go）。
//
// 原 router.go 的内容并入此处。
func (h *Handler) RegisterRoutes(r gin.IRouter) {
	// 认证
	r.POST("/api/v1/auth/login", h.Login)

	// 用户资源
	g := r.Group("/api/v1/users")
	{
		g.POST("", h.CreateUser)
		g.GET("", h.ListUsers)
		g.GET("/:user_id", h.GetUser)
		g.PATCH("/:user_id", h.UpdateUser)
		g.DELETE("/:user_id", h.DeleteUser)

		// 自定义方法（AIP-136）
		g.POST("/:user_id/change-password", h.ChangePassword)
		g.POST("/:user_id/reset-password", h.ResetPassword)
	}
}

// Login 处理 POST /v1/auth/login
func (h *Handler) Login(c *gin.Context) {
	var req userv1.LoginRequest
	if !httpx.ReadBody(c, &req) || !httpx.Validate(c, h.validate, &req) {
		return
	}

	resp, err := h.svc.Login(c.Request.Context(), &req, c.ClientIP())
	if err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusOK, resp)
}

// CreateUser 处理 POST /v1/users
func (h *Handler) CreateUser(c *gin.Context) {
	var req userv1.CreateUserRequest
	if !httpx.ReadBody(c, &req) || !httpx.Validate(c, h.validate, &req) {
		return
	}

	u, err := h.svc.CreateUser(c.Request.Context(), &req)
	if err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusCreated, &userv1.CreateUserResponse{User: u})
}

// GetUser 处理 GET /v1/users/:user_id
func (h *Handler) GetUser(c *gin.Context) {
	req := &userv1.GetUserRequest{UserId: c.Param("user_id")}
	if !httpx.Validate(c, h.validate, req) {
		return
	}

	u, err := h.svc.GetUser(c.Request.Context(), req.GetUserId())
	if err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusOK, &userv1.GetUserResponse{User: u})
}

// ListUsers 处理 GET /api/v1/users
//
// query 绑定交给 httpx.BindQuery（与 gateway 入口共用 grpc-gateway 的解析器），
// 因此 page_size / status / keyword / show_deleted / filter / order_by 全部自动绑定，
// 且 bool 支持标准写法（true/TRUE/1），枚举支持枚举名。
func (h *Handler) ListUsers(c *gin.Context) {
	req := &userv1.ListUsersRequest{}
	if !httpx.BindQuery(c, req) || !httpx.Validate(c, h.validate, req) {
		return
	}

	users, next, err := h.svc.ListUsers(c.Request.Context(), req)
	if err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusOK, &userv1.ListUsersResponse{Users: users, NextPageToken: next})
}

// UpdateUser 处理 PATCH /v1/users/:user_id
func (h *Handler) UpdateUser(c *gin.Context) {
	var req userv1.UpdateUserRequest
	// 先读 body，再补路径参数，最后统一校验：顺序颠倒会让 user_id 的 uuid 规则对空值报错。
	if !httpx.ReadBody(c, &req) {
		return
	}
	req.UserId = c.Param("user_id")
	if !httpx.Validate(c, h.validate, &req) {
		return
	}

	u, err := h.svc.UpdateUser(c.Request.Context(), &req)
	if err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusOK, &userv1.UpdateUserResponse{User: u})
}

// DeleteUser 处理 DELETE /v1/users/:user_id
func (h *Handler) DeleteUser(c *gin.Context) {
	req := &userv1.DeleteUserRequest{UserId: c.Param("user_id")}
	if !httpx.Validate(c, h.validate, req) {
		return
	}

	if err := h.svc.DeleteUser(c.Request.Context(), req.GetUserId()); err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusOK, &userv1.DeleteUserResponse{UserId: req.GetUserId()})
}

// ChangePassword 处理 POST /v1/users/:user_id/change-password
// （proto 中的自定义方法路径为 /v1/users/{user_id}:changePassword，Gin 不支持段内冒号，
//
//	该路径的原生形态由 grpc-gateway 提供，见 internal/user/server.go。）
func (h *Handler) ChangePassword(c *gin.Context) {
	var req userv1.ChangePasswordRequest
	if !httpx.ReadBody(c, &req) {
		return
	}
	req.UserId = c.Param("user_id")
	if !httpx.Validate(c, h.validate, &req) {
		return
	}

	changedAt, err := h.svc.ChangePassword(c.Request.Context(), &req)
	if err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusOK, &userv1.ChangePasswordResponse{
		PasswordChangedAt: timestamppb.New(changedAt),
	})
}

// ResetPassword 处理 POST /v1/users/:user_id/reset-password（管理员操作）
func (h *Handler) ResetPassword(c *gin.Context) {
	req := &userv1.ResetPasswordRequest{UserId: c.Param("user_id")}
	if !httpx.Validate(c, h.validate, req) {
		return
	}

	temp, err := h.svc.ResetPassword(c.Request.Context(), req)
	if err != nil {
		httpx.WriteMapped(c, h.log, err)
		return
	}
	httpx.WriteProto(c, http.StatusOK, &userv1.ResetPasswordResponse{TemporaryPassword: temp})
}
