package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestDMIntegration 覆盖私信全链路：
//   - 创建会话幂等（重复创建返回同一 ID）；
//   - 发送、对方未读数与会话列表预览；非会话成员一律 404；
//   - 已读幂等清零；撤回后双方占位且不计未读；
//   - 删除会话视图（单侧隐藏）与来新消息后恢复；
//   - 接收策略矩阵（none / following 仅我关注的 / mutual 互关）实时生效；
//   - AI 账号双向不参与、停用账号拒绝新消息；
//   - 接收策略设置接口往返与非法值校验；
//   - 管理员看不到他人私信（无任何管理接口，非成员访问 404）。
func TestDMIntegration(t *testing.T) {
	srv := testkit.NewIntegrationServer(t)
	suffix := testkit.IntegrationShortSuffix()

	alice := srv.Client(t)
	aliceName := "dm_alice_" + suffix
	alice.RegisterAndLogin(aliceName, "dm-alice-"+suffix+"@example.com")
	aliceUser := currentUser(t, alice)
	aliceID := testkit.MustString(t, aliceUser, "id")

	bob := srv.Client(t)
	bobName := "dm_bob_" + suffix
	bob.RegisterAndLogin(bobName, "dm-bob-"+suffix+"@example.com")
	bobUser := currentUser(t, bob)
	bobID := testkit.MustString(t, bobUser, "id")

	carol := srv.Client(t)
	carol.RegisterAndLogin("dm_carol_"+suffix, "dm-carol-"+suffix+"@example.com")

	admin := srv.Client(t)
	admin.LoginAndSetCSRF(testkit.IntegrationAdminUsername, testkit.IntegrationAdminPassword)

	// ── 创建会话：幂等且规范化 ──
	status, _, payload, raw := alice.Call(http.MethodPost, "/api/v1/dm/conversations", true,
		map[string]any{"username": bobName})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	conversation := testkit.MustNested(t, payload, "conversation")
	conversationID := testkit.MustString(t, conversation, "id")
	peer := testkit.MustNested(t, conversation, "peer")
	if testkit.MustString(t, peer, "username") != bobName {
		t.Fatalf("会话对方应为 %s: %s", bobName, raw)
	}
	status, _, payload, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations", true,
		map[string]any{"username": bobName})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := testkit.MustString(t, testkit.MustNested(t, payload, "conversation"), "id"); got != conversationID {
		t.Fatalf("重复创建应返回同一会话：%s != %s", got, conversationID)
	}

	// ── 发送消息与未读数 ──
	status, _, payload, raw = alice.Call(http.MethodPost,
		"/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>你好</p>"})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	firstMessage := testkit.MustNested(t, payload, "message")
	firstMessageID := testkit.MustString(t, firstMessage, "id")
	if testkit.MustString(t, testkit.MustNested(t, firstMessage, "sender"), "id") != aliceID {
		t.Fatalf("消息发送者应为 Alice: %s", raw)
	}
	if !strings.Contains(testkit.MustString(t, firstMessage, "content_html"), "你好") {
		t.Fatalf("消息正文应包含清洗后的文本: %s", raw)
	}

	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/dm/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if unread, _ := payload["unread_count"].(float64); unread != 1 {
		t.Fatalf("Bob 未读数应为 1: %s", raw)
	}
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/dm/conversations", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("Bob 会话列表应有 1 条: %s", raw)
	}
	item, _ := items[0].(map[string]any)
	if unread, _ := item["unread_count"].(float64); unread != 1 {
		t.Fatalf("会话未读数应为 1: %s", raw)
	}
	lastMessage, _ := item["last_message"].(map[string]any)
	if lastMessage == nil || testkit.MustString(t, lastMessage, "preview") != "你好" {
		t.Fatalf("会话预览应为最后消息: %s", raw)
	}

	// ── 非会话成员一律 404（不暴露存在性）──
	status, _, _, raw = carol.Call(http.MethodGet, "/api/v1/dm/conversations/"+conversationID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, _, raw = carol.Call(http.MethodGet, "/api/v1/dm/conversations/"+conversationID+"/messages", false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>hi</p>"})
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, _, raw = admin.Call(http.MethodGet, "/api/v1/dm/conversations/"+conversationID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)

	// ── 已读幂等 ──
	status, _, payload, raw = bob.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/read", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if updated, _ := payload["updated"].(float64); updated != 1 {
		t.Fatalf("首次已读应更新 1 条: %s", raw)
	}
	status, _, payload, raw = bob.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/read", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if updated, _ := payload["updated"].(float64); updated != 0 {
		t.Fatalf("重复已读应更新 0 条: %s", raw)
	}
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/dm/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if unread, _ := payload["unread_count"].(float64); unread != 0 {
		t.Fatalf("已读后未读数应为 0: %s", raw)
	}

	// ── 撤回：正文占位、幂等、不计未读 ──
	status, _, payload, raw = alice.Call(http.MethodPost, "/api/v1/dm/messages/"+firstMessageID+"/recall", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	recalled := testkit.MustNested(t, payload, "message")
	if testkit.MustString(t, recalled, "status") != "recalled" {
		t.Fatalf("撤回后状态应为 recalled: %s", raw)
	}
	if html, _ := recalled["content_html"].(string); html != "" {
		t.Fatalf("撤回后不应返回正文: %s", raw)
	}
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/messages/"+firstMessageID+"/recall", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/dm/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if unread, _ := payload["unread_count"].(float64); unread != 0 {
		t.Fatalf("撤回消息不应计入未读: %s", raw)
	}

	// ── 删除会话视图（单侧隐藏）与恢复 ──
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/hide", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/dm/conversations", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items, _ := payload["items"].([]any); len(items) != 0 {
		t.Fatalf("隐藏后 Bob 列表应为空: %s", raw)
	}
	status, _, payload, raw = alice.Call(http.MethodGet, "/api/v1/dm/conversations", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items, _ := payload["items"].([]any); len(items) != 1 {
		t.Fatalf("隐藏只影响自己一侧: %s", raw)
	}
	status, _, _, raw = alice.Call(http.MethodPost,
		"/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>新消息</p>"})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/dm/conversations", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	items, _ = payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("新消息后会话应重新出现: %s", raw)
	}
	item, _ = items[0].(map[string]any)
	if unread, _ := item["unread_count"].(float64); unread != 1 {
		t.Fatalf("新消息会话未读数应为 1: %s", raw)
	}

	// ── 再次隐藏：该会话未读同时清零，徽章不残留 ──
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/hide", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/dm/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if unread, _ := payload["unread_count"].(float64); unread != 0 {
		t.Fatalf("隐藏会话应同时清零该会话未读: %s", raw)
	}

	// ── 接收策略：none ──
	updateDMPolicy(t, bob, "none")
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>hi</p>"})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations", true,
		map[string]any{"username": bobName})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)

	// ── 接收策略：following（仅我关注的：Bob 关注 Alice 才允许）──
	updateDMPolicy(t, bob, "following")
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>hi</p>"})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/users/"+aliceName+"/follow", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>hi</p>"})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)

	// ── 接收策略：mutual（需双向关注）──
	updateDMPolicy(t, bob, "mutual")
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>hi</p>"})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/users/"+bobName+"/follow", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>hi</p>"})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)

	// ── 设置接口：往返与非法值 ──
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/users/me/dm-settings", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if testkit.MustString(t, payload, "dm_policy") != "mutual" {
		t.Fatalf("策略回显应为 mutual: %s", raw)
	}
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/users/me/dm-settings", true,
		map[string]any{"dm_policy": "everyone"})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	updateDMPolicy(t, bob, "all")

	// ── 正文长度：超过上限（2000 字）拒绝，恰好达到上限允许 ──
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/dm/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	unreadBefore, _ := payload["unread_count"].(float64)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>" + strings.Repeat("字", 2001) + "</p>"})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>" + strings.Repeat("字", 2000) + "</p>"})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	// 达到上限的消息真实落库：Bob 未读数应恰好 +1。
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/dm/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if unread, _ := payload["unread_count"].(float64); unread != unreadBefore+1 {
		t.Fatalf("达到上限的正文应可发送并计入未读: %s", raw)
	}

	// ── AI 账号双向不参与私信 ──
	aiUsername := "dm_ai_" + suffix
	status, _, payload, raw = admin.Call(http.MethodPost, "/api/v1/admin/providers", true, map[string]any{
		"name": "DM Mock " + suffix, "base_url": "http://127.0.0.1:8099/v1",
		"api_key": "sk-dm-integration-key", "default_model": "mock-1",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	providerID := testkit.MustString(t, testkit.MustNested(t, payload, "provider"), "id")
	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/ai", true, map[string]any{
		"username": aiUsername, "nickname": "私信 AI", "bio": "",
		"background": "背景", "personality": "沉稳", "core_values": "真实",
		"language_style": "简洁", "topics": "Go", "target_language": "zh-CN",
		"taboos": "无", "system_prompt": "你是私信测试 AI",
		"provider_id": providerID, "model_override": "", "is_enabled": false,
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations", true,
		map[string]any{"username": aiUsername})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)

	// ── 停用账号不能接收新消息 ──
	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/users/"+bobID+"/suspend", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>hi</p>"})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/users/"+bobID+"/unsuspend", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	// ── 消息格式：markdown 与图片被拒绝 ──
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "markdown", "content_source": "**hi**"})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/dm/conversations/"+conversationID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": `<p><img src="/x"></p>`})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)

	// ── 管理员无私信接口：后台路由不存在（404），且不能查看他人会话 ──
	status, _, _, raw = admin.Call(http.MethodGet, "/api/v1/admin/dm/conversations", false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	// Alice 与 Bob 的会话 ID 未出现在管理端任何既有接口中。
	status, _, payload, raw = admin.Call(http.MethodGet, "/api/v1/dm/conversations", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items, _ := payload["items"].([]any); len(items) != 0 {
		t.Fatalf("管理员私信列表只应包含自己的会话: %s", raw)
	}
}

// currentUser 读取当前登录账号（GET /auth/me → user）。
func currentUser(t *testing.T, client *testkit.Client) map[string]any {
	t.Helper()
	status, _, payload, raw := client.Call(http.MethodGet, "/api/v1/auth/me", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	return testkit.MustUser(t, payload)
}

// updateDMPolicy 修改指定客户端的私信接收策略并校验返回。
func updateDMPolicy(t *testing.T, client *testkit.Client, policy string) {
	t.Helper()
	status, _, payload, raw := client.Call(http.MethodPost, "/api/v1/users/me/dm-settings", true,
		map[string]any{"dm_policy": policy})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := testkit.MustString(t, payload, "dm_policy"); got != policy {
		t.Fatalf("策略更新回显 = %s，期望 %s: %s", got, policy, raw)
	}
}
