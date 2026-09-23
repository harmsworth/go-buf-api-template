package todo

import (
	"testing"
	"time"

	todov1 "go-buf-api-template/gen/go/todo/v1"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestTodo_ToProto_MapsNullableColumnsToNil(t *testing.T) {
	m := &Todo{
		ID:        "00000000-0000-0000-0000-000000000001",
		Title:     "t",
		Status:    int32(todov1.TodoStatus_TODO_STATUS_DONE),
		CreatedAt: time.Unix(0, 0).UTC(),
		UpdatedAt: time.Unix(0, 0).UTC(),
		// Description / CompletedAt 为 NULL
	}

	got := m.ToProto()
	if got.Description != nil {
		t.Fatalf("description = %v, want nil (NULL 不应落成空串)", got.GetDescription())
	}
	if got.CompletedAt != nil {
		t.Fatal("completedAt should be nil when column is NULL")
	}
	if got.GetStatus() != todov1.TodoStatus_TODO_STATUS_DONE {
		t.Fatalf("status = %v", got.GetStatus())
	}
}

func TestTodo_ApplyUpdate_ThreeState(t *testing.T) {
	now := time.Unix(0, 0).UTC()

	tests := []struct {
		name       string
		req        *todov1.UpdateTodoRequest
		wantTitle  string
		wantDesc   *string
		wantStatus todov1.TodoStatus
		wantDoneAt bool
	}{
		{
			name:       "absent_keeps_value",
			req:        &todov1.UpdateTodoRequest{Id: "1"},
			wantTitle:  "old",
			wantDesc:   proto.String("desc"),
			wantStatus: todov1.TodoStatus_TODO_STATUS_PENDING,
		},
		{
			name:       "explicit_value_updates",
			req:        &todov1.UpdateTodoRequest{Id: "1", Title: proto.String("new")},
			wantTitle:  "new",
			wantDesc:   proto.String("desc"),
			wantStatus: todov1.TodoStatus_TODO_STATUS_PENDING,
		},
		{
			name:       "explicit_clear_empties",
			req:        &todov1.UpdateTodoRequest{Id: "1", Description: proto.String("")},
			wantTitle:  "old",
			wantDesc:   nil,
			wantStatus: todov1.TodoStatus_TODO_STATUS_PENDING,
		},
		{
			name:       "status_done_sets_completed_at",
			req:        &todov1.UpdateTodoRequest{Id: "1", Status: todov1.TodoStatus_TODO_STATUS_DONE.Enum()},
			wantTitle:  "old",
			wantDesc:   proto.String("desc"),
			wantStatus: todov1.TodoStatus_TODO_STATUS_DONE,
			wantDoneAt: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Todo{
				Title:       "old",
				Description: proto.String("desc"),
				Status:      int32(todov1.TodoStatus_TODO_STATUS_PENDING),
			}
			if err := m.applyUpdate(tt.req, now); err != nil {
				t.Fatalf("applyUpdate: %v", err)
			}
			if m.Title != tt.wantTitle {
				t.Fatalf("title = %q, want %q", m.Title, tt.wantTitle)
			}
			if (m.Description != nil) != (tt.wantDesc != nil) {
				t.Fatalf("description = %v, want %v", m.Description, tt.wantDesc)
			}
			if m.Description != nil && *m.Description != *tt.wantDesc {
				t.Fatalf("description = %q, want %q", *m.Description, *tt.wantDesc)
			}
			if todov1.TodoStatus(m.Status) != tt.wantStatus {
				t.Fatalf("status = %v, want %v", m.Status, tt.wantStatus)
			}
			if (m.CompletedAt != nil) != tt.wantDoneAt {
				t.Fatalf("completedAt set = %v, want %v", m.CompletedAt != nil, tt.wantDoneAt)
			}
		})
	}
}

func TestTodo_ApplyUpdate_MaskRejectsNonUpdatablePath(t *testing.T) {
	m := &Todo{Title: "old"}
	err := m.applyUpdate(&todov1.UpdateTodoRequest{
		Id:         "1",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"password_hash"}},
	}, time.Now())
	if err == nil {
		t.Fatal("applyUpdate should reject unknown/non-updatable mask path")
	}
	if m.Title != "old" {
		t.Fatal("failed validation must not mutate the model")
	}
}
