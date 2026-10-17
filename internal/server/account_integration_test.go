package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestAccountIntegration 使用隔离测试 schema 执行完整账号链路，不需要 CREATE DATABASE，
// 也不写入生产业务 schema。默认跳过；设置 TIMELINE_INTEGRATION=1 后显式运行。
func TestAccountIntegration(t *testing.T) {
	srv := testkit.NewIntegrationServer(t)

	admin := srv.Client(t)
	user := srv.Client(t)
	secondSession := srv.Client(t)
	guest := srv.Client(t)

	suffix := testkit.IntegrationSuffix()
	username := "it_user_" + suffix
	email := "it-user-" + suffix + "@example.com"

	guest.RefreshCSRF()
	status, _, payload, raw := guest.Call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
		"username": username,
		"email":    email,
		"password": "user-password-123",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	userID := testkit.MustUser(t, payload)["id"]
	if _, ok := userID.(string); !ok {
		t.Fatalf("用户 ID 必须是 JSON 字符串: %s", raw)
	}
	if strings.Contains(raw, "password_hash") || strings.Contains(raw, "user-password-123") {
		t.Fatalf("注册响应泄露密码信息: %s", raw)
	}

	status, _, _, raw = guest.Call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
		"username": username,
		"email":    "other-" + email,
		"password": "user-password-123",
	})
	testkit.ExpectStatus(t, status, http.StatusConflict, raw)

	user.RefreshCSRF()
	status, headers, payload, raw := user.Call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": username,
		"password":   "user-password-123",
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	testkit.MustSetCSRF(t, payload, user)
	cookie := headers.Get("Set-Cookie")
	if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Lax") {
		t.Fatalf("会话 Cookie 安全属性不足: %s", cookie)
	}

	status, _, _, raw = user.Call(http.MethodPost, "/api/v1/users/me", false, map[string]any{
		"nickname": "不应保存",
	})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
	if !strings.Contains(raw, "csrf_failed") {
		t.Fatalf("缺少 CSRF 时应返回 csrf_failed: %s", raw)
	}
	status, _, payload, raw = user.Call(http.MethodPost, "/api/v1/users/me", true, map[string]any{
		"nickname": "集成测试用户",
		"bio":      "账号流程验证",
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := testkit.MustUser(t, payload)["nickname"]; got != "集成测试用户" {
		t.Fatalf("资料未更新: %s", raw)
	}
	status, _, _, raw = user.Call(http.MethodPost, "/api/v1/users/me/follow-settings", true, map[string]any{
		"require_follow_approval": true,
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	secondSession.RefreshCSRF()
	status, _, payload, raw = secondSession.Call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": email,
		"password":   "user-password-123",
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	testkit.MustSetCSRF(t, payload, secondSession)

	status, _, _, raw = user.Call(http.MethodPost, "/api/v1/auth/password", true, map[string]any{
		"old_password": "user-password-123",
		"new_password": "user-password-456",
	})
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, _, raw = user.Call(http.MethodGet, "/api/v1/auth/me", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = secondSession.Call(http.MethodGet, "/api/v1/auth/me", false, nil)
	testkit.ExpectStatus(t, status, http.StatusUnauthorized, raw)

	admin.RefreshCSRF()
	status, _, payload, raw = admin.Call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": testkit.IntegrationAdminEmail,
		"password":   testkit.IntegrationAdminPassword,
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	testkit.MustSetCSRF(t, payload, admin)

	status, _, _, raw = user.Call(http.MethodGet, "/api/v1/admin/users", false, nil)
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)

	idString := userID.(string)
	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/users/"+idString+"/suspend", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	// 重复停用保持幂等，同时仍记录管理操作并确保旧会话被吊销。
	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/users/"+idString+"/suspend", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = user.Call(http.MethodPost, "/api/v1/users/me/follow-settings", true, map[string]any{
		"require_follow_approval": false,
	})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
	if !strings.Contains(raw, "account_suspended") {
		t.Fatalf("停用账号写操作应返回 account_suspended: %s", raw)
	}
	status, _, _, raw = user.Call(http.MethodGet, "/api/v1/auth/me", false, nil)
	testkit.ExpectStatus(t, status, http.StatusUnauthorized, raw)
	status, _, payload, raw = guest.Call(http.MethodGet, "/api/v1/users/"+username, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if suspended := testkit.MustUser(t, payload)["is_suspended"]; suspended != true {
		t.Fatalf("停用后公开资料应保留只读标识: %s", raw)
	}

	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/users/"+idString+"/unsuspend", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	user.RefreshCSRF()
	status, _, payload, raw = user.Call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": username,
		"password":   "user-password-456",
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	testkit.MustSetCSRF(t, payload, user)

	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/users/"+idString+"/reset-password", true, map[string]any{
		"new_password": "user-password-789",
	})
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, _, raw = user.Call(http.MethodGet, "/api/v1/auth/me", false, nil)
	testkit.ExpectStatus(t, status, http.StatusUnauthorized, raw)

	user.RefreshCSRF()
	status, _, _, raw = user.Call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": username,
		"password":   "user-password-456",
	})
	testkit.ExpectStatus(t, status, http.StatusUnauthorized, raw)
	status, _, payload, raw = user.Call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": username,
		"password":   "user-password-789",
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	testkit.MustSetCSRF(t, payload, user)

	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/settings", true, map[string]any{
		"registration_enabled": false,
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = guest.Call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
		"username": "closed_" + suffix,
		"email":    "closed-" + suffix + "@example.com",
		"password": "closed-password-123",
	})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/settings", true, map[string]any{
		"registration_enabled": true,
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	status, _, payload, raw = admin.Call(http.MethodGet, "/api/v1/admin/audit-logs", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	items, ok := payload["items"].([]any)
	if !ok || len(items) < 4 {
		t.Fatalf("账号管理操作缺少审计记录: %s", raw)
	}

	status, _, _, raw = user.Call(http.MethodPost, "/api/v1/auth/logout", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, _, raw = user.Call(http.MethodGet, "/api/v1/auth/me", false, nil)
	testkit.ExpectStatus(t, status, http.StatusUnauthorized, raw)
}
