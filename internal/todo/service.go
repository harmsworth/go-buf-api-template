package todo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	todov1 "go-buf-api-template/gen/go/todo/v1"
	"go-buf-api-template/internal/platform/aipgorm"

	"github.com/google/uuid"
	"go.einride.tech/aip/filtering"
	"go.einride.tech/aip/pagination"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// ErrNotFound 表示目标待办不存在。
var ErrNotFound = errors.New("todo not found")

// ErrInvalidArgument 表示请求参数（filter / order_by / page_token）不合法。
var ErrInvalidArgument = errors.New("invalid argument")

// defaultPageSize 是未显式指定 page_size 时的分页大小。
const defaultPageSize = 20

// querySchema 声明 ListTodos 可过滤 / 可排序的字段：proto 字段路径 → 数据库列。
// 未在此声明的字段会在过滤表达式类型检查阶段被拒绝，不会进入 SQL。
var querySchema = aipgorm.Schema{
	"id":          {Column: "id", Type: filtering.TypeString},
	"title":       {Column: "title", Type: filtering.TypeString},
	"description": {Column: "description", Type: filtering.TypeString},
	// status 是枚举：按 string ident 声明（einride 的 '=' 没有 enum↔string 重载），
	// 因此写成 status = "DONE"，由 aipgorm 把枚举名翻译成枚举号再与整型列比较。
	"status":     {Column: "status", Type: filtering.TypeString, Enum: todov1.TodoStatus(0).Type()},
	"created_at": {Column: "created_at", Type: filtering.TypeTimestamp},
	"updated_at": {Column: "updated_at", Type: filtering.TypeTimestamp},
}

// filterDeclarations 过滤表达式的类型声明，进程内只构建一次（构建开销较大）。
var (
	declsOnce sync.Once
	declsVal  *filtering.Declarations
	declsErr  error
)

func filterDeclarations() (*filtering.Declarations, error) {
	declsOnce.Do(func() {
		declsVal, declsErr = aipgorm.Declarations(querySchema)
	})
	return declsVal, declsErr
}

// Service 承载 Todo 的核心业务逻辑与 GORM CRUD。
type Service struct {
	db  *gorm.DB
	log *slog.Logger
}

// NewService 构造函数，供 wire 注入。
func NewService(db *gorm.DB, log *slog.Logger) *Service {
	return &Service{db: db, log: log}
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

	if err := s.db.WithContext(ctx).Create(m).Error; err != nil {
		s.log.Error("create todo failed", "error", err)
		return nil, err
	}
	return m.ToProto(), nil
}

// Get 按 ID 查询单个待办；不存在时返回 ErrNotFound。
func (s *Service) Get(ctx context.Context, id string) (*todov1.Todo, error) {
	var m Todo
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		s.log.Error("get todo failed", "id", id, "error", err)
		return nil, err
	}
	return m.ToProto(), nil
}

// List 分页列出待办，返回数据列表与下一页游标（空串表示没有更多数据）。
//
// AIP 三件套：
//   - AIP-158 分页：pagination.ParsePageToken 解析游标，pageToken.Next 生成下一页游标；
//   - AIP-160 过滤：filtering.ParseFilter + aipgorm 翻译成 GORM WHERE；
//   - AIP-132 排序：ordering.ParseOrderBy + 字段白名单校验后拼 ORDER BY。
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

	q := s.db.WithContext(ctx).Model(&Todo{})

	// 旧字段 status 保留兼容：与 filter 同时出现时按 AND 叠加。
	if req.Status != nil {
		q = q.Where("status = ?", int32(req.GetStatus()))
	}

	decls, err := filterDeclarations()
	if err != nil {
		return nil, "", fmt.Errorf("%w: build filter declarations", ErrInvalidArgument)
	}
	if q, err = aipgorm.ApplyFilter(q, req, decls, querySchema); err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}
	if q, err = aipgorm.ApplyOrderBy(q, req, querySchema, "id ASC"); err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}

	var rows []Todo
	// 多取一条用于判断是否存在下一页。
	if err := q.Offset(int(pageToken.Offset)).Limit(pageSize + 1).Find(&rows).Error; err != nil {
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
	var m Todo
	if err := s.db.WithContext(ctx).Where("id = ?", req.GetId()).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		s.log.Error("load todo for update failed", "id", req.GetId(), "error", err)
		return nil, err
	}

	if err := m.applyUpdate(req, time.Now()); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}

	if err := s.db.WithContext(ctx).Save(&m).Error; err != nil {
		s.log.Error("update todo failed", "id", m.ID, "error", err)
		return nil, err
	}
	return m.ToProto(), nil
}

// Delete 软删除待办（依赖 gorm.DeletedAt，deleted_at 非空即视为已删除）。
func (s *Service) Delete(ctx context.Context, id string) error {
	res := s.db.WithContext(ctx).Where("id = ?", id).Delete(&Todo{})
	if res.Error != nil {
		s.log.Error("delete todo failed", "id", id, "error", res.Error)
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
