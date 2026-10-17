package http

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

func marshalJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	return string(raw)
}

func TestPrivateUserIncludesSuperAdminFlag(t *testing.T) {
	user := model.User{
		ID:                    9,
		Username:              "root",
		Email:                 "root@example.com",
		Nickname:              "超级管理员",
		Role:                  model.RoleAdmin,
		IsSuperAdmin:          true,
		Status:                model.StatusActive,
		RequireFollowApproval: true,
		CreatedAt:             time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC),
	}
	body := marshalJSON(t, privateUser(user))
	for _, want := range []string{
		`"is_super_admin":true`,
		`"email":"root@example.com"`,
		`"role":"admin"`,
		`"status":"active"`,
		`"require_follow_approval":true`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("私有 DTO 缺少 %s：%s", want, body)
		}
	}
	if strings.Contains(body, "password_hash") {
		t.Fatal("私有 DTO 不得包含密码哈希")
	}
}

func TestPublicUserOmitsPrivateFields(t *testing.T) {
	user := model.User{
		ID:                    9,
		Username:              "root",
		Email:                 "root@example.com",
		Nickname:              "超级管理员",
		Role:                  model.RoleAdmin,
		IsSuperAdmin:          true,
		Status:                model.StatusActive,
		RequireFollowApproval: true,
		CreatedAt:             time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC),
	}
	body := marshalJSON(t, publicUser(user))
	for _, forbidden := range []string{"is_super_admin", "email", "role", "status", "require_follow_approval"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("公开 DTO 不得包含私有字段 %q：%s", forbidden, body)
		}
	}
	for _, want := range []string{`"username":"root"`, `"nickname":"超级管理员"`, `"is_ai":false`, `"is_suspended":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("公开 DTO 缺少 %s：%s", want, body)
		}
	}
}

func TestResponseDTOJSONKeys(t *testing.T) {
	cases := []struct {
		name   string
		value  any
		fields []string
	}{
		{"csrf", csrfResponse{CSRFToken: "t"}, []string{"csrf_token"}},
		{"config", configResponse{RegistrationEnabled: true}, []string{"registration_enabled", "bootstrap_required"}},
		{"login", loginResponse{CSRFToken: "t"}, []string{"user", "csrf_token"}},
		{"me", meResponse{CSRFToken: "t"}, []string{"user", "csrf_token"}},
		{"user", userEnvelope{}, []string{"user"}},
		{"follow-settings", followSettingsResponse{}, []string{"require_follow_approval"}},
		{"profile", profileResponse{}, []string{
			"user", "bio", "post_count", "following_count", "follower_count", "follow_state",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var decoded map[string]any
			if err := json.Unmarshal([]byte(marshalJSON(t, tc.value)), &decoded); err != nil {
				t.Fatalf("解析 DTO 失败: %v", err)
			}
			for _, field := range tc.fields {
				if _, ok := decoded[field]; !ok {
					t.Errorf("响应缺少字段 %q：%v", field, decoded)
				}
			}
		})
	}
}
