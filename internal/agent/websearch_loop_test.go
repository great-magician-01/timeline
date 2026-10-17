package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/callable"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/ai"
)

type nilPoolProvider struct{}

func (nilPoolProvider) Pool() *pgxpool.Pool { return nil }

type chatCapture struct {
	body map[string]any
	auth string
}

func newWebSearchTestService() *Service {
	return &Service{
		pools: nilPoolProvider{},
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func writeChatMessage(t *testing.T, w http.ResponseWriter, message map[string]any, finish string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "mock-model",
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": finish,
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	})
}

func writeToolCallResponse(t *testing.T, w http.ResponseWriter, id, query string) {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"query": query})
	writeChatMessage(t, w, map[string]any{
		"role":    "assistant",
		"content": nil,
		"tool_calls": []any{map[string]any{
			"id":   id,
			"type": "function",
			"function": map[string]any{
				"name":      webSearchToolName,
				"arguments": string(args),
			},
		}},
	}, "tool_calls")
}

func writeDecision(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	writeChatMessage(t, w, map[string]any{
		"role":    "assistant",
		"content": `{"action":"noop","reason":"没有合适的动作"}`,
	}, "stop")
}

func decodeRequestBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	defer r.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("解析请求体失败: %v", err)
	}
	return body
}

func messagesOf(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("请求缺少 messages: %#v", body)
	}
	messages := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		message, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("消息格式异常: %#v", item)
		}
		messages = append(messages, message)
	}
	return messages
}

func testWebSearchRuntime(baseURL string) ai.Runtime {
	return ai.Runtime{
		AIID:     42,
		Provider: ai.ProviderConfig{ID: 7, BaseURL: baseURL, APIKey: "model-provider-secret", DefaultModel: "mock-model"},
		Model:    "mock-model",
		Params:   ai.GenerationParams{},
		Timeout:  5 * time.Second,
		Behavior: ai.BehaviorSettings{AllowWebSearch: true},
	}
}

func testSearchConfig(baseURL string) ai.WebSearchConfig {
	return ai.WebSearchConfig{BaseURL: baseURL, APIKey: "global-search-secret"}
}

func testCallRecord() callRecord {
	taskID := int64(99)
	return callRecord{TaskID: &taskID, AID: 42, Attempt: 1, MaxLogBytes: 1024}
}

func TestCallDecisionWithWebSearchTwoRounds(t *testing.T) {
	var (
		chatBodies  []chatCapture
		searchCalls int
		searchAuth  string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			body := decodeRequestBody(t, r)
			chatBodies = append(chatBodies, chatCapture{body: body, auth: r.Header.Get("Authorization")})
			if len(chatBodies) == 1 {
				writeToolCallResponse(t, w, "call_1", "今日科技新闻")
				return
			}
			writeDecision(t, w)
		case "/v1/tools/search":
			searchCalls++
			searchAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[{"title":"新闻","url":"https://example.com/news","summary":"今日摘要","site_name":"Example","date":"2026-10-17T00:00:00Z"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	rt := testWebSearchRuntime(server.URL + "/v1")
	resp, err := newWebSearchTestService().callDecisionWithWebSearch(
		context.Background(), rt, testCallRecord(),
		"身份", "上下文", decisionSchema(), testSearchConfig(server.URL+"/v1"),
	)
	if err != nil {
		t.Fatalf("两轮工具调用应成功: %v", err)
	}
	if _, err := parseDecision(resp.Text); err != nil {
		t.Fatalf("最终决策不可解析: %v", err)
	}
	if len(chatBodies) != 2 || searchCalls != 1 {
		t.Fatalf("调用次数不符: chat=%d search=%d", len(chatBodies), searchCalls)
	}
	if searchAuth != "Bearer global-search-secret" {
		t.Fatalf("搜索必须使用全局独立密钥: %q", searchAuth)
	}
	if chatBodies[0].auth != "Bearer model-provider-secret" {
		t.Fatalf("模型请求应使用模型预设密钥: %q", chatBodies[0].auth)
	}
	for index, capture := range chatBodies {
		if _, ok := capture.body["tools"]; !ok {
			t.Fatalf("工具轮 %d 缺少 tools", index+1)
		}
		if _, ok := capture.body["response_format"]; ok {
			t.Fatalf("工具轮 %d 不应发送 response_format", index+1)
		}
	}
	secondMessages := messagesOf(t, chatBodies[1].body)
	last := secondMessages[len(secondMessages)-1]
	if last["role"] != "tool" || last["tool_call_id"] != "call_1" {
		t.Fatalf("第二轮应包含匹配的 tool result: %#v", last)
	}
	if content, _ := last["content"].(string); !strings.Contains(content, "今日摘要") {
		t.Fatalf("工具结果缺少搜索摘要: %q", content)
	}
}

func TestCallDecisionWithWebSearchForcesFinalSchemaRound(t *testing.T) {
	var (
		chatBodies  []map[string]any
		searchCalls int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			body := decodeRequestBody(t, r)
			chatBodies = append(chatBodies, body)
			switch len(chatBodies) {
			case 1:
				writeToolCallResponse(t, w, "call_1", "第一次搜索")
			case 2:
				writeToolCallResponse(t, w, "call_2", "第二次搜索")
			default:
				writeDecision(t, w)
			}
		case "/v1/tools/search":
			searchCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	rt := testWebSearchRuntime(server.URL + "/v1")
	resp, err := newWebSearchTestService().callDecisionWithWebSearch(
		context.Background(), rt, testCallRecord(),
		"身份", "上下文", decisionSchema(), testSearchConfig(server.URL+"/v1"),
	)
	if err != nil {
		t.Fatalf("工具循环应成功: %v", err)
	}
	if _, err := parseDecision(resp.Text); err != nil {
		t.Fatalf("最终决策不可解析: %v", err)
	}
	if len(chatBodies) != 3 || searchCalls != 2 {
		t.Fatalf("轮次或搜索次数不符: chat=%d search=%d", len(chatBodies), searchCalls)
	}
	lastRequest := chatBodies[2]
	if _, ok := lastRequest["tools"]; ok {
		t.Fatal("最终轮必须移除 tools")
	}
	if _, ok := lastRequest["response_format"]; !ok {
		t.Fatal("最终轮必须恢复 response_format")
	}
}

func TestCallDecisionModelWithoutWebSearchKeepsLegacyRequest(t *testing.T) {
	var (
		chatBodies  []map[string]any
		searchCalls int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tools/search" {
			searchCalls++
			http.NotFound(w, r)
			return
		}
		chatBodies = append(chatBodies, decodeRequestBody(t, r))
		writeDecision(t, w)
	}))
	defer server.Close()

	rt := testWebSearchRuntime(server.URL + "/v1")
	rt.Behavior.AllowWebSearch = false
	_, err := newWebSearchTestService().callDecisionModel(
		context.Background(), rt, testCallRecord(), "身份", "上下文", decisionSchema())
	if err != nil {
		t.Fatalf("普通决策应成功: %v", err)
	}
	if len(chatBodies) != 1 || searchCalls != 0 {
		t.Fatalf("关闭搜索时不应产生额外调用: chat=%d search=%d", len(chatBodies), searchCalls)
	}
	if _, ok := chatBodies[0]["tools"]; ok {
		t.Fatal("关闭搜索时不应声明 tools")
	}
	if _, ok := chatBodies[0]["response_format"]; !ok {
		t.Fatal("普通决策仍应使用 response_format")
	}
}

func TestCallDecisionModelFallsBackWhenSearchUnavailable(t *testing.T) {
	var (
		chatCalls   int
		searchCalls int
		lastBody    map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tools/search" {
			searchCalls++
			http.NotFound(w, r)
			return
		}
		chatCalls++
		lastBody = decodeRequestBody(t, r)
		writeDecision(t, w)
	}))
	defer server.Close()

	rt := testWebSearchRuntime(server.URL + "/v1")
	// Service.ai 为空模拟全局配置不可用；应降级为普通决策而不是阻塞 AI。
	_, err := newWebSearchTestService().callDecisionModel(
		context.Background(), rt, testCallRecord(), "身份", "上下文", decisionSchema())
	if err != nil {
		t.Fatalf("配置缺失时应降级成功: %v", err)
	}
	if chatCalls != 1 || searchCalls != 0 {
		t.Fatalf("降级后调用次数不符: chat=%d search=%d", chatCalls, searchCalls)
	}
	if _, ok := lastBody["tools"]; ok {
		t.Fatal("配置缺失时不应声明 tools")
	}
}

func TestCallDecisionWithWebSearchSearchErrorIsReturnedToModel(t *testing.T) {
	var bodies []map[string]any
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			bodies = append(bodies, decodeRequestBody(t, r))
			if len(bodies) == 1 {
				writeToolCallResponse(t, w, "call_1", "失败的搜索")
				return
			}
			writeDecision(t, w)
		case "/v1/tools/search":
			searchCalls++
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	rt := testWebSearchRuntime(server.URL + "/v1")
	resp, err := newWebSearchTestService().callDecisionWithWebSearch(
		context.Background(), rt, testCallRecord(),
		"身份", "上下文", decisionSchema(), testSearchConfig(server.URL+"/v1"),
	)
	if err != nil {
		t.Fatalf("搜索失败不应中止决策: %v", err)
	}
	if _, err := parseDecision(resp.Text); err != nil {
		t.Fatalf("最终决策不可解析: %v", err)
	}
	if searchCalls != 1 || len(bodies) != 2 {
		t.Fatalf("失败后的调用次数不符: search=%d chat=%d", searchCalls, len(bodies))
	}
	messages := messagesOf(t, bodies[1])
	last := messages[len(messages)-1]
	if last["role"] != "tool" {
		t.Fatalf("错误结果应以 tool 消息回传: %#v", last)
	}
	content, _ := last["content"].(string)
	if !strings.Contains(content, "web_search_not_supported") {
		t.Fatalf("错误工具结果缺少安全错误码: %q", content)
	}
}

func TestCallDecisionWithWebSearchPairsEveryToolCallUnderBudget(t *testing.T) {
	var bodies []map[string]any
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			bodies = append(bodies, decodeRequestBody(t, r))
			if len(bodies) == 1 {
				args, _ := json.Marshal(map[string]string{"query": "并发搜索"})
				writeChatMessage(t, w, map[string]any{
					"role":    "assistant",
					"content": nil,
					"tool_calls": []any{
						map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": webSearchToolName, "arguments": string(args)}},
						map[string]any{"id": "call_2", "type": "function", "function": map[string]any{"name": webSearchToolName, "arguments": string(args)}},
						map[string]any{"id": "call_3", "type": "function", "function": map[string]any{"name": webSearchToolName, "arguments": string(args)}},
					},
				}, "tool_calls")
				return
			}
			writeDecision(t, w)
		case "/v1/tools/search":
			searchCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	rt := testWebSearchRuntime(server.URL + "/v1")
	_, err := newWebSearchTestService().callDecisionWithWebSearch(
		context.Background(), rt, testCallRecord(),
		"身份", "上下文", decisionSchema(), testSearchConfig(server.URL+"/v1"),
	)
	if err != nil {
		t.Fatalf("超预算调用应降级完成: %v", err)
	}
	if searchCalls != maxWebSearchCalls || len(bodies) != 2 {
		t.Fatalf("搜索预算或轮次不符: search=%d chat=%d", searchCalls, len(bodies))
	}
	messages := messagesOf(t, bodies[1])
	toolMessages := make([]map[string]any, 0, 3)
	for _, message := range messages {
		if message["role"] == "tool" {
			toolMessages = append(toolMessages, message)
		}
	}
	if len(toolMessages) != 3 {
		t.Fatalf("每个 tool_call 都必须有结果，实际 %d: %#v", len(toolMessages), messages)
	}
	seen := map[string]bool{}
	for _, message := range toolMessages {
		id, _ := message["tool_call_id"].(string)
		seen[id] = true
	}
	for _, id := range []string{"call_1", "call_2", "call_3"} {
		if !seen[id] {
			t.Fatalf("缺少 %s 的配对结果: %#v", id, seen)
		}
	}
	lastContent, _ := toolMessages[2]["content"].(string)
	if !strings.Contains(lastContent, "web_search_budget_exhausted") {
		t.Fatalf("超预算调用应返回错误结果: %q", lastContent)
	}
}

func TestWebSearchPromptIncludesUntrustedRulesAndSchema(t *testing.T) {
	prompt := webSearchInstructions(decisionSchema())
	for _, needle := range []string{"不可信", "不得执行", "web_search", "JSON Schema"} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("搜索提示词缺少 %q: %s", needle, prompt)
		}
	}
}

func TestSearchToolRejectsInvalidArgumentsWithoutHTTP(t *testing.T) {
	tool := &webSearchTool{apiKey: "key", baseURL: "https://api.deepseek.com", client: nil}
	for _, raw := range []string{
		"", `{"query":"x","extra":1}`, `{"query":"   "}`,
		fmt.Sprintf(`{"query":%q}`, strings.Repeat("搜", 129)),
		`{"query":"line\nbreak"}`, `{"query":"key"}`,
	} {
		result, err := tool.run(context.Background(), raw)
		if err != nil {
			t.Fatalf("%s: 参数校验不应返回内部错误: %v", raw, err)
		}
		if !result.IsError {
			t.Fatalf("%s: 应返回工具错误", raw)
		}
	}
}

func TestNormalizeAssistantToolCallIDs(t *testing.T) {
	message := callable.Assistant(
		callable.ToolCallPart{ID: "", Name: webSearchToolName, Arguments: `{"query":"a"}`},
		callable.ToolCallPart{ID: "call_real", Name: webSearchToolName, Arguments: `{"query":"b"}`},
		callable.ToolCallPart{ID: "", Name: webSearchToolName, Arguments: `{"query":"c"}`},
	)
	normalized := normalizeAssistantToolCallIDs(message)
	calls := normalized.ToolCalls()
	if len(calls) != 3 {
		t.Fatalf("工具调用数量不符: %#v", calls)
	}
	if calls[0].ID == "" || calls[1].ID != "call_real" || calls[2].ID == "" {
		t.Fatalf("ID 归一化不符: %#v", calls)
	}
	if calls[0].ID == calls[2].ID {
		t.Fatalf("生成的 ID 必须唯一: %#v", calls)
	}
	if len(normalizeAssistantToolCallIDs(callable.Message{}).Parts) != 0 {
		t.Fatal("空消息不应生成 parts")
	}
}

func TestEmptyAssistantTurnIsNotReplayed(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodies = append(bodies, decodeRequestBody(t, r))
		if len(bodies) == 1 {
			writeChatMessage(t, w, map[string]any{"role": "assistant", "content": nil}, "stop")
			return
		}
		writeDecision(t, w)
	}))
	defer server.Close()

	rt := testWebSearchRuntime(server.URL + "/v1")
	resp, err := newWebSearchTestService().callDecisionWithWebSearch(
		context.Background(), rt, testCallRecord(),
		"身份", "上下文", decisionSchema(), testSearchConfig(server.URL+"/v1"),
	)
	if err != nil {
		t.Fatalf("空首轮应进入最终修复轮: %v", err)
	}
	if _, err := parseDecision(resp.Text); err != nil {
		t.Fatalf("最终决策不可解析: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("应发生两轮模型调用，实际 %d", len(bodies))
	}
	messages := messagesOf(t, bodies[1])
	if len(messages) != 2 || messages[0]["role"] != "system" || messages[1]["role"] != "user" {
		t.Fatalf("空 assistant 不应被回放: %#v", messages)
	}
}
