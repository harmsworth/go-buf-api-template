package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	todov1 "go-buf-api-template/gen/go/todo/v1"
	userv1 "go-buf-api-template/gen/go/user/v1"
	"go-buf-api-template/internal/platform/httpx"

	"github.com/gin-gonic/gin"
)

// newQueryContext 构造一个只带 query string 的 Gin 上下文。
func newQueryContext(t *testing.T, rawQuery string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/todos?"+rawQuery, nil)
	return c, w
}

func TestBindQuery_BindsAIPFields(t *testing.T) {
	req := &todov1.ListTodosRequest{}
	c, _ := newQueryContext(t,
		`page_size=2&page_token=abc&status=TODO_STATUS_DONE&filter=title%20%3A%20%22bug%22&order_by=created_at%20desc`)

	if !httpx.BindQuery(c, req) {
		t.Fatal("BindQuery returned false for a valid query")
	}
	if req.GetPageSize() != 2 {
		t.Fatalf("page_size = %d, want 2", req.GetPageSize())
	}
	if req.GetPageToken() != "abc" {
		t.Fatalf("page_token = %q", req.GetPageToken())
	}
	// 枚举可用「枚举名」——这是改造前 Gin 侧做不到的（当时只认数字）。
	// optional 枚举生成的是指针字段，用 nil 判存在（本仓库生成代码不产出 Has* 方法）。
	if req.Status == nil || req.GetStatus() != todov1.TodoStatus_TODO_STATUS_DONE {
		t.Fatalf("status = %v (nil=%v), want TODO_STATUS_DONE", req.GetStatus(), req.Status == nil)
	}
	if req.GetFilter() != `title : "bug"` {
		t.Fatalf("filter = %q", req.GetFilter())
	}
	if req.GetOrderBy() != "created_at desc" {
		t.Fatalf("order_by = %q", req.GetOrderBy())
	}
}

func TestBindQuery_AcceptsEnumNumberAndCamelCaseName(t *testing.T) {
	t.Run("enum_number", func(t *testing.T) {
		req := &todov1.ListTodosRequest{}
		c, _ := newQueryContext(t, "status=3")
		if !httpx.BindQuery(c, req) {
			t.Fatal("BindQuery rejected an enum number")
		}
		if req.GetStatus() != todov1.TodoStatus_TODO_STATUS_DONE {
			t.Fatalf("status = %v, want TODO_STATUS_DONE(3)", req.GetStatus())
		}
	})

	t.Run("camel_case_field_name", func(t *testing.T) {
		req := &todov1.ListTodosRequest{}
		c, _ := newQueryContext(t, "pageSize=5&orderBy=title%20asc")
		if !httpx.BindQuery(c, req) {
			t.Fatal("BindQuery rejected lowerCamel field names")
		}
		if req.GetPageSize() != 5 || req.GetOrderBy() != "title asc" {
			t.Fatalf("pageSize/orderBy = %d/%q", req.GetPageSize(), req.GetOrderBy())
		}
	})
}

func TestBindQuery_BindsUserRequestIncludingOptionalBool(t *testing.T) {
	req := &userv1.ListUsersRequest{}
	c, _ := newQueryContext(t, "page_size=10&status=USER_STATUS_ACTIVE&keyword=ali&show_deleted=true")

	if !httpx.BindQuery(c, req) {
		t.Fatal("BindQuery returned false for a valid query")
	}
	if req.GetStatus() != userv1.UserStatus_USER_STATUS_ACTIVE {
		t.Fatalf("status = %v", req.GetStatus())
	}
	if req.GetKeyword() != "ali" {
		t.Fatalf("keyword = %q", req.GetKeyword())
	}
	if req.ShowDeleted == nil || !req.GetShowDeleted() {
		t.Fatalf("show_deleted = %v (nil=%v), want true", req.GetShowDeleted(), req.ShowDeleted == nil)
	}
}

// 未知参数被静默忽略（与 gateway 入口同源 —— 二者共用同一个解析器），
// 这里固化该行为，避免后续误以为"未知参数会被拒绝"。
func TestBindQuery_IgnoresUnknownParameter(t *testing.T) {
	req := &todov1.ListTodosRequest{}
	c, w := newQueryContext(t, "page_size=2&not_a_field=x")

	if !httpx.BindQuery(c, req) {
		t.Fatalf("unknown query parameters should be ignored, got status %d", w.Code)
	}
	if req.GetPageSize() != 2 {
		t.Fatalf("page_size = %d, want 2 (已知字段仍应绑定)", req.GetPageSize())
	}
}

func TestBindQuery_RejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		rawQuery string
	}{
		{"non_integer_page_size", "page_size=abc"},
		{"unknown_enum_name", "status=NOT_A_STATUS"},
		{"invalid_bool", "show_deleted=maybe"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &userv1.ListUsersRequest{}
			c, w := newQueryContext(t, tt.rawQuery)
			if httpx.BindQuery(c, req) {
				t.Fatalf("BindQuery should reject %q", tt.rawQuery)
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
		})
	}
}
