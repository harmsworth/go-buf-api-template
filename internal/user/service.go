package user

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	userv1 "go-buf-api-template/gen/go/user/v1"
	"go-buf-api-template/internal/platform/aipgorm"

	sqlmysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"go.einride.tech/aip/filtering"
	"go.einride.tech/aip/pagination"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// 领域错误：由 handler / server 映射为 HTTP 或 gRPC 状态码。
var (
	// ErrNotFound 表示目标用户不存在（含已软删除）。
	ErrNotFound = errors.New("user not found")
	// ErrInvalidCredentials 表示用户名或密码错误。
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrDuplicateUser 表示 username / email 唯一约束冲突。
	ErrDuplicateUser = errors.New("username or email already exists")
	// ErrSamePassword 表示新密码与旧密码相同。
	ErrSamePassword = errors.New("new password must differ from the old one")
	// ErrInvalidArgument 表示请求参数（filter / order_by / page_token）不合法。
	ErrInvalidArgument = errors.New("invalid argument")
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

// filterDeclarations 过滤表达式的类型声明，进程内只构建一次。
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

// defaultPageSize 是未显式指定 page_size 时的分页大小。
const defaultPageSize = 20

// tempPasswordChars 是生成临时密码使用的字符集（满足大小写 + 数字的复杂度要求）。
const tempPasswordChars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// Service 承载 User 的核心业务逻辑与 GORM CRUD。
type Service struct {
	db  *gorm.DB
	log *slog.Logger
}

// NewService 构造函数，供 wire 注入。
func NewService(db *gorm.DB, log *slog.Logger) *Service {
	return &Service{db: db, log: log}
}

// Login 校验用户名密码并签发令牌。
//
// JWT 签发为留桩实现（access_token / refresh_token 为占位串），接入认证模块时替换本函数即可。
func (s *Service) Login(ctx context.Context, req *userv1.LoginRequest, clientIP string) (*userv1.LoginResponse, error) {
	var m UserPO
	if err := s.db.WithContext(ctx).Where("username = ?", req.GetUsername()).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 用户不存在与密码错误返回同一错误，避免账号枚举。
			return nil, ErrInvalidCredentials
		}
		s.log.Error("load user for login failed", "error", err)
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(m.PasswordHash), []byte(req.GetPassword())); err != nil {
		return nil, ErrInvalidCredentials
	}

	now := time.Now()
	ip := clientIP
	if ip == "" {
		ip = ""
	}
	if err := s.db.WithContext(ctx).Model(&UserPO{}).
		Where("user_id = ?", m.UserID).
		Updates(map[string]any{
			"last_login_at": now,
			"last_login_ip": ip,
		}).Error; err != nil {
		s.log.Error("update last login failed", "user_id", m.UserID, "error", err)
		return nil, err
	}
	m.LastLoginAt = &now
	if ip != "" {
		m.LastLoginIP = &ip
	}

	return &userv1.LoginResponse{
		AccessToken:          "stub-access-token",
		RefreshToken:         "stub-refresh-token",
		AccessTokenExpiresAt: timestamppb.New(now.Add(time.Hour)),
		User:                 m.ToProto(),
		TokenType:            "Bearer",
	}, nil
}

// CreateUser 创建用户：bcrypt 加密密码，初始状态为 ACTIVE。
func (s *Service) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(req.GetPassword()), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	m := NewFromCreateRequest(req, string(hash))
	m.UserID = uuid.NewString()
	m.CreatedAt = time.Now()
	m.UpdatedAt = m.CreatedAt

	if err := s.db.WithContext(ctx).Create(m).Error; err != nil {
		if isDuplicate(err) {
			return nil, ErrDuplicateUser
		}
		s.log.Error("create user failed", "error", err)
		return nil, err
	}
	return m.ToProto(), nil
}

// GetUser 按 ID 查询用户。
func (s *Service) GetUser(ctx context.Context, id string) (*userv1.User, error) {
	var m UserPO
	if err := s.db.WithContext(ctx).Where("user_id = ?", id).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		s.log.Error("get user failed", "user_id", id, "error", err)
		return nil, err
	}
	return m.ToProto(), nil
}

// ListUsers 分页列出用户，返回数据列表与下一页游标（空串表示没有更多数据）。
//
// AIP 三件套：
//   - AIP-158 分页：pagination.ParsePageToken 解析游标，pageToken.Next 生成下一页游标；
//   - AIP-160 过滤：filtering.ParseFilter + aipgorm 翻译成 GORM WHERE；
//   - AIP-132 排序：ordering.ParseOrderBy + 字段白名单校验后拼 ORDER BY。
func (s *Service) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) ([]*userv1.User, string, error) {
	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}

	// AIP-158：解析不透明游标（内含 offset 与"请求校验和"，跨页条件变化会被拒绝）。
	pageToken, err := pagination.ParsePageToken(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid page_token", ErrInvalidArgument)
	}

	q := s.db.WithContext(ctx).Model(&UserPO{})
	if req.GetShowDeleted() {
		q = q.Unscoped()
	}

	// 旧字段 status / keyword 保留兼容：与 filter 同时出现时按 AND 叠加。
	if req.Status != nil {
		q = q.Where("status = ?", int32(req.GetStatus()))
	}
	if kw := strings.TrimSpace(req.GetKeyword()); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("username LIKE ? OR nickname LIKE ?", like, like)
	}

	decls, err := filterDeclarations()
	if err != nil {
		return nil, "", fmt.Errorf("%w: build filter declarations", ErrInvalidArgument)
	}
	if q, err = aipgorm.ApplyFilter(q, req, decls, querySchema); err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}
	if q, err = aipgorm.ApplyOrderBy(q, req, querySchema, "user_id ASC"); err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}

	var rows []UserPO
	if err := q.Offset(int(pageToken.Offset)).Limit(pageSize + 1).Find(&rows).Error; err != nil {
		s.log.Error("list users failed", "error", err)
		return nil, "", err
	}

	next := ""
	if len(rows) > pageSize {
		rows = rows[:pageSize]
		next = pageToken.Next(req).String()
	}

	out := make([]*userv1.User, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].ToProto())
	}
	return out, next, nil
}

// UpdateUser 更新用户。
// 传了 update_mask 走 AIP-134 增量更新，未传则走 PATCH 三态语义（见 model.applyUpdate）。
func (s *Service) UpdateUser(ctx context.Context, req *userv1.UpdateUserRequest) (*userv1.User, error) {
	var m UserPO
	if err := s.db.WithContext(ctx).Where("user_id = ?", req.GetUserId()).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		s.log.Error("load user for update failed", "user_id", req.GetUserId(), "error", err)
		return nil, err
	}

	if err := m.applyUpdate(req); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}

	if err := s.db.WithContext(ctx).Save(&m).Error; err != nil {
		s.log.Error("update user failed", "user_id", m.UserID, "error", err)
		return nil, err
	}
	return m.ToProto(), nil
}

// DeleteUser 软删除用户（deleted_at 非空即视为已删除）。
func (s *Service) DeleteUser(ctx context.Context, id string) error {
	res := s.db.WithContext(ctx).Where("user_id = ?", id).Delete(&UserPO{})
	if res.Error != nil {
		s.log.Error("delete user failed", "user_id", id, "error", res.Error)
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ChangePassword 修改本人密码：校验旧密码 → 新旧不得相同 → 写入新哈希。
func (s *Service) ChangePassword(ctx context.Context, req *userv1.ChangePasswordRequest) (time.Time, error) {
	var m UserPO
	if err := s.db.WithContext(ctx).Where("user_id = ?", req.GetUserId()).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, ErrNotFound
		}
		s.log.Error("load user for password change failed", "user_id", req.GetUserId(), "error", err)
		return time.Time{}, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(m.PasswordHash), []byte(req.GetOldPassword())); err != nil {
		return time.Time{}, ErrInvalidCredentials
	}
	if req.GetOldPassword() == req.GetNewPassword() {
		return time.Time{}, ErrSamePassword
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.GetNewPassword()), bcrypt.DefaultCost)
	if err != nil {
		return time.Time{}, fmt.Errorf("hash new password: %w", err)
	}

	now := time.Now()
	m.PasswordHash = string(hash)
	m.UpdatedAt = now
	if err := s.db.WithContext(ctx).Save(&m).Error; err != nil {
		s.log.Error("change password failed", "user_id", m.UserID, "error", err)
		return time.Time{}, err
	}
	return now, nil
}

// ResetPassword 管理员重置密码：服务端生成一次性临时密码，仅本次返回明文，不落日志。
func (s *Service) ResetPassword(ctx context.Context, req *userv1.ResetPasswordRequest) (string, error) {
	var m UserPO
	if err := s.db.WithContext(ctx).Where("user_id = ?", req.GetUserId()).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrNotFound
		}
		s.log.Error("load user for password reset failed", "user_id", req.GetUserId(), "error", err)
		return "", err
	}

	temp, err := generateTempPassword(12)
	if err != nil {
		return "", err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(temp), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash temp password: %w", err)
	}

	m.PasswordHash = string(hash)
	m.UpdatedAt = time.Now()
	if err := s.db.WithContext(ctx).Save(&m).Error; err != nil {
		s.log.Error("reset password failed", "user_id", m.UserID, "error", err)
		return "", err
	}
	return temp, nil
}

// isDuplicate 判断是否为唯一键冲突。
// GORM 的 ErrDuplicatedKey 依赖 Dialector 的错误转换，MySQL 下并不可靠，
// 因此同时直接识别 MySQL 的 1062（ER_DUP_ENTRY）。
func isDuplicate(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var mysqlErr *sqlmysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	return false
}

// generateTempPassword 生成满足"小写 + 大写 + 数字"复杂度的随机临时密码。
func generateTempPassword(n int) (string, error) {
	if n < 8 {
		n = 12
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(tempPasswordChars))))
		if err != nil {
			return "", fmt.Errorf("generate temp password: %w", err)
		}
		b.WriteByte(tempPasswordChars[idx.Int64()])
	}
	return b.String(), nil
}
