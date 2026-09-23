package user

import (
	"testing"
	"time"

	userv1 "go-buf-api-template/gen/go/user/v1"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestRoleList_ValueAndScan(t *testing.T) {
	roles := RoleList{userv1.UserRole_USER_ROLE_ADMIN, userv1.UserRole_USER_ROLE_VIEWER}

	v, err := roles.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	raw, ok := v.(string)
	if !ok {
		t.Fatalf("Value type = %T, want string", v)
	}
	if raw != "[1,3]" {
		t.Fatalf("Value = %q, want [1,3]", raw)
	}

	var scanned RoleList
	if err := scanned.Scan(raw); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(scanned) != 2 || scanned[0] != userv1.UserRole_USER_ROLE_ADMIN || scanned[1] != userv1.UserRole_USER_ROLE_VIEWER {
		t.Fatalf("scanned = %v", scanned)
	}

	// NULL → nil
	var nullRoles RoleList
	if err := nullRoles.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if nullRoles != nil {
		t.Fatalf("Scan(nil) = %v, want nil", nullRoles)
	}
}

func TestUserPO_ToProto_NeverExposesPasswordHash(t *testing.T) {
	m := &UserPO{
		UserID:       "00000000-0000-0000-0000-000000000001",
		Username:     "alice",
		Email:        "alice@example.com",
		PasswordHash: "$2a$10$super-secret-hash",
		Status:       int32(userv1.UserStatus_USER_STATUS_ACTIVE),
		Roles:        RoleList{userv1.UserRole_USER_ROLE_ADMIN},
		CreatedAt:    time.Unix(0, 0).UTC(),
		UpdatedAt:    time.Unix(0, 0).UTC(),
	}

	got := m.ToProto()
	if got.PasswordHash != "" {
		t.Fatalf("PasswordHash leaked: %q", got.PasswordHash)
	}
	// 可空列 NULL 时应保持 nil，而非空串。
	if got.Nickname != nil || got.Phone != nil || got.AvatarUrl != nil || got.LastLoginAt != nil {
		t.Fatal("NULL columns should map to nil, not zero values")
	}
	if got.GetStatus() != userv1.UserStatus_USER_STATUS_ACTIVE {
		t.Fatalf("status = %v", got.GetStatus())
	}
	if len(got.GetRoles()) != 1 || got.GetRoles()[0] != userv1.UserRole_USER_ROLE_ADMIN {
		t.Fatalf("roles = %v", got.GetRoles())
	}
}

func TestUserPO_ApplyUpdate(t *testing.T) {
	const uid = "00000000-0000-0000-0000-000000000001"

	t.Run("three_state_without_mask", func(t *testing.T) {
		m := &UserPO{UserID: uid, Nickname: proto.String("old"), Phone: proto.String("13800138000")}

		// 未传 → 不变
		if err := m.applyUpdate(&userv1.UpdateUserRequest{UserId: uid}); err != nil {
			t.Fatalf("applyUpdate: %v", err)
		}
		if m.Nickname == nil || *m.Nickname != "old" {
			t.Fatal("absent field must not be touched")
		}
		// 传值 → 更新
		if err := m.applyUpdate(&userv1.UpdateUserRequest{UserId: uid, Nickname: proto.String("new")}); err != nil {
			t.Fatalf("applyUpdate: %v", err)
		}
		if *m.Nickname != "new" {
			t.Fatalf("nickname = %q", *m.Nickname)
		}
		// 空串 → 显式清空
		if err := m.applyUpdate(&userv1.UpdateUserRequest{UserId: uid, Nickname: proto.String("")}); err != nil {
			t.Fatalf("applyUpdate: %v", err)
		}
		if m.Nickname != nil {
			t.Fatalf("nickname = %v, want cleared", *m.Nickname)
		}
	})

	t.Run("mask_rejects_non_updatable_path", func(t *testing.T) {
		m := &UserPO{UserID: uid, Nickname: proto.String("old")}
		err := m.applyUpdate(&userv1.UpdateUserRequest{
			UserId:     uid,
			Nickname:   proto.String("new"),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"user_id"}},
		})
		if err == nil {
			t.Fatal("user_id must not be updatable via mask")
		}
		if m.Nickname == nil || *m.Nickname != "old" {
			t.Fatal("failed validation must not mutate the model")
		}
	})
}
