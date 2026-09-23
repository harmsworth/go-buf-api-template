package user

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	userv1 "go-buf-api-template/gen/go/user/v1"
	"go-buf-api-template/internal/platform/errorsx"

	"github.com/google/uuid"
	"go.einride.tech/aip/pagination"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 领域错误：HTTP 状态码与 gRPC code 在**声明处**一次性固化，
// Handler / Server 不再各自维护一份 switch（见 internal/platform/errorsx）。
var (
	// ErrNotFound 表示目标用户不存在（含已软删除）。
	ErrNotFound = errorsx.New("USER_NOT_FOUND", "user not found", http.StatusNotFound, codes.NotFound)
	// ErrInvalidCredentials 表示用户名或密码错误（Login 场景）。
	ErrInvalidCredentials = errorsx.New("USER_INVALID_CREDENTIALS", "invalid username or password",
		http.StatusUnauthorized, codes.Unauthenticated)
	// ErrPasswordMismatch 表示 ChangePassword 的旧密码校验失败。
	//
	// 与 ErrInvalidCredentials 分离的原因：ChangePassword 的调用者已经完成认证，
	// 按契约回 FailedPrecondition / 400；而 Login 的凭证错误回 Unauthenticated / 401。
	// 共用一个哨兵会造成"同一错误在两个 RPC 上语义不同"——改造前正是靠 server.go 里
	// 一段特例 switch 兜住的，现在由声明处直接表达。
	ErrPasswordMismatch = errorsx.New("USER_PASSWORD_MISMATCH", "old password does not match",
		http.StatusBadRequest, codes.FailedPrecondition)
	// ErrDuplicateUser 表示 username / email 唯一约束冲突。
	ErrDuplicateUser = errorsx.New("USER_ALREADY_EXISTS", "username or email already exists",
		http.StatusConflict, codes.AlreadyExists)
	// ErrSamePassword 表示新密码与旧密码相同。
	ErrSamePassword = errorsx.New("USER_SAME_PASSWORD", "new password must differ from the old one",
		http.StatusBadRequest, codes.InvalidArgument)
	// ErrInvalidArgument 表示请求参数（filter / order_by / page_token / update_mask）不合法。
	ErrInvalidArgument = errorsx.New("USER_INVALID_ARGUMENT", "invalid argument",
		http.StatusBadRequest, codes.InvalidArgument)
)

// defaultPageSize 是未显式指定 page_size 时的分页大小。
const defaultPageSize = 20

// tempPasswordChars 是生成临时密码使用的字符集（满足大小写 + 数字的复杂度要求）。
const tempPasswordChars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// Repository 是 User 持久层的**消费方视图**：
//
// 接口定义在 Service 侧（而非 repository.go），方法签名与 *repository 一一对应。
// 生产注入 GORM 实现，单测注入 fake，两者都只依赖这个窄接口。
type Repository interface {
	Create(ctx context.Context, m *UserPO) error
	Get(ctx context.Context, id string) (*UserPO, error)
	GetByUsername(ctx context.Context, username string) (*UserPO, error)
	List(ctx context.Context, q ListQuery) ([]UserPO, error)
	Update(ctx context.Context, m *UserPO) error
	UpdateLastLogin(ctx context.Context, id string, at time.Time, ip string) error
	SoftDelete(ctx context.Context, id string) (int64, error)
}

// Service 承载 User 的核心业务逻辑。
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

// Login 校验用户名密码并签发令牌。
//
// JWT 签发为留桩实现（access_token / refresh_token 为占位串），接入认证模块时替换本函数即可。
func (s *Service) Login(ctx context.Context, req *userv1.LoginRequest, clientIP string) (*userv1.LoginResponse, error) {
	m, err := s.repo.GetByUsername(ctx, req.GetUsername())
	if err != nil {
		// 用户不存在与密码错误返回同一错误，避免账号枚举。
		if errors.Is(err, ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		s.log.Error("load user for login failed", "error", err)
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(m.PasswordHash), []byte(req.GetPassword())); err != nil {
		return nil, ErrInvalidCredentials
	}

	now := time.Now()
	if err := s.repo.UpdateLastLogin(ctx, m.UserID, now, clientIP); err != nil {
		s.log.Error("update last login failed", "user_id", m.UserID, "error", err)
		return nil, err
	}
	m.LastLoginAt = &now
	if clientIP != "" {
		m.LastLoginIP = proto.String(clientIP)
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

	if err := s.repo.Create(ctx, m); err != nil {
		// 唯一键冲突由 Repository 层翻译成领域错误（GORM/驱动的细节不外泄到业务层）。
		if errors.Is(err, ErrDuplicateUser) {
			return nil, ErrDuplicateUser
		}
		s.log.Error("create user failed", "error", err)
		return nil, err
	}
	return m.ToProto(), nil
}

// GetUser 按 ID 查询用户。
func (s *Service) GetUser(ctx context.Context, id string) (*userv1.User, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Error("get user failed", "user_id", id, "error", err)
		}
		return nil, err
	}
	return m.ToProto(), nil
}

// ListUsers 分页列出用户，返回数据列表与下一页游标（空串表示没有更多数据）。
//
// AIP 三件套：AIP-158 游标分页在 Service 侧完成，
// AIP-160 过滤 / AIP-132 排序由 Repository 经 aipgorm 翻译成 SQL。
func (s *Service) ListUsers(ctx context.Context, req *userv1.ListUsersRequest) ([]*userv1.User, string, error) {
	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}

	pageToken, err := pagination.ParsePageToken(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid page_token", ErrInvalidArgument)
	}

	q := ListQuery{
		Offset:         int(pageToken.Offset),
		Limit:          pageSize + 1,
		Filter:         req.GetFilter(),
		OrderBy:        req.GetOrderBy(),
		Keyword:        req.GetKeyword(),
		IncludeDeleted: req.GetShowDeleted(),
	}
	if req.Status != nil {
		q.Status = proto.Int32(int32(req.GetStatus()))
	}

	rows, err := s.repo.List(ctx, q)
	if err != nil {
		if !errors.Is(err, ErrInvalidArgument) {
			s.log.Error("list users failed", "error", err)
		}
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
	m, err := s.repo.Get(ctx, req.GetUserId())
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Error("load user for update failed", "user_id", req.GetUserId(), "error", err)
		}
		return nil, err
	}

	if err := m.applyUpdate(req); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}
	if err := s.repo.Update(ctx, m); err != nil {
		s.log.Error("update user failed", "user_id", m.UserID, "error", err)
		return nil, err
	}
	return m.ToProto(), nil
}

// DeleteUser 软删除用户。
func (s *Service) DeleteUser(ctx context.Context, id string) error {
	affected, err := s.repo.SoftDelete(ctx, id)
	if err != nil {
		s.log.Error("delete user failed", "user_id", id, "error", err)
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ChangePassword 修改本人密码：校验旧密码 → 新旧不得相同 → 写入新哈希。
func (s *Service) ChangePassword(ctx context.Context, req *userv1.ChangePasswordRequest) (time.Time, error) {
	m, err := s.repo.Get(ctx, req.GetUserId())
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Error("load user for password change failed", "user_id", req.GetUserId(), "error", err)
		}
		return time.Time{}, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(m.PasswordHash), []byte(req.GetOldPassword())); err != nil {
		// 旧密码不匹配：语义上不同于 Login 的凭证错误，用专门的哨兵表达
		// （改造前 server.go 需要一段特例 switch 才能把它映射成 FailedPrecondition）。
		return time.Time{}, ErrPasswordMismatch
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
	if err := s.repo.Update(ctx, m); err != nil {
		s.log.Error("change password failed", "user_id", m.UserID, "error", err)
		return time.Time{}, err
	}
	return now, nil
}

// ResetPassword 管理员重置密码：服务端生成一次性临时密码，仅本次返回明文，不落日志。
func (s *Service) ResetPassword(ctx context.Context, req *userv1.ResetPasswordRequest) (string, error) {
	m, err := s.repo.Get(ctx, req.GetUserId())
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Error("load user for password reset failed", "user_id", req.GetUserId(), "error", err)
		}
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
	if err := s.repo.Update(ctx, m); err != nil {
		s.log.Error("reset password failed", "user_id", m.UserID, "error", err)
		return "", err
	}
	return temp, nil
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
