package server_test

import (
	"net/http"
	"testing"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestBannerIntegration 覆盖个人主页横幅（0024）：上传回写 banner_url、公开资料
// 与私有资料携带横幅、横幅附件游客可读、替换覆盖、移除回退默认占位、非图片拒绝、
// 游客写拒绝。只在隔离测试 schema 中执行，默认跳过，需 TIMELINE_INTEGRATION=1。
func TestBannerIntegration(t *testing.T) {
	srv := testkit.NewIntegrationServer(t)
	suffix := testkit.IntegrationShortSuffix()

	alice := srv.Client(t)
	aliceName := "bn_alice_" + suffix
	alice.RegisterAndLogin(aliceName, "bn-alice-"+suffix+"@example.com")

	guest := srv.Client(t)
	guest.RefreshCSRF()

	profileBannerURL := func(t *testing.T, c *testkit.Client, path string) any {
		t.Helper()
		status, _, payload, raw := c.Call(http.MethodGet, path, false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
		user, _ := payload["user"].(map[string]any)
		if user == nil {
			t.Fatalf("资料响应缺少 user: %s", raw)
		}
		return user["banner_url"]
	}

	// ── 游客写拒绝 ──
	status, _, _, raw := guest.Call(http.MethodPost, "/api/v1/users/me/banner/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusUnauthorized, raw)

	// ── 上传横幅：附件落库后同事务写回 banner_url ──
	banner := alice.UploadWithPurpose("banner")
	bannerURL, _ := banner["content_url"].(string)
	if bannerURL == "" {
		t.Fatalf("横幅附件缺少 content_url: %v", banner)
	}
	if got := profileBannerURL(t, alice, "/api/v1/auth/me"); got != bannerURL {
		t.Fatalf("auth/me banner_url = %v，期望 %v", got, bannerURL)
	}
	if got := profileBannerURL(t, guest, "/api/v1/users/"+aliceName); got != bannerURL {
		t.Fatalf("公开资料 banner_url = %v，期望 %v", got, bannerURL)
	}

	// ── 横幅附件公开可读（主页公开展示内容，游客无需会话）──
	contentStatus, contentType, _ := guest.RawGET(bannerURL)
	if contentStatus != http.StatusOK {
		t.Fatalf("游客读取横幅附件状态 = %d，期望 200", contentStatus)
	}
	if contentType != "image/png" {
		t.Fatalf("横幅内容类型 = %q，期望 image/png", contentType)
	}

	// ── 替换横幅：banner_url 切换到新附件 ──
	replacement := alice.UploadWithPurpose("banner")
	replacementURL, _ := replacement["content_url"].(string)
	if replacementURL == "" || replacementURL == bannerURL {
		t.Fatalf("替换横幅 content_url 异常: %v", replacement)
	}
	if got := profileBannerURL(t, guest, "/api/v1/users/"+aliceName); got != replacementURL {
		t.Fatalf("替换后公开资料 banner_url = %v，期望 %v", got, replacementURL)
	}

	// ── 移除横幅：banner_url 清空，主页回退默认占位 ──
	status, _, removedPayload, raw := alice.Call(http.MethodPost, "/api/v1/users/me/banner/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	removedUser, _ := removedPayload["user"].(map[string]any)
	if removedUser == nil || removedUser["banner_url"] != "" {
		t.Fatalf("移除后 banner_url 应为空串: %s", raw)
	}
	if got := profileBannerURL(t, guest, "/api/v1/users/"+aliceName); got != "" {
		t.Fatalf("移除后公开资料 banner_url = %v，期望空串", got)
	}

	// ── 非图片拒绝：横幅用途只接受图片内容 ──
	status, badPayload, raw := alice.UploadRawWithPurpose("banner", "banner.txt", []byte("plain text banner"))
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	if code, _ := badPayload["error"].(map[string]any)["code"].(string); code != "invalid_request" {
		t.Fatalf("错误码 = %q，期望 invalid_request: %s", code, raw)
	}
	if got := profileBannerURL(t, alice, "/api/v1/auth/me"); got != "" {
		t.Fatalf("被拒绝的上传不应改动 banner_url，实际 %v", got)
	}
}
