package todo

import (
	"context"
	"errors"
	"sort"
	"testing"

	todov1 "go-buf-api-template/gen/go/todo/v1"
	"go-buf-api-template/internal/platform/logger"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// fakeRepo 是 Repository 的测试替身：不接触数据库，只验证 Service 的行为与传参。
type fakeRepo struct {
	todos     map[string]*Todo
	createErr error
	listErr   error
	// lastQuery 记录最近一次 List 调用的参数，用于断言分页/过滤/排序是否透传。
	lastQuery ListQuery
}

func newFakeRepo(todos ...*Todo) *fakeRepo {
	f := &fakeRepo{todos: map[string]*Todo{}}
	for _, m := range todos {
		f.todos[m.ID] = m
	}
	return f
}

func (f *fakeRepo) Create(_ context.Context, m *Todo) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.todos[m.ID] = m
	return nil
}

func (f *fakeRepo) Get(_ context.Context, id string) (*Todo, error) {
	m, ok := f.todos[id]
	if !ok {
		return nil, ErrNotFound
	}
	return m, nil
}

func (f *fakeRepo) List(_ context.Context, q ListQuery) ([]Todo, error) {
	f.lastQuery = q
	if f.listErr != nil {
		return nil, f.listErr
	}
	rows := make([]Todo, 0, len(f.todos))
	for _, m := range f.todos {
		rows = append(rows, *m)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, nil
}

func (f *fakeRepo) Update(_ context.Context, m *Todo) error {
	f.todos[m.ID] = m
	return nil
}

func (f *fakeRepo) SoftDelete(_ context.Context, id string) (int64, error) {
	if _, ok := f.todos[id]; !ok {
		return 0, nil
	}
	delete(f.todos, id)
	return 1, nil
}

func TestService_Create(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, logger.Nop())

	got, err := svc.Create(context.Background(), &todov1.CreateTodoRequest{
		Title:       "buy milk",
		Description: proto.String("2 bottles"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.GetId() == "" {
		t.Fatal("Create should generate a server-side ID")
	}
	if got.GetStatus() != todov1.TodoStatus_TODO_STATUS_PENDING {
		t.Fatalf("status = %v, want PENDING", got.GetStatus())
	}
	if got.GetDescription() != "2 bottles" {
		t.Fatalf("description = %q", got.GetDescription())
	}
}

func TestService_Get_NotFound(t *testing.T) {
	svc := NewService(newFakeRepo(), logger.Nop())
	_, err := svc.Get(context.Background(), "6f9619ff-8b86-d011-b42d-00c04fc964ff")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestService_List_PassesPaginationAndHasNextPage(t *testing.T) {
	repo := newFakeRepo(
		&Todo{ID: "00000000-0000-0000-0000-000000000001", Title: "a"},
		&Todo{ID: "00000000-0000-0000-0000-000000000002", Title: "b"},
		&Todo{ID: "00000000-0000-0000-0000-000000000003", Title: "c"},
	)
	svc := NewService(repo, logger.Nop())

	got, next, err := svc.List(context.Background(), &todov1.ListTodosRequest{
		PageSize: 2,
		Filter:   `status = "PENDING"`,
		OrderBy:  "created_at desc",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if next == "" {
		t.Fatal("next page token should not be empty when more rows exist")
	}
	// 分页参数与 AIP 表达式应原样透传给 Repository。
	if repo.lastQuery.Offset != 0 || repo.lastQuery.Limit != 3 {
		t.Fatalf("offset/limit = %d/%d, want 0/3 (pageSize+1)", repo.lastQuery.Offset, repo.lastQuery.Limit)
	}
	if repo.lastQuery.Filter != `status = "PENDING"` || repo.lastQuery.OrderBy != "created_at desc" {
		t.Fatalf("filter/order_by not passed through: %+v", repo.lastQuery)
	}
}

func TestService_Update_ThreeState(t *testing.T) {
	existing := &Todo{
		ID:          "00000000-0000-0000-0000-000000000001",
		Title:       "old title",
		Description: proto.String("old desc"),
		Status:      int32(todov1.TodoStatus_TODO_STATUS_PENDING),
	}
	svc := NewService(newFakeRepo(existing), logger.Nop())

	// 未传字段 → 保持不变；传值 → 更新
	got, err := svc.Update(context.Background(), &todov1.UpdateTodoRequest{
		Id:    existing.ID,
		Title: proto.String("new title"),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.GetTitle() != "new title" || got.GetDescription() != "old desc" {
		t.Fatalf("title/description = %q/%q", got.GetTitle(), got.GetDescription())
	}

	// 传空串 → 显式清空
	got, err = svc.Update(context.Background(), &todov1.UpdateTodoRequest{
		Id:          existing.ID,
		Description: proto.String(""),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Description != nil {
		t.Fatalf("description = %v, want cleared", got.GetDescription())
	}
}

func TestService_Update_WithMaskOnlyUpdatesMaskedFields(t *testing.T) {
	existing := &Todo{
		ID:          "00000000-0000-0000-0000-000000000001",
		Title:       "old title",
		Description: proto.String("old desc"),
	}
	svc := NewService(newFakeRepo(existing), logger.Nop())

	got, err := svc.Update(context.Background(), &todov1.UpdateTodoRequest{
		Id:          existing.ID,
		Title:       proto.String("new title"),
		Description: proto.String("SHOULD BE IGNORED"),
		UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{"title"}},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.GetTitle() != "new title" {
		t.Fatalf("title = %q, want updated", got.GetTitle())
	}
	if got.GetDescription() != "old desc" {
		t.Fatalf("description = %q, want untouched (not in mask)", got.GetDescription())
	}
}

func TestService_Update_RejectsNonUpdatableMaskPath(t *testing.T) {
	existing := &Todo{ID: "00000000-0000-0000-0000-000000000001", Title: "t"}
	svc := NewService(newFakeRepo(existing), logger.Nop())

	_, err := svc.Update(context.Background(), &todov1.UpdateTodoRequest{
		Id:         existing.ID,
		Title:      proto.String("x"),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"id"}},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	svc := NewService(newFakeRepo(), logger.Nop())
	if err := svc.Delete(context.Background(), "00000000-0000-0000-0000-000000000009"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
