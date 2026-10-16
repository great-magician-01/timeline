package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/id"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/migrate"
	"github.com/great-magician-01/timeline/internal/storage"
)

// TestContentIntegration 覆盖帖子、草稿、定时、编辑历史、附件、可见性、时间线、
// 关注、点赞、评论、转发、通知、标签与搜索、以及管理后台内容审核。
// 与账号集成测试相同：只在 timeline_test / user_test 隔离 schema 中执行，
// 默认跳过，需显式设置 TIMELINE_INTEGRATION=1。
func TestContentIntegration(t *testing.T) {
	if os.Getenv("TIMELINE_INTEGRATION") != "1" {
		t.Skip("设置 TIMELINE_INTEGRATION=1 后运行测试 schema 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cfg := loadIntegrationConfig(t)
	testCfg := *cfg
	testCfg.CookieSecure = false
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
	if _, err := pool.Exec(ctx, `
		INSERT INTO system_settings (key, value) VALUES ('registration.enabled', 'true'::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		t.Fatalf("恢复测试注册开关失败: %v", err)
	}
	ids, err := id.NewGenerator(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreateInitialAdmin(ctx, pool, ids, auth.RegisterInput{
		Username: "admin_integration",
		Email:    "admin-integration@example.com",
		Password: "admin-password-123",
	}); err != nil && !errors.Is(err, auth.ErrAdminExists) {
		t.Fatalf("初始化测试管理员失败: %v", err)
	}
	state := NewState()
	state.SetPool(pool)
	state.MarkMigrationsDone()
	store, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatalf("初始化测试附件目录失败: %v", err)
	}
	httpServer = httptest.NewServer(New(&testCfg, log, state, ids, jobs.NewRunner(state, ids, log), store).Handler())

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	alice := newIntegrationClient(t, httpServer.URL)
	bob := newIntegrationClient(t, httpServer.URL)
	carol := newIntegrationClient(t, httpServer.URL)
	admin := newIntegrationClient(t, httpServer.URL)
	guest := newIntegrationClient(t, httpServer.URL)

	registerIntegrationUser(t, alice, "ct_alice_"+suffix, "ct-alice-"+suffix+"@example.com")
	registerIntegrationUser(t, bob, "ct_bob_"+suffix, "ct-bob-"+suffix+"@example.com")
	registerIntegrationUser(t, carol, "ct_carol_"+suffix, "ct-carol-"+suffix+"@example.com")
	loginIntegrationUser(t, admin, "admin_integration", "admin-password-123")
	guest.refreshCSRF()

	aliceName := "ct_alice_" + suffix
	bobName := "ct_bob_" + suffix

	// --- 公开帖：话题标签、提及、时间线可见性 ---
	keyword := "integration" + suffix
	status, _, payload, raw := alice.call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": fmt.Sprintf("<p>你好 #话题%s# 与 @%s</p>", suffix, bobName),
		"visibility":     "public",
		"action":         "publish",
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	publicPost := integrationNested(payload, t, "post")
	publicPostID := integrationString(publicPost, t, "id")
	if got := integrationString(publicPost, t, "status"); got != "published" {
		t.Fatalf("帖子状态应为 published，实际 %s", got)
	}
	if html := integrationString(publicPost, t, "content_html"); !strings.Contains(html, `class="tl-tag"`) ||
		!strings.Contains(html, `class="tl-mention"`) {
		t.Fatalf("正文应包含可点击的话题与提及链接: %s", html)
	}

	// 游客可见、未登录也能读
	status, _, payload, raw = guest.call(http.MethodGet, "/api/v1/posts/"+publicPostID, false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	status, _, payload, raw = guest.call(http.MethodGet, "/api/v1/timeline?filter=all", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(payload, "id", publicPostID) {
		t.Fatalf("全站时间线应包含公开帖: %v", payload)
	}
	if _, _, _, raw := guest.call(http.MethodGet, "/api/v1/timeline?filter=ai", false, nil); !strings.Contains(raw, `"items"`) {
		t.Fatalf("AI 筛选返回异常: %s", raw)
	}

	// 提及通知：被提及者收到通知
	status, _, payload, raw = bob.call(http.MethodGet, "/api/v1/notifications?filter=all", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListHasType(payload, "mention") {
		t.Fatalf("被提及用户应收到 mention 通知: %v", payload)
	}
	status, _, payload, raw = bob.call(http.MethodGet, "/api/v1/notifications/unread-count", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if count, ok := payload["unread_count"].(float64); !ok || count < 1 {
		t.Fatalf("未读通知数量异常: %v", payload)
	}

	// --- 标签聚合与搜索：权限过滤 ---
	status, _, payload, raw = guest.call(http.MethodGet, "/api/v1/tags/话题"+suffix+"/posts", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(payload, "id", publicPostID) {
		t.Fatalf("标签聚合应包含公开帖: %v", payload)
	}
	status, _, payload, raw = guest.call(http.MethodGet, "/api/v1/search/posts?q="+keyword, false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)

	// --- 仅关注者 / 仅自己可见性 ---
	status, _, followersPostPayload, raw := alice.call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>只有关注者能看到</p>",
		"visibility":     "followers",
		"action":         "publish",
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	followersPostID := integrationString(integrationNested(followersPostPayload, t, "post"), t, "id")

	status, _, _, raw = guest.call(http.MethodGet, "/api/v1/posts/"+followersPostID, false, nil)
	expectIntegrationStatus(t, status, http.StatusNotFound, raw)
	status, _, _, raw = bob.call(http.MethodGet, "/api/v1/posts/"+followersPostID, false, nil)
	expectIntegrationStatus(t, status, http.StatusNotFound, raw)

	// 关注后可见；且对方收到关注通知
	status, _, followPayload, raw := bob.call(http.MethodPost, "/api/v1/users/"+aliceName+"/follow", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if state := integrationString(followPayload, t, "follow_state"); state != "following" {
		t.Fatalf("关注状态应为 following，实际 %s", state)
	}
	status, _, _, raw = bob.call(http.MethodGet, "/api/v1/posts/"+followersPostID, false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	status, _, followTimeline, raw := bob.call(http.MethodGet, "/api/v1/timeline/following", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(followTimeline, "id", publicPostID) {
		t.Fatalf("关注时间线应包含被关注作者的公开帖: %v", followTimeline)
	}
	status, _, _, raw = carol.call(http.MethodGet, "/api/v1/posts/"+followersPostID, false, nil)
	expectIntegrationStatus(t, status, http.StatusNotFound, raw)

	status, _, privatePostPayload, raw := alice.call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>仅自己可见 " + keyword + "</p>",
		"visibility":     "private",
		"action":         "publish",
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	privatePostID := integrationString(integrationNested(privatePostPayload, t, "post"), t, "id")
	status, _, _, raw = bob.call(http.MethodGet, "/api/v1/posts/"+privatePostID, false, nil)
	expectIntegrationStatus(t, status, http.StatusNotFound, raw)
	status, _, searchPayload, raw := guest.call(http.MethodGet, "/api/v1/search/posts?q="+keyword, false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if integrationListContains(searchPayload, "id", privatePostID) {
		t.Fatalf("搜索不得返回仅自己可见的帖子: %v", searchPayload)
	}
	status, _, _, raw = guest.call(http.MethodGet, "/api/v1/search/posts?q=", false, nil)
	expectIntegrationStatus(t, status, http.StatusBadRequest, raw)

	// 非公开帖不允许转发
	status, _, _, raw = bob.call(http.MethodPost, "/api/v1/posts/"+privatePostID+"/repost", true, nil)
	if status == http.StatusOK {
		t.Fatalf("仅自己可见的帖子不应允许转发: %s", raw)
	}

	// --- 点赞、评论、回复、转发 ---
	status, _, likePayload, raw := bob.call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/like", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if count, _ := likePayload["like_count"].(float64); count != 1 {
		t.Fatalf("点赞计数应为 1: %v", likePayload)
	}
	status, _, likePayload, raw = bob.call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/like", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if count, _ := likePayload["like_count"].(float64); count != 1 {
		t.Fatalf("重复点赞必须幂等: %v", likePayload)
	}
	status, _, likePayload, raw = bob.call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/unlike", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if count, _ := likePayload["like_count"].(float64); count != 0 {
		t.Fatalf("取消点赞后计数应为 0: %v", likePayload)
	}

	status, _, commentPayload, raw := bob.call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/comments", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>第一条评论</p>",
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	commentID := integrationString(integrationNested(commentPayload, t, "comment"), t, "id")

	status, _, replyPayload, raw := carol.call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/comments", true, map[string]any{
		"content_format":    "richtext",
		"content_source":    "<p>回复一下</p>",
		"parent_comment_id": commentID,
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	reply := integrationNested(replyPayload, t, "comment")
	if integrationString(reply, t, "root_comment_id") != commentID {
		t.Fatalf("回复的 root_comment_id 应为顶层评论: %v", reply)
	}

	status, _, comments, raw := guest.call(http.MethodGet, "/api/v1/posts/"+publicPostID+"/comments", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(comments, "id", commentID) {
		t.Fatalf("评论列表应包含顶层评论: %v", comments)
	}

	status, _, _, raw = bob.call(http.MethodPost, "/api/v1/comments/"+commentID+"/delete", true, nil)
	expectIntegrationStatus(t, status, http.StatusNoContent, raw)
	status, _, comments, raw = guest.call(http.MethodGet, "/api/v1/posts/"+publicPostID+"/comments", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListHasStatus(comments, "deleted") {
		t.Fatalf("已删除评论应保留占位: %v", comments)
	}

	status, _, repostPayload, raw := bob.call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/repost", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if reposted, _ := repostPayload["reposted"].(bool); !reposted {
		t.Fatalf("转发状态异常: %v", repostPayload)
	}
	status, _, timelinePayload, raw := guest.call(http.MethodGet, "/api/v1/timeline?filter=all", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListHasKind(timelinePayload, "repost") {
		t.Fatalf("时间线应包含纯转发项: %v", timelinePayload)
	}

	// --- 草稿、冲突与定时 ---
	status, _, draftPayload, raw := alice.call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>草稿内容</p>",
		"visibility":     "public",
		"action":         "draft",
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	draft := integrationNested(draftPayload, t, "post")
	draftID := integrationString(draft, t, "id")
	if revision, _ := draft["revision"].(float64); revision != 1 {
		t.Fatalf("草稿初始版本号应为 1: %v", draft)
	}

	status, _, saved, raw := alice.call(http.MethodPost, "/api/v1/posts/"+draftID, true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>草稿内容（自动保存）</p>",
		"visibility":     "public",
		"revision":       1,
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if revision := saved["post"].(map[string]any)["revision"].(float64); revision != 2 {
		t.Fatalf("保存后版本号应为 2: %v", saved)
	}

	status, _, conflict, raw := alice.call(http.MethodPost, "/api/v1/posts/"+draftID, true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>基于旧版本的保存</p>",
		"visibility":     "public",
		"revision":       1,
	})
	expectIntegrationStatus(t, status, http.StatusConflict, raw)
	if code := integrationErrorCode(conflict); code != "draft_conflict" {
		t.Fatalf("版本落后应返回 draft_conflict: %v", conflict)
	}

	status, _, drafts, raw := alice.call(http.MethodGet, "/api/v1/me/drafts", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(drafts, "id", draftID) {
		t.Fatalf("草稿列表应包含刚保存的草稿: %v", drafts)
	}
	status, _, _, raw = guest.call(http.MethodGet, "/api/v1/posts/"+draftID, false, nil)
	expectIntegrationStatus(t, status, http.StatusNotFound, raw)

	status, _, scheduled, raw := alice.call(http.MethodPost, "/api/v1/posts/"+draftID+"/schedule", true, map[string]any{
		"scheduled_at": time.Now().Add(2 * time.Hour).Format(time.RFC3339),
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if head := scheduled["post"].(map[string]any); integrationString(head, t, "status") != "scheduled" {
		t.Fatalf("设置定时后状态应为 scheduled: %v", scheduled)
	}
	status, _, scheduledList, raw := alice.call(http.MethodGet, "/api/v1/me/scheduled", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(scheduledList, "id", draftID) {
		t.Fatalf("定时列表应包含该帖: %v", scheduledList)
	}
	status, _, scheduled, raw = alice.call(http.MethodPost, "/api/v1/posts/"+draftID+"/cancel-schedule", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if head := scheduled["post"].(map[string]any); integrationString(head, t, "status") != "draft" {
		t.Fatalf("取消定时后应回到草稿: %v", scheduled)
	}

	status, _, published, raw := alice.call(http.MethodPost, "/api/v1/posts/"+draftID+"/publish", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if head := published["post"].(map[string]any); integrationString(head, t, "status") != "published" {
		t.Fatalf("发布草稿后状态应为 published: %v", published)
	}

	// --- 编辑历史与恢复 ---
	status, _, edited, raw := alice.call(http.MethodPost, "/api/v1/posts/"+draftID, true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>发布后的第二次编辑</p>",
		"visibility":     "public",
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	versionNumber, _ := edited["post"].(map[string]any)["version_number"].(float64)
	if versionNumber < 2 {
		t.Fatalf("已发布帖编辑后应生成新版本: %v", edited)
	}
	status, _, versions, raw := guest.call(http.MethodGet, "/api/v1/posts/"+draftID+"/versions", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	items, _ := versions["items"].([]any)
	if len(items) < 2 {
		t.Fatalf("历史版本应至少有两条: %v", versions)
	}
	firstVersionID := items[len(items)-1].(map[string]any)["id"].(string)
	status, _, _, raw = guest.call(http.MethodGet, "/api/v1/posts/"+draftID+"/versions/"+firstVersionID, false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	status, _, restored, raw := alice.call(http.MethodPost,
		"/api/v1/posts/"+draftID+"/versions/"+firstVersionID+"/restore", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if number, _ := restored["post"].(map[string]any)["version_number"].(float64); number <= versionNumber {
		t.Fatalf("恢复历史后应生成更新的版本: %v", restored)
	}

	// 非作者不能编辑、不能恢复
	status, _, _, raw = bob.call(http.MethodPost, "/api/v1/posts/"+draftID, true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>越权编辑</p>",
		"visibility":     "public",
	})
	if status == http.StatusOK {
		t.Fatalf("他人不应能编辑该帖子: %s", raw)
	}

	// --- 附件 ---
	attachmentID := uploadIntegrationPNG(t, alice)
	status, _, _, raw = alice.call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>带附件的帖子</p>",
		"visibility":     "public",
		"action":         "publish",
		"attachment_ids": []string{attachmentID},
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	// 附件内容接口返回二进制，不走 JSON 解析。
	rawStatus, contentType, bodyHead := rawIntegrationGET(t, guest, "/api/v1/attachments/"+attachmentID+"/content")
	if rawStatus != http.StatusOK {
		t.Fatalf("读取附件内容失败: status=%d", rawStatus)
	}
	if !strings.HasPrefix(contentType, "image/png") {
		t.Fatalf("附件内容类型异常: %q", contentType)
	}
	if len(bodyHead) == 0 || bodyHead[0] != 0x89 {
		t.Fatalf("附件内容不是 PNG: %v", bodyHead)
	}

	// --- 管理后台内容审核 ---
	status, _, adminPosts, raw := admin.call(http.MethodGet, "/api/v1/admin/posts?visibility=private", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if !integrationListContains(adminPosts, "id", privatePostID) {
		t.Fatalf("管理员应能查询到仅自己可见的帖子: %v", adminPosts)
	}
	status, _, _, raw = admin.call(http.MethodGet, "/api/v1/admin/posts/"+privatePostID, false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	status, _, overview, raw := admin.call(http.MethodGet, "/api/v1/admin/overview", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if users, ok := overview["users"].(map[string]any); !ok || users["total"] == nil {
		t.Fatalf("管理概览缺少用户统计: %v", overview)
	}
	status, _, _, raw = bob.call(http.MethodGet, "/api/v1/admin/posts", false, nil)
	expectIntegrationStatus(t, status, http.StatusForbidden, raw)

	status, _, _, raw = admin.call(http.MethodPost, "/api/v1/admin/posts/"+privatePostID+"/delete", true, nil)
	expectIntegrationStatus(t, status, http.StatusNoContent, raw)
	// 普通入口（作者与游客）都不再可见；管理员保留审核可见性并看到 deleted 状态。
	status, _, _, raw = guest.call(http.MethodGet, "/api/v1/posts/"+privatePostID, false, nil)
	expectIntegrationStatus(t, status, http.StatusNotFound, raw)
	status, _, deletedView, raw := admin.call(http.MethodGet, "/api/v1/posts/"+privatePostID, false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if head := deletedView["post"].(map[string]any); integrationString(head, t, "status") != "deleted" {
		t.Fatalf("管理员审核视角应看到 deleted 状态: %v", deletedView)
	}

	status, _, audits, raw := admin.call(http.MethodGet, "/api/v1/admin/audit-logs?action_type=post_delete", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if items, _ := audits["items"].([]any); len(items) == 0 {
		t.Fatalf("管理员删除帖子必须写入审计: %v", audits)
	}

	// 作者删除自己的帖子：普通入口不再可见
	status, _, _, raw = alice.call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/delete", true, nil)
	expectIntegrationStatus(t, status, http.StatusNoContent, raw)
	status, _, _, raw = guest.call(http.MethodGet, "/api/v1/posts/"+publicPostID, false, nil)
	expectIntegrationStatus(t, status, http.StatusNotFound, raw)
	status, _, timelinePayload, raw = guest.call(http.MethodGet, "/api/v1/timeline?filter=all", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if integrationListContains(timelinePayload, "id", publicPostID) {
		t.Fatalf("已删除帖子不应出现在时间线: %v", timelinePayload)
	}
}

// --- 测试辅助 ---

// registerIntegrationUser 注册并登录：注册接口只创建账号，不下发会话，
// 后续写操作需要登录后的会话 CSRF 令牌。
func registerIntegrationUser(t *testing.T, client *integrationClient, username, email string) {
	t.Helper()
	client.refreshCSRF()
	status, _, payload, raw := client.call(http.MethodPost, "/api/v1/auth/register", true, map[string]any{
		"username": username,
		"email":    email,
		"password": "user-password-123",
	})
	expectIntegrationStatus(t, status, http.StatusCreated, raw)
	integrationUser(payload, t)
	loginIntegrationUser(t, client, username, "user-password-123")
}

func loginIntegrationUser(t *testing.T, client *integrationClient, identifier, password string) {
	t.Helper()
	client.refreshCSRF()
	status, _, payload, raw := client.call(http.MethodPost, "/api/v1/auth/login", true, map[string]any{
		"identifier": identifier,
		"password":   password,
	})
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	integrationCSRF(payload, t, client)
}

// uploadIntegrationPNG 上传一张 8x8 的合法 PNG，返回附件 ID。
func uploadIntegrationPNG(t *testing.T, client *integrationClient) string {
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
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("上传附件失败: status=%d body=%s", resp.StatusCode, string(data))
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("上传响应不是合法 JSON: %v", err)
	}
	return integrationString(integrationNested(payload, t, "attachment"), t, "id")
}

// rawIntegrationGET 发起原始 GET，返回状态码、内容类型与前 16 字节，用于二进制响应。
func rawIntegrationGET(t *testing.T, client *integrationClient, path string) (int, string, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, client.base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	head := make([]byte, 16)
	n, _ := io.ReadFull(resp.Body, head)
	return resp.StatusCode, resp.Header.Get("Content-Type"), head[:n]
}

func integrationNested(payload map[string]any, t *testing.T, key string) map[string]any {
	t.Helper()
	value, ok := payload[key].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 %s: %v", key, payload)
	}
	return value
}

func integrationString(payload map[string]any, t *testing.T, key string) string {
	t.Helper()
	value, ok := payload[key].(string)
	if !ok || value == "" {
		t.Fatalf("响应缺少字符串字段 %s: %v", key, payload)
	}
	return value
}

func integrationErrorCode(payload map[string]any) string {
	if errObj, ok := payload["error"].(map[string]any); ok {
		if code, ok := errObj["code"].(string); ok {
			return code
		}
	}
	return ""
}

func integrationListContains(payload map[string]any, key, want string) bool {
	items, _ := payload["items"].([]any)
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if value, ok := entry[key].(string); ok && value == want {
			return true
		}
	}
	return false
}

func integrationListHasType(payload map[string]any, want string) bool {
	return integrationListContains(payload, "type", want)
}

func integrationListHasStatus(payload map[string]any, want string) bool {
	return integrationListContains(payload, "status", want)
}

func integrationListHasKind(payload map[string]any, want string) bool {
	return integrationListContains(payload, "kind", want)
}
