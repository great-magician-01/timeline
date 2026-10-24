package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestGroupIntegration 覆盖群聊全链路：
//   - 建群（群主+初始成员）与列表/详情/成员分页；
//   - 拉人资格矩阵（关闭私信拒绝、停用拒绝、不存在 404、重复幂等）；
//   - 发送、未读数（游标模型）、会话列表预览；非成员一律 404；
//   - 已读幂等；撤回占位且不计未读；
//   - 隐藏会话视图与新消息重现（未读从零累计）；
//   - 成员管理：踢人、转让后角色互换、群主退出前置、解散只读；
//   - AI 账号可被拉入群（与私信不同）、停用账号拒绝拉入；
//   - 管理后台完全不可见群聊（无任何管理接口，非成员访问 404）。
func TestGroupIntegration(t *testing.T) {
	srv := testkit.NewIntegrationServer(t)
	suffix := testkit.IntegrationShortSuffix()

	alice := srv.Client(t)
	alice.RegisterAndLogin("grp_alice_"+suffix, "grp-alice-"+suffix+"@example.com")

	bob := srv.Client(t)
	bob.RegisterAndLogin("grp_bob_"+suffix, "grp-bob-"+suffix+"@example.com")
	bobUser := currentUser(t, bob)
	bobID := testkit.MustString(t, bobUser, "id")

	carol := srv.Client(t)
	carol.RegisterAndLogin("grp_carol_"+suffix, "grp-carol-"+suffix+"@example.com")

	admin := srv.Client(t)
	admin.LoginAndSetCSRF(testkit.IntegrationAdminUsername, testkit.IntegrationAdminPassword)

	// ── 建群：群主 + 初始成员 ──
	status, _, payload, raw := alice.Call(http.MethodPost, "/api/v1/groups", true,
		map[string]any{"name": " 集成测试群 ", "usernames": []string{"grp_bob_" + suffix}})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	group := testkit.MustNested(t, payload, "group")
	groupID := testkit.MustString(t, group, "id")
	if testkit.MustString(t, group, "name") != "集成测试群" {
		t.Fatalf("群名应去首尾空白: %s", raw)
	}
	if testkit.MustString(t, group, "role") != "owner" {
		t.Fatalf("创建者应为群主: %s", raw)
	}
	if count, _ := group["member_count"].(float64); count != 2 {
		t.Fatalf("成员数应为 2: %s", raw)
	}

	// ── 空群不进列表，有消息后进入 ──
	status, _, payload, raw = alice.Call(http.MethodGet, "/api/v1/groups", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items, _ := payload["items"].([]any); len(items) != 0 {
		t.Fatalf("无消息群不应进入列表: %s", raw)
	}

	// ── 发送消息与未读数 ──
	status, _, payload, raw = alice.Call(http.MethodPost,
		"/api/v1/groups/"+groupID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>大家好</p>"})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	firstMessage := testkit.MustNested(t, payload, "message")
	firstMessageID := testkit.MustString(t, firstMessage, "id")

	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if unread, _ := payload["unread_count"].(float64); unread != 1 {
		t.Fatalf("Bob 群未读总数应为 1: %s", raw)
	}
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("Bob 群列表应有 1 条: %s", raw)
	}
	item, _ := items[0].(map[string]any)
	if unread, _ := item["unread_count"].(float64); unread != 1 {
		t.Fatalf("会话未读数应为 1: %s", raw)
	}
	lastMessage, _ := item["last_message"].(map[string]any)
	if lastMessage == nil || testkit.MustString(t, lastMessage, "preview") != "大家好" {
		t.Fatalf("会话预览应为最后消息: %s", raw)
	}
	if testkit.MustString(t, item, "role") != "member" {
		t.Fatalf("Bob 应为普通成员: %s", raw)
	}

	// ── 群详情与成员分页 ──
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups/"+groupID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	detail := testkit.MustNested(t, payload, "group")
	if count, _ := detail["member_count"].(float64); count != 2 {
		t.Fatalf("详情成员数应为 2: %s", raw)
	}
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups/"+groupID+"/members", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	members, _ := payload["items"].([]any)
	if len(members) != 2 {
		t.Fatalf("成员列表应有 2 人: %s", raw)
	}
	first, _ := members[0].(map[string]any)
	if testkit.MustString(t, first, "role") != "owner" {
		t.Fatalf("成员按加入时间正序，群主应最先: %s", raw)
	}

	// ── 非成员一律 404（不暴露存在性）──
	status, _, _, raw = carol.Call(http.MethodGet, "/api/v1/groups/"+groupID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, _, raw = carol.Call(http.MethodGet, "/api/v1/groups/"+groupID+"/messages", false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>hi</p>"})
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/read", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, _, raw = admin.Call(http.MethodGet, "/api/v1/groups/"+groupID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)

	// ── 已读幂等 ──
	status, _, payload, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/read", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if updated, _ := payload["updated"].(float64); updated != 1 {
		t.Fatalf("首次已读应更新 1: %s", raw)
	}
	status, _, payload, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/read", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if updated, _ := payload["updated"].(float64); updated != 0 {
		t.Fatalf("重复已读应更新 0: %s", raw)
	}
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if unread, _ := payload["unread_count"].(float64); unread != 0 {
		t.Fatalf("已读后未读数应为 0: %s", raw)
	}

	// ── 拉人资格矩阵：关闭私信（dm_policy=none）的用户不可被拉入 ──
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/users/me/dm-settings", true,
		map[string]any{"dm_policy": "none"})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, payload, raw = bob.Call(http.MethodPost, "/api/v1/groups", true,
		map[string]any{"name": "拒绝群", "usernames": []string{}})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	rejectGroupID := testkit.MustString(t, testkit.MustNested(t, payload, "group"), "id")
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+rejectGroupID+"/invite", true,
		map[string]any{"usernames": []string{"grp_carol_" + suffix}})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)

	// ── 拉人：不存在 404、重复幂等、非群主 403 ──
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/invite", true,
		map[string]any{"usernames": []string{"grp_ghost_" + suffix}})
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	status, _, payload, raw = alice.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/invite", true,
		map[string]any{"usernames": []string{"grp_bob_" + suffix}})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if members, _ := payload["members"].([]any); len(members) != 0 {
		t.Fatalf("重复拉在群成员应幂等空返回: %s", raw)
	}
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/invite", true,
		map[string]any{"usernames": []string{"grp_carol_" + suffix}})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)

	// ── 踢人：群主可踢普通成员，非群主不能踢 ──
	// 先恢复 carol 的接收策略（拒绝用例关闭了私信），再正常拉入。
	status, _, _, raw = carol.Call(http.MethodPost, "/api/v1/users/me/dm-settings", true,
		map[string]any{"dm_policy": "all"})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/invite", true,
		map[string]any{"usernames": []string{"grp_carol_" + suffix}})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	carolUser := currentUser(t, carol)
	carolID := testkit.MustString(t, carolUser, "id")
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/members/"+carolID+"/remove", true, nil)
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/members/"+carolID+"/remove", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, _, raw = carol.Call(http.MethodGet, "/api/v1/groups/"+groupID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)

	// ── 隐藏会话视图与新消息重现 ──
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/hide", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items, _ := payload["items"].([]any); len(items) != 0 {
		t.Fatalf("隐藏后 Bob 群列表应为空: %s", raw)
	}
	status, _, payload, raw = alice.Call(http.MethodPost,
		"/api/v1/groups/"+groupID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>新消息</p>"})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	secondMessageID := testkit.MustString(t, testkit.MustNested(t, payload, "message"), "id")
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	items, _ = payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("新消息后群应重新出现: %s", raw)
	}
	item, _ = items[0].(map[string]any)
	if unread, _ := item["unread_count"].(float64); unread != 1 {
		t.Fatalf("新消息群未读数应为 1（隐藏不残留旧未读）: %s", raw)
	}

	// ── 撤回：占位、幂等、不计未读 ──
	// 第一条（大家好）Bob 已读过：撤回只验证占位与幂等；
	// 「新消息」是 Bob 的未读：撤回后未读应同步 -1。
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	unreadBefore, _ := payload["unread_count"].(float64)
	status, _, payload, raw = alice.Call(http.MethodPost, "/api/v1/groups/messages/"+firstMessageID+"/recall", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	recalled := testkit.MustNested(t, payload, "message")
	if testkit.MustString(t, recalled, "status") != "recalled" {
		t.Fatalf("撤回后状态应为 recalled: %s", raw)
	}
	if html, _ := recalled["content_html"].(string); html != "" {
		t.Fatalf("撤回后不应返回正文: %s", raw)
	}
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/groups/messages/"+firstMessageID+"/recall", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, payload, raw = alice.Call(http.MethodPost, "/api/v1/groups/messages/"+secondMessageID+"/recall", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if unread, _ := payload["unread_count"].(float64); unread != unreadBefore-1 {
		t.Fatalf("撤回消息不应计入未读: %s", raw)
	}

	// ── 群主转让与退出 ──
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/leave", true, nil)
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw) // 群主不能直接退出
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/transfer", true,
		map[string]any{"user_id": bobID})
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, payload, raw = alice.Call(http.MethodGet, "/api/v1/groups/"+groupID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := testkit.MustString(t, testkit.MustNested(t, payload, "group"), "role"); got != "member" {
		t.Fatalf("转让后原群主应为 member: %s", raw)
	}
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/leave", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, _, raw = alice.Call(http.MethodGet, "/api/v1/groups/"+groupID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	// 新群主 bob 可改名。
	status, _, payload, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/rename", true,
		map[string]any{"name": "改名后的群"})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := testkit.MustString(t, testkit.MustNested(t, payload, "group"), "name"); got != "改名后的群" {
		t.Fatalf("改名应生效: %s", raw)
	}

	// ── 解散：只读保留，可隐藏 ──
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/dissolve", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>还想说</p>"})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups/"+groupID+"/messages", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw) // 历史只读可见
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/invite", true,
		map[string]any{"usernames": []string{"grp_carol_" + suffix}})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)

	// ── 解散群可隐藏：列表消失、未读清零、不再重新出现 ──
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+groupID+"/hide", true, nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items, _ := payload["items"].([]any); len(items) != 0 {
		t.Fatalf("隐藏后解散群应从列表消失: %s", raw)
	}
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups/unread-count", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if unread, _ := payload["unread_count"].(float64); unread != 0 {
		t.Fatalf("隐藏后解散群未读应清零: %s", raw)
	}
	// 隐藏只影响列表视图：详情与历史仍可只读访问（直接链接）。
	status, _, payload, raw = bob.Call(http.MethodGet, "/api/v1/groups/"+groupID, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := testkit.MustString(t, testkit.MustNested(t, payload, "group"), "status"); got != "dissolved" {
		t.Fatalf("解散群详情应只读可见: %s", raw)
	}

	// ── AI 账号可被拉入群聊（与私信不同）；但 AI 不能发起群操作 ──
	aiUsername := "grp_ai_" + suffix
	status, _, payload, raw = admin.Call(http.MethodPost, "/api/v1/admin/providers", true, map[string]any{
		"name": "Group Mock " + suffix, "base_url": "http://127.0.0.1:8099/v1",
		"api_key": "sk-group-integration-key", "default_model": "mock-1",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	providerID := testkit.MustString(t, testkit.MustNested(t, payload, "provider"), "id")
	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/ai", true, map[string]any{
		"username": aiUsername, "nickname": "群聊 AI", "bio": "",
		"background": "背景", "personality": "沉稳", "core_values": "真实",
		"language_style": "简洁", "topics": "Go", "target_language": "zh-CN",
		"taboos": "无", "system_prompt": "你是群聊测试 AI",
		"provider_id": providerID, "model_override": "", "is_enabled": false,
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	status, _, payload, raw = bob.Call(http.MethodPost, "/api/v1/groups", true,
		map[string]any{"name": "AI 群", "usernames": []string{aiUsername}})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	aiGroupID := testkit.MustString(t, testkit.MustNested(t, payload, "group"), "id")
	if count, _ := testkit.MustNested(t, payload, "group")["member_count"].(float64); count != 2 {
		t.Fatalf("AI 应可被拉入群聊（成员数 2）: %s", raw)
	}
	_ = aiGroupID

	// ── 停用账号拒绝被拉入 ──
	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/users/"+carolID+"/suspend", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+aiGroupID+"/invite", true,
		map[string]any{"usernames": []string{"grp_carol_" + suffix}})
	testkit.ExpectStatus(t, status, http.StatusForbidden, raw)
	status, _, _, raw = admin.Call(http.MethodPost, "/api/v1/admin/users/"+carolID+"/unsuspend", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	// ── 正文校验：长度/图片/格式 ──
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+aiGroupID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": "<p>" + strings.Repeat("字", 2001) + "</p>"})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+aiGroupID+"/messages", true,
		map[string]any{"content_format": "richtext", "content_source": `<p><img src="/x"></p>`})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/groups/"+aiGroupID+"/messages", true,
		map[string]any{"content_format": "markdown", "content_source": "**hi**"})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)

	// ── 管理后台无群聊接口 ──
	status, _, _, raw = admin.Call(http.MethodGet, "/api/v1/admin/groups", false, nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
}
