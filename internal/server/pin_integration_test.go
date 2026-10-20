package server_test

import (
	"net/http"
	"testing"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestPinIntegration 覆盖个人主页置顶（0022）：置顶/取消置顶/排序的权限与幂等、
// 3 条上限、主页第一页 pinned_posts 与普通流去重、可见性与删除联动。
// 只在隔离测试 schema 中执行，默认跳过，需 TIMELINE_INTEGRATION=1。
func TestPinIntegration(t *testing.T) {
	srv := testkit.NewIntegrationServer(t)

	suffix := testkit.IntegrationSuffix()
	alice := srv.Client(t)
	bob := srv.Client(t)
	guest := srv.Client(t)

	alice.RegisterAndLogin("pin_alice_"+suffix, "pin-alice-"+suffix+"@example.com")
	bob.RegisterAndLogin("pin_bob_"+suffix, "pin-bob-"+suffix+"@example.com")
	guest.RefreshCSRF()

	aliceName := "pin_alice_" + suffix

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
	// pinnedIDs 返回主页第一页的置顶帖 ID 顺序；普通流是否包含某帖用 ListContains 判断。
	pinnedIDs := func(t *testing.T, c *testkit.Client, username string) []string {
		t.Helper()
		status, _, payload, raw := c.Call(http.MethodGet, "/api/v1/users/"+username+"/posts", false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
		list, _ := payload["pinned_posts"].([]any)
		ids := make([]string, 0, len(list))
		for _, item := range list {
			entry, _ := item.(map[string]any)
			if entry != nil {
				ids = append(ids, testkit.MustString(t, entry, "id"))
			}
		}
		return ids
	}
	profile := func(t *testing.T, c *testkit.Client, username string) map[string]any {
		t.Helper()
		status, _, payload, raw := c.Call(http.MethodGet, "/api/v1/users/"+username+"/posts", false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
		return payload
	}

	postA := publish(t, alice, "置顶测试 A "+suffix, "public")
	postB := publish(t, alice, "置顶测试 B "+suffix, "public")
	postC := publish(t, alice, "置顶测试 C "+suffix, "public")
	postD := publish(t, alice, "置顶测试 D "+suffix, "public")
	postE := publish(t, bob, "Bob 的帖子 "+suffix, "public")
	_ = postE

	// --- 权限：游客拒绝；他人按不存在处理；作者成功 ---
	status, _, _, raw := guest.Call(http.MethodPost, "/api/v1/posts/"+postA+"/pin", true, nil)
	testkit.ExpectStatus(t, status, http.StatusUnauthorized, raw)
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/posts/"+postA+"/pin", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)

	status, _, pinPayload, raw := alice.Call(http.MethodPost, "/api/v1/posts/"+postA+"/pin", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	pinned := testkit.MustNested(t, pinPayload, "post")
	if got := testkit.MustString(t, pinned, "pinned_at"); got == "" {
		t.Fatalf("置顶后详情应返回 pinned_at: %v", pinPayload)
	}
	if canPin, _ := pinned["can_pin"].(bool); !canPin {
		t.Fatalf("作者视角 can_pin 应为 true: %v", pinned)
	}

	// 重复置顶幂等
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts/"+postA+"/pin", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	// --- 主页第一页：pinned_posts 展示置顶帖，普通流不再重复 ---
	first := profile(t, guest, aliceName)
	pinList, _ := first["pinned_posts"].([]any)
	if len(pinList) != 1 || testkit.MustString(t, pinList[0].(map[string]any), "id") != postA {
		t.Fatalf("主页第一页应返回且仅返回置顶帖 A: %v", first)
	}
	if testkit.ListContains(first, "id", postA) {
		t.Fatalf("普通流不应重复包含置顶帖: %v", first)
	}
	if !testkit.ListContains(first, "id", postB) {
		t.Fatalf("普通流应包含未置顶帖: %v", first)
	}

	// --- 置满 3 条后第四条冲突 ---
	for _, id := range []string{postB, postC} {
		status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts/"+id+"/pin", true, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
	}
	status, _, limitPayload, raw := alice.Call(http.MethodPost, "/api/v1/posts/"+postD+"/pin", true, nil)
	testkit.ExpectStatus(t, status, http.StatusConflict, raw)
	if code := testkit.MustNested(t, limitPayload, "error")["code"]; code != "pin_limit" {
		t.Fatalf("置顶上限应返回 pin_limit: %v", limitPayload)
	}

	// --- 排序：集合不一致拒绝；一致则按给定顺序生效 ---
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts/pinned/reorder", true, map[string]any{
		"post_ids": []string{postA},
	})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/posts/pinned/reorder", true, map[string]any{
		"post_ids": []string{postC, postA, postB},
	})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)

	status, _, reorderPayload, raw := alice.Call(http.MethodPost, "/api/v1/posts/pinned/reorder", true, map[string]any{
		"post_ids": []string{postC, postA, postB},
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	reordered, _ := reorderPayload["pinned_posts"].([]any)
	if len(reordered) != 3 {
		t.Fatalf("排序响应应包含 3 条置顶帖: %v", reorderPayload)
	}
	if got := pinnedIDs(t, guest, aliceName); len(got) != 3 ||
		got[0] != postC || got[1] != postA || got[2] != postB {
		t.Fatalf("排序后置顶顺序应为 C、A、B，实际 %v", got)
	}

	// --- 草稿不可置顶 ---
	status, _, draftPayload, raw := alice.Call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>草稿不可置顶</p>",
		"visibility":     "public",
		"action":         "draft",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	draftID := testkit.MustString(t, testkit.MustNested(t, draftPayload, "post"), "id")
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts/"+draftID+"/pin", true, nil)
	testkit.ExpectStatus(t, status, http.StatusConflict, raw)

	// --- 取消置顶：剩余顺序保持相对次序 ---
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts/"+postC+"/unpin", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := pinnedIDs(t, guest, aliceName); len(got) != 2 || got[0] != postA || got[1] != postB {
		t.Fatalf("取消置顶后顺序应保持 A、B，实际 %v", got)
	}

	// --- 删除置顶帖：置顶自动失效 ---
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts/"+postA+"/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	if got := pinnedIDs(t, guest, aliceName); len(got) != 1 || got[0] != postB {
		t.Fatalf("删除置顶帖后应只剩 B，实际 %v", got)
	}

	// --- 仅关注者可见的置顶帖：访客不可见，关注者可见 ---
	followersPostID := publish(t, alice, "置顶的仅关注者帖 "+suffix, "followers")
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts/"+followersPostID+"/pin", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	guestView := profile(t, guest, aliceName)
	if testkit.ListContains(guestView, "id", followersPostID) {
		t.Fatalf("游客不应看到仅关注者帖（置顶或普通流）: %v", guestView)
	}
	if guestPinned := pinnedIDs(t, guest, aliceName); len(guestPinned) != 1 || guestPinned[0] != postB {
		t.Fatalf("游客视角应只看到公开置顶帖 B，实际 %v", guestPinned)
	}

	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/users/"+aliceName+"/follow", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	// 关注者视角：置顶的仅关注者帖出现在置顶区块（普通流排除置顶帖）。
	if bobPinned := pinnedIDs(t, bob, aliceName); len(bobPinned) != 2 ||
		bobPinned[0] != postB || bobPinned[1] != followersPostID {
		t.Fatalf("关注者应按顺序看到置顶帖 B 与仅关注者帖，实际 %v", bobPinned)
	}
}
