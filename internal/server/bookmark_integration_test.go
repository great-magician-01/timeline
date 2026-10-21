package server_test

import (
	"net/http"
	"testing"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestBookmarkIntegration 覆盖收藏帖子（0023）：收藏/取消/幂等、viewer_state 标记、
// 收藏夹 CRUD 与上限、删除夹归还默认、移动归属、游客/停用拒绝、列表可见性过滤、
// 管理后台无收藏数据。只在隔离测试 schema 中执行，默认跳过，需 TIMELINE_INTEGRATION=1。
func TestBookmarkIntegration(t *testing.T) {
	srv := testkit.NewIntegrationServer(t)
	suffix := testkit.IntegrationShortSuffix()

	alice := srv.Client(t)
	aliceName := "bm_alice_" + suffix
	aliceUser := alice.RegisterAndLogin(aliceName, "bm-alice-"+suffix+"@example.com")

	bob := srv.Client(t)
	bobName := "bm_bob_" + suffix
	bobUser := bob.RegisterAndLogin(bobName, "bm-bob-"+suffix+"@example.com")
	bobID := testkit.MustString(t, bobUser, "id")

	carol := srv.Client(t)
	carol.RegisterAndLogin("bm_carol_"+suffix, "bm-carol-"+suffix+"@example.com")

	guest := srv.Client(t)
	guest.RefreshCSRF()

	admin := srv.Client(t)
	admin.LoginAndSetCSRF(testkit.IntegrationAdminUsername, testkit.IntegrationAdminPassword)

	publish := func(t *testing.T, c *testkit.Client, text, visibility string) string {
		t.Helper()
		status, _, payload, raw := c.Call(http.MethodPost, "/api/v1/posts", true, map[string]any{
			"content_format": "richtext",
			"content_source": "<p>" + text + "</p>",
			"visibility":     visibility,
			"action":         "publish",
		})
		testkit.ExpectStatus(t, status, http.StatusCreated, raw)
		return testkit.MustString(t, testkit.MustNested(t, payload, "post"), "id")
	}
	// listIDs 返回收藏列表当前页的帖子 ID。
	listIDs := func(t *testing.T, c *testkit.Client, query string) []string {
		t.Helper()
		status, _, payload, raw := c.Call(http.MethodGet, "/api/v1/me/bookmarks"+query, false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
		items, _ := payload["items"].([]any)
		ids := make([]string, 0, len(items))
		for _, item := range items {
			entry, _ := item.(map[string]any)
			if entry != nil {
				ids = append(ids, testkit.MustString(t, entry, "id"))
			}
		}
		return ids
	}
	bookmarkState := func(t *testing.T, payload map[string]any) (bool, any) {
		t.Helper()
		bookmarked, _ := payload["bookmarked"].(bool)
		return bookmarked, payload["folder_id"]
	}

	alicePost := publish(t, alice, "Alice 的公开帖 "+suffix, "public")
	bobPost := publish(t, bob, "Bob 的公开帖 "+suffix, "public")
	bobFollowersPost := publish(t, bob, "Bob 的仅关注者帖 "+suffix, "followers")

	// ── 游客拒绝 ──
	var (
		status  int
		payload map[string]any
		raw     string
	)
	status, _, _, raw = guest.Call(http.MethodPost, "/api/v1/posts/"+bobPost+"/bookmark", true, nil)
	testkit.ExpectStatus(t, status, http.StatusUnauthorized, raw)
	status, _, _, raw = guest.Call(http.MethodGet, "/api/v1/me/bookmarks", false, nil)
	testkit.ExpectStatus(t, status, http.StatusUnauthorized, raw)

	// ── 收藏/取消/幂等 ──
	status, _, payload, raw = carol.Call(http.MethodPost, "/api/v1/posts/"+bobPost+"/bookmark", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	bookmarked, folderID := bookmarkState(t, payload)
	if !bookmarked || folderID != nil {
		t.Fatalf("收藏响应 = (%v, %v)，期望 (true, nil)", bookmarked, folderID)
	}
	status, _, payload, raw = carol.Call(http.MethodPost, "/api/v1/posts/"+bobPost+"/bookmark", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	bookmarked, _ = bookmarkState(t, payload)
	if !bookmarked {
		t.Fatalf("重复收藏应幂等: %v", payload)
	}

	// 时间线与详情的 viewer_state.bookmarked 一致。
	status, _, payload, raw = carol.Call(http.MethodGet, "/api/v1/timeline", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListContains(payload, "id", bobPost) {
		t.Fatalf("时间线应包含 Bob 的帖子: %s", raw)
	}
	if entry := bookmarkEntry(t, payload, bobPost); entry == nil {
		t.Fatalf("时间线条目应存在: %s", raw)
	} else if viewerState, _ := entry["viewer_state"].(map[string]any); viewerState == nil || viewerState["bookmarked"] != true {
		t.Fatalf("时间线 viewer_state.bookmarked 应为 true: %v", entry)
	}
	status, _, payload, raw = carol.Call(http.MethodGet, "/api/v1/posts/"+bobPost, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	detailViewerState := testkit.MustNested(t, testkit.MustNested(t, payload, "post"), "viewer_state")
	if detailViewerState["bookmarked"] != true {
		t.Fatalf("详情 viewer_state.bookmarked 应为 true: %v", detailViewerState)
	}

	status, _, payload, raw = carol.Call(http.MethodPost, "/api/v1/posts/"+bobPost+"/unbookmark", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	bookmarked, _ = bookmarkState(t, payload)
	if bookmarked {
		t.Fatalf("取消后 bookmarked 应为 false: %v", payload)
	}
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/posts/"+bobPost+"/unbookmark", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if ids := listIDs(t, carol, ""); len(ids) != 0 {
		t.Fatalf("取消后收藏列表应为空，实际 %v", ids)
	}

	// ── 收藏夹 CRUD 与上限 ──
	createFolder := func(t *testing.T, c *testkit.Client, name string) (int, map[string]any, string) {
		t.Helper()
		status, _, payload, raw := c.Call(http.MethodPost, "/api/v1/me/bookmark-folders", true, map[string]any{"name": name})
		return status, payload, raw
	}
	status, payload, raw = createFolder(t, carol, "技术文章")
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	folderA := testkit.MustString(t, payload, "id")
	status, payload, raw = createFolder(t, carol, "读书笔记")
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	folderB := testkit.MustString(t, payload, "id")

	status, _, payload, raw = carol.Call(http.MethodGet, "/api/v1/me/bookmark-folders", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	folders, _ := payload["items"].([]any)
	if len(folders) != 2 {
		t.Fatalf("收藏夹数量 = %d，期望 2（新建在前）: %s", len(folders), raw)
	}
	if got := testkit.MustString(t, folders[0].(map[string]any), "name"); got != "读书笔记" {
		t.Fatalf("新建收藏夹应排最前，实际 %q", got)
	}

	status, _, payload, raw = carol.Call(http.MethodPost, "/api/v1/me/bookmark-folders/"+folderA, true,
		map[string]any{"name": "技术收藏"})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := testkit.MustString(t, payload, "name"); got != "技术收藏" {
		t.Fatalf("重命名结果 = %q，期望 技术收藏", got)
	}

	// 他人收藏夹按 404（不暴露存在性）。
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/me/bookmark-folders/"+folderA, true,
		map[string]any{"name": "抢名字"})
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/me/bookmark-folders/"+folderA+"/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)

	// 上限 20：第 21 个返回 409 folder_limit。
	for i := 2; i < 20; i++ {
		status, _, raw = createFolder(t, carol, "批量夹"+string(rune('a'+i)))
		testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	}
	status, payload, raw = createFolder(t, carol, "第 21 个")
	testkit.ExpectStatus(t, status, http.StatusConflict, raw)
	if code := testkit.MustNested(t, payload, "error")["code"]; code != "folder_limit" {
		t.Fatalf("收藏夹上限应返回 folder_limit: %v", payload)
	}

	// ── 收藏到指定夹与移动 ──
	status, _, payload, raw = carol.Call(http.MethodPost, "/api/v1/posts/"+bobPost+"/bookmark", true,
		map[string]any{"folder_id": folderA})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	bookmarked, folderID = bookmarkState(t, payload)
	if !bookmarked || folderID != folderA {
		t.Fatalf("指定夹收藏响应 = (%v, %v)，期望 (true, %s)", bookmarked, folderID, folderA)
	}
	alicePostOwned := publish(t, carol, "Carol 自己的帖 "+suffix, "public")
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/posts/"+alicePostOwned+"/bookmark", true,
		map[string]any{"folder_id": folderB})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	if ids := listIDs(t, carol, "?folder_id="+folderA); len(ids) != 1 || ids[0] != bobPost {
		t.Fatalf("夹 A 应只含 Bob 的帖，实际 %v", ids)
	}
	if ids := listIDs(t, carol, "?folder_id=default"); len(ids) != 0 {
		t.Fatalf("默认收藏应为空，实际 %v", ids)
	}
	if ids := listIDs(t, carol, ""); len(ids) != 2 {
		t.Fatalf("全部收藏应有 2 条，实际 %v", ids)
	}
	status, _, _, raw = carol.Call(http.MethodGet, "/api/v1/me/bookmarks?folder_id=abc", false, nil)
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)

	// 移动：对已收藏帖子再次收藏到夹 B。
	status, _, payload, raw = carol.Call(http.MethodPost, "/api/v1/posts/"+bobPost+"/bookmark", true,
		map[string]any{"folder_id": folderB})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if ids := listIDs(t, carol, "?folder_id="+folderA); len(ids) != 0 {
		t.Fatalf("移动后夹 A 应为空，实际 %v", ids)
	}
	if ids := listIDs(t, carol, "?folder_id="+folderB); len(ids) != 2 {
		t.Fatalf("移动后夹 B 应有 2 条，实际 %v", ids)
	}

	// ── 列表条目携带 bookmark 归属 ──
	status, _, payload, raw = carol.Call(http.MethodGet, "/api/v1/me/bookmarks?folder_id="+folderB, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	entry := bookmarkEntry(t, payload, bobPost)
	if entry == nil {
		t.Fatalf("收藏列表应包含 Bob 的帖: %s", raw)
	}
	bookmarkMeta, _ := entry["bookmark"].(map[string]any)
	if bookmarkMeta == nil || bookmarkMeta["folder_id"] != folderB {
		t.Fatalf("列表条目应携带 bookmark.folder_id=%s: %v", folderB, entry)
	}

	// ── 私有性：他人不可见我的收藏 ──
	status, _, payload, raw = alice.Call(http.MethodGet, "/api/v1/me/bookmarks", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items, _ := payload["items"].([]any); len(items) != 0 {
		t.Fatalf("Alice 的收藏列表应为空: %s", raw)
	}
	// 时间线等公开接口不携带他人收藏标记：alice 视角 Bob 帖的 viewer_state.bookmarked=false。
	status, _, payload, raw = alice.Call(http.MethodGet, "/api/v1/posts/"+bobPost, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	aliceState := testkit.MustNested(t, testkit.MustNested(t, payload, "post"), "viewer_state")
	if aliceState["bookmarked"] != false {
		t.Fatalf("他人视角 bookmarked 应为 false: %v", aliceState)
	}

	// ── 可见性过滤：删帖消失；仅关注者帖取关后消失 ──
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/posts/"+alicePost+"/bookmark", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts/"+alicePost+"/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	if ids := listIDs(t, carol, ""); hasID(ids, alicePost) {
		t.Fatalf("已删除帖子应从收藏列表消失: %v", ids)
	}

	// Bob 的仅关注者帖：carol 关注后收藏，取关后从列表消失。
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/users/"+bobName+"/follow", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/posts/"+bobFollowersPost+"/bookmark", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if ids := listIDs(t, carol, ""); !hasID(ids, bobFollowersPost) {
		t.Fatalf("关注者应能在收藏列表看到仅关注者帖: %v", ids)
	}
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/users/"+bobName+"/unfollow", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if ids := listIDs(t, carol, ""); hasID(ids, bobFollowersPost) {
		t.Fatalf("取关后仅关注者帖应从收藏列表消失: %v", ids)
	}

	// ── 停用账号拒绝新收藏 ──
	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/users/"+bobID+"/suspend", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/posts/"+bobPost+"/bookmark", true, nil)
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/users/"+bobID+"/unsuspend", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	// ── 删除夹：夹内收藏归还默认 ──
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/me/bookmark-folders/"+folderB+"/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	if ids := listIDs(t, carol, "?folder_id=default"); !hasID(ids, bobPost) || !hasID(ids, alicePostOwned) {
		t.Fatalf("删除夹后收藏应回到默认收藏: %v", ids)
	}
	status, _, _, raw = carol.Call(http.MethodGet, "/api/v1/me/bookmarks?folder_id="+folderB, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)

	// ── 管理后台无收藏数据：路由不存在 ──
	status, _, _, raw = admin.Call(http.MethodGet, "/api/v1/admin/bookmarks", false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, _, raw = admin.Call(http.MethodGet, "/api/v1/admin/users/"+testkit.MustString(t, aliceUser, "id")+"/bookmarks", false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
}

// bookmarkEntry 从列表 payload 中取出指定帖子 ID 的条目。
func bookmarkEntry(t *testing.T, payload map[string]any, postID string) map[string]any {
	t.Helper()
	items, _ := payload["items"].([]any)
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if entry != nil && testkit.MustString(t, entry, "id") == postID {
			return entry
		}
	}
	return nil
}

// hasID 报告 ID 列表中是否包含指定值。
func hasID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
