package user

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// sqlRecorder 是只记录最近一条 SQL 的 gorm logger，供 DryRun 场景断言。
type sqlRecorder struct {
	gormlogger.Interface
	sql string
}

func (r *sqlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	r.sql, _ = fc()
}

// newDryRunRepo 构造一个不连真库的 repository：SkipInitializeWithVersion 跳过版本探测，
// DryRun 只构建 SQL 不执行，自定义 logger 捕获插值后的 SQL。
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

const testUserID = "0193f0e2-0000-7000-8000-000000000002"

// TestRepository_Get_SelectsByIDWithSoftDeleteFilter 读路径 SQL：按主键 + 软删除过滤。
//
// 注意：DryRun 下 First 在零行时返回 error == nil，因此"驱动 ErrRecordNotFound →
// ErrNotFound"的翻译不在此覆盖（需真实 MySQL 或 sqlmock）。
func TestRepository_Get_SelectsByIDWithSoftDeleteFilter(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	got, err := repo.Get(context.Background(), testUserID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.UserID != "" {
		t.Fatalf("DryRun 不应返回数据: %+v", got)
	}
	if !strings.Contains(rec.sql, "`deleted_at` IS NULL") {
		t.Fatalf("SQL 缺少软删除条件: %s", rec.sql)
	}
}

// TestRepository_GetByUsername 按登录名查询（Login 路径）。
func TestRepository_GetByUsername(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	if _, err := repo.GetByUsername(context.Background(), "alice"); err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if !strings.Contains(rec.sql, "username = 'alice'") {
		t.Fatalf("SQL 缺少 username 条件: %s", rec.sql)
	}
}

// TestRepository_List_IncludeDeletedTogglesUnscoped 软删除可见性由 IncludeDeleted 控制。
func TestRepository_List_IncludeDeletedTogglesUnscoped(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	if _, err := repo.List(context.Background(), ListQuery{Limit: 10}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(rec.sql, "`deleted_at` IS NULL") {
		t.Fatalf("默认应过滤软删数据: %s", rec.sql)
	}

	if _, err := repo.List(context.Background(), ListQuery{Limit: 10, IncludeDeleted: true}); err != nil {
		t.Fatalf("List(Unscoped): %v", err)
	}
	if strings.Contains(rec.sql, "IS NULL") {
		t.Fatalf("IncludeDeleted=true 时不应带软删条件: %s", rec.sql)
	}
}

// TestRepository_List_KeywordIsParenthesized keyword 的 OR 条件必须带括号：
// 否则 AND 优先级会让 AIP 过滤条件只作用于其中一个分支。
func TestRepository_List_KeywordIsParenthesized(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	_, err := repo.List(context.Background(), ListQuery{Limit: 10, Keyword: "ali", Filter: `status = "ACTIVE"`})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(rec.sql, "(username LIKE '%ali%' OR nickname LIKE '%ali%')") {
		t.Fatalf("keyword 的 OR 条件必须带括号: %s", rec.sql)
	}
	if !strings.Contains(rec.sql, "status = 1") {
		t.Fatalf("枚举未被翻译成枚举号: %s", rec.sql)
	}
}

// TestRepository_List_LegacyStatusAndOrderBy 旧字段 status + AIP 排序。
func TestRepository_List_LegacyStatusAndOrderBy(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	status := int32(2)
	_, err := repo.List(context.Background(), ListQuery{
		Limit: 5, Offset: 10, Status: &status, OrderBy: "created_at desc",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, want := range []string{"status = 2", "ORDER BY created_at DESC", "LIMIT 5", "OFFSET 10"} {
		if !strings.Contains(rec.sql, want) {
			t.Fatalf("SQL 缺少 %q: %s", want, rec.sql)
		}
	}
}

// TestRepository_List_RejectsUnknownFilterField 未声明字段被拒绝。
func TestRepository_List_RejectsUnknownFilterField(t *testing.T) {
	repo, _ := newDryRunRepo(t)

	_, err := repo.List(context.Background(), ListQuery{Limit: 10, Filter: `password_hash = "x"`})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

// TestRepository_UpdateLastLogin 登录成功路径的时间/IP 回写。
func TestRepository_UpdateLastLogin(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	if err := repo.UpdateLastLogin(context.Background(), testUserID, time.Now(), "10.0.0.1"); err != nil {
		t.Fatalf("UpdateLastLogin: %v", err)
	}
	if !strings.Contains(rec.sql, "UPDATE") || !strings.Contains(rec.sql, "last_login_at") {
		t.Fatalf("SQL 不是预期的 UPDATE: %s", rec.sql)
	}
}

// TestIsDuplicateKey MySQL 1062 与 GORM ErrDuplicatedKey 都被识别为唯一键冲突，
// 其余错误不得误判（否则会把普通失败当成 409 返回给调用方）。
//
// 这是 repository.Create 内部使用的判定函数，冲突 → ErrDuplicateUser 的翻译由它保证；
// DryRun 无法产生真实唯一键冲突，因此直接对它做表驱动断言。
func TestIsDuplicateKey(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"mysql_1062", &sqlmysql.MySQLError{Number: 1062, Message: "Duplicate entry"}, true},
		{"gorm_duplicated_key", gorm.ErrDuplicatedKey, true},
		{"mysql_other", &sqlmysql.MySQLError{Number: 1146, Message: "table doesn't exist"}, false},
		{"plain_error", errors.New("connection reset"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDuplicateKey(tc.err); got != tc.want {
				t.Fatalf("isDuplicateKey(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestRepository_Create_InsertsRow Create 走 INSERT（DryRun 下不落库）。
func TestRepository_Create_InsertsRow(t *testing.T) {
	repo, rec := newDryRunRepo(t)

	m := &UserPO{UserID: testUserID, Username: "alice", Email: "alice@example.com", PasswordHash: "x"}
	if err := repo.Create(context.Background(), m); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(rec.sql, "INSERT INTO `users`") {
		t.Fatalf("SQL 不是 INSERT: %s", rec.sql)
	}
}
