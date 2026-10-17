package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/ai"
	"github.com/great-magician-01/timeline/internal/settings"
)

// TestAdminSettingsAndAttachmentIntegration 覆盖本轮修复的关键契约：
// 公开注册配置、管理端系统设置的读写与即时生效、存储配额、附件审核列表与删除、
// 用户内容/关系统计、定时发布列表的失败状态。
func TestAdminSettingsAndAttachmentIntegration(t *testing.T) {
	if os.Getenv("TIMELINE_INTEGRATION") != "1" {
		t.Skip("设置 TIMELINE_INTEGRATION=1 后运行测试 schema 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool, srv, baseURL := setupIntegrationServer(t)
	guest := newIntegrationClient(t, baseURL)
	alice := newIntegrationClient(t, baseURL)
	admin := newIntegrationClient(t, baseURL)

	suffix := integrationSuffix()
	registerIntegrationUser(t, alice, "st_alice_"+suffix, "st-alice-"+suffix+"@example.com")
	loginIntegrationUser(t, admin, "admin_integration", "admin-password-123")
	guest.refreshCSRF()

	// 公开认证配置不要求登录。
	status, _, payload, raw := guest.call(http.MethodGet, "/api/v1/auth/config", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if enabled, _ := payload["registration_enabled"].(bool); !enabled {
		t.Fatalf("默认注册开关应为开启: %v", payload)
	}

	// 管理端读取与更新设置。
	status, _, payload, raw = admin.call(http.MethodGet, "/api/v1/admin/settings", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	for _, field := range []string{"max_upload_bytes", "max_request_bytes", "storage_quota_bytes", "post_rate_limit", "upload_rate_limit", "call_log_max_bytes"} {
		if payload[field] == nil {
			t.Fatalf("系统设置缺少字段 %s: %v", field, payload)
		}
	}

	status, _, payload, raw = admin.call(http.MethodPost, "/api/v1/admin/settings", true, map[string]any{
		"max_upload_bytes":                1024,
		"max_request_bytes":               (1 << 20) + 1024,
		"storage_quota_bytes":             10,
		"post_rate_limit":                 5,
		"post_rate_window_seconds":        60,
		"upload_rate_limit":               5,
		"upload_rate_window_seconds":      60,
		"temp_attachment_retention_hours": 24,
		"call_log_max_bytes":              4096,
		"call_log_retention_days":         7,
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	resetSettings := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for key, value := range map[string]string{
			"upload.max_file_bytes":                 "104857600",
			"upload.max_request_bytes":              "105906176",
			"storage.quota_bytes":                   "5368709120",
			"post.rate_limit":                       "30",
			"post.rate_window_seconds":              "600",
			"upload.rate_limit":                     "60",
			"upload.rate_window_seconds":            "600",
			"media.temp_attachment_retention_hours": "168",
			"ai.call_log_max_bytes":                 "65536",
			"ai.call_log_retention_days":            "90",
		} {
			if _, err := pool.Exec(cleanupCtx, `
				INSERT INTO system_settings (key, value) VALUES ($1, $2::jsonb)
				ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
				key, value); err != nil {
				t.Logf("恢复测试设置 %s 失败: %v", key, err)
			}
		}
	}
	t.Cleanup(resetSettings)
	if got, _ := payload["storage_quota_bytes"].(float64); got != 10 {
		t.Fatalf("设置未即时返回新值: %v", payload)
	}
	var stored int64
	if err := pool.QueryRow(ctx, `SELECT value::text::bigint FROM system_settings WHERE key = 'storage.quota_bytes'`).Scan(&stored); err != nil {
		t.Fatalf("读取存储配额设置失败: %v", err)
	}
	if stored != 10 {
		t.Fatalf("存储配额未落库: %d", stored)
	}

	// 全局网页搜索密钥是独立配置：只写、加密落库、响应不返回明文或密文。
	restoreSearchSettings := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for key, value := range map[string]string{
			settings.KeyWebSearchBaseURL:      `"https://api.deepseek.com"`,
			settings.KeyWebSearchAPIKeyCipher: `""`,
		} {
			if _, err := pool.Exec(cleanupCtx, `
				INSERT INTO system_settings (key, value) VALUES ($1, $2::jsonb)
				ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
				key, value); err != nil {
				t.Logf("恢复网页搜索设置 %s 失败: %v", key, err)
			}
		}
	}
	restoreSearchSettings()
	t.Cleanup(restoreSearchSettings)

	status, _, payload, raw = admin.call(http.MethodGet, "/api/v1/admin/settings/web-search", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if payload["configured"] != false || payload["base_url"] == "" {
		t.Fatalf("初始网页搜索设置不符: %v", payload)
	}
	const searchKey = "sk-integration-search-secret"
	status, _, payload, raw = admin.call(http.MethodPost, "/api/v1/admin/settings/web-search", true, map[string]any{
		"base_url": "https://gateway.example.com/deepseek/",
		"api_key":  searchKey,
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if payload["configured"] != true || payload["usable"] != true || payload["api_key_masked"] == "" {
		t.Fatalf("保存搜索密钥后状态不符: %v", payload)
	}
	if strings.Contains(string(raw), searchKey) || strings.Contains(string(raw), "v1:") {
		t.Fatalf("响应泄漏搜索密钥或密文: %s", raw)
	}
	var cipherText string
	if err := pool.QueryRow(ctx, `SELECT value #>> '{}' FROM system_settings WHERE key = $1`,
		settings.KeyWebSearchAPIKeyCipher).Scan(&cipherText); err != nil {
		t.Fatalf("读取搜索密钥密文失败: %v", err)
	}
	if cipherText == "" || strings.Contains(cipherText, searchKey) {
		t.Fatalf("搜索密钥未加密落库: %q", cipherText)
	}
	decrypted, err := ai.DecryptSecret(srv.cfg.SecretKey, cipherText)
	if err != nil || decrypted != searchKey {
		t.Fatalf("搜索密钥解密失败: %v", err)
	}
	status, _, payload, raw = admin.call(http.MethodPost, "/api/v1/admin/settings/web-search", true, map[string]any{
		"clear_api_key": true,
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if payload["configured"] != false || payload["usable"] != false {
		t.Fatalf("清除后应标记为未配置: %v", payload)
	}

	// 配额为 10 字节时上传应被拒绝。
	status, _, _, raw = uploadIntegrationPNGStatus(t, alice)
	expectIntegrationStatus(t, status, http.StatusRequestEntityTooLarge, raw)
	// 恢复合理配额后可以上传。
	status, _, _, raw = admin.call(http.MethodPost, "/api/v1/admin/settings", true, map[string]any{
		"storage_quota_bytes": 10 << 20,
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	attachmentID := uploadIntegrationPNG(t, alice)

	// 管理端附件列表可见并可逻辑删除。
	status, _, payload, raw = admin.call(http.MethodGet, "/api/v1/admin/attachments?purpose=post", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(payload, "id", attachmentID) {
		t.Fatalf("后台附件列表未包含新上传附件: %v", payload)
	}
	status, _, _, raw = admin.call(http.MethodPost, "/api/v1/admin/attachments/"+attachmentID+"/delete", true, nil)
	expectIntegrationStatus(t, status, http.StatusNoContent, raw)
	status, _, payload, raw = admin.call(http.MethodGet, "/api/v1/admin/attachments?status=deleted", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(payload, "id", attachmentID) {
		t.Fatalf("逻辑删除后附件应仍在后台列表: %v", payload)
	}

	// 管理端用户详情包含内容与关系统计。
	status, _, payload, raw = admin.call(http.MethodGet, "/api/v1/admin/users", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	items, _ := payload["items"].([]any)
	if len(items) == 0 {
		t.Fatal("用户列表为空")
	}
	var aliceID string
	for _, rawItem := range items {
		user, _ := rawItem.(map[string]any)
		if user["username"] == "st_alice_"+suffix {
			aliceID = integrationString(user, t, "id")
			break
		}
	}
	if aliceID == "" {
		t.Fatalf("用户列表中未找到测试用户: %v", payload)
	}
	status, _, payload, raw = admin.call(http.MethodGet, "/api/v1/admin/users/"+aliceID, false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	stats, ok := payload["stats"].(map[string]any)
	if !ok || stats["posts"] == nil || stats["followers"] == nil {
		t.Fatalf("用户详情缺少 stats: %v", payload)
	}

	// 定时发布列表返回状态与失败原因。
	status, _, payload, raw = alice.call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>定时测试</p>",
		"visibility":     "public",
		"action":         "schedule",
		"scheduled_at":   time.Now().Add(2 * time.Hour).Format(time.RFC3339),
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	post := integrationNested(payload, t, "post")
	scheduledID := integrationString(post, t, "id")
	status, _, payload, raw = alice.call(http.MethodGet, "/api/v1/me/scheduled", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(payload, "id", scheduledID) {
		t.Fatalf("定时列表未包含新建定时帖: %v", payload)
	}
	items, _ = payload["items"].([]any)
	first, _ := items[0].(map[string]any)
	if got := integrationString(first, t, "status"); got != "scheduled" {
		t.Fatalf("定时帖展示状态应为 scheduled，实际 %s", got)
	}
	if _, ok := first["attachment_count"]; !ok {
		t.Fatalf("定时列表缺少 attachment_count: %v", first)
	}
	// 模拟定时发布作业失败，列表应展示失败状态与原因。
	postID, err := strconv.ParseInt(scheduledID, 10, 64)
	if err != nil {
		t.Fatalf("解析定时帖 ID 失败: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE background_jobs SET status = 'failed', last_error = '模拟发布失败'
		WHERE job_type = 'post_publish' AND ref_type = 'post' AND ref_id = $1`, postID); err != nil {
		t.Fatalf("模拟定时任务失败: %v", err)
	}
	status, _, payload, raw = alice.call(http.MethodGet, "/api/v1/me/scheduled", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	items, _ = payload["items"].([]any)
	first, _ = items[0].(map[string]any)
	if first["publish_failed"] != true {
		t.Fatalf("定时失败状态未返回: %v", first)
	}
	if got, _ := first["publish_error"].(string); got != "模拟发布失败" {
		t.Fatalf("定时失败原因不正确: %v", first)
	}

	// 清理本轮设置，避免影响其他集成测试（测试 schema 可重复运行）。
	status, _, _, raw = admin.call(http.MethodPost, "/api/v1/admin/settings", true, map[string]any{
		"max_upload_bytes":                100 << 20,
		"max_request_bytes":               (100 << 20) + (1 << 20),
		"storage_quota_bytes":             5 << 30,
		"post_rate_limit":                 30,
		"post_rate_window_seconds":        600,
		"upload_rate_limit":               60,
		"upload_rate_window_seconds":      600,
		"temp_attachment_retention_hours": 168,
		"call_log_max_bytes":              64 << 10,
		"call_log_retention_days":         90,
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
}

// uploadIntegrationPNGStatus 上传合法 PNG 并返回状态码，不会在失败时终止测试。
func uploadIntegrationPNGStatus(t *testing.T, client *integrationClient) (int, http.Header, map[string]any, string) {
	t.Helper()
	pngBytes := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00, 0x08,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x4b, 0x6d, 0x29,
		0xdc, 0x00, 0x00, 0x00, 0x13, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xd7, 0x63, 0xfc, 0xcf, 0xc0, 0xf0,
		0x9f, 0x81, 0x81, 0x01, 0x00, 0x00, 0x00, 0xff,
		0xff, 0x03, 0x00, 0x0f, 0x2c, 0x03, 0x8b, 0xd8,
		0x4e, 0x4c, 0x51, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "integration.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(pngBytes); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("purpose", "post"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, client.base+"/api/v1/attachments", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-CSRF-Token", client.csrf)
	resp, err := client.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &payload)
	}
	return resp.StatusCode, resp.Header, payload, string(data)
}
