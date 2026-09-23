package aipgorm

import (
	"strings"
	"testing"
	"time"

	todov1 "go-buf-api-template/gen/go/todo/v1"

	"go.einride.tech/aip/filtering"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// testRow 是被查询的假 PO（只为生成 SQL，不落库）。
type testRow struct {
	ID        string
	Title     string
	Status    int32
	CreatedAt time.Time
}

func (testRow) TableName() string { return "todos" }

// testSchema 覆盖 string / enum / timestamp 三种字段声明形态。
var testSchema = Schema{
	"id":         {Column: "id", Type: filtering.TypeString},
	"title":      {Column: "title", Type: filtering.TypeString},
	"status":     {Column: "status", Type: filtering.TypeString, Enum: todov1.TodoStatus(0).Type()},
	"created_at": {Column: "created_at", Type: filtering.TypeTimestamp},
}

var testDecls = MustDeclarations(testSchema)

// fakeRequest 实现 filtering.Request / ordering.Request。
type fakeRequest struct{ filter, orderBy string }

func (r fakeRequest) GetFilter() string  { return r.filter }
func (r fakeRequest) GetOrderBy() string { return r.orderBy }

// newDB 用 mysql 方言构建不连库的 GORM 实例（SkipInitializeWithVersion 跳过版本探测）。
func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "root@tcp(127.0.0.1:3306)/test?parseTime=true&loc=UTC",
		SkipInitializeWithVersion: true,
		DefaultStringSize:         256,
	}), &gorm.Config{
		DryRun: true,
		// GORM 默认在 Open 后自动 Ping、并对写操作开启默认事务，两者都会真的建立连接。
		// DryRun 场景必须同时关掉，否则读操作看似可用、写操作却报 1045。
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
		Logger:                 gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open gorm: %v", err)
	}
	return db
}

// filterSQL 把 ApplyFilter 的结果渲染成 SQL 字符串（含参数插值）。
func filterSQL(t *testing.T, filter string) string {
	t.Helper()
	var err error
	sql := newDB(t).ToSQL(func(tx *gorm.DB) *gorm.DB {
		tx, err = ApplyFilter(tx, fakeRequest{filter: filter}, testDecls, testSchema)
		if err != nil {
			t.Fatalf("ApplyFilter(%q): %v", filter, err)
		}
		return tx.Find(&[]testRow{})
	})
	return sql
}

// TestDeclarations_RejectsUndeclaredField 未在 Schema 声明的字段在类型检查阶段就被拒绝，
// 不会进入 SQL 翻译。
func TestDeclarations_RejectsUndeclaredField(t *testing.T) {
	_, err := ApplyFilter(newDB(t), fakeRequest{filter: `nope = "x"`}, testDecls, testSchema)
	if err == nil {
		t.Fatal("未声明字段必须报错")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("错误信息应指出字段名: %v", err)
	}
}

// TestApplyFilter_StringEquality 字符串等值走参数绑定。
func TestApplyFilter_StringEquality(t *testing.T) {
	sql := filterSQL(t, `title = "buy milk"`)
	if !strings.Contains(sql, "title = 'buy milk'") {
		t.Fatalf("SQL = %s", sql)
	}
}

// TestApplyFilter_HasBecomesLike AIP-160 的 `:` 映射为 LIKE %value%。
func TestApplyFilter_HasBecomesLike(t *testing.T) {
	sql := filterSQL(t, `title:"bug"`)
	if !strings.Contains(sql, "title LIKE '%bug%'") {
		t.Fatalf("SQL = %s", sql)
	}
}

// TestApplyFilter_EnumNameTranslated 枚举名（短名与全限定名）都被翻译成枚举号。
func TestApplyFilter_EnumNameTranslated(t *testing.T) {
	for _, expr := range []string{`status = "DONE"`, `status = "TODO_STATUS_DONE"`} {
		sql := filterSQL(t, expr)
		if !strings.Contains(sql, "status = 3") {
			t.Fatalf("filter %q 未被翻译成枚举号: %s", expr, sql)
		}
	}
}

// TestApplyFilter_UnknownEnumValue 未知枚举名必须报错，而不是落成 0。
func TestApplyFilter_UnknownEnumValue(t *testing.T) {
	_, err := ApplyFilter(newDB(t), fakeRequest{filter: `status = "NOT_A_STATUS"`}, testDecls, testSchema)
	if err == nil {
		t.Fatal("未知枚举名必须报错")
	}
}

// TestApplyFilter_AndOrNotNesting AND / OR / NOT 的括号嵌套必须正确，
// 否则运算符优先级会改变查询语义。
func TestApplyFilter_AndOrNotNesting(t *testing.T) {
	sql := filterSQL(t, `title = "a" AND NOT (status = "DONE" OR title = "b")`)
	if !strings.Contains(sql, "AND") {
		t.Fatalf("缺少 AND: %s", sql)
	}
	if !strings.Contains(sql, "OR") {
		t.Fatalf("缺少 OR: %s", sql)
	}
	// NOT 必须包住整个括号组
	if !strings.Contains(sql, "NOT (") {
		t.Fatalf("NOT 未包裹子表达式: %s", sql)
	}
	if !strings.Contains(sql, "(status = 3 OR title = 'b')") {
		t.Fatalf("OR 组未加括号: %s", sql)
	}
}

// TestApplyFilter_TimestampComparison 时间常量按 TIMESTAMP 处理。
func TestApplyFilter_TimestampComparison(t *testing.T) {
	sql := filterSQL(t, `created_at >= "2026-01-01T00:00:00Z"`)
	if !strings.Contains(sql, "created_at >=") {
		t.Fatalf("SQL = %s", sql)
	}
}

// TestApplyOrderBy_Fallback 未指定 order_by 时使用 fallback。
func TestApplyOrderBy_Fallback(t *testing.T) {
	db := newDB(t)
	got, err := ApplyOrderBy(db, fakeRequest{}, testSchema, "id ASC")
	if err != nil {
		t.Fatalf("ApplyOrderBy: %v", err)
	}
	sql := got.ToSQL(func(tx *gorm.DB) *gorm.DB { return tx.Find(&[]testRow{}) })
	if !strings.Contains(sql, "ORDER BY id ASC") {
		t.Fatalf("SQL = %s", sql)
	}
}

// TestApplyOrderBy_MultipleFieldsAndDirection 多字段与升降序。
func TestApplyOrderBy_MultipleFieldsAndDirection(t *testing.T) {
	db := newDB(t)
	got, err := ApplyOrderBy(db, fakeRequest{orderBy: "created_at desc, title asc"}, testSchema, "id ASC")
	if err != nil {
		t.Fatalf("ApplyOrderBy: %v", err)
	}
	sql := got.ToSQL(func(tx *gorm.DB) *gorm.DB { return tx.Find(&[]testRow{}) })
	if !strings.Contains(sql, "ORDER BY created_at DESC, title ASC") {
		t.Fatalf("SQL = %s", sql)
	}
}

// TestApplyOrderBy_RejectsUnknownField 排序字段走白名单校验。
func TestApplyOrderBy_RejectsUnknownField(t *testing.T) {
	_, err := ApplyOrderBy(newDB(t), fakeRequest{orderBy: "secret_col desc"}, testSchema, "id ASC")
	if err == nil {
		t.Fatal("未声明字段排序必须报错")
	}
}

// TestPaths_Sorted 白名单路径按字典序返回（便于错误信息与断言稳定）。
func TestPaths_Sorted(t *testing.T) {
	got := Paths(testSchema)
	want := []string{"created_at", "id", "status", "title"}
	if len(got) != len(want) {
		t.Fatalf("Paths() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Paths() = %v, want %v", got, want)
		}
	}
}

// TestMustDeclarations_ValidSchema 合法 Schema 返回可用的声明集合（正例）。
//
// MustDeclarations 在 Schema 非法时 panic（包初始化即失败）。这里只锁正例：
// einride 的 DeclareIdent 对字段名的宽松度较高，构造一个稳定失败的 Schema 反而脆弱。
func TestMustDeclarations_ValidSchema(t *testing.T) {
	if testDecls == nil {
		t.Fatal("MustDeclarations 应返回非 nil 声明集合")
	}
}

// TestList_AppliesFilterOrderAndPaging List 骨架把过滤 / 排序 / 分页串成一条链。
func TestList_AppliesFilterOrderAndPaging(t *testing.T) {
	sql := newDB(t).ToSQL(func(tx *gorm.DB) *gorm.DB {
		tx, err := ApplyFilter(tx.Model(&testRow{}), fakeRequest{filter: `title = "x"`}, testDecls, testSchema)
		if err != nil {
			t.Fatalf("ApplyFilter: %v", err)
		}
		if tx, err = ApplyOrderBy(tx, fakeRequest{}, testSchema, "id ASC"); err != nil {
			t.Fatalf("ApplyOrderBy: %v", err)
		}
		return tx.Offset(20).Limit(10).Find(&[]testRow{})
	})
	for _, want := range []string{"title = 'x'", "ORDER BY id ASC", "LIMIT 10", "OFFSET 20"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL 缺少 %q: %s", want, sql)
		}
	}
}
