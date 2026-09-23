package todo

import (
	"context"
	"errors"
	"fmt"

	todov1 "go-buf-api-template/gen/go/todo/v1"
	"go-buf-api-template/internal/platform/aipgorm"

	"go.einride.tech/aip/filtering"
	"gorm.io/gorm"
)

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

// queryDecls 是 ListTodos 过滤表达式的类型声明，包初始化时构建一次。
// Schema 是编译期常量，构造失败只能 panic（见 aipgorm.MustDeclarations）。
var queryDecls = aipgorm.MustDeclarations(querySchema)

// ListQuery 是列表查询参数。它不依赖 proto 请求类型（便于复用与单测），
// 同时实现 filtering.Request / ordering.Request / aipgorm.ListQuery，可被 aipgorm 直接消费。
type ListQuery struct {
	Offset  int
	Limit   int
	Filter  string
	OrderBy string
	// Status 是旧字段 status 的兼容过滤条件（nil = 不过滤）。
	Status *int32
}

// GetFilter 实现 filtering.Request。
func (q ListQuery) GetFilter() string { return q.Filter }

// GetOrderBy 实现 ordering.Request。
func (q ListQuery) GetOrderBy() string { return q.OrderBy }

// GetOffset 实现 aipgorm.ListQuery。
func (q ListQuery) GetOffset() int { return q.Offset }

// GetLimit 实现 aipgorm.ListQuery。
func (q ListQuery) GetLimit() int { return q.Limit }

// repository 是 Todo 的 GORM 持久层实现。
//
// 约定：本文件**不定义接口**（避免预先抽象），接口由消费方 Service 按需声明，
// 符合 Go 的 "accept interfaces, return structs" 与"接口定义在消费方"惯例。
type repository struct {
	db *gorm.DB
}

// 编译期断言：Repository 接口与实现的签名漂移在构建期即暴露，
// 不会等到运行时才以难以定位的方式报错。
var _ Repository = (*repository)(nil)

// NewRepository 构造 GORM 仓储实现，供 wire 注入。
//
// 这里**返回接口而非具体类型**（偏离 "return structs" 惯例），是有意的权衡：
//  1. wire 按类型身份求解依赖图：直接产出 todo.Repository 就不需要 wire.Bind；
//     而 wire.Bind 写在 cmd/server 时无法引用未导出的 *todo.repository，物理上做不到。
//  2. 把可替换点从 NewService 内部（原先硬编码 newRepository(db)）上移到 NewService 的形参，
//     单测因此可以直接 NewService(fake, log)，不必再绕过构造函数去写未导出字段。
//  3. 具体实现保持未导出，调用方无法绕过接口直接依赖结构体。
func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

// Create 插入一条待办。
func (r *repository) Create(ctx context.Context, m *Todo) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// Get 按 ID 查询；不存在时返回包级领域错误 ErrNotFound。
func (r *repository) Get(ctx context.Context, id string) (*Todo, error) {
	var m Todo
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// List 按条件查询列表：旧字段 status 过滤 + AIP-160 过滤 + AIP-132 排序 + offset/limit。
//
// 骨架（过滤→排序→分页→查询）由 aipgorm.List 统一承载；这里只声明
// "本域可过滤可排序的字段"（querySchema）与"旧字段兼容条件"（withDB）。
func (r *repository) List(ctx context.Context, q ListQuery) ([]Todo, error) {
	rows, err := aipgorm.List[Todo](
		r.db.WithContext(ctx).Model(&Todo{}), q, queryDecls, querySchema, "id ASC",
		func(db *gorm.DB) *gorm.DB {
			// 旧字段 status 保留兼容：与 filter 同时出现时按 AND 叠加。
			if q.Status == nil {
				return db
			}
			return db.Where("status = ?", *q.Status)
		},
	)
	if err != nil {
		// 过滤 / 排序表达式非法属于调用方错误，统一包装成 ErrInvalidArgument。
		return nil, fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}
	return rows, nil
}

// Update 保存变更（GORM Save：零值字段也会被写入，故由调用方按需赋值）。
func (r *repository) Update(ctx context.Context, m *Todo) error {
	return r.db.WithContext(ctx).Save(m).Error
}

// SoftDelete 软删除，返回受影响行数（0 表示目标不存在）。
func (r *repository) SoftDelete(ctx context.Context, id string) (int64, error) {
	res := r.db.WithContext(ctx).Where("id = ?", id).Delete(&Todo{})
	return res.RowsAffected, res.Error
}
