package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	userv1 "go-buf-api-template/gen/go/user/v1"
	"go-buf-api-template/internal/platform/aipgorm"

	sqlmysql "github.com/go-sql-driver/mysql"
	"go.einride.tech/aip/filtering"
	"gorm.io/gorm"
)

// querySchema 声明 ListUsers 可过滤 / 可排序的字段：proto 字段路径 → 数据库列。
var querySchema = aipgorm.Schema{
	"user_id":  {Column: "user_id", Type: filtering.TypeString},
	"username": {Column: "username", Type: filtering.TypeString},
	"email":    {Column: "email", Type: filtering.TypeString},
	"nickname": {Column: "nickname", Type: filtering.TypeString},
	// status 是枚举：按 string ident 声明，写成 status = "ACTIVE"。
	"status":     {Column: "status", Type: filtering.TypeString, Enum: userv1.UserStatus(0).Type()},
	"phone":      {Column: "phone", Type: filtering.TypeString},
	"created_at": {Column: "created_at", Type: filtering.TypeTimestamp},
	"updated_at": {Column: "updated_at", Type: filtering.TypeTimestamp},
}

// queryDecls 是 ListUsers 过滤表达式的类型声明，包初始化时构建一次。
var queryDecls = aipgorm.MustDeclarations(querySchema)

// ListQuery 是列表查询参数（不依赖 proto 请求类型），
// 同时实现 filtering.Request / ordering.Request / aipgorm.ListQuery。
type ListQuery struct {
	Offset  int
	Limit   int
	Filter  string
	OrderBy string
	// Status / Keyword 是旧字段的兼容过滤条件。
	Status  *int32
	Keyword string
	// IncludeDeleted 为 true 时包含已软删除用户（Unscoped）。
	IncludeDeleted bool
}

// GetFilter 实现 filtering.Request。
func (q ListQuery) GetFilter() string { return q.Filter }

// GetOrderBy 实现 ordering.Request。
func (q ListQuery) GetOrderBy() string { return q.OrderBy }

// GetOffset 实现 aipgorm.ListQuery。
func (q ListQuery) GetOffset() int { return q.Offset }

// GetLimit 实现 aipgorm.ListQuery。
func (q ListQuery) GetLimit() int { return q.Limit }

// repository 是 User 的 GORM 持久层实现。
//
// 约定：本文件**不定义接口**（避免预先抽象），接口由消费方 Service 按需声明。
type repository struct {
	db *gorm.DB
}

// 编译期断言：Repository 接口与实现的签名漂移在构建期即暴露。
var _ Repository = (*repository)(nil)

// NewRepository 构造 GORM 仓储实现，供 wire 注入。
//
// 返回接口而非具体类型的原因与 todo/repository.go 一致：
// wire 免 Bind、可替换点上移到 NewService 形参、实现类型不外泄。
func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

// Create 插入用户。
//
// 唯一键冲突在此翻译成领域错误 ErrDuplicateUser：GORM 的 ErrDuplicatedKey 依赖
// Dialector 的错误转换（MySQL 下并不可靠），因此同时直接识别 MySQL 的 1062（ER_DUP_ENTRY）。
// 把驱动细节收在仓储层，Service 与更上层不必再 import 任何数据库驱动类型。
func (r *repository) Create(ctx context.Context, m *UserPO) error {
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		if isDuplicateKey(err) {
			return ErrDuplicateUser
		}
		return err
	}
	return nil
}

// isDuplicateKey 判断是否为唯一键冲突。
func isDuplicateKey(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var mysqlErr *sqlmysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	return false
}

// Get 按 ID 查询；不存在时返回包级领域错误 ErrNotFound。
func (r *repository) Get(ctx context.Context, id string) (*UserPO, error) {
	var m UserPO
	if err := r.db.WithContext(ctx).Where("user_id = ?", id).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// GetByUsername 按登录名查询（用于 Login）；不存在时返回 ErrNotFound。
func (r *repository) GetByUsername(ctx context.Context, username string) (*UserPO, error) {
	var m UserPO
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// List 按条件查询列表：旧字段兼容 + AIP-160 过滤 + AIP-132 排序 + offset/limit。
//
// 骨架（过滤→排序→分页→查询）由 aipgorm.List 统一承载。
func (r *repository) List(ctx context.Context, q ListQuery) ([]UserPO, error) {
	rows, err := aipgorm.List[UserPO](
		r.db.WithContext(ctx).Model(&UserPO{}), q, queryDecls, querySchema, "user_id ASC",
		func(db *gorm.DB) *gorm.DB {
			// 顺序即语义：这些业务条件必须先生效，AIP 表达式再与之 AND。
			if q.IncludeDeleted {
				db = db.Unscoped()
			}
			if q.Status != nil {
				db = db.Where("status = ?", *q.Status)
			}
			if kw := strings.TrimSpace(q.Keyword); kw != "" {
				// 括号不可省略：OR 的优先级低于 AND，缺括号会让 AIP 过滤条件
				// 只作用于 nickname 分支（原先就是如此），语义与调用方预期不符。
				like := "%" + kw + "%"
				db = db.Where("(username LIKE ? OR nickname LIKE ?)", like, like)
			}
			return db
		},
	)
	if err != nil {
		// 过滤 / 排序表达式非法属于调用方错误。
		return nil, fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}
	return rows, nil
}

// Update 保存变更。
func (r *repository) Update(ctx context.Context, m *UserPO) error {
	return r.db.WithContext(ctx).Save(m).Error
}

// UpdateLastLogin 记录最后登录时间与 IP。
func (r *repository) UpdateLastLogin(ctx context.Context, id string, at time.Time, ip string) error {
	return r.db.WithContext(ctx).Model(&UserPO{}).
		Where("user_id = ?", id).
		Updates(map[string]any{"last_login_at": at, "last_login_ip": ip}).Error
}

// SoftDelete 软删除，返回受影响行数（0 表示目标不存在）。
func (r *repository) SoftDelete(ctx context.Context, id string) (int64, error) {
	res := r.db.WithContext(ctx).Where("user_id = ?", id).Delete(&UserPO{})
	return res.RowsAffected, res.Error
}
