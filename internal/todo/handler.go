package todo

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	todov1 "go-buf-api-template/gen/go/todo/v1"

	"buf.build/go/protovalidate"
	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Handler 是 Todo 模块的 Gin HTTP 处理层：解析请求 → 调用 Service → 输出 Proto JSON。
//
// 入参校验不在此手写：约束声明在 api/todo/v1/todo.proto 的 buf.validate 规则中，
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

// Create 处理 POST /api/v1/todos
func (h *Handler) Create(c *gin.Context) {
	var req todov1.CreateTodoRequest
	if !h.readProto(c, &req) || !h.validateReq(c, &req) {
		return
	}
	h.log.Debug("create todo", "title", req.GetTitle())

	todo, err := h.svc.Create(c.Request.Context(), &req)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "create todo failed")
		return
	}
	writeProto(c, http.StatusCreated, &todov1.CreateTodoResponse{Todo: todo})
}

// Get 处理 GET /api/v1/todos/:id
func (h *Handler) Get(c *gin.Context) {
	req := &todov1.GetTodoRequest{Id: c.Param("id")}
	if !h.validateReq(c, req) {
		return
	}

	todo, err := h.svc.Get(c.Request.Context(), req.GetId())
	if err != nil {
		writeStatusError(c, err)
		return
	}
	writeProto(c, http.StatusOK, &todov1.GetTodoResponse{Todo: todo})
}

// List 处理 GET /api/v1/todos
func (h *Handler) List(c *gin.Context) {
	req := &todov1.ListTodosRequest{
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
		req.Status = todov1.TodoStatus(n).Enum()
	}
	if !h.validateReq(c, req) {
		return
	}

	todos, next, err := h.svc.List(c.Request.Context(), req)
	if err != nil {
		// filter / order_by / page_token 非法 → 400，其余 → 500
		writeStatusError(c, err)
		return
	}
	writeProto(c, http.StatusOK, &todov1.ListTodosResponse{Todos: todos, NextPageToken: next})
}

// Update 处理 PATCH /api/v1/todos/:id
func (h *Handler) Update(c *gin.Context) {
	var req todov1.UpdateTodoRequest
	// 先读 body，再补路径参数，最后统一校验：顺序颠倒会让 id 的 uuid 规则对空值报错。
	if !h.readProto(c, &req) {
		return
	}
	req.Id = c.Param("id")
	if !h.validateReq(c, &req) {
		return
	}

	todo, err := h.svc.Update(c.Request.Context(), &req)
	if err != nil {
		writeStatusError(c, err)
		return
	}
	writeProto(c, http.StatusOK, &todov1.UpdateTodoResponse{Todo: todo})
}

// Delete 处理 DELETE /api/v1/todos/:id
func (h *Handler) Delete(c *gin.Context) {
	req := &todov1.DeleteTodoRequest{Id: c.Param("id")}
	if !h.validateReq(c, req) {
		return
	}

	if err := h.svc.Delete(c.Request.Context(), req.GetId()); err != nil {
		writeStatusError(c, err)
		return
	}
	writeProto(c, http.StatusOK, &todov1.DeleteTodoResponse{Id: req.GetId()})
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

// writeStatusError 将领域错误映射为 HTTP 状态码。
func writeStatusError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(c, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrInvalidArgument):
		// filter / order_by / page_token 非法
		writeError(c, http.StatusBadRequest, err.Error())
	default:
		writeError(c, http.StatusInternalServerError, "internal server error")
	}
}
