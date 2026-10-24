// Package http_test 是 ai 模块的 HTTP 集成测试（外部测试包）：通过 internal/testkit
// 构造真实组合根（app.New）+ 隔离测试 schema（timeline_test/user_test）+ httptest 服务，
// 只做黑盒 HTTP 断言，不依赖 production 包内部结构。
//
// 覆盖场景（对应旧 internal/ai/ai_integration_test.go）：
//   - 模型接口预设：创建/重名冲突/非法地址/列表过滤/连接测试（成功与鉴权失败）/更新/删除保护；
//   - AI 配置：创建（身份、生成参数、行为策略）、详情、列表、资料更新、模型更新、行为更新
//     （含 allow_web_search、rate_limits、candidate_scope）、启用/停用（暂停后台作业、取消待执行任务）；
//   - 分层记忆：创建/列表/搜索/更新/逻辑删除/清空；
//   - 生成任务：创建任务后停用 AI 会取消 pending 任务并暂停 pending 后台作业；
//   - 审计：关键动作写入 admin_audit_logs。
//
// 集成测试默认跳过；设置 TIMELINE_INTEGRATION=1 后连接开发实例中的
// timeline_test / user_test 隔离 schema 运行：
//
//	TIMELINE_INTEGRATION=1 go test ./internal/modules/ai/http -run TestAIAdminIntegration -count=1 -v
//
// 测试数据只写入隔离测试 schema，遵从“不物理删除”约定：断言基于本次运行唯一后缀隔离，
// 可重复运行；不做物理 DELETE。
package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestAIAdminIntegration 覆盖 ai 模块管理接口的完整 HTTP 流程。
func TestAIAdminIntegration(t *testing.T) {
	prepareTestWorkingDir(t)
	srv := testkit.NewIntegrationServer(t)
	client := srv.Client(t)
	adminID := loginAdmin(t, client)
	suffix := testkit.IntegrationSuffix()
	ctx := context.Background()

	plainKey := "sk-integration-secret-key"

	// ── 连接测试所用的本地 mock 端点：正常返回模型列表 / 返回 401 ──
	modelsMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"mock-1"}]}`))
	}))
	t.Cleanup(modelsMock.Close)
	unauthorizedMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"type":"authentication_error","message":"invalid api key"}}`))
	}))
	t.Cleanup(unauthorizedMock.Close)

	// ── 模型接口预设 ──
	providerName := "本地 Mock " + suffix
	status, payload, raw := postJSON(t, client, "/api/v1/admin/providers", map[string]any{
		"name": providerName, "base_url": modelsMock.URL + "/v1",
		"api_key": plainKey, "default_model": "mock-1",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	if strings.Contains(raw, plainKey) || strings.Contains(raw, "v1:") {
		t.Fatalf("创建预设响应泄露密钥: %s", raw)
	}
	providerID := requiredString(t, payload, "provider", "id")
	if masked := requiredString(t, payload, "provider", "api_key_masked"); masked != "sk-...-key" {
		t.Fatalf("密钥掩码不符: %q", masked)
	}

	status, payload, raw = postJSON(t, client, "/api/v1/admin/providers", map[string]any{
		"name": providerName, "base_url": modelsMock.URL + "/v1",
		"api_key": plainKey, "default_model": "mock-1",
	})
	testkit.ExpectStatus(t, status, http.StatusConflict, raw)
	if testkit.ErrorCode(payload) != "duplicate" {
		t.Fatalf("重名预设错误码不符: %s", raw)
	}

	status, _, raw = postJSON(t, client, "/api/v1/admin/providers", map[string]any{
		"name": "坏地址 " + suffix, "base_url": "ftp://x", "api_key": "", "default_model": "m",
	})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)

	status, payload, raw = getJSON(t, client,
		"/api/v1/admin/providers?q="+url.QueryEscape("Mock "+suffix)+"&status=enabled&limit=20")
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if strings.Contains(raw, plainKey) || strings.Contains(raw, "v1:") {
		t.Fatalf("预设列表泄露密钥: %s", raw)
	}
	items := requiredList(t, payload)
	if len(items) != 1 || requiredString(t, items[0].(map[string]any), "id") != providerID {
		t.Fatalf("预设列表结果不符: %s", raw)
	}
	if count := requiredNumber(t, items[0].(map[string]any), "ai_count"); count != 0 {
		t.Fatalf("ai_count 应为 0: %v", count)
	}

	// 连接测试：可达 mock 返回可用模型，响应绝不包含明文密钥。
	status, payload, raw = postJSON(t, client, "/api/v1/admin/providers/"+providerID+"/test", nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if ok, _ := payload["ok"].(bool); !ok {
		t.Fatalf("连接测试应成功: %s", raw)
	}
	if strings.Contains(raw, plainKey) {
		t.Fatalf("连接测试响应泄露密钥: %s", raw)
	}
	if models, _ := payload["models"].([]any); len(models) != 1 || models[0] != "mock-1" {
		t.Fatalf("连接测试模型列表不符: %s", raw)
	}

	// 鉴权失败路径：仍返回 200，但 ok=false 且错误信息脱敏。
	status, payload, raw = postJSON(t, client, "/api/v1/admin/providers", map[string]any{
		"name": "认证失败 Mock " + suffix, "base_url": unauthorizedMock.URL + "/v1",
		"api_key": "sk-bad-integration-key", "default_model": "mock-1",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	badProviderID := requiredString(t, payload, "provider", "id")
	status, payload, raw = postJSON(t, client, "/api/v1/admin/providers/"+badProviderID+"/test", nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if ok, _ := payload["ok"].(bool); ok {
		t.Fatalf("鉴权失败时 ok 应为 false: %s", raw)
	}
	if message, _ := payload["error"].(string); !strings.Contains(message, "认证被拒绝") {
		t.Fatalf("鉴权失败错误信息不符: %v", payload["error"])
	}
	if strings.Contains(raw, "sk-bad-integration-key") {
		t.Fatalf("连接测试响应泄露密钥: %s", raw)
	}

	// 备用预设：用于后续切换关联、验证删除保护。
	status, payload, raw = postJSON(t, client, "/api/v1/admin/providers", map[string]any{
		"name": "备用 Mock " + suffix, "base_url": modelsMock.URL + "/v1",
		"api_key": "sk-second-key", "default_model": "mock-2",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	secondProviderID := requiredString(t, payload, "provider", "id")

	// ── 创建 AI ──
	aiUsername := "ai_check_" + suffix
	createAI := func(username string, provider string, extra map[string]any) (int, map[string]any, string) {
		body := map[string]any{
			"username": username, "nickname": "校验助手", "bio": "临时校验",
			"background": "背景", "personality": "沉稳", "core_values": "真实",
			"language_style": "简洁", "topics": "Go,写作", "target_language": "zh-CN",
			"taboos": "无", "system_prompt": "你是校验助手",
			"provider_id": provider, "model_override": "",
			"generation_params": map[string]any{"max_tokens": 256, "temperature": 0.7, "stop": []string{"END"}},
			"behavior": map[string]any{
				"allow_passive_reply": true, "candidate_scope_type": "tags",
				"candidate_scope_params": map[string]any{"tags": []string{"go"}},
				"rate_limits":            map[string]any{"daily_like": 5},
			},
			"is_enabled": false,
		}
		for key, value := range extra {
			body[key] = value
		}
		return postJSON(t, client, "/api/v1/admin/ai", body)
	}

	status, payload, raw = createAI(aiUsername, providerID, nil)
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	if strings.Contains(raw, plainKey) || strings.Contains(raw, "api_key") {
		t.Fatalf("AI 详情泄露密钥字段: %s", raw)
	}
	aiID := requiredString(t, payload, "ai", "id")
	if got := requiredString(t, payload, "ai", "behavior", "candidate_scope_type"); got != "tags" {
		t.Fatalf("行为设置未保存: %s", raw)
	}
	if got := requiredBool(t, payload, "ai", "behavior", "allow_web_search"); got {
		t.Fatalf("联网搜索应默认关闭: %s", raw)
	}
	if got := requiredNumber(t, payload, "ai", "model", "generation_params", "max_tokens"); got != 256 {
		t.Fatalf("生成参数未保存: %s", raw)
	}

	status, payload, raw = createAI(aiUsername, providerID, nil)
	testkit.ExpectStatus(t, status, http.StatusConflict, raw)
	if testkit.ErrorCode(payload) != "duplicate" {
		t.Fatalf("重名 AI 错误码不符: %s", raw)
	}
	status, _, raw = createAI(aiUsername+"b", providerID,
		map[string]any{"behavior": map[string]any{"candidate_scope_type": "tags"}})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	status, _, raw = createAI(aiUsername+"c", "999999999", nil)
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	status, _, raw = createAI(aiUsername+"d", providerID,
		map[string]any{"generation_params": map[string]any{"top_n": 5}})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)

	// ── 详情、列表、更新 ──
	status, payload, raw = getJSON(t, client, "/api/v1/admin/ai/"+aiID)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := requiredString(t, payload, "ai", "profile", "background"); got != "背景" {
		t.Fatalf("identity 资料未返回: %s", raw)
	}
	if got := requiredString(t, payload, "ai", "model", "provider_id"); got != providerID {
		t.Fatalf("model.provider_id 不符: %s", raw)
	}
	if got := requiredNumber(t, payload, "ai", "memory_counts", "recent"); got != 0 {
		t.Fatalf("memory_counts.recent 应为 0: %s", raw)
	}
	if got := requiredListAt(t, payload, "ai", "recent_tasks"); len(got) != 0 {
		t.Fatalf("recent_tasks 应为空: %s", raw)
	}
	status, _, raw = getJSON(t, client, "/api/v1/admin/ai/"+adminID)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)

	status, payload, raw = getJSON(t, client,
		"/api/v1/admin/ai?q="+url.QueryEscape(aiUsername)+"&status=disabled")
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	aiItems := requiredList(t, payload)
	if len(aiItems) != 1 {
		t.Fatalf("AI 列表结果不符: %s", raw)
	}
	first := aiItems[0].(map[string]any)
	if enabled, _ := first["is_enabled"].(bool); enabled {
		t.Fatalf("创建的 AI 应为停用状态: %v", first)
	}
	if actions, _ := first["today_actions"].(float64); actions != 0 {
		t.Fatalf("today_actions 应为 0: %v", first)
	}
	if first["provider_name"] != providerName {
		t.Fatalf("provider_name 不符: %v", first["provider_name"])
	}

	status, payload, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID, map[string]any{
		"nickname": "新昵称", "bio": "新简介", "system_prompt": "新的提示词",
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := requiredString(t, payload, "ai", "user", "nickname"); got != "新昵称" {
		t.Fatalf("昵称未更新: %s", raw)
	}
	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID, map[string]any{"nickname": " "})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)

	status, payload, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/model", map[string]any{
		"provider_id": providerID, "model_override": "mock-2",
		"generation_params":       map[string]any{"max_tokens": 512, "temperature": 0.3},
		"request_timeout_seconds": 30,
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := requiredString(t, payload, "model", "model"); got != "mock-2" {
		t.Fatalf("模型覆盖未保存: %s", raw)
	}
	if got := requiredNumber(t, payload, "model", "request_timeout_seconds"); got != 30 {
		t.Fatalf("请求超时未保存: %s", raw)
	}
	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/model",
		map[string]any{"request_timeout_seconds": 3})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/model",
		map[string]any{"provider_id": "888888888"})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)

	status, payload, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/behavior", map[string]any{
		"allow_like": true, "allow_ai_to_ai": true, "allow_web_search": true, "cooldown_seconds": 60,
		"allow_group_message":    true,
		"rate_limits":            map[string]any{"daily_like": 5, "daily_total": 10},
		"candidate_scope_type":   "authors",
		"candidate_scope_params": map[string]any{"usernames": []string{"alice"}},
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := requiredNumber(t, payload, "behavior", "cooldown_seconds"); got != 60 {
		t.Fatalf("行为冷却未保存: %s", raw)
	}
	if got := requiredBool(t, payload, "behavior", "allow_web_search"); !got {
		t.Fatalf("联网搜索开关未保存: %s", raw)
	}
	if got := requiredBool(t, payload, "behavior", "allow_group_message"); !got {
		t.Fatalf("群聊发言开关未保存: %s", raw)
	}
	// 群聊发言开关默认关闭：补丁不含该字段时保持 true（补丁语义），全量重读校验持久化。
	status, payload, raw = getJSON(t, client, "/api/v1/admin/ai/"+aiID)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := requiredBool(t, payload, "ai", "behavior", "allow_group_message"); !got {
		t.Fatalf("群聊发言开关未持久化: %s", raw)
	}
	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/behavior",
		map[string]any{"rate_limits": map[string]any{"daily_unknown": 1}})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	// 校验的是补丁应用后的最终状态：切到 tags 但不提供 tags 数组必须被拒绝。
	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/behavior",
		map[string]any{"candidate_scope_type": "tags"})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/behavior", map[string]any{
		"candidate_scope_type": "authors", "candidate_scope_params": map[string]any{"usernames": []string{"bob"}},
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	// ── 记忆 ──
	status, payload, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/memories", map[string]any{
		"memory_type": "recent", "content": "和用户讨论了 Go 并发", "category": "event",
		"keywords": []string{"Go", "go", "  ", "并发"}, "importance": 4,
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	recentMemoryID := requiredString(t, payload, "memory", "id")
	if keywords := requiredListAt(t, payload, "memory", "keywords"); len(keywords) != 2 {
		t.Fatalf("关键词应按大小写不敏感去重: %s", raw)
	}
	status, payload, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/memories", map[string]any{
		"memory_type": "long_term", "content": "用户长期关注 Go 语言的写作话题", "category": "topic",
		"keywords": []string{"Go", "写作"}, "importance": 5, "inference_level": "summary",
	})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	longMemoryID := requiredString(t, payload, "memory", "id")
	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/memories", map[string]any{
		"memory_type": "mid", "content": "x",
	})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/memories", map[string]any{
		"memory_type": "recent", "content": "",
	})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)

	status, payload, raw = getJSON(t, client, "/api/v1/admin/ai/"+aiID+"/memories?type=recent")
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items := requiredList(t, payload); len(items) != 1 {
		t.Fatalf("近期记忆列表不符: %s", raw)
	}
	status, payload, raw = getJSON(t, client,
		"/api/v1/admin/ai/"+aiID+"/memories?type=long_term&q="+url.QueryEscape("写作"))
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items := requiredList(t, payload); len(items) != 1 {
		t.Fatalf("长期记忆搜索不符: %s", raw)
	}
	status, payload, raw = getJSON(t, client,
		"/api/v1/admin/ai/"+aiID+"/memories?type=long_term&q="+url.QueryEscape("不存在"))
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items := requiredList(t, payload); len(items) != 0 {
		t.Fatalf("无匹配记忆应为空: %s", raw)
	}
	status, _, raw = getJSON(t, client, "/api/v1/admin/ai/"+aiID+"/memories")
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)

	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/memories/"+recentMemoryID,
		map[string]any{"content": "更新后的记忆", "importance": 2})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	// 逻辑删除：默认只列出 active；status 过滤可查已删除。
	status, _, raw = postJSON(t, client,
		"/api/v1/admin/ai/"+aiID+"/memories/"+recentMemoryID+"/delete", nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, payload, raw = getJSON(t, client, "/api/v1/admin/ai/"+aiID+"/memories?type=recent")
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items := requiredList(t, payload); len(items) != 0 {
		t.Fatalf("默认只应列出 active: %s", raw)
	}
	status, payload, raw = getJSON(t, client,
		"/api/v1/admin/ai/"+aiID+"/memories?type=recent&status=deleted")
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items := requiredList(t, payload); len(items) != 1 {
		t.Fatalf("已删除记忆应可查: %s", raw)
	}

	status, payload, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/memories/clear",
		map[string]any{"type": "long_term"})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := requiredNumber(t, payload, "updated"); got != 1 {
		t.Fatalf("清空长期记忆数目不符: %s", raw)
	}
	status, payload, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/memories/clear",
		map[string]any{"type": "recent"})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if got := requiredNumber(t, payload, "updated"); got != 0 {
		t.Fatalf("清空近期记忆数目不符: %s", raw)
	}
	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/memories/clear",
		map[string]any{"type": "mid"})
	testkit.ExpectStatus(t, status, http.StatusBadRequest, raw)
	_ = longMemoryID

	// ── 生成任务（admin 手动生成；runner 未启动，任务保持 pending）──
	status, payload, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/generate", map[string]any{})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	taskID := mustID(t, requiredString(t, payload, "task", "id"))
	if got := taskStatus(t, srv.Pool, taskID); got != "pending" {
		t.Fatalf("生成任务应为 pending，实际 %s", got)
	}

	// ── 启用/停用：停用暂停 pending 后台作业并取消 pending 任务 ──
	jobID, jobErr := srv.IDs.Next()
	if jobErr != nil {
		t.Fatalf("生成后台作业 ID 失败: %v", jobErr)
	}
	at := srv.Clock.Now()
	if _, err := srv.Pool.Exec(ctx, `
		INSERT INTO background_jobs (
			id, job_type, ref_type, ref_id, status, run_at, attempts, max_attempts,
			last_error, idempotency_key, payload, created_at, updated_at
		) VALUES ($1, 'ai_schedule', 'ai', $2, 'pending', $3, 0, 3, '', $4, '{}'::jsonb, $3, $3)`,
		jobID, mustID(t, aiID), at, "integration-ai-pause-"+suffix); err != nil {
		t.Fatalf("写入待暂停后台作业失败: %v", err)
	}
	status, payload, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/disable", nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if enabled, _ := payload["is_enabled"].(bool); enabled {
		t.Fatalf("停用响应不符: %s", raw)
	}
	if got := taskStatus(t, srv.Pool, taskID); got != "canceled" {
		t.Fatalf("停用后待执行任务应取消，实际 %s", got)
	}
	if got := jobStatus(t, srv.Pool, jobID); got != "paused" {
		t.Fatalf("停用后 pending 后台作业应暂停，实际 %s", got)
	}
	status, payload, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/enable", nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if enabled, _ := payload["is_enabled"].(bool); !enabled {
		t.Fatalf("启用响应不符: %s", raw)
	}
	status, payload, raw = getJSON(t, client,
		"/api/v1/admin/ai?q="+url.QueryEscape(aiUsername)+"&status=enabled")
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items := requiredList(t, payload); len(items) != 1 {
		t.Fatalf("启用后 AI 列表不符: %s", raw)
	}

	// ── 预设删除保护与更新 ──
	// AI 仍关联原预设：逻辑删除必须被拒绝并返回关联数量。
	status, payload, raw = postJSON(t, client, "/api/v1/admin/providers/"+providerID+"/delete", nil)
	testkit.ExpectStatus(t, status, http.StatusConflict, raw)
	if code := testkit.ErrorCode(payload); code != "duplicate" {
		t.Fatalf("删除有关联 AI 的预设错误码不符: %s", raw)
	}
	if count := requiredNumber(t, payload, "error", "details", "ai_count"); count != 1 {
		t.Fatalf("冲突详情 ai_count 不符: %s", raw)
	}

	// 把 AI 切到备用预设后，原预设可以逻辑删除。
	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/model",
		map[string]any{"provider_id": secondProviderID})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, raw = postJSON(t, client, "/api/v1/admin/providers/"+providerID+"/delete", nil)
	testkit.ExpectStatus(t, status, http.StatusNoContent, raw)
	status, payload, raw = getJSON(t, client,
		"/api/v1/admin/providers?q="+url.QueryEscape("本地 Mock "+suffix))
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if items := requiredList(t, payload); len(items) != 0 {
		t.Fatalf("逻辑删除后的预设不应出现在列表: %s", raw)
	}
	status, _, raw = postJSON(t, client, "/api/v1/admin/providers/"+providerID+"/test", nil)
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)

	// 更新备用预设：api_key 留空表示不变更，掩码保持不变。
	status, payload, raw = postJSON(t, client, "/api/v1/admin/providers/"+secondProviderID, map[string]any{
		"name": "改名 Mock " + suffix, "api_key": "",
	})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	updated := mustNested(t, payload, "provider")
	if updated["name"] != "改名 Mock "+suffix || updated["api_key_masked"] != "sk-...-key" {
		t.Fatalf("更新预设结果不符: %v", updated)
	}

	// ── 收尾：恢复本次创建的 AI 为停用状态，避免影响后续运行 ──
	status, _, raw = postJSON(t, client, "/api/v1/admin/ai/"+aiID+"/disable", nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	// ── 审计：关键动作都写入 admin_audit_logs ──
	var auditCount int
	if err := srv.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM admin_audit_logs
		WHERE admin_id = $1 AND action_type IN (
			'provider_create', 'provider_update', 'provider_delete',
			'ai_create', 'ai_update', 'ai_model_update', 'ai_behavior_update',
			'ai_enable', 'ai_disable', 'memory_create', 'memory_update',
			'memory_delete', 'memory_clear', 'ai_generate'
		)`, mustID(t, adminID)).Scan(&auditCount); err != nil {
		t.Fatalf("统计审计失败: %v", err)
	}
	if auditCount < 15 {
		t.Fatalf("审计记录过少: %d", auditCount)
	}
	t.Logf("AI 管理接口集成测试通过，审计记录 %d 条", auditCount)
}

// ── 测试辅助 ──

// prepareTestWorkingDir 让 testkit.LoadConfig 能定位仓库根目录。
//
// testkit.LoadConfig 目前假设测试包位于仓库深度 2（如 internal/server），而本测试位于
// internal/modules/ai/http（深度 4）。这里先向上查找 go.mod 定位仓库根，再切到
// internal/testkit（深度 2），使 testkit 的 "..", ".." 相对路径仍指向仓库根。
// TODO(testkit): 建议 LoadConfig 改为向上查找 go.mod，届时本函数可直接删除。
func prepareTestWorkingDir(t *testing.T) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("读取工作目录失败: %v", err)
	}
	root := original
	for {
		if _, statErr := os.Stat(filepath.Join(root, "go.mod")); statErr == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatalf("找不到仓库根目录（从 %s 向上未发现 go.mod）", original)
		}
		root = parent
	}
	target := filepath.Join(root, "internal", "testkit")
	if err := os.Chdir(target); err != nil {
		t.Fatalf("切换到 testkit 目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
}

// loginAdmin 登录共享测试超管并返回账号 ID（字符串形式）。
func loginAdmin(t *testing.T, client *testkit.Client) string {
	t.Helper()
	payload := client.LoginAndSetCSRF(testkit.IntegrationAdminUsername, testkit.IntegrationAdminPassword)
	return testkit.MustString(t, testkit.MustUser(t, payload), "id")
}

// getJSON 发起 GET 请求并返回状态、JSON 与原始响应体。
func getJSON(t *testing.T, client *testkit.Client, path string) (int, map[string]any, string) {
	t.Helper()
	status, _, payload, raw := client.Call(http.MethodGet, path, false, nil)
	return status, payload, raw
}

// postJSON 发起携带 CSRF 的 POST 请求并返回状态、JSON 与原始响应体。
func postJSON(t *testing.T, client *testkit.Client, path string, body any) (int, map[string]any, string) {
	t.Helper()
	status, _, payload, raw := client.Call(http.MethodPost, path, true, body)
	return status, payload, raw
}

// requiredValue 读取嵌套 JSON 字段；缺失时 Fatal。
func requiredValue(t *testing.T, payload map[string]any, keys ...string) any {
	t.Helper()
	var current any = payload
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("路径 %v 在 %q 处不是对象: %v", keys, key, current)
		}
		current, ok = object[key]
		if !ok {
			t.Fatalf("路径 %v 缺少键 %q，实际: %v", keys, key, object)
		}
	}
	return current
}

// requiredString 读取嵌套字符串字段。
func requiredString(t *testing.T, payload map[string]any, keys ...string) string {
	t.Helper()
	value, ok := requiredValue(t, payload, keys...).(string)
	if !ok || value == "" {
		t.Fatalf("路径 %v 不是非空字符串: %v", keys, requiredValue(t, payload, keys...))
	}
	return value
}

// requiredNumber 读取嵌套数字字段。
func requiredNumber(t *testing.T, payload map[string]any, keys ...string) float64 {
	t.Helper()
	value, ok := requiredValue(t, payload, keys...).(float64)
	if !ok {
		t.Fatalf("路径 %v 不是数字: %v", keys, requiredValue(t, payload, keys...))
	}
	return value
}

// requiredBool 读取嵌套布尔字段。
func requiredBool(t *testing.T, payload map[string]any, keys ...string) bool {
	t.Helper()
	value, ok := requiredValue(t, payload, keys...).(bool)
	if !ok {
		t.Fatalf("路径 %v 不是布尔: %v", keys, requiredValue(t, payload, keys...))
	}
	return value
}

// requiredList 读取响应 items 列表。
func requiredList(t *testing.T, payload map[string]any) []any {
	t.Helper()
	items, ok := payload["items"].([]any)
	if !ok {
		t.Fatalf("响应缺少 items 列表: %v", payload)
	}
	return items
}

// requiredListAt 读取嵌套列表字段。
func requiredListAt(t *testing.T, payload map[string]any, keys ...string) []any {
	t.Helper()
	items, ok := requiredValue(t, payload, keys...).([]any)
	if !ok {
		t.Fatalf("路径 %v 不是列表: %v", keys, requiredValue(t, payload, keys...))
	}
	return items
}

// mustNested 读取响应中的嵌套对象。
func mustNested(t *testing.T, payload map[string]any, key string) map[string]any {
	t.Helper()
	return testkit.MustNested(t, payload, key)
}

// mustID 解析雪花 ID 字符串。
func mustID(t *testing.T, raw string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		t.Fatalf("解析 ID %q 失败: %v", raw, err)
	}
	return id
}

// taskStatus 读取 ai_tasks 状态（任务表由 agent 模块拥有，这里只做测试断言）。
func taskStatus(t *testing.T, pool *pgxpool.Pool, taskID int64) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM ai_tasks WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("读取 AI 任务状态失败: %v", err)
	}
	return status
}

// jobStatus 读取 background_jobs 状态（作业表由 platform/jobs 拥有，这里只做测试断言）。
func jobStatus(t *testing.T, pool *pgxpool.Pool, jobID int64) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM background_jobs WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("读取后台作业状态失败: %v", err)
	}
	return status
}
