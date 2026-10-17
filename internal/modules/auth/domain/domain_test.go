package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestNormalizeRegistration(t *testing.T) {
	valid, details := NormalizeRegistration(RegisterInput{
		Username: "  Alice_01 ", Email: " Alice@Example.COM ", Password: "password123",
	})
	if details != nil {
		t.Fatalf("合法注册不应报错: %v", details)
	}
	if valid.Username != "Alice_01" || valid.Email != "alice@example.com" {
		t.Fatalf("规范化结果不符: %+v", valid)
	}

	cases := []struct {
		name string
		in   RegisterInput
		key  string
	}{
		{"用户名过短", RegisterInput{Username: "ab", Email: "a@b.com", Password: "password123"}, "username"},
		{"用户名非法字符", RegisterInput{Username: "alice!", Email: "a@b.com", Password: "password123"}, "username"},
		{"邮箱非法", RegisterInput{Username: "alice", Email: "not-an-email", Password: "password123"}, "email"},
		{"密码过短", RegisterInput{Username: "alice", Email: "a@b.com", Password: "short"}, "password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, details := NormalizeRegistration(tc.in); details[tc.key] == "" {
				t.Fatalf("应返回字段 %q 的错误: %v", tc.key, details)
			}
		})
	}
}

func TestValidatePasswordAndHash(t *testing.T) {
	if err := ValidatePassword("password123"); err != nil {
		t.Fatalf("合法密码不应报错: %v", err)
	}
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("过短密码应报错")
	}
	hash, err := HashPassword("password123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !PasswordMatches(hash, "password123") {
		t.Fatal("密码应匹配")
	}
	if PasswordMatches(hash, "wrong-password") || PasswordMatches("", "password123") {
		t.Fatal("错误密码/空哈希不应匹配")
	}
}

func TestTokensEqual(t *testing.T) {
	if !TokensEqual("abc", "abc") {
		t.Fatal("相同令牌应相等")
	}
	if TokensEqual("abc", "abd") || TokensEqual("", "") || TokensEqual("abc", "") {
		t.Fatal("不同/空令牌不应相等")
	}
}

func TestValidateSessionMatrix(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	baseUser := model.User{ID: 1, Role: model.RoleAdmin, Status: model.StatusActive, TokenVersion: 2}
	baseSession := model.Session{ID: 9, UserID: 1, TokenVersion: 2, ExpiresAt: now.Add(time.Hour)}

	cases := []struct {
		name    string
		user    model.User
		session model.Session
		want    SessionState
	}{
		{"有效", baseUser, baseSession, SessionValid},
		{"已吊销", baseUser, func() model.Session {
			s := baseSession
			revokedAt := now.Add(-time.Minute)
			s.RevokedAt = &revokedAt
			return s
		}(), SessionRevoked},
		{"已过期", baseUser, func() model.Session {
			s := baseSession
			s.ExpiresAt = now
			return s
		}(), SessionExpired},
		{"版本不匹配", baseUser, func() model.Session {
			s := baseSession
			s.TokenVersion = 1
			return s
		}(), SessionVersionMismatch},
		{"账号停用", func() model.User {
			u := baseUser
			u.Status = model.StatusSuspended
			return u
		}(), baseSession, SessionSuspended},
		{"停用且会话已吊销时停用优先", func() model.User {
			u := baseUser
			u.Status = model.StatusSuspended
			return u
		}(), func() model.Session {
			s := baseSession
			revokedAt := now.Add(-time.Minute)
			s.RevokedAt = &revokedAt
			return s
		}(), SessionSuspended},
		{"停用且会话已过期时停用优先", func() model.User {
			u := baseUser
			u.Status = model.StatusSuspended
			return u
		}(), func() model.Session {
			s := baseSession
			s.ExpiresAt = now
			return s
		}(), SessionSuspended},
		{"AI 会话", func() model.User {
			u := baseUser
			u.IsAI = true
			u.Role = model.RoleUser
			return u
		}(), baseSession, SessionAIAccount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			viewer, state := ValidateSession(tc.user, tc.session, now)
			if state != tc.want {
				t.Fatalf("state=%v want=%v", state, tc.want)
			}
			if state == SessionValid && !viewer.Authenticated {
				t.Fatal("有效会话必须得到已认证 Viewer")
			}
			if state != SessionValid && viewer.Authenticated {
				t.Fatal("无效会话不得得到已认证 Viewer")
			}
		})
	}
}

func TestSuperAdminInvariants(t *testing.T) {
	role, super := BootstrapRole()
	if role != RoleAdmin || !super {
		t.Fatalf("首注册角色=%q super=%v", role, super)
	}
	if err := ValidateSuperAdminInvariant(RoleAdmin, true, false); err != nil {
		t.Fatalf("合法超管不应报错: %v", err)
	}
	if err := ValidateSuperAdminInvariant(RoleUser, true, false); err == nil {
		t.Fatal("超管必须是 admin 角色")
	}
	if err := ValidateSuperAdminInvariant(RoleAdmin, true, true); err == nil {
		t.Fatal("AI 不能是超管")
	}
}

func TestValidateProfile(t *testing.T) {
	nickname := "  new-name  "
	bio := strings.Repeat("a", 501)
	details := ValidateProfile(&nickname, &bio)
	if nickname != "new-name" {
		t.Fatalf("昵称应被 trim: %q", nickname)
	}
	if details["bio"] == "" {
		t.Fatalf("超长简介应报错: %v", details)
	}
}
