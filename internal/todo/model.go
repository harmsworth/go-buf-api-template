// Package todo 是待办事项业务模块：GORM PO、业务逻辑、Gin Handler 与路由全部收拢于此。
package todo

import (
	"fmt"
	"time"

	todov1 "go-buf-api-template/gen/go/todo/v1"

	"go.einride.tech/aip/fieldmask"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// Todo 是 todos 表的持久化对象（PO），字段与 db/migrations 中的表结构一一对应。
type Todo struct {
	ID          string         `gorm:"primaryKey;column:id;type:varchar(36)"`
	Title       string         `gorm:"column:title;type:varchar(128);not null"`
	Description *string        `gorm:"column:description;type:varchar(2048)"`
	Status      int32          `gorm:"column:status;type:tinyint;not null;default:1"`
	CreatedAt   time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time      `gorm:"column:updated_at;not null"`
	CompletedAt *time.Time     `gorm:"column:completed_at"`
	DeletedAt   gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

// TableName 指定表名。
func (Todo) TableName() string { return "todos" }

// ToProto 将 PO 转换为传输层 DTO（todo.v1.Todo）。
// 可空列用指针表达：NULL → proto 的 optional 字段保持 nil（不落零值，规避 Zero Value Trap）。
func (m *Todo) ToProto() *todov1.Todo {
	out := &todov1.Todo{
		Id:        m.ID,
		Title:     m.Title,
		Status:    todov1.TodoStatus(m.Status),
		CreatedAt: timestamppb.New(m.CreatedAt),
		UpdatedAt: timestamppb.New(m.UpdatedAt),
	}
	if m.Description != nil {
		out.Description = proto.String(*m.Description)
	}
	if m.CompletedAt != nil {
		out.CompletedAt = timestamppb.New(*m.CompletedAt)
	}
	return out
}

// updatablePaths 是允许出现在 update_mask 中的字段白名单（不含 id / update_mask）。
var updatablePaths = map[string]struct{}{
	"title":       {},
	"description": {},
	"status":      {},
}

// applyUpdate 将请求字段写回 PO，支持两种语义：
//
//  1. 传了 update_mask（AIP-134）：只有 mask 中声明的字段会被写入，其余字段（含零值）一律忽略，
//     从根本上避免"零值覆盖"问题。mask 含 "*" 时表示全量替换。
//  2. 未传 update_mask：PATCH 三态语义 —— Absent（nil）不变、
//     Explicit Value 赋值、Explicit Clear（空串）置空。
func (m *Todo) applyUpdate(req *todov1.UpdateTodoRequest, now time.Time) error {
	mask := req.GetUpdateMask()

	// ---- 模式 1：AIP-134 字段掩码增量更新 ----
	if mask != nil && len(mask.GetPaths()) > 0 && !fieldmask.IsFullReplacement(mask) {
		// 先用 einride 校验路径在 Request 消息上合法（语法 + 字段存在性）。
		if err := fieldmask.Validate(mask, req); err != nil {
			return fmt.Errorf("invalid update_mask: %w", err)
		}
		for _, path := range mask.GetPaths() {
			// 再用业务白名单收敛：id / update_mask 等不可更新字段在此被拒绝。
			if _, ok := updatablePaths[path]; !ok {
				return fmt.Errorf("update_mask path %q is not updatable", path)
			}
			switch path {
			case "title":
				m.Title = req.GetTitle()
			case "description":
				if req.GetDescription() == "" {
					m.Description = nil // mask 显式声明 + 零值 = 清空
				} else {
					m.Description = proto.String(req.GetDescription())
				}
			case "status":
				m.setStatus(req.GetStatus(), now)
			}
		}
		return nil
	}

	// ---- 模式 2：PATCH 三态（向后兼容）----
	if req.Title != nil {
		m.Title = req.GetTitle()
	}
	if req.Description != nil {
		if req.GetDescription() == "" {
			m.Description = nil // 显式清空
		} else {
			m.Description = proto.String(req.GetDescription())
		}
	}
	if req.Status != nil {
		m.setStatus(req.GetStatus(), now)
	}
	return nil
}

// setStatus 更新状态并联动 completed_at：置为 DONE 时记录完成时间，其余状态清空。
func (m *Todo) setStatus(status todov1.TodoStatus, now time.Time) {
	m.Status = int32(status)
	if status == todov1.TodoStatus_TODO_STATUS_DONE {
		if m.CompletedAt == nil {
			m.CompletedAt = &now
		}
		return
	}
	m.CompletedAt = nil
}
