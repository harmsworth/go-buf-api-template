package user

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	userv1 "go-buf-api-template/gen/go/user/v1"

	"buf.build/go/protovalidate"
	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler 是 User 模块的 Gin HTTP 处理层：解析请求 → 调用 Service → 输出 Proto JSON。
//
// 入参校验不在此手写：约束声明在 api/user/v1/user.proto 的 buf.validate 规则中，
// 由 protovalidate 统一执行，保持 Handler 层零手写校验。
type Handler struct {
	svc      *Service
	log      *slog.Logger
	validate protovalidate.Validator
}

// NewHandler 构造函数，供 wire 注入。
func NewHandler(svc *Service, log *slog.Logger) (*Handler, error) {
	v, err := protovalidate.New()
	if err != nil {
		return nil, err
	}
	return &Handler{svc: svc, log: log, validate: v}, nil
}

// Login 处理 POST /v1/auth/login
func (h *Handler) Login(c *gin.Context) {
	var req userv1.LoginRequest
	if !h.readProto(c, &req) || !h.validateReq(c, &req) {
		return
	}

	resp, err := h.svc.Login(c.Request.Context(), &req, c.ClientIP())
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	writeProto(c, http.StatusOK, resp)
}

// CreateUser 处理 POST /v1/users
func (h *Handler) CreateUser(c *gin.Context) {
	var req userv1.CreateUserRequest
	if !h.readProto(c, &req) || !h.validateReq(c, &req) {
		return
	}

	u, err := h.svc.CreateUser(c.Request.Context(), &req)
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	writeProto(c, http.StatusCreated, &userv1.CreateUserResponse{User: u})
}

// GetUser 处理 GET /v1/users/:user_id
func (h *Handler) GetUser(c *gin.Context) {
	req := &userv1.GetUserRequest{UserId: c.Param("user_id")}
	if !h.validateReq(c, req) {
		return
	}

	u, err := h.svc.GetUser(c.Request.Context(), req.GetUserId())
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	writeProto(c, http.StatusOK, &userv1.GetUserResponse{User: u})
}

// ListUsers 处理 GET /v1/users
func (h *Handler) ListUsers(c *gin.Context) {
	req := &userv1.ListUsersRequest{
		PageToken: c.DefaultQuery("page_token", ""),
		// AIP-160 过滤表达式 / AIP-132 排序表达式
		Filter:  c.DefaultQuery("filter", ""),
		OrderBy: c.DefaultQuery("order_by", ""),
	}
	if v := c.Query("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(c, http.StatusBadRequest, "page_size must be an integer")
			return
		}
		req.PageSize = int32(n)
	}
	if v := c.Query("status"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(c, http.StatusBadRequest, "status must be an integer")
			return
		}
		req.Status = userv1.UserStatus(n).Enum()
	}
	if v := c.Query("keyword"); v != "" {
		req.Keyword = proto.String(v)
	}
	if v := c.Query("show_deleted"); v != "" {
		req.ShowDeleted = proto.Bool(v == "true" || v == "1")
	}
	if !h.validateReq(c, req) {
		return
	}

	users, next, err := h.svc.ListUsers(c.Request.Context(), req)
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	writeProto(c, http.StatusOK, &userv1.ListUsersResponse{Users: users, NextPageToken: next})
}

// UpdateUser 处理 PATCH /v1/users/:user_id
func (h *Handler) UpdateUser(c *gin.Context) {
	var req userv1.UpdateUserRequest
	// 先读 body，再补路径参数，最后统一校验：顺序颠倒会让 user_id 的 uuid 规则对空值报错。
	if !h.readProto(c, &req) {
		return
	}
	req.UserId = c.Param("user_id")
	if !h.validateReq(c, &req) {
		return
	}

	u, err := h.svc.UpdateUser(c.Request.Context(), &req)
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	writeProto(c, http.StatusOK, &userv1.UpdateUserResponse{User: u})
}

// DeleteUser 处理 DELETE /v1/users/:user_id
func (h *Handler) DeleteUser(c *gin.Context) {
	req := &userv1.DeleteUserRequest{UserId: c.Param("user_id")}
	if !h.validateReq(c, req) {
		return
	}

	if err := h.svc.DeleteUser(c.Request.Context(), req.GetUserId()); err != nil {
		h.writeDomainError(c, err)
		return
	}
	writeProto(c, http.StatusOK, &userv1.DeleteUserResponse{UserId: req.GetUserId()})
}

// ChangePassword 处理 POST /v1/users/:user_id/change-password
// （proto 中的自定义方法路径为 /v1/users/{user_id}:changePassword，Gin 不支持段内冒号，
//
//	该路径的原生形态由 grpc-gateway 提供，见 internal/user/server.go。）
func (h *Handler) ChangePassword(c *gin.Context) {
	var req userv1.ChangePasswordRequest
	if !h.readProto(c, &req) {
		return
	}
	req.UserId = c.Param("user_id")
	if !h.validateReq(c, &req) {
		return
	}

	changedAt, err := h.svc.ChangePassword(c.Request.Context(), &req)
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	writeProto(c, http.StatusOK, &userv1.ChangePasswordResponse{
		PasswordChangedAt: timestamppb.New(changedAt),
	})
}

// ResetPassword 处理 POST /v1/users/:user_id/reset-password（管理员操作）
func (h *Handler) ResetPassword(c *gin.Context) {
	req := &userv1.ResetPasswordRequest{UserId: c.Param("user_id")}
	if !h.validateReq(c, req) {
		return
	}

	temp, err := h.svc.ResetPassword(c.Request.Context(), req)
	if err != nil {
		h.writeDomainError(c, err)
		return
	}
	writeProto(c, http.StatusOK, &userv1.ResetPasswordResponse{TemporaryPassword: temp})
}

// readProto 读取请求体并用 protojson 解析（兼容 snake_case / lowerCamel 字段名与枚举名）。
// 只做反序列化，不执行校验——校验由调用方在补全路径参数后再触发。
func (h *Handler) readProto(c *gin.Context, msg proto.Message) bool {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeError(c, http.StatusBadRequest, "read request body failed")
		return false
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	opts := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := opts.Unmarshal(body, msg); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// validateReq 执行契约中声明的 buf.validate 规则。
func (h *Handler) validateReq(c *gin.Context, msg proto.Message) bool {
	if err := h.validate.Validate(msg); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

// writeDomainError 将领域错误映射为 HTTP 状态码。
func (h *Handler) writeDomainError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(c, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrInvalidCredentials):
		writeError(c, http.StatusUnauthorized, err.Error())
	case errors.Is(err, ErrDuplicateUser):
		writeError(c, http.StatusConflict, err.Error())
	case errors.Is(err, ErrSamePassword):
		writeError(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrInvalidArgument):
		// filter / order_by / page_token 非法
		writeError(c, http.StatusBadRequest, err.Error())
	default:
		h.log.Error("unhandled service error", "error", err)
		writeError(c, http.StatusInternalServerError, "internal server error")
	}
}

// writeProto 以 Proto JSON 输出响应。
func writeProto(c *gin.Context, status int, msg proto.Message) {
	data, err := protojson.Marshal(msg)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "marshal response failed")
		return
	}
	c.Data(status, "application/json", data)
}

// writeError 输出简单错误响应。
func writeError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"code": status, "message": message})
}
