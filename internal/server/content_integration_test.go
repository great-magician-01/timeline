package server_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestContentIntegration 覆盖帖子、草稿、定时、编辑历史、附件、可见性、时间线、
// 关注、点赞、评论、转发、通知、标签与搜索、以及管理后台内容审核。
// 与账号集成测试相同：只在隔离测试 schema 中执行，默认跳过，需 TIMELINE_INTEGRATION=1。
func TestContentIntegration(t *testing.T) {
	srv := testkit.NewIntegrationServer(t)

	suffix := testkit.IntegrationSuffix()
	alice := srv.Client(t)
	bob := srv.Client(t)
	carol := srv.Client(t)
	admin := srv.Client(t)
	guest := srv.Client(t)

	alice.RegisterAndLogin("ct_alice_"+suffix, "ct-alice-"+suffix+"@example.com")
	bob.RegisterAndLogin("ct_bob_"+suffix, "ct-bob-"+suffix+"@example.com")
	carol.RegisterAndLogin("ct_carol_"+suffix, "ct-carol-"+suffix+"@example.com")
	admin.LoginAndSetCSRF(testkit.IntegrationAdminUsername, testkit.IntegrationAdminPassword)
	guest.RefreshCSRF()

	aliceName := "ct_alice_" + suffix
	bobName := "ct_bob_" + suffix

	// --- 公开帖：话题标签、提及、时间线可见性 ---
	keyword := "integration" + suffix
	status, _, payload, raw := alice.Call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": fmt.Sprintf("<p>你好 #话题%s# 与 @%s</p>", suffix, bobName),
		"visibility":     "public",
		"action":         "publish",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	publicPost := testkit.MustNested(t, payload, "post")
	publicPostID := testkit.MustString(t, publicPost, "id")
	if got := testkit.MustString(t, publicPost, "status"); got != "published" {
		t.Fatalf("帖子状态应为 published，实际 %s", got)
	}
	if html := testkit.MustString(t, publicPost, "content_html"); !strings.Contains(html, `class="tl-tag"`) ||
		!strings.Contains(html, `class="tl-mention"`) {
		t.Fatalf("正文应包含可点击的话题与提及链接: %s", html)
	}

	// 游客可见、未登录也能读
	status, _, payload, raw = guest.Call(http.MethodGet, "/api/v1/posts/"+publicPostID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, payload, raw = guest.Call(http.MethodGet, "/api/v1/timeline?filter=all", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListContains(payload, "id", publicPostID) {
		t.Fatalf("全站时间线应包含公开帖: %v", payload)
	}
	if _, _, _, raw := guest.Call(http.MethodGet, "/api/v1/timeline?filter=ai", false, nil); !strings.Contains(raw, `"items"`) {
		t.Fatalf("AI 筛选返回异常: %s", raw)
	}

	// 提及通知：被提及者收到通知
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/notifications?filter=all", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListHasType(payload, "mention") {
		t.Fatalf("被提及用户应收到 mention 通知: %v", payload)
	}
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/notifications/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if count, ok := payload["unread_count"].(float64); !ok || count < 1 {
		t.Fatalf("未读通知数量异常: %v", payload)
	}

	// --- 标签聚合与搜索：权限过滤 ---
	status, _, payload, raw = guest.Call(http.MethodGet, "/api/v1/tags/话题"+suffix+"/posts", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListContains(payload, "id", publicPostID) {
		t.Fatalf("标签聚合应包含公开帖: %v", payload)
	}
	status, _, payload, raw = guest.Call(http.MethodGet, "/api/v1/search/posts?q="+keyword, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	// --- 仅关注者 / 仅自己可见性 ---
	status, _, followersPostPayload, raw := alice.Call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>只有关注者能看到</p>",
		"visibility":     "followers",
		"action":         "publish",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	followersPostID := testkit.MustString(t, testkit.MustNested(t, followersPostPayload, "post"), "id")

	status, _, _, raw = guest.Call(http.MethodGet, "/api/v1/posts/"+followersPostID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, _, raw = bob.Call(http.MethodGet, "/api/v1/posts/"+followersPostID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)

	// 关注后可见；且对方收到关注通知
	status, _, followPayload, raw := bob.Call(http.MethodPost, "/api/v1/users/"+aliceName+"/follow", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if state := testkit.MustString(t, followPayload, "follow_state"); state != "following" {
		t.Fatalf("关注状态应为 following，实际 %s", state)
	}
	status, _, _, raw = bob.Call(http.MethodGet, "/api/v1/posts/"+followersPostID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, followTimeline, raw := bob.Call(http.MethodGet, "/api/v1/timeline/following", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListContains(followTimeline, "id", publicPostID) {
		t.Fatalf("关注时间线应包含被关注作者的公开帖: %v", followTimeline)
	}
	status, _, _, raw = carol.Call(http.MethodGet, "/api/v1/posts/"+followersPostID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)

	status, _, privatePostPayload, raw := alice.Call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>仅自己可见 " + keyword + "</p>",
		"visibility":     "private",
		"action":         "publish",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	privatePostID := testkit.MustString(t, testkit.MustNested(t, privatePostPayload, "post"), "id")
	status, _, _, raw = bob.Call(http.MethodGet, "/api/v1/posts/"+privatePostID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, searchPayload, raw := guest.Call(http.MethodGet, "/api/v1/search/posts?q="+keyword, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if testkit.ListContains(searchPayload, "id", privatePostID) {
		t.Fatalf("搜索不得返回仅自己可见的帖子: %v", searchPayload)
	}
	status, _, _, raw = guest.Call(http.MethodGet, "/api/v1/search/posts?q=", false, nil)
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)

	// 非公开帖不允许转发
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/posts/"+privatePostID+"/repost", true, nil)
	if status == http.StatusOK {
		t.Fatalf("仅自己可见的帖子不应允许转发: %s", raw)
	}

	// --- 点赞、评论、回复、转发 ---
	status, _, likePayload, raw := bob.Call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/like", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if count, _ := likePayload["like_count"].(float64); count != 1 {
		t.Fatalf("点赞计数应为 1: %v", likePayload)
	}
	status, _, likePayload, raw = bob.Call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/like", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if count, _ := likePayload["like_count"].(float64); count != 1 {
		t.Fatalf("重复点赞必须幂等: %v", likePayload)
	}
	status, _, likePayload, raw = bob.Call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/unlike", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if count, _ := likePayload["like_count"].(float64); count != 0 {
		t.Fatalf("取消点赞后计数应为 0: %v", likePayload)
	}

	status, _, commentPayload, raw := bob.Call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/comments", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>第一条评论</p>",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	commentID := testkit.MustString(t, testkit.MustNested(t, commentPayload, "comment"), "id")

	status, _, replyPayload, raw := carol.Call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/comments", true, map[string]any{
		"content_format":    "richtext",
		"content_source":    "<p>回复一下</p>",
		"parent_comment_id": commentID,
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	reply := testkit.MustNested(t, replyPayload, "comment")
	if testkit.MustString(t, reply, "root_comment_id") != commentID {
		t.Fatalf("回复的 root_comment_id 应为顶层评论: %v", reply)
	}

	status, _, comments, raw := guest.Call(http.MethodGet, "/api/v1/posts/"+publicPostID+"/comments", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListContains(comments, "id", commentID) {
		t.Fatalf("评论列表应包含顶层评论: %v", comments)
	}

	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/comments/"+commentID+"/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, comments, raw = guest.Call(http.MethodGet, "/api/v1/posts/"+publicPostID+"/comments", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListHasStatus(comments, "deleted") {
		t.Fatalf("已删除评论应保留占位: %v", comments)
	}

	status, _, repostPayload, raw := bob.Call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/repost", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if reposted, _ := repostPayload["reposted"].(bool); !reposted {
		t.Fatalf("转发状态异常: %v", repostPayload)
	}
	status, _, timelinePayload, raw := guest.Call(http.MethodGet, "/api/v1/timeline?filter=all", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListHasKind(timelinePayload, "repost") {
		t.Fatalf("时间线应包含纯转发项: %v", timelinePayload)
	}

	// --- 草稿、冲突与定时 ---
	status, _, draftPayload, raw := alice.Call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>草稿内容</p>",
		"visibility":     "public",
		"action":         "draft",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	draft := testkit.MustNested(t, draftPayload, "post")
	draftID := testkit.MustString(t, draft, "id")
	if revision, _ := draft["revision"].(float64); revision != 1 {
		t.Fatalf("草稿初始版本号应为 1: %v", draft)
	}

	status, _, saved, raw := alice.Call(http.MethodPost, "/api/v1/posts/"+draftID, true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>草稿内容（自动保存）</p>",
		"visibility":     "public",
		"revision":       1,
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if revision := saved["post"].(map[string]any)["revision"].(float64); revision != 2 {
		t.Fatalf("保存后版本号应为 2: %v", saved)
	}

	status, _, conflict, raw := alice.Call(http.MethodPost, "/api/v1/posts/"+draftID, true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>基于旧版本的保存</p>",
		"visibility":     "public",
		"revision":       1,
	})
	testkit.ExpectStatus(t, status, http.StatusConflict, raw)
	if code := testkit.ErrorCode(conflict); code != "draft_conflict" {
		t.Fatalf("版本落后应返回 draft_conflict: %v", conflict)
	}

	status, _, drafts, raw := alice.Call(http.MethodGet, "/api/v1/me/drafts", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListContains(drafts, "id", draftID) {
		t.Fatalf("草稿列表应包含刚保存的草稿: %v", drafts)
	}
	status, _, _, raw = guest.Call(http.MethodGet, "/api/v1/posts/"+draftID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)

	status, _, scheduled, raw := alice.Call(http.MethodPost, "/api/v1/posts/"+draftID+"/schedule", true, map[string]any{
		"scheduled_at": time.Now().Add(2 * time.Hour).Format(time.RFC3339),
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if head := scheduled["post"].(map[string]any); testkit.MustString(t, head, "status") != "scheduled" {
		t.Fatalf("设置定时后状态应为 scheduled: %v", scheduled)
	}
	status, _, scheduledList, raw := alice.Call(http.MethodGet, "/api/v1/me/scheduled", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListContains(scheduledList, "id", draftID) {
		t.Fatalf("定时列表应包含该帖: %v", scheduledList)
	}
	status, _, scheduled, raw = alice.Call(http.MethodPost, "/api/v1/posts/"+draftID+"/cancel-schedule", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if head := scheduled["post"].(map[string]any); testkit.MustString(t, head, "status") != "draft" {
		t.Fatalf("取消定时后应回到草稿: %v", scheduled)
	}

	status, _, published, raw := alice.Call(http.MethodPost, "/api/v1/posts/"+draftID+"/publish", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if head := published["post"].(map[string]any); testkit.MustString(t, head, "status") != "published" {
		t.Fatalf("发布草稿后状态应为 published: %v", published)
	}

	// --- 编辑历史与恢复 ---
	status, _, edited, raw := alice.Call(http.MethodPost, "/api/v1/posts/"+draftID, true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>发布后的第二次编辑</p>",
		"visibility":     "public",
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	versionNumber, _ := edited["post"].(map[string]any)["version_number"].(float64)
	if versionNumber < 2 {
		t.Fatalf("已发布帖编辑后应生成新版本: %v", edited)
	}
	status, _, versions, raw := guest.Call(http.MethodGet, "/api/v1/posts/"+draftID+"/versions", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	items, _ := versions["items"].([]any)
	if len(items) < 2 {
		t.Fatalf("历史版本应至少有两条: %v", versions)
	}
	firstVersionID := items[len(items)-1].(map[string]any)["id"].(string)
	status, _, _, raw = guest.Call(http.MethodGet, "/api/v1/posts/"+draftID+"/versions/"+firstVersionID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, restored, raw := alice.Call(http.MethodPost,
		"/api/v1/posts/"+draftID+"/versions/"+firstVersionID+"/restore", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if number, _ := restored["post"].(map[string]any)["version_number"].(float64); number <= versionNumber {
		t.Fatalf("恢复历史后应生成更新的版本: %v", restored)
	}

	// 非作者不能编辑、不能恢复
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/posts/"+draftID, true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>越权编辑</p>",
		"visibility":     "public",
	})
	if status == http.StatusOK {
		t.Fatalf("他人不应能编辑该帖子: %s", raw)
	}

	// --- 附件 ---
	attachmentID := alice.Upload()
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>带附件的帖子</p>",
		"visibility":     "public",
		"action":         "publish",
		"attachment_ids": []string{attachmentID},
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	// 附件内容接口返回二进制，不走 JSON 解析。
	rawStatus, contentType, bodyHead := guest.RawGET("/api/v1/attachments/" + attachmentID + "/content")
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
	status, _, adminPosts, raw := admin.Call(http.MethodGet, "/api/v1/admin/posts?visibility=private", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListContains(adminPosts, "id", privatePostID) {
		t.Fatalf("管理员应能查询到仅自己可见的帖子: %v", adminPosts)
	}
	status, _, _, raw = admin.Call(http.MethodGet, "/api/v1/admin/posts/"+privatePostID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, overview, raw := admin.Call(http.MethodGet, "/api/v1/admin/overview", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if users, ok := overview["users"].(map[string]any); !ok || users["total"] == nil {
		t.Fatalf("管理概览缺少用户统计: %v", overview)
	}
	status, _, _, raw = bob.Call(http.MethodGet, "/api/v1/admin/posts", false, nil)
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)

	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/posts/"+privatePostID+"/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	// 普通入口（作者与游客）都不再可见；管理员保留审核可见性并看到 deleted 状态。
	status, _, _, raw = guest.Call(http.MethodGet, "/api/v1/posts/"+privatePostID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, deletedView, raw := admin.Call(http.MethodGet, "/api/v1/posts/"+privatePostID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if head := deletedView["post"].(map[string]any); testkit.MustString(t, head, "status") != "deleted" {
		t.Fatalf("管理员审核视角应看到 deleted 状态: %v", deletedView)
	}

	status, _, audits, raw := admin.Call(http.MethodGet, "/api/v1/admin/audit-logs?action_type=post_delete", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items, _ := audits["items"].([]any); len(items) == 0 {
		t.Fatalf("管理员删除帖子必须写入审计: %v", audits)
	}

	// 作者删除自己的帖子：普通入口不再可见
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts/"+publicPostID+"/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, _, raw = guest.Call(http.MethodGet, "/api/v1/posts/"+publicPostID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, timelinePayload, raw = guest.Call(http.MethodGet, "/api/v1/timeline?filter=all", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if testkit.ListContains(timelinePayload, "id", publicPostID) {
		t.Fatalf("已删除帖子不应出现在时间线: %v", timelinePayload)
	}
}
