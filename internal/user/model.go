// Package user 是用户业务模块：GORM PO、业务逻辑、Gin Handler / gRPC Server 与路由全部收拢于此。
package user

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	userv1 "go-buf-api-template/gen/go/user/v1"

	"go.einride.tech/aip/fieldmask"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// RoleList 是 user.v1.UserRole 列表，以 JSON 数组形式落库（roles VARCHAR）。
// 实现 driver.Valuer / sql.Scanner，让 GORM 自动完成编解码。
type RoleList []userv1.UserRole

// Value 将角色列表序列化为 JSON 数组（枚举按整型存储）。
func (r RoleList) Value() (driver.Value, error) {
	if r == nil {
		return "[]", nil
	}
	nums := make([]int32, 0, len(r))
	for _, role := range r {
		nums = append(nums, int32(role))
	}
	data, err := json.Marshal(nums)
	if err != nil {
		return nil, fmt.Errorf("marshal roles: %w", err)
	}
	return string(data), nil
}

// Scan 将数据库中的 JSON 数组反序列化为角色列表。
func (r *RoleList) Scan(src any) error {
	if src == nil {
		*r = nil
		return nil
	}

	var raw []byte
	switch v := src.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		return fmt.Errorf("unsupported roles column type %T", src)
	}

	if len(raw) == 0 {
		*r = nil
		return nil
	}

	var nums []int32
	if err := json.Unmarshal(raw, &nums); err != nil {
		return fmt.Errorf("unmarshal roles: %w", err)
	}
	roles := make(RoleList, 0, len(nums))
	for _, n := range nums {
		roles = append(roles, userv1.UserRole(n))
	}
	*r = roles
	return nil
}

// UserPO 是 users 表的持久化对象（PO），字段与 db/migrations/000002 一一对应。
type UserPO struct {
	UserID       string         `gorm:"primaryKey;column:user_id;type:varchar(36)"`
	Username     string         `gorm:"column:username;type:varchar(32);not null;uniqueIndex:uk_username"`
	Email        string         `gorm:"column:email;type:varchar(254);not null;uniqueIndex:uk_email"`
	Nickname     *string        `gorm:"column:nickname;type:varchar(32)"`
	Status       int32          `gorm:"column:status;type:tinyint;not null;default:1"`
	Roles        RoleList       `gorm:"column:roles;type:varchar(255);not null;default:'[]'"`
	Phone        *string        `gorm:"column:phone;type:varchar(20)"`
	AvatarURL    *string        `gorm:"column:avatar_url;type:varchar(2048)"`
	PasswordHash string         `gorm:"column:password_hash;type:varchar(72);not null;default:''"`
	LastLoginAt  *time.Time     `gorm:"column:last_login_at"`
	LastLoginIP  *string        `gorm:"column:last_login_ip;type:varchar(45)"`
	CreatedAt    time.Time      `gorm:"column:created_at;not null;autoCreateTime"`
	UpdatedAt    time.Time      `gorm:"column:updated_at;not null;autoUpdateTime"`
	DeletedAt    gorm.DeletedAt `gorm:"column:deleted_at;index:idx_users_deleted_at"`
}

// TableName 指定表名。
func (UserPO) TableName() string { return "users" }

// ToProto 将 PO 转换为传输层 DTO（user.v1.User）。
//
// 安全约束：password_hash 是服务端独占字段，此处绝不映射，任何情况下都不下发。
// 可空列用指针表达：NULL → proto 的 optional 字段保持 nil（不落零值，规避 Zero Value Trap）。
func (m *UserPO) ToProto() *userv1.User {
	out := &userv1.User{
		UserId:    m.UserID,
		Username:  m.Username,
		Email:     m.Email,
		Status:    userv1.UserStatus(m.Status).Enum(),
		Roles:     m.Roles,
		CreatedAt: timestamppb.New(m.CreatedAt),
		UpdatedAt: timestamppb.New(m.UpdatedAt),
	}
	if m.Nickname != nil {
		out.Nickname = proto.String(*m.Nickname)
	}
	if m.Phone != nil {
		out.Phone = proto.String(*m.Phone)
	}
	if m.AvatarURL != nil {
		out.AvatarUrl = proto.String(*m.AvatarURL)
	}
	if m.LastLoginAt != nil {
		out.LastLoginAt = timestamppb.New(*m.LastLoginAt)
	}
	if m.LastLoginIP != nil {
		out.LastLoginIp = proto.String(*m.LastLoginIP)
	}
	if m.DeletedAt.Valid {
		out.DeletedAt = timestamppb.New(m.DeletedAt.Time)
	}
	return out
}

// NewFromCreateRequest 由创建请求构造 PO；passwordHash 为调用方已加密的 bcrypt 摘要。
func NewFromCreateRequest(req *userv1.CreateUserRequest, passwordHash string) *UserPO {
	m := &UserPO{
		UserID:       "", // 由 Service 填充 UUID
		Username:     req.GetUsername(),
		Email:        req.GetEmail(),
		Status:       int32(userv1.UserStatus_USER_STATUS_ACTIVE),
		Roles:        RoleList{req.GetRole()},
		PasswordHash: passwordHash,
	}
	if req.Nickname != nil && req.GetNickname() != "" {
		m.Nickname = proto.String(req.GetNickname())
	}
	if req.Phone != nil && req.GetPhone() != "" {
		m.Phone = proto.String(req.GetPhone())
	}
	return m
}

// updatablePaths 是允许出现在 update_mask 中的字段白名单（不含 user_id / update_mask）。
var updatablePaths = map[string]struct{}{
	"nickname":   {},
	"phone":      {},
	"avatar_url": {},
	"status":     {},
}

// applyUpdate 将请求字段写回 PO，支持两种语义：
//
//  1. 传了 update_mask（AIP-134）：只有 mask 中声明的字段会被写入，其余字段（含零值）一律忽略，
//     从根本上避免"零值覆盖"问题。mask 含 "*" 时表示全量替换。
//  2. 未传 update_mask：PATCH 三态语义 —— Absent（nil）不变、
//     Explicit Value 赋值、Explicit Clear（空串）置空。
func (m *UserPO) applyUpdate(req *userv1.UpdateUserRequest) error {
	mask := req.GetUpdateMask()

	// ---- 模式 1：AIP-134 字段掩码增量更新 ----
	if mask != nil && len(mask.GetPaths()) > 0 && !fieldmask.IsFullReplacement(mask) {
		// 先用 einride 校验路径在 Request 消息上合法（语法 + 字段存在性）。
		if err := fieldmask.Validate(mask, req); err != nil {
			return fmt.Errorf("invalid update_mask: %w", err)
		}
		for _, path := range mask.GetPaths() {
			// 再用业务白名单收敛：user_id / update_mask 等不可更新字段在此被拒绝。
			if _, ok := updatablePaths[path]; !ok {
				return fmt.Errorf("update_mask path %q is not updatable", path)
			}
			switch path {
			case "nickname":
				m.Nickname = nilOrValue(req.GetNickname())
			case "phone":
				m.Phone = nilOrValue(req.GetPhone())
			case "avatar_url":
				m.AvatarURL = nilOrValue(req.GetAvatarUrl())
			case "status":
				m.Status = int32(req.GetStatus())
			}
		}
		return nil
	}

	// ---- 模式 2：PATCH 三态（向后兼容）----
	if req.Nickname != nil {
		m.Nickname = nilOrValue(req.GetNickname())
	}
	if req.Phone != nil {
		m.Phone = nilOrValue(req.GetPhone())
	}
	if req.AvatarUrl != nil {
		m.AvatarURL = nilOrValue(req.GetAvatarUrl())
	}
	// 枚举无法表达"清空"，仅两态：nil = 不变，显式值 = 变更。
	if req.Status != nil {
		m.Status = int32(req.GetStatus())
	}
	return nil
}

// nilOrValue 把"空串"映射为 NULL（显式清空），非空映射为值。
func nilOrValue(s string) *string {
	if s == "" {
		return nil
	}
	return proto.String(s)
}
