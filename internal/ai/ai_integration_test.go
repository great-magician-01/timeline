package ai

// AI 管理接口集成测试：把真实 Handler 指向开发实例的 timeline_test / user_test
// 隔离 schema，验证 SQL、类型转换与完整流程。默认跳过，需显式设置
// TIMELINE_INTEGRATION=1 才会执行（docs/DATA_MODEL.md 4.3）：
//
//	TIMELINE_INTEGRATION=1 go test ./internal/ai/ -run TestAIAdminIntegration -count=1 -v
//
// 测试数据只写入隔离测试 schema（不触碰生产业务 schema），且与账号集成测试一致，
// 遵从“不物理删除”的约定：断言全部基于本次运行生成的唯一标识，可重复运行。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/config"
	"github.com/great-magician-01/timeline/internal/id"
	"github.com/great-magician-01/timeline/internal/migrate"
)

type checkPools struct{ pool *pgxpool.Pool }

func (p checkPools) Pool() *pgxpool.Pool { return p.pool }

func loadCheckConfig(t *testing.T) *config.Config {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wd, "..", "..")
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	return cfg
}

func checkDSN(productionDSN, timelineSchema, userSchema string) (string, error) {
	u, err := url.Parse(productionDSN)
	if err != nil {
		return "", err
	}
	query := u.Query()
	query.Set("search_path", timelineSchema+","+userSchema)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func checkCall(
	t *testing.T,
	fn func(*gin.Context),
	method, target string,
	body any,
	adminID int64,
	params gin.Params,
) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.Request = req
	c.Params = params
	c.Set("timeline.auth.user", auth.User{ID: adminID, Role: auth.RoleAdmin, Status: auth.StatusActive})
	fn(c)
	// 直接调用 Handler 时不会经过 Engine 的收尾阶段：这里补一次 WriteHeaderNow，
	// 让只设置状态码（如 204）而不写正文的响应也能落到记录器上。
	c.Writer.WriteHeaderNow()
	return w
}

func checkDecode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v，原文: %s", err, w.Body.String())
	}
	return out
}

func checkJSONPath(t *testing.T, payload map[string]any, path ...string) any {
	t.Helper()
	var current any = payload
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("路径 %v 在 %q 处不是对象: %v", path, key, current)
		}
		current, ok = m[key]
		if !ok {
			t.Fatalf("路径 %v 缺少键 %q，实际: %v", path, key, m)
		}
	}
	return current
}

func TestAIAdminIntegration(t *testing.T) {
	if os.Getenv("TIMELINE_INTEGRATION") != "1" {
		t.Skip("设置 TIMELINE_INTEGRATION=1 后运行临时数据库校验")
	}
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cfg := loadCheckConfig(t)
	dsn, err := checkDSN(cfg.DSN(), "timeline_test", "user_test")
	if err != nil {
		t.Fatalf("构造测试 DSN 失败: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接测试 schema 失败: %v", err)
	}
	// 先注册连接池关闭，最后注册数据清理：t.Cleanup 后进先出，
	// 保证清理时连接池仍然可用。
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := migrate.RunWithSchemas(ctx, pool, log, "timeline_test", "user_test"); err != nil {
		// 其他并行工作的迁移文件可能暂时不可解析；只要所需表已存在就继续。
		var ready bool
		if checkErr := pool.QueryRow(ctx, `
			SELECT to_regclass('timeline_test.ai_memories') IS NOT NULL
			   AND to_regclass('timeline_test.ai_behavior_settings') IS NOT NULL
			   AND to_regclass('user_test.users') IS NOT NULL`).Scan(&ready); checkErr != nil {
			t.Fatalf("检查测试表失败: %v", checkErr)
		}
		if !ready {
			t.Fatalf("测试 schema 迁移失败且所需表不存在: %v", err)
		}
		t.Logf("迁移报错但所需表已存在，继续执行: %v", err)
	}
	ids, err := id.NewGenerator(7)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(checkPools{pool}, ids, log, testKey())
	adminID, _ := ids.Next()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	username := "ai_check_" + suffix
	const plainKey = "sk-integration-secret-key"
	// 本次运行的唯一标识（suffix）用于隔离断言；测试行保留在测试 schema 中，
	// 不做物理删除，后续运行不会互相干扰。

	// ── 预设 CRUD ──
	w := checkCall(t, service.CreateProvider, "POST", "/api/v1/admin/providers", map[string]any{
		"name": "本地 Mock " + suffix, "base_url": "http://127.0.0.1:9/v1",
		"api_key": plainKey, "default_model": "mock-1",
	}, adminID, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("创建预设失败: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), plainKey) {
		t.Fatal("创建预设响应泄露明文密钥")
	}
	payload := checkDecode(t, w)
	providerID := checkJSONPath(t, payload, "provider", "id").(string)
	if masked := checkJSONPath(t, payload, "provider", "api_key_masked").(string); masked != "sk-...-key" {
		t.Fatalf("掩码不符合预期: %q", masked)
	}

	w = checkCall(t, service.CreateProvider, "POST", "/api/v1/admin/providers", map[string]any{
		"name": "本地 Mock " + suffix, "base_url": "http://127.0.0.1:9/v1",
		"api_key": plainKey, "default_model": "mock-1",
	}, adminID, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("重名预设应返回 409: %d %s", w.Code, w.Body.String())
	}
	w = checkCall(t, service.CreateProvider, "POST", "/api/v1/admin/providers", map[string]any{
		"name": "坏地址 " + suffix, "base_url": "ftp://x", "api_key": "", "default_model": "m",
	}, adminID, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 base_url 应返回 400: %d %s", w.Code, w.Body.String())
	}

	// 预设名含空格，查询参数必须转义后才能放进请求目标。
	nameQuery := url.QueryEscape("Mock " + suffix)
	w = checkCall(t, service.ListProviders, "GET", "/api/v1/admin/providers?q="+nameQuery+"&status=enabled&limit=20", nil, adminID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("列表失败: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), plainKey) || strings.Contains(w.Body.String(), "v1:") {
		t.Fatalf("列表响应泄露密钥: %s", w.Body.String())
	}
	items := checkJSONPath(t, checkDecode(t, w), "items").([]any)
	if len(items) != 1 || checkJSONPath(t, items[0].(map[string]any), "id").(string) != providerID {
		t.Fatalf("列表结果不符合预期: %v", items)
	}
	if count := checkJSONPath(t, items[0].(map[string]any), "ai_count").(float64); count != 0 {
		t.Fatalf("ai_count 应为 0: %v", count)
	}

	w = checkCall(t, service.TestProvider, "POST", "/api/v1/admin/providers/"+providerID+"/test", nil, adminID,
		gin.Params{{Key: "id", Value: providerID}})
	if w.Code != http.StatusOK {
		t.Fatalf("连接测试必须返回 200: %d %s", w.Code, w.Body.String())
	}
	testPayload := checkDecode(t, w)
	if testPayload["ok"] != false {
		t.Fatalf("不可达地址应返回 ok=false: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), plainKey) {
		t.Fatal("连接测试错误信息泄露密钥")
	}
	t.Logf("连接测试错误信息: %v", testPayload["error"])

	// ── 创建 AI ──
	createAI := func(username string, provider string, extra map[string]any) *httptest.ResponseRecorder {
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
		return checkCall(t, service.CreateAI, "POST", "/api/v1/admin/ai", body, adminID, nil)
	}

	w = createAI(username, providerID, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("创建 AI 失败: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), plainKey) || strings.Contains(w.Body.String(), "api_key") {
		t.Fatalf("AI 详情泄露密钥字段: %s", w.Body.String())
	}
	aiID := checkJSONPath(t, checkDecode(t, w), "ai", "id").(string)
	if got := checkJSONPath(t, checkDecode(t, w), "ai", "behavior", "candidate_scope_type").(string); got != "tags" {
		t.Fatalf("行为设置未保存: %s", w.Body.String())
	}
	if got := checkJSONPath(t, checkDecode(t, w), "ai", "behavior", "allow_web_search").(bool); got {
		t.Fatalf("联网搜索应默认关闭: %s", w.Body.String())
	}
	if got := checkJSONPath(t, checkDecode(t, w), "ai", "model", "generation_params", "max_tokens").(float64); got != 256 {
		t.Fatalf("生成参数未保存: %s", w.Body.String())
	}

	if w := createAI(username, providerID, nil); w.Code != http.StatusConflict {
		t.Fatalf("重名 AI 应返回 409: %d %s", w.Code, w.Body.String())
	}
	if w := createAI(username+"b", providerID, map[string]any{"behavior": map[string]any{"candidate_scope_type": "tags"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("tags 缺参数应返回 400: %d %s", w.Code, w.Body.String())
	}
	if w := createAI(username+"c", "999999999", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("不存在预设应返回 400: %d %s", w.Code, w.Body.String())
	}
	if w := createAI(username+"d", providerID, map[string]any{"generation_params": map[string]any{"top_n": 5}}); w.Code != http.StatusBadRequest {
		t.Fatalf("未知生成参数应返回 400: %d %s", w.Code, w.Body.String())
	}

	// ── 详情、列表、更新 ──
	w = checkCall(t, service.GetAI, "GET", "/api/v1/admin/ai/"+aiID, nil, adminID, gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK {
		t.Fatalf("读取 AI 详情失败: %d %s", w.Code, w.Body.String())
	} else {
		detail := checkDecode(t, w)
		if got := checkJSONPath(t, detail, "ai", "profile", "background").(string); got != "背景" {
			t.Fatalf("profile 未返回: %s", w.Body.String())
		}
		if got := checkJSONPath(t, detail, "ai", "model", "provider_id").(string); got != providerID {
			t.Fatalf("model.provider_id 不符: %s", w.Body.String())
		}
		if got := checkJSONPath(t, detail, "ai", "memory_counts", "recent").(float64); got != 0 {
			t.Fatalf("memory_counts 应为 0: %s", w.Body.String())
		}
		if got := checkJSONPath(t, detail, "ai", "recent_tasks").([]any); len(got) != 0 {
			t.Fatalf("recent_tasks 应为空: %s", w.Body.String())
		}
	}
	nonAIID, _ := ids.Next()
	if w := checkCall(t, service.GetAI, "GET", "/x", nil, adminID, gin.Params{{Key: "id", Value: fmt.Sprintf("%d", nonAIID)}}); w.Code != http.StatusNotFound {
		t.Fatalf("非 AI 用户应 404: %d", w.Code)
	}

	w = checkCall(t, service.ListAIs, "GET", "/api/v1/admin/ai?q="+username+"&status=disabled", nil, adminID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("AI 列表失败: %d %s", w.Code, w.Body.String())
	}
	aiItems := checkJSONPath(t, checkDecode(t, w), "items").([]any)
	if len(aiItems) != 1 {
		t.Fatalf("AI 列表结果不符: %s", w.Body.String())
	}
	first := aiItems[0].(map[string]any)
	if first["is_enabled"] != false || first["today_actions"].(float64) != 0 || first["provider_name"] != "本地 Mock "+suffix {
		t.Fatalf("AI 列表字段不符: %v", first)
	}

	w = checkCall(t, service.UpdateAI, "POST", "/api/v1/admin/ai/"+aiID, map[string]any{
		"nickname": "新昵称", "bio": "新简介", "system_prompt": "新的提示词",
	}, adminID, gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK {
		t.Fatalf("更新 AI 失败: %d %s", w.Code, w.Body.String())
	}
	if got := checkJSONPath(t, checkDecode(t, w), "ai", "user", "nickname").(string); got != "新昵称" {
		t.Fatalf("昵称未更新: %s", w.Body.String())
	}
	if w := checkCall(t, service.UpdateAI, "POST", "/x", map[string]any{"nickname": " "}, adminID,
		gin.Params{{Key: "id", Value: aiID}}); w.Code != http.StatusBadRequest {
		t.Fatalf("空昵称应 400: %d %s", w.Code, w.Body.String())
	}

	w = checkCall(t, service.UpdateAIModel, "POST", "/x", map[string]any{
		"provider_id": providerID, "model_override": "mock-2",
		"generation_params":       map[string]any{"max_tokens": 512, "temperature": 0.3},
		"request_timeout_seconds": 30,
	}, adminID, gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK {
		t.Fatalf("更新模型失败: %d %s", w.Code, w.Body.String())
	}
	modelBlock := checkJSONPath(t, checkDecode(t, w), "model").(map[string]any)
	if modelBlock["model"] != "mock-2" || modelBlock["request_timeout_seconds"].(float64) != 30 {
		t.Fatalf("模型设置不符: %v", modelBlock)
	}
	if w := checkCall(t, service.UpdateAIModel, "POST", "/x", map[string]any{"request_timeout_seconds": 3}, adminID,
		gin.Params{{Key: "id", Value: aiID}}); w.Code != http.StatusBadRequest {
		t.Fatalf("超时越界应 400: %d %s", w.Code, w.Body.String())
	}
	if w := checkCall(t, service.UpdateAIModel, "POST", "/x", map[string]any{"provider_id": "888888888"}, adminID,
		gin.Params{{Key: "id", Value: aiID}}); w.Code != http.StatusBadRequest {
		t.Fatalf("不存在预设应 400: %d %s", w.Code, w.Body.String())
	}

	w = checkCall(t, service.UpdateAIBehavior, "POST", "/x", map[string]any{
		"allow_like": true, "allow_ai_to_ai": true, "allow_web_search": true, "cooldown_seconds": 60,
		"rate_limits":            map[string]any{"daily_like": 5, "daily_total": 10},
		"candidate_scope_type":   "authors",
		"candidate_scope_params": map[string]any{"usernames": []string{"alice"}},
	}, adminID, gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK {
		t.Fatalf("更新行为失败: %d %s", w.Code, w.Body.String())
	}
	if got := checkJSONPath(t, checkDecode(t, w), "behavior", "cooldown_seconds").(float64); got != 60 {
		t.Fatalf("行为设置不符: %s", w.Body.String())
	}
	if got := checkJSONPath(t, checkDecode(t, w), "behavior", "allow_web_search").(bool); !got {
		t.Fatalf("联网搜索开关未保存: %s", w.Body.String())
	}
	if w := checkCall(t, service.UpdateAIBehavior, "POST", "/x", map[string]any{"rate_limits": map[string]any{"daily_unknown": 1}}, adminID,
		gin.Params{{Key: "id", Value: aiID}}); w.Code != http.StatusBadRequest {
		t.Fatalf("未知率限制应 400: %d %s", w.Code, w.Body.String())
	}
	// 校验的是补丁应用后的最终状态：把范围切到 tags 但不提供 tags 数组必须被拒绝
	//（既有参数里只有 usernames，无法满足 tags 的要求）。
	if w := checkCall(t, service.UpdateAIBehavior, "POST", "/x", map[string]any{"candidate_scope_type": "tags"}, adminID,
		gin.Params{{Key: "id", Value: aiID}}); w.Code != http.StatusBadRequest {
		t.Fatalf("tags 缺参数应 400: %d %s", w.Code, w.Body.String())
	}
	if w := checkCall(t, service.UpdateAIBehavior, "POST", "/x", map[string]any{
		"candidate_scope_type": "authors", "candidate_scope_params": map[string]any{"usernames": []string{"bob"}},
	}, adminID, gin.Params{{Key: "id", Value: aiID}}); w.Code != http.StatusOK {
		t.Fatalf("补充 authors 参数应成功: %d %s", w.Code, w.Body.String())
	}

	// ── 记忆 ──
	w = checkCall(t, service.CreateMemory, "POST", "/x", map[string]any{
		"memory_type": "recent", "content": "和用户讨论了 Go 并发", "category": "event",
		"keywords": []string{"Go", "go", "  ", "并发"}, "importance": 4,
	}, adminID, gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusCreated {
		t.Fatalf("创建近期记忆失败: %d %s", w.Code, w.Body.String())
	}
	recentMemoryID := checkJSONPath(t, checkDecode(t, w), "memory", "id").(string)
	// 关键词按大小写不敏感去重并去掉空白项："Go" 与 "go" 合并，只剩 2 个。
	if kw := checkJSONPath(t, checkDecode(t, w), "memory", "keywords").([]any); len(kw) != 2 {
		t.Fatalf("关键词去重失败: %s", w.Body.String())
	}
	w = checkCall(t, service.CreateMemory, "POST", "/x", map[string]any{
		"memory_type": "long_term", "content": "用户长期关注 Go 语言的写作话题", "category": "topic",
		"keywords": []string{"Go", "写作"}, "importance": 5, "inference_level": "summary",
	}, adminID, gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusCreated {
		t.Fatalf("创建长期记忆失败: %d %s", w.Code, w.Body.String())
	}
	longMemoryID := checkJSONPath(t, checkDecode(t, w), "memory", "id").(string)
	if w := checkCall(t, service.CreateMemory, "POST", "/x", map[string]any{
		"memory_type": "mid", "content": "x",
	}, adminID, gin.Params{{Key: "id", Value: aiID}}); w.Code != http.StatusBadRequest {
		t.Fatalf("非法类型应 400: %d %s", w.Code, w.Body.String())
	}
	if w := checkCall(t, service.CreateMemory, "POST", "/x", map[string]any{
		"memory_type": "recent", "content": "",
	}, adminID, gin.Params{{Key: "id", Value: aiID}}); w.Code != http.StatusBadRequest {
		t.Fatalf("空正文应 400: %d %s", w.Code, w.Body.String())
	}

	w = checkCall(t, service.ListMemories, "GET", "/x?type=recent", nil, adminID, gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK {
		t.Fatalf("记忆列表失败: %d %s", w.Code, w.Body.String())
	}
	if items := checkJSONPath(t, checkDecode(t, w), "items").([]any); len(items) != 1 {
		t.Fatalf("近期记忆列表不符: %s", w.Body.String())
	}
	w = checkCall(t, service.ListMemories, "GET", "/x?type=long_term&q=写作", nil, adminID, gin.Params{{Key: "id", Value: aiID}})
	if items := checkJSONPath(t, checkDecode(t, w), "items").([]any); len(items) != 1 {
		t.Fatalf("长期记忆搜索不符: %s", w.Body.String())
	}
	w = checkCall(t, service.ListMemories, "GET", "/x?type=long_term&q=不存在", nil, adminID, gin.Params{{Key: "id", Value: aiID}})
	if items := checkJSONPath(t, checkDecode(t, w), "items").([]any); len(items) != 0 {
		t.Fatalf("无匹配应返回空: %s", w.Body.String())
	}
	if w := checkCall(t, service.ListMemories, "GET", "/x", nil, adminID, gin.Params{{Key: "id", Value: aiID}}); w.Code != http.StatusBadRequest {
		t.Fatalf("缺少 type 应 400: %d", w.Code)
	}

	w = checkCall(t, service.UpdateMemory, "POST", "/x", map[string]any{
		"content": "更新后的记忆", "importance": 2,
	}, adminID, gin.Params{{Key: "id", Value: aiID}, {Key: "memory_id", Value: recentMemoryID}})
	if w.Code != http.StatusOK {
		t.Fatalf("更新记忆失败: %d %s", w.Code, w.Body.String())
	}

	// 检索函数
	recent, err := service.RecentMemories(ctx, pool, mustID(t, aiID), 10)
	if err != nil || len(recent) != 1 || recent[0].Content != "更新后的记忆" {
		t.Fatalf("RecentMemories 不符: %v %v", recent, err)
	}
	found, err := service.SearchLongTerm(ctx, pool, mustID(t, aiID), nil, []string{"Go"}, 10)
	if err != nil || len(found) != 1 {
		t.Fatalf("SearchLongTerm 关键词检索不符: %v %v", found, err)
	}
	found, err = service.SearchLongTerm(ctx, pool, mustID(t, aiID), []string{"topic"}, []string{"写作"}, 10)
	if err != nil || len(found) != 1 {
		t.Fatalf("SearchLongTerm 分类+关键词不符: %v %v", found, err)
	}
	found, err = service.SearchLongTerm(ctx, pool, mustID(t, aiID), nil, nil, 10)
	if err != nil || len(found) != 1 {
		t.Fatalf("SearchLongTerm 空条件不符: %v %v", found, err)
	}
	found, err = service.SearchLongTerm(ctx, pool, mustID(t, aiID), nil, []string{"毫无关系"}, 10)
	if err != nil || len(found) != 0 {
		t.Fatalf("SearchLongTerm 无匹配应为空: %v %v", found, err)
	}
	if err := service.TouchMemories(ctx, pool, []int64{recent[0].ID}); err != nil {
		t.Fatalf("TouchMemories 失败: %v", err)
	}
	if count, err := service.CountRecentMemories(ctx, pool, mustID(t, aiID)); err != nil || count != 1 {
		t.Fatalf("CountRecentMemories 不符: %d %v", count, err)
	}
	// 带来源的记忆失效
	sourceMemoryID, _ := ids.Next()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ai_memories (id, ai_id, memory_type, status, content, category, source_type, source_ref_type, source_ref_id)
		VALUES ($1, $2, 'recent', 'active', '来源帖子摘要', 'event', 'post', 'post', $3)`,
		sourceMemoryID, mustID(t, aiID), int64(4242)); err != nil {
		t.Fatalf("写入来源记忆失败: %v", err)
	}
	if err := service.InvalidateBySource(ctx, pool, mustID(t, aiID), "post", 4242); err != nil {
		t.Fatalf("InvalidateBySource 失败: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM ai_memories WHERE id = $1`, sourceMemoryID).Scan(&status); err != nil || status != "invalidated" {
		t.Fatalf("来源记忆未失效: %s %v", status, err)
	}

	// 删除与清空
	w = checkCall(t, service.DeleteMemory, "POST", "/x", nil, adminID,
		gin.Params{{Key: "id", Value: aiID}, {Key: "memory_id", Value: recentMemoryID}})
	if w.Code != http.StatusNoContent {
		t.Fatalf("删除记忆失败: %d %s", w.Code, w.Body.String())
	}
	// 近期记忆此时只剩一条已被删除、一条已失效，默认（active）列表应为空。
	w = checkCall(t, service.ListMemories, "GET", "/x?type=recent", nil, adminID, gin.Params{{Key: "id", Value: aiID}})
	if items := checkJSONPath(t, checkDecode(t, w), "items").([]any); len(items) != 0 {
		t.Fatalf("默认只应列出 active: %s", w.Body.String())
	}
	w = checkCall(t, service.ListMemories, "GET", "/x?type=recent&status=deleted", nil, adminID, gin.Params{{Key: "id", Value: aiID}})
	if items := checkJSONPath(t, checkDecode(t, w), "items").([]any); len(items) != 1 {
		t.Fatalf("已删除记忆应可查: %s", w.Body.String())
	}
	w = checkCall(t, service.ListMemories, "GET", "/x?type=recent&status=invalidated", nil, adminID, gin.Params{{Key: "id", Value: aiID}})
	if items := checkJSONPath(t, checkDecode(t, w), "items").([]any); len(items) != 1 {
		t.Fatalf("已失效记忆应可查: %s", w.Body.String())
	}
	w = checkCall(t, service.ClearMemories, "POST", "/x", map[string]any{"type": "long_term"}, adminID,
		gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK || checkDecode(t, w)["updated"].(float64) != 1 {
		t.Fatalf("清空长期记忆不符: %d %s", w.Code, w.Body.String())
	}
	w = checkCall(t, service.ClearMemories, "POST", "/x", map[string]any{"type": "recent"}, adminID,
		gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK || checkDecode(t, w)["updated"].(float64) != 0 {
		t.Fatalf("清空近期记忆（应只剩 deleted）不符: %d %s", w.Code, w.Body.String())
	}
	w = checkCall(t, service.ClearMemories, "POST", "/x", map[string]any{"type": "mid"}, adminID,
		gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法清空类型应 400: %d", w.Code)
	}
	_ = longMemoryID

	// ── Runtime 与复用 API ──
	rt, err := service.RuntimeByAI(ctx, pool, mustID(t, aiID))
	if err != nil {
		t.Fatalf("RuntimeByAI 失败: %v", err)
	}
	if rt.AIID != mustID(t, aiID) || rt.Provider.ID != mustID(t, providerID) || rt.Provider.APIKey != plainKey {
		t.Fatalf("Runtime 预设不符: %+v", rt.Provider)
	}
	if rt.Model != "mock-2" || rt.Timeout != 30*time.Second || !rt.Behavior.AllowLike ||
		!rt.Behavior.AllowWebSearch || rt.Behavior.DailyLimit("like") != 5 ||
		rt.Behavior.CandidateScopeType != "authors" {
		t.Fatalf("Runtime 参数不符: %+v", rt)
	}
	// 创建时 is_enabled=false，此时资料快照仍应是关闭状态。
	if rt.Profile.IsEnabled {
		t.Fatalf("Runtime 资料不符: %+v", rt.Profile)
	}
	if ids, err := service.EnabledAIIDs(ctx, pool); err != nil || containsID(ids, mustID(t, aiID)) {
		t.Fatalf("未启用的 AI 不应出现在 EnabledAIIDs: %v %v", ids, err)
	}
	providerConfig, err := service.ProviderByID(ctx, pool, mustID(t, providerID))
	if err != nil || providerConfig.APIKey != plainKey || providerConfig.DefaultModel != "mock-1" {
		t.Fatalf("ProviderByID 不符: %+v %v", providerConfig, err)
	}
	if _, err := service.ProviderByID(ctx, pool, 777777777); err == nil {
		t.Fatal("不存在的预设应报错")
	}
	if client := service.NewClient(rt.Provider, rt.Params, rt.Timeout); client == nil {
		t.Fatal("NewClient 返回 nil")
	}
	if _, err := service.RuntimeByAI(ctx, pool, nonAIID); err == nil {
		t.Fatal("非 AI 用户 RuntimeByAI 应报错")
	}

	// ── 启停与预设删除 ──
	w = checkCall(t, service.DisableAI, "POST", "/x", nil, adminID, gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK || checkDecode(t, w)["is_enabled"] != false {
		t.Fatalf("停用 AI 失败: %d %s", w.Code, w.Body.String())
	}
	taskID, _ := ids.Next()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ai_tasks (id, ai_id, trigger_type, status, idempotency_key)
		VALUES ($1, $2, 'manual', 'pending', $3)`, taskID, mustID(t, aiID), "tmp-check-"+suffix); err != nil {
		t.Fatalf("写入待执行任务失败: %v", err)
	}
	w = checkCall(t, service.DisableAI, "POST", "/x", nil, adminID, gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK {
		t.Fatalf("停用 AI 失败: %d", w.Code)
	}
	if status := taskStatus(t, pool, taskID); status != "canceled" {
		t.Fatalf("停用后待执行任务应取消，实际 %s", status)
	}
	w = checkCall(t, service.EnableAI, "POST", "/x", nil, adminID, gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK || checkDecode(t, w)["is_enabled"] != true {
		t.Fatalf("启用 AI 失败: %d %s", w.Code, w.Body.String())
	}
	enabledIDs, err := service.EnabledAIIDs(ctx, pool)
	if err != nil || !containsID(enabledIDs, mustID(t, aiID)) {
		t.Fatalf("启用后应出现在 EnabledAIIDs: %v %v", enabledIDs, err)
	}
	rtEnabled, err := service.RuntimeByAI(ctx, pool, mustID(t, aiID))
	if err != nil || !rtEnabled.Profile.IsEnabled {
		t.Fatalf("启用后 Runtime 资料应为启用状态: %+v %v", rtEnabled.Profile, err)
	}

	w = checkCall(t, service.DeleteProvider, "POST", "/x", nil, adminID, gin.Params{{Key: "id", Value: providerID}})
	if w.Code != http.StatusConflict {
		t.Fatalf("有关联 AI 时删除预设应 409: %d %s", w.Code, w.Body.String())
	}
	if count := checkJSONPath(t, checkDecode(t, w), "error", "details", "ai_count").(float64); count != 1 {
		t.Fatalf("冲突详情不符: %s", w.Body.String())
	}

	// 第二个预设 + 切换关联后再删除
	w = checkCall(t, service.CreateProvider, "POST", "/api/v1/admin/providers", map[string]any{
		"name": "备用 Mock " + suffix, "base_url": "http://127.0.0.1:9/v2",
		"api_key": "sk-second-key", "default_model": "mock-2",
	}, adminID, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("创建第二个预设失败: %d %s", w.Code, w.Body.String())
	}
	secondProviderID := checkJSONPath(t, checkDecode(t, w), "provider", "id").(string)
	w = checkCall(t, service.UpdateAIModel, "POST", "/x", map[string]any{"provider_id": secondProviderID}, adminID,
		gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK {
		t.Fatalf("切换预设失败: %d %s", w.Code, w.Body.String())
	}
	w = checkCall(t, service.DeleteProvider, "POST", "/x", nil, adminID, gin.Params{{Key: "id", Value: providerID}})
	if w.Code != http.StatusNoContent {
		t.Fatalf("删除预设应 204: %d %s", w.Code, w.Body.String())
	}
	w = checkCall(t, service.ListProviders, "GET", "/api/v1/admin/providers?q="+nameQuery, nil, adminID, nil)
	if items := checkJSONPath(t, checkDecode(t, w), "items").([]any); len(items) != 1 {
		t.Fatalf("删除后列表应只剩备用预设: %s", w.Body.String())
	}
	w = checkCall(t, service.TestProvider, "POST", "/x", nil, adminID, gin.Params{{Key: "id", Value: providerID}})
	if w.Code != http.StatusNotFound {
		t.Fatalf("已删除预设测试应 404: %d", w.Code)
	}
	// 更新预设：密钥留空表示不变更
	w = checkCall(t, service.UpdateProvider, "POST", "/x", map[string]any{
		"name": "改名 Mock " + suffix, "api_key": "",
	}, adminID, gin.Params{{Key: "id", Value: secondProviderID}})
	if w.Code != http.StatusOK {
		t.Fatalf("更新预设失败: %d %s", w.Code, w.Body.String())
	}
	updatedProvider := checkJSONPath(t, checkDecode(t, w), "provider").(map[string]any)
	if updatedProvider["name"] != "改名 Mock "+suffix || updatedProvider["api_key_masked"] != "sk-...-key" {
		t.Fatalf("更新预设结果不符: %v", updatedProvider)
	}

	// ── 收尾 ──
	// 全局 AI 开关的读写路径由 internal/agent 与 internal/admin 的 HTTP 接口承担，
	// 在 internal/server 的集成测试中覆盖；这里只负责恢复本次创建的 AI 为停用状态，
	// 避免影响后续运行与其他用例。
	w = checkCall(t, service.DisableAI, "POST", "/x", nil, adminID, gin.Params{{Key: "id", Value: aiID}})
	if w.Code != http.StatusOK || checkDecode(t, w)["is_enabled"] != false {
		t.Fatalf("恢复 AI 停用失败: %d %s", w.Code, w.Body.String())
	}

	// ── 审计 ──
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM admin_audit_logs
		WHERE admin_id = $1 AND action_type IN (
			'provider_create', 'provider_update', 'provider_delete',
			'ai_create', 'ai_update', 'ai_model_update', 'ai_behavior_update',
			'ai_enable', 'ai_disable', 'memory_create', 'memory_update',
			'memory_delete', 'memory_clear', 'ai_global_start', 'ai_global_stop'
		)`, adminID).Scan(&auditCount); err != nil {
		t.Fatalf("统计审计失败: %v", err)
	}
	if auditCount < 15 {
		t.Fatalf("审计记录过少: %d", auditCount)
	}

	t.Logf("AI 管理接口集成测试通过，审计记录 %d 条", auditCount)
}

func containsID(ids []int64, target int64) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func mustID(t *testing.T, raw string) int64 {
	t.Helper()
	var id int64
	if _, err := fmt.Sscanf(raw, "%d", &id); err != nil {
		t.Fatalf("解析 ID %q 失败: %v", raw, err)
	}
	return id
}

func taskStatus(t *testing.T, pool *pgxpool.Pool, taskID int64) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM ai_tasks WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("读取任务状态失败: %v", err)
	}
	return status
}
