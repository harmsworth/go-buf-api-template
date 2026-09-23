package todo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	todov1 "go-buf-api-template/gen/go/todo/v1"
	"go-buf-api-template/internal/platform/errorsx"

	"github.com/google/uuid"
	"go.einride.tech/aip/pagination"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

// 领域错误：HTTP 状态码与 gRPC code 在**声明处**一次性固化，
// Handler / Server 不再各自维护一份 switch（见 internal/platform/errorsx）。
var (
	// ErrNotFound 表示目标待办不存在。
	ErrNotFound = errorsx.New("TODO_NOT_FOUND", "todo not found", http.StatusNotFound, codes.NotFound)
	// ErrInvalidArgument 表示请求参数（filter / order_by / page_token / update_mask）不合法。
	ErrInvalidArgument = errorsx.New("TODO_INVALID_ARGUMENT", "invalid argument",
		http.StatusBadRequest, codes.InvalidArgument)
)

// defaultPageSize 是未显式指定 page_size 时的分页大小。
const defaultPageSize = 20

// Repository 是 Todo 持久层的**消费方视图**：
//
// 接口定义在 Service 侧（而非 repository.go），方法签名与 *repository 一一对应。
// 这样既能在生产注入 GORM 实现，也能在单测注入 fake，且不产生"为抽象而抽象"的空接口。
type Repository interface {
	Create(ctx context.Context, m *Todo) error
	Get(ctx context.Context, id string) (*Todo, error)
	List(ctx context.Context, q ListQuery) ([]Todo, error)
	Update(ctx context.Context, m *Todo) error
	SoftDelete(ctx context.Context, id string) (int64, error)
}

// Service 承载 Todo 的核心业务逻辑。
type Service struct {
	repo Repository
	log  *slog.Logger
}

// NewService 构造函数，供 wire 注入。
//
// repo 是唯一的可替换点：生产注入 NewRepository(*gorm.DB) 的 GORM 实现，单测注入 fake。
// 形参必须是接口而非 *gorm.DB —— 否则替换点被封死在构造函数内部，
// 测试只能绕过构造函数直接写未导出字段。
func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Create 创建待办。ID 由服务端生成（UUID v4），初始状态为 PENDING。
func (s *Service) Create(ctx context.Context, req *todov1.CreateTodoRequest) (*todov1.Todo, error) {
	now := time.Now()
	m := &Todo{
		ID:        uuid.NewString(),
		Title:     req.GetTitle(),
		Status:    int32(todov1.TodoStatus_TODO_STATUS_PENDING),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if req.Description != nil && req.GetDescription() != "" {
		m.Description = proto.String(req.GetDescription())
	}

	if err := s.repo.Create(ctx, m); err != nil {
		s.log.Error("create todo failed", "error", err)
		return nil, err
	}
	return m.ToProto(), nil
}

// Get 按 ID 查询单个待办；不存在时返回 ErrNotFound。
func (s *Service) Get(ctx context.Context, id string) (*todov1.Todo, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Error("get todo failed", "id", id, "error", err)
		}
		return nil, err
	}
	return m.ToProto(), nil
}

// List 分页列出待办，返回数据列表与下一页游标（空串表示没有更多数据）。
//
// AIP 三件套：
//   - AIP-158 分页：pagination.ParsePageToken 解析游标，pageToken.Next 生成下一页游标；
//   - AIP-160 过滤 / AIP-132 排序：由 Repository 经 aipgorm 翻译成 SQL。
func (s *Service) List(ctx context.Context, req *todov1.ListTodosRequest) ([]*todov1.Todo, string, error) {
	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}

	// AIP-158：解析不透明游标（内含 offset 与"请求校验和"，跨页条件变化会被拒绝）。
	pageToken, err := pagination.ParsePageToken(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid page_token", ErrInvalidArgument)
	}

	q := ListQuery{
		Offset:  int(pageToken.Offset),
		Limit:   pageSize + 1, // 多取一条用于判断是否存在下一页
		Filter:  req.GetFilter(),
		OrderBy: req.GetOrderBy(),
	}
	if req.Status != nil {
		q.Status = proto.Int32(int32(req.GetStatus()))
	}

	rows, err := s.repo.List(ctx, q)
	if err != nil {
		// 过滤 / 排序表达式非法属于调用方错误。
		if errors.Is(err, ErrInvalidArgument) {
			return nil, "", err
		}
		s.log.Error("list todos failed", "error", err)
		return nil, "", err
	}

	next := ""
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		next = pageToken.Next(req).String()
	}

	out := make([]*todov1.Todo, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].ToProto())
	}
	return out, next, nil
}

// Update 更新待办。
// 传了 update_mask 走 AIP-134 增量更新，未传则走 PATCH 三态语义（见 model.applyUpdate）。
func (s *Service) Update(ctx context.Context, req *todov1.UpdateTodoRequest) (*todov1.Todo, error) {
	m, err := s.repo.Get(ctx, req.GetId())
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Error("load todo for update failed", "id", req.GetId(), "error", err)
		}
		return nil, err
	}

	if err := m.applyUpdate(req, time.Now()); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}
	if err := s.repo.Update(ctx, m); err != nil {
		s.log.Error("update todo failed", "id", m.ID, "error", err)
		return nil, err
	}
	return m.ToProto(), nil
}

// Delete 软删除待办（deleted_at 非空即视为已删除）。
func (s *Service) Delete(ctx context.Context, id string) error {
	affected, err := s.repo.SoftDelete(ctx, id)
	if err != nil {
		s.log.Error("delete todo failed", "id", id, "error", err)
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
