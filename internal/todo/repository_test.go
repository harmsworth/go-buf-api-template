package todo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-buf-api-template/internal/platform/aipgorm"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// 编译期断言：ListQuery 满足 aipgorm 的查询契约。
var _ aipgorm.ListQuery = ListQuery{}

// sqlRecorder 是只记录最近一条 SQL 的 gorm logger，供 DryRun 场景断言。
//
// GORM 在 DryRun 下仍会走完 callbacks 并调用 Logger.Trace，
// 其 fc() 内部用 Dialector.Explain 做参数插值，因此这里拿到的是"可读且带参数"的 SQL。
type sqlRecorder struct {
	gormlogger.Interface
	sql string
}

func (r *sqlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	r.sql, _ = fc()
}

// newDryRunRepo 构造一个**不连真库**的 repository：
//   - mysql 驱动的 SkipInitializeWithVersion 跳过版本探测，因此 gorm.Open 不会建立连接；
//   - DryRun 让 GORM 只构建 SQL 而不执行；
//   - 自定义 logger 捕获插值后的 SQL 与参数。
//
// 这样可以在零外部依赖（无需 Docker / MySQL）的前提下，把仓储层的 SQL 语义钉住。
func newDryRunRepo(t *testing.T) (*repository, *sqlRecorder) {
	t.Helper()
	rec := &sqlRecorder{Interface: gormlogger.Default.LogMode(gormlogger.Silent)}
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
		Logger:                 rec,
	})
	if err != nil {
		t.Fatalf("open dry-run gorm: %v", err)
	}
	return &repository{db: db}, rec
}

const testID = "0193f0e2-0000-7000-8000-000000000001"

// TestRepository_Get_SelectsByIDWithSoftDeleteFilter 锁定读路径的 SQL 形态：
// 按主键查询 + 自动带软删除过滤。
//
// 注意：DryRun 下 GORM 的 First 在零行时返回 error == nil（不产生 ErrRecordNotFound），
// 因此"驱动错误 → ErrNotFound"的翻译无法用 DryRun 覆盖，需要真实 MySQL 或 sqlmock。
// 这是 DryRun 方案的已知边界，已在 RULE.mdc 中记录。
func TestRepository_Get_SelectsByIDWithSoftDeleteFilter(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	got, err := repo.Get(context.Background(), testID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.ID != "" {
		t.Fatalf("DryRun 不应返回数据: %+v", got)
	}
	if !strings.Contains(rec.sql, "`deleted_at` IS NULL") {
		t.Fatalf("SQL 缺少软删除条件: %s", rec.sql)
	}
	if !strings.Contains(rec.sql, "id = '"+testID+"'") {
		t.Fatalf("SQL 缺少主键条件: %s", rec.sql)
	}
}

// TestRepository_List_DefaultOrderAndPaging 无 filter/order_by 时使用 fallback 排序与 offset/limit。
func TestRepository_List_DefaultOrderAndPaging(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	rows, err := repo.List(context.Background(), ListQuery{Offset: 40, Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("DryRun 不应返回数据，got %d 行", len(rows))
	}
	for _, want := range []string{"ORDER BY id ASC", "LIMIT 20", "OFFSET 40", "`deleted_at` IS NULL"} {
		if !strings.Contains(rec.sql, want) {
			t.Fatalf("SQL 缺少 %q: %s", want, rec.sql)
		}
	}
}

// TestRepository_List_LegacyStatusFilter 旧字段 status 与 AIP 过滤按 AND 叠加。
func TestRepository_List_LegacyStatusFilter(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	status := int32(1)
	if _, err := repo.List(context.Background(), ListQuery{Limit: 10, Status: &status}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(rec.sql, "status = 1") {
		t.Fatalf("SQL 缺少 status 条件: %s", rec.sql)
	}
}

// TestRepository_List_AIPFilterAndOrder AIP-160 过滤与 AIP-132 排序被翻译进 SQL。
func TestRepository_List_AIPFilterAndOrder(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	_, err := repo.List(context.Background(), ListQuery{
		Limit:   10,
		Filter:  `title:"bug"`,
		OrderBy: "created_at desc",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// `:` 是 AIP-160 的子串匹配，映射为 LIKE %value%
	if !strings.Contains(rec.sql, "title LIKE '%bug%'") {
		t.Fatalf("SQL 缺少 LIKE 子串匹配: %s", rec.sql)
	}
	if !strings.Contains(rec.sql, "ORDER BY created_at DESC") {
		t.Fatalf("SQL 缺少排序: %s", rec.sql)
	}
}

// TestRepository_List_EnumFilterTranslatedToNumber 枚举名按契约翻译成枚举号再与整型列比较。
func TestRepository_List_EnumFilterTranslatedToNumber(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	if _, err := repo.List(context.Background(), ListQuery{Limit: 10, Filter: `status = "DONE"`}); err != nil {
		t.Fatalf("List: %v", err)
	}
	// TODO_STATUS_DONE = 3（见 api/todo/v1/todo.proto）
	if !strings.Contains(rec.sql, "status = 3") {
		t.Fatalf("枚举未被翻译成枚举号: %s", rec.sql)
	}
}

// TestRepository_List_RejectsUnknownFilterField 未在 querySchema 声明的字段被拒绝（400 而非 500）。
func TestRepository_List_RejectsUnknownFilterField(t *testing.T) {
	repo, _ := newDryRunRepo(t)

	_, err := repo.List(context.Background(), ListQuery{Limit: 10, Filter: `unknown_field = "x"`})
	if err == nil {
		t.Fatal("未声明的过滤字段必须报错")
	}
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

// TestRepository_List_RejectsUnknownOrderByField 排序字段同样走白名单。
func TestRepository_List_RejectsUnknownOrderByField(t *testing.T) {
	repo, _ := newDryRunRepo(t)

	_, err := repo.List(context.Background(), ListQuery{Limit: 10, OrderBy: "unknown_field desc"})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

// TestRepository_SoftDelete_UsesUpdate 软删除是 UPDATE ... SET deleted_at，而非物理 DELETE。
func TestRepository_SoftDelete_UsesUpdate(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	if _, err := repo.SoftDelete(context.Background(), testID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if !strings.Contains(rec.sql, "UPDATE") || !strings.Contains(rec.sql, "deleted_at") {
		t.Fatalf("软删除必须是 UPDATE deleted_at: %s", rec.sql)
	}
	if strings.Contains(rec.sql, "DELETE FROM") {
		t.Fatalf("软删除不应生成物理 DELETE: %s", rec.sql)
	}
}

// TestRepository_Create_InsertsRow Create 走 INSERT。
func TestRepository_Create_InsertsRow(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	m := &Todo{ID: testID, Title: "buy milk", Status: 1}
	if err := repo.Create(context.Background(), m); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(rec.sql, "INSERT INTO `todos`") {
		t.Fatalf("SQL 不是 INSERT: %s", rec.sql)
	}
}
