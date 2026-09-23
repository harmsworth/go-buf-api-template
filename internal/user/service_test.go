package user

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	userv1 "go-buf-api-template/gen/go/user/v1"
	"go-buf-api-template/internal/platform/logger"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// fakeRepo 是 Repository 的测试替身：不接触数据库，只验证 Service 行为与传参。
type fakeRepo struct {
	users     map[string]*UserPO
	createErr error
	lastQuery ListQuery
	// lastLogin 记录 Login 成功时写入的最后登录信息。
	lastLogin struct {
		id string
		at time.Time
		ip string
	}
}

func newFakeRepo(users ...*UserPO) *fakeRepo {
	f := &fakeRepo{users: map[string]*UserPO{}}
	for _, u := range users {
		f.users[u.UserID] = u
	}
	return f
}

func (f *fakeRepo) Create(_ context.Context, m *UserPO) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.users[m.UserID] = m
	return nil
}

func (f *fakeRepo) Get(_ context.Context, id string) (*UserPO, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	return u, nil
}

func (f *fakeRepo) GetByUsername(_ context.Context, username string) (*UserPO, error) {
	for _, u := range f.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, ErrNotFound
}

func (f *fakeRepo) List(_ context.Context, q ListQuery) ([]UserPO, error) {
	f.lastQuery = q
	rows := make([]UserPO, 0, len(f.users))
	for _, u := range f.users {
		rows = append(rows, *u)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UserID < rows[j].UserID })
	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	return rows, nil
}

func (f *fakeRepo) Update(_ context.Context, m *UserPO) error {
	f.users[m.UserID] = m
	return nil
}

func (f *fakeRepo) UpdateLastLogin(_ context.Context, id string, at time.Time, ip string) error {
	f.lastLogin.id, f.lastLogin.at, f.lastLogin.ip = id, at, ip
	return nil
}

func (f *fakeRepo) SoftDelete(_ context.Context, id string) (int64, error) {
	if _, ok := f.users[id]; !ok {
		return 0, nil
	}
	delete(f.users, id)
	return 1, nil
}

// newUserWithPassword 构造一个密码已用 bcrypt 加密的用户（测试用 MinCost 提速）。
func newUserWithPassword(t *testing.T, id, username, plain string) *UserPO {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return &UserPO{
		UserID:       id,
		Username:     username,
		Email:        username + "@example.com",
		Nickname:     proto.String("nick"),
		Status:       int32(userv1.UserStatus_USER_STATUS_ACTIVE),
		Roles:        RoleList{userv1.UserRole_USER_ROLE_EDITOR},
		PasswordHash: string(hash),
		CreatedAt:    time.Unix(0, 0).UTC(),
		UpdatedAt:    time.Unix(0, 0).UTC(),
	}
}

func TestService_CreateUser_HashesPassword(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, logger.Nop())

	got, err := svc.CreateUser(context.Background(), &userv1.CreateUserRequest{
		Username: "alice",
		Email:    "alice@example.com",
		Password: "Passw0rd1",
		Role:     userv1.UserRole_USER_ROLE_ADMIN,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if got.GetUserId() == "" {
		t.Fatal("CreateUser should generate a server-side ID")
	}
	// 安全：响应绝不携带密码哈希。
	if got.PasswordHash != "" {
		t.Fatalf("PasswordHash leaked in response: %q", got.PasswordHash)
	}

	stored := repo.users[got.GetUserId()]
	if stored.PasswordHash == "Passw0rd1" {
		t.Fatal("password must be stored as a bcrypt hash, not plaintext")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte("Passw0rd1")); err != nil {
		t.Fatalf("stored hash does not match password: %v", err)
	}
	if got.GetStatus() != userv1.UserStatus_USER_STATUS_ACTIVE {
		t.Fatalf("status = %v, want ACTIVE", got.GetStatus())
	}
}

func TestService_Login(t *testing.T) {
	const uid = "00000000-0000-0000-0000-000000000001"

	t.Run("success_records_last_login", func(t *testing.T) {
		repo := newFakeRepo(newUserWithPassword(t, uid, "alice", "Passw0rd1"))
		svc := NewService(repo, logger.Nop())

		resp, err := svc.Login(context.Background(), &userv1.LoginRequest{
			Username: "alice", Password: "Passw0rd1",
		}, "127.0.0.1")
		if err != nil {
			t.Fatalf("Login: %v", err)
		}
		if resp.GetAccessToken() == "" || resp.GetTokenType() != "Bearer" {
			t.Fatalf("token/type = %q/%q", resp.GetAccessToken(), resp.GetTokenType())
		}
		if resp.GetUser().PasswordHash != "" {
			t.Fatal("LoginResponse must not carry password hash")
		}
		if repo.lastLogin.id != uid || repo.lastLogin.ip != "127.0.0.1" {
			t.Fatalf("last login not recorded: %+v", repo.lastLogin)
		}
	})

	t.Run("wrong_password", func(t *testing.T) {
		repo := newFakeRepo(newUserWithPassword(t, uid, "alice", "Passw0rd1"))
		svc := NewService(repo, logger.Nop())

		_, err := svc.Login(context.Background(), &userv1.LoginRequest{
			Username: "alice", Password: "WrongPass1",
		}, "")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("err = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("unknown_user_returns_same_error", func(t *testing.T) {
		svc := NewService(newFakeRepo(), logger.Nop())
		_, err := svc.Login(context.Background(), &userv1.LoginRequest{
			Username: "ghost", Password: "Passw0rd1",
		}, "")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("err = %v, want ErrInvalidCredentials (avoid account enumeration)", err)
		}
	})
}

func TestService_UpdateUser(t *testing.T) {
	const uid = "00000000-0000-0000-0000-000000000001"

	t.Run("without_mask_three_state", func(t *testing.T) {
		u := newUserWithPassword(t, uid, "alice", "Passw0rd1")
		svc := NewService(newFakeRepo(u), logger.Nop())

		// Explicit Value
		got, err := svc.UpdateUser(context.Background(), &userv1.UpdateUserRequest{
			UserId: uid, Nickname: proto.String("new-nick"),
		})
		if err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}
		if got.GetNickname() != "new-nick" {
			t.Fatalf("nickname = %q", got.GetNickname())
		}

		// Explicit Clear
		got, err = svc.UpdateUser(context.Background(), &userv1.UpdateUserRequest{
			UserId: uid, Nickname: proto.String(""),
		})
		if err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}
		if got.Nickname != nil {
			t.Fatalf("nickname = %v, want cleared", got.GetNickname())
		}
	})

	t.Run("with_mask_ignores_other_fields", func(t *testing.T) {
		u := newUserWithPassword(t, uid, "alice", "Passw0rd1")
		u.Phone = proto.String("13800138000")
		svc := NewService(newFakeRepo(u), logger.Nop())

		got, err := svc.UpdateUser(context.Background(), &userv1.UpdateUserRequest{
			UserId:     uid,
			Nickname:   proto.String("masked"),
			Phone:      proto.String("13900139000"),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"nickname"}},
		})
		if err != nil {
			t.Fatalf("UpdateUser: %v", err)
		}
		if got.GetNickname() != "masked" {
			t.Fatalf("nickname = %q, want updated", got.GetNickname())
		}
		if got.GetPhone() != "13800138000" {
			t.Fatalf("phone = %q, want untouched (not in mask)", got.GetPhone())
		}
	})
}

func TestService_ChangePassword(t *testing.T) {
	const uid = "00000000-0000-0000-0000-000000000001"

	t.Run("success", func(t *testing.T) {
		u := newUserWithPassword(t, uid, "alice", "Passw0rd1")
		svc := NewService(newFakeRepo(u), logger.Nop())

		if _, err := svc.ChangePassword(context.Background(), &userv1.ChangePasswordRequest{
			UserId: uid, OldPassword: "Passw0rd1", NewPassword: "Passw0rd2",
		}); err != nil {
			t.Fatalf("ChangePassword: %v", err)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("Passw0rd2")); err != nil {
			t.Fatalf("new password not persisted: %v", err)
		}
	})

	t.Run("wrong_old_password", func(t *testing.T) {
		svc := NewService(newFakeRepo(newUserWithPassword(t, uid, "alice", "Passw0rd1")), logger.Nop())
		_, err := svc.ChangePassword(context.Background(), &userv1.ChangePasswordRequest{
			UserId: uid, OldPassword: "Nope1234", NewPassword: "Passw0rd2",
		})
		// 旧密码不匹配是 ChangePassword 专属语义（FailedPrecondition / 400），
		// 与 Login 的凭证错误（Unauthenticated / 401）区分开，见 ErrPasswordMismatch 的声明注释。
		if !errors.Is(err, ErrPasswordMismatch) {
			t.Fatalf("err = %v, want ErrPasswordMismatch", err)
		}
	})

	t.Run("same_password_rejected", func(t *testing.T) {
		svc := NewService(newFakeRepo(newUserWithPassword(t, uid, "alice", "Passw0rd1")), logger.Nop())
		_, err := svc.ChangePassword(context.Background(), &userv1.ChangePasswordRequest{
			UserId: uid, OldPassword: "Passw0rd1", NewPassword: "Passw0rd1",
		})
		if !errors.Is(err, ErrSamePassword) {
			t.Fatalf("err = %v, want ErrSamePassword", err)
		}
	})
}

func TestService_ResetPassword_ReturnsUsableTempPassword(t *testing.T) {
	const uid = "00000000-0000-0000-0000-000000000001"
	u := newUserWithPassword(t, uid, "alice", "Passw0rd1")
	svc := NewService(newFakeRepo(u), logger.Nop())

	temp, err := svc.ResetPassword(context.Background(), &userv1.ResetPasswordRequest{UserId: uid})
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if len(temp) < 8 {
		t.Fatalf("temp password too short: %q", temp)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(temp)); err != nil {
		t.Fatalf("temp password not persisted as hash: %v", err)
	}
}

func TestService_ListUsers_Pagination(t *testing.T) {
	repo := newFakeRepo(
		&UserPO{UserID: "00000000-0000-0000-0000-000000000001"},
		&UserPO{UserID: "00000000-0000-0000-0000-000000000002"},
		&UserPO{UserID: "00000000-0000-0000-0000-000000000003"},
	)
	svc := NewService(repo, logger.Nop())

	got, next, err := svc.ListUsers(context.Background(), &userv1.ListUsersRequest{
		PageSize: 2,
		Filter:   `status = "ACTIVE"`,
		OrderBy:  "username asc",
	})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(got) != 2 || next == "" {
		t.Fatalf("len/next = %d/%q, want 2 and non-empty token", len(got), next)
	}
	if repo.lastQuery.Limit != 3 {
		t.Fatalf("limit = %d, want 3 (pageSize+1)", repo.lastQuery.Limit)
	}
	if repo.lastQuery.Filter != `status = "ACTIVE"` || repo.lastQuery.OrderBy != "username asc" {
		t.Fatalf("filter/order_by not passed through: %+v", repo.lastQuery)
	}
}

func TestService_DeleteUser_NotFound(t *testing.T) {
	svc := NewService(newFakeRepo(), logger.Nop())
	if err := svc.DeleteUser(context.Background(), "00000000-0000-0000-0000-000000000009"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
