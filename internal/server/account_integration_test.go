package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/config"
	"github.com/great-magician-01/timeline/internal/id"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/migrate"
	"github.com/great-magician-01/timeline/internal/storage"
)

// TestAccountIntegration 使用现有开发实例中的 timeline_test / user_test 隔离 schema
// 执行完整账号链路，不需要 CREATE DATABASE，也不写入生产业务 schema。测试记录只保留在测试 schema。
// 默认跳过；本机已配置开发 PostgreSQL 时可通过 TIMELINE_INTEGRATION=1 显式运行。
func TestAccountIntegration(t *testing.T) {
	if os.Getenv("TIMELINE_INTEGRATION") != "1" {
		t.Skip("设置 TIMELINE_INTEGRATION=1 后运行测试 schema 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg := loadIntegrationConfig(t)
	testCfg := *cfg
	testCfg.CookieSecure = false
	testCfg.LogLevel = "info"
	dsn, err := integrationDSN(testCfg.DSN(), "timeline_test", "user_test")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接测试 schema 失败: %v", err)
	}
	var httpServer *httptest.Server
	t.Cleanup(func() {
		if httpServer != nil {
			httpServer.Close()
		}
		pool.Close()
	})
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping 测试数据库失败: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := migrate.RunWithSchemas(ctx, pool, log, "timeline_test", "user_test"); err != nil {
		t.Fatalf("执行测试 schema 迁移失败: %v", err)
	}
	// 上次失败可能停在关闭注册步骤；每次只在测试 schema 恢复默认开关。
	if _, err := pool.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ('registration.enabled', 'true'::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		t.Fatalf("恢复测试注册开关失败: %v", err)
	}
	ids, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	ensureIntegrationAdmin(t, pool, ids)
	state := NewState()
	state.SetPool(pool)
	state.MarkMigrationsDone()
	runner := jobs.NewRunner(state, ids, log)
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("初始化测试附件目录失败: %v", err)
	}
	httpServer = httptest.NewServer(New(&testCfg, log, state, ids, runner, store).Handler())

	admin := newIntegrationClient(t, httpServer.URL)
	user := newIntegrationClient(t, httpServer.URL)
	secondSession := newIntegrationClient(t, httpServer.URL)
	guest := newIntegrationClient(t, httpServer.URL)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	username := "it_user_" + suffix
	email := "it-user-" + suffix + "@example.com"

	guest.refreshCSRF()
	status, _, payload, raw := guest.call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
		"username": username,
		"email":    email,
		"password": "user-password-123",
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	userID := integrationUser(payload, t)["id"]
	if _, ok := userID.(string); !ok {
		t.Fatalf("用户 ID 必须是 JSON 字符串: %s", raw)
	}
	if strings.Contains(raw, "password_hash") || strings.Contains(raw, "user-password-123") {
		t.Fatalf("注册响应泄露密码信息: %s", raw)
	}

	status, _, _, raw = guest.call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
		"username": username,
		"email":    "other-" + email,
		"password": "user-password-123",
	})
	expectIntegrationStatus(t, status, http.StatusConflict, raw)

	user.refreshCSRF()
	status, headers, payload, raw := user.call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": username,
		"password":   "user-password-123",
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	integrationCSRF(payload, t, user)
	cookie := headers.Get("Set-Cookie")
	if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Lax") {
		t.Fatalf("会话 Cookie 安全属性不足: %s", cookie)
	}

	status, _, _, raw = user.call(http.MethodPost, "/api/v1/users/me", false, map[string]any{
		"nickname": "不应保存",
	})
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)
	if !strings.Contains(raw, "csrf_failed") {
		t.Fatalf("缺少 CSRF 时应返回 csrf_failed: %s", raw)
	}
	status, _, payload, raw = user.call(http.MethodPost, "/api/v1/users/me", true, map[string]any{
		"nickname": "集成测试用户",
		"bio":      "账号流程验证",
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if got := integrationUser(payload, t)["nickname"]; got != "集成测试用户" {
		t.Fatalf("资料未更新: %s", raw)
	}
	status, _, _, raw = user.call(http.MethodPost, "/api/v1/users/me/follow-settings", true, map[string]any{
		"require_follow_approval": true,
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)

	secondSession.refreshCSRF()
	status, _, payload, raw = secondSession.call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": email,
		"password":   "user-password-123",
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	integrationCSRF(payload, t, secondSession)

	status, _, _, raw = user.call(http.MethodPost, "/api/v1/auth/password", true, map[string]any{
		"old_password": "user-password-123",
		"new_password": "user-password-456",
	})
	expectIntegrationStatus(t, status, http.StatusNoContent, raw)
	status, _, _, raw = user.call(http.MethodGet, "/api/v1/auth/me", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = secondSession.call(http.MethodGet, "/api/v1/auth/me", false, nil)
	expectIntegrationStatus(t, status, http.StatusUnauthorized, raw)

	admin.refreshCSRF()
	status, _, payload, raw = admin.call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": "admin-integration@example.com",
		"password":   "admin-password-123",
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	integrationCSRF(payload, t, admin)

	status, _, _, raw = user.call(http.MethodGet, "/api/v1/admin/users", false, nil)
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)

	idString := userID.(string)
	status, _, _, raw = admin.call(http.MethodPost, "/api/v1/admin/users/"+idString+"/suspend", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	// 重复停用保持幂等，同时仍记录管理操作并确保旧会话被吊销。
	status, _, _, raw = admin.call(http.MethodPost, "/api/v1/admin/users/"+idString+"/suspend", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = user.call(http.MethodPost, "/api/v1/users/me/follow-settings", true, map[string]any{
		"require_follow_approval": false,
	})
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)
	if !strings.Contains(raw, "account_suspended") {
		t.Fatalf("停用账号写操作应返回 account_suspended: %s", raw)
	}
	status, _, _, raw = user.call(http.MethodGet, "/api/v1/auth/me", false, nil)
	expectIntegrationStatus(t, status, http.StatusUnauthorized, raw)
	status, _, payload, raw = guest.call(http.MethodGet, "/api/v1/users/"+username, false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if suspended := integrationUser(payload, t)["is_suspended"]; suspended != true {
		t.Fatalf("停用后公开资料应保留只读标识: %s", raw)
	}

	status, _, _, raw = admin.call(http.MethodPost, "/api/v1/admin/users/"+idString+"/unsuspend", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	user.refreshCSRF()
	status, _, payload, raw = user.call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": username,
		"password":   "user-password-456",
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	integrationCSRF(payload, t, user)

	status, _, _, raw = admin.call(http.MethodPost, "/api/v1/admin/users/"+idString+"/reset-password", true, map[string]any{
		"new_password": "user-password-789",
	})
	expectIntegrationStatus(t, status, http.StatusNoContent, raw)
	status, _, _, raw = user.call(http.MethodGet, "/api/v1/auth/me", false, nil)
	expectIntegrationStatus(t, status, http.StatusUnauthorized, raw)

	user.refreshCSRF()
	status, _, _, raw = user.call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": username,
		"password":   "user-password-456",
	})
	expectIntegrationStatus(t, status, http.StatusUnauthorized, raw)
	status, _, payload, raw = user.call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": username,
		"password":   "user-password-789",
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	integrationCSRF(payload, t, user)

	status, _, _, raw = admin.call(http.MethodPost, "/api/v1/admin/settings", true, map[string]any{
		"registration_enabled": false,
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = guest.call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
		"username": "closed_" + suffix,
		"email":    "closed-" + suffix + "@example.com",
		"password": "closed-password-123",
	})
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)
	status, _, _, raw = admin.call(http.MethodPost, "/api/v1/admin/settings", true, map[string]any{
		"registration_enabled": true,
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)

	status, _, payload, raw = admin.call(http.MethodGet, "/api/v1/admin/audit-logs", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	items, ok := payload["items"].([]any)
	if !ok || len(items) < 4 {
		t.Fatalf("账号管理操作缺少审计记录: %s", raw)
	}

	status, _, _, raw = user.call(http.MethodPost, "/api/v1/auth/logout", true, nil)
	expectIntegrationStatus(t, status, http.StatusNoContent, raw)
	status, _, _, raw = user.call(http.MethodGet, "/api/v1/auth/me", false, nil)
	expectIntegrationStatus(t, status, http.StatusUnauthorized, raw)
}

func integrationDSN(productionDSN, timelineSchema, userSchema string) (string, error) {
	u, err := url.Parse(productionDSN)
	if err != nil {
		return "", fmt.Errorf("解析测试 DSN 失败: %w", err)
	}
	query := u.Query()
	query.Set("search_path", timelineSchema+","+userSchema)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func loadIntegrationConfig(t *testing.T) *config.Config {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(wd); err != nil {
			t.Errorf("恢复测试工作目录失败: %v", err)
		}
	}()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载集成测试配置失败: %v", err)
	}
	return cfg
}

type integrationClient struct {
	t      *testing.T
	base   string
	client *http.Client
	csrf   string
}

func newIntegrationClient(t *testing.T, base string) *integrationClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &integrationClient{t: t, base: base, client: &http.Client{Jar: jar, Timeout: 15 * time.Second}}
}

func (c *integrationClient) refreshCSRF() {
	c.t.Helper()
	status, _, payload, raw := c.call(http.MethodGet, "/api/v1/auth/csrf", false, nil)
	expectIntegrationStatus(c.t, status, http.StatusOK, raw)
	integrationCSRF(payload, c.t, c)
}

func (c *integrationClient) call(
	method, path string,
	withCSRF bool,
	body any,
) (status int, header http.Header, payload map[string]any, raw string) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withCSRF {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		c.t.Fatal(err)
	}
	payload = make(map[string]any)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &payload); err != nil {
			c.t.Fatalf("接口返回非 JSON: %s", string(data))
		}
	}
	return resp.StatusCode, resp.Header, payload, string(data)
}

func integrationCSRF(payload map[string]any, t *testing.T, client *integrationClient) {
	t.Helper()
	token, ok := payload["csrf_token"].(string)
	if !ok || token == "" {
		t.Fatalf("响应缺少 csrf_token: %v", payload)
	}
	client.csrf = token
}

func integrationUser(payload map[string]any, t *testing.T) map[string]any {
	t.Helper()
	u, ok := payload["user"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 user: %v", payload)
	}
	return u
}

func expectIntegrationStatus(t *testing.T, got, want int, raw string) {
	t.Helper()
	if got != want {
		t.Fatalf("HTTP 状态不符: got=%d want=%d body=%s", got, want, raw)
	}
}
