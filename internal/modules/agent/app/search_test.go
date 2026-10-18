package app

// search.go 用例测试：联网搜索主题清洗、工具参数校验、错误映射、调用记录与
// 有限工具轮次的边界分支。模型/搜索均使用本地 httptest fake 服务。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/great-magician-01/callable"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/platform/websearch"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

func TestSanitizeSearchQuery(t *testing.T) {
	cleaned := sanitizeSearchQuery("第一行\n第二行\t三\r四\x00五")
	if strings.ContainsAny(cleaned, "\n\r\t\x00") {
		t.Fatalf("应移除换行与控制字符: %q", cleaned)
	}
	if cleaned != "第一行 第二行 三 四五" {
		t.Fatalf("清洗结果错误: %q", cleaned)
	}
	if got := sanitizeSearchQuery("  两侧空白  "); got != "两侧空白" {
		t.Fatalf("应去掉首尾空白: %q", got)
	}
	long := sanitizeSearchQuery(strings.Repeat("搜", maxWebSearchTopicRunes+50))
	if runes := len([]rune(long)); runes != maxWebSearchTopicRunes {
		t.Fatalf("超长主题应截断到 %d 字符，实际 %d", maxWebSearchTopicRunes, runes)
	}
}

func TestTruncateBytes(t *testing.T) {
	cases := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{"空串", "", 5, ""},
		{"非正上限不截断", "abcdef", 0, "abcdef"},
		{"未超上限", "abc", 5, "abc"},
		{"ASCII 截断", "abcdef", 3, "abc"},
		{"UTF-8 边界安全", "中文测试", 7, "中文"},
		{"恰好切在多字节字符中间", "中文", 4, "中"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := truncateBytes(testCase.value, testCase.limit)
			if got != testCase.want {
				t.Fatalf("truncateBytes(%q,%d) = %q，期望 %q", testCase.value, testCase.limit, got, testCase.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("截断结果必须是合法 UTF-8: %q", got)
			}
		})
	}
}

func TestSearchErrorHelpers(t *testing.T) {
	searchErr := &websearch.Error{Code: "invalid_api_key", StatusCode: 401, Message: "密钥无效"}
	code, status := searchErrorInfo(searchErr)
	if code != "invalid_api_key" || status != 401 {
		t.Fatalf("错误信息映射错误: %s %d", code, status)
	}
	if message := searchErrorMessage(searchErr); message != "密钥无效" {
		t.Fatalf("错误文案错误: %q", message)
	}

	code, status = searchErrorInfo(context.Canceled)
	if code != "web_search_canceled" || status != 0 {
		t.Fatalf("取消映射错误: %s %d", code, status)
	}
	if message := searchErrorMessage(context.DeadlineExceeded); message != "网页搜索已取消" {
		t.Fatalf("超时文案错误: %q", message)
	}

	code, status = searchErrorInfo(errBoom)
	if code != "web_search_failed" || status != 0 {
		t.Fatalf("未知错误映射错误: %s %d", code, status)
	}
	if message := searchErrorMessage(errBoom); message != "网页搜索暂时不可用" {
		t.Fatalf("未知错误文案错误: %q", message)
	}
	if message := searchErrorMessage(&websearch.Error{Code: "x"}); message != "网页搜索暂时不可用" {
		t.Fatalf("空文案应回退固定文案: %q", message)
	}
}

func TestToolErrorResultAndInvalidToolResult(t *testing.T) {
	result := toolErrorResult("invalid_query", http.StatusBadRequest, "参数错误")
	if !result.IsError || !strings.Contains(result.Content, "invalid_query") || !strings.Contains(result.Content, "400") {
		t.Fatalf("工具错误结果错误: %+v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("工具错误结果必须是 JSON: %v", err)
	}

	invalid := invalidToolResult(callable.ToolCallPart{ID: "call_1", Name: "fetch"}, "不支持的工具")
	if !invalid.IsError || invalid.ToolCallID != "call_1" || !strings.Contains(invalid.Content, "invalid_tool_call") {
		t.Fatalf("非法工具结果错误: %+v", invalid)
	}
}

func TestWebSearchToolExecute(t *testing.T) {
	tool := &webSearchTool{}
	result := tool.Execute(context.Background(), "不是 JSON")
	if !result.IsError {
		t.Fatalf("非法参数应返回错误结果: %+v", result)
	}
}

func TestWebSearchInstructions(t *testing.T) {
	instructions := webSearchInstructions(domain.DecisionSchema())
	for _, want := range []string{"【联网搜索工具】", webSearchToolName, "不可信", "JSON Schema", `"action"`} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("搜索指令缺少 %q", want)
		}
	}
	withoutSchema := webSearchInstructions(nil)
	if strings.Contains(withoutSchema, "JSON Schema") {
		t.Fatalf("无 schema 时不应输出 schema 段: %s", withoutSchema)
	}
}

func TestWarnWebSearchWithNilLogger(t *testing.T) {
	svc := &Service{}
	svc.warnWebSearch("测试告警", 1, 0, errBoom)
}

func TestCallModelRoundRecordsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"参数错误"}}`))
	}))
	t.Cleanup(server.Close)

	f := newFlowFixture()
	f.ai.runtime = contracts.Runtime{
		AIID:     1,
		Provider: contracts.ProviderConfig{ID: 2, BaseURL: server.URL + "/v1", APIKey: "k", DefaultModel: "test-model", IsEnabled: true},
		Model:    "test-model",
	}
	request := callable.NewRequest(callable.System("系统提示"), callable.User("用户输入"))
	taskID := int64(42)
	_, err := f.svc.callModelRound(context.Background(), f.ai.runtime, taskID, request, 1, false)
	if err == nil {
		t.Fatal("4xx 模型响应应返回错误")
	}
	if len(f.callLogs.inserts) != 1 {
		t.Fatalf("失败调用也应写调用记录，实际 %d", len(f.callLogs.inserts))
	}
	entry := f.callLogs.inserts[0]
	if entry.Status != "error" || entry.TaskID == nil || *entry.TaskID != taskID ||
		entry.AIID == nil || *entry.AIID != 1 || entry.HTTPStatus == nil || *entry.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("失败调用记录字段错误: %+v", entry)
	}
	if entry.ErrorType != domain.ErrorTypeForbidden || entry.ErrorMessage == "" {
		t.Fatalf("失败调用记录错误分类错误: %+v", entry)
	}
	if !strings.Contains(entry.RequestContent, "系统提示") || !strings.Contains(entry.RequestContent, "用户输入") {
		t.Fatalf("调用记录应保存请求内容: %q", entry.RequestContent)
	}
}

func TestCallModelRoundSuccessRecordsUsage(t *testing.T) {
	state := &chatServerState{}
	chat := newChatServer(t, state, func(_ *chatServerState, _ map[string]any) map[string]any {
		return chatCompletionResponse(map[string]any{"role": "assistant", "content": "模型回复"}, "stop")
	})
	temperature := 0.7
	topP := 0.9
	rt := contracts.Runtime{
		AIID:     1,
		Provider: contracts.ProviderConfig{ID: 2, BaseURL: chat.URL + "/v1", APIKey: "k", DefaultModel: "test-model", IsEnabled: true},
		Model:    "override-model",
		Params:   contracts.GenerationParams{MaxTokens: 128, Temperature: &temperature, TopP: &topP},
		Timeout:  5 * time.Second,
	}
	f := newFlowFixture()
	f.ai.runtime = rt
	request := callable.NewRequest(callable.System("系统提示"), callable.User("用户输入"))
	resp, err := f.svc.callModelRound(context.Background(), rt, 7, request, 2, true)
	if err != nil {
		t.Fatalf("模型调用不应失败: %v", err)
	}
	if resp == nil || resp.Text != "模型回复" {
		t.Fatalf("模型响应错误: %+v", resp)
	}
	if len(f.callLogs.inserts) != 1 {
		t.Fatalf("应写 1 条调用记录，实际 %d", len(f.callLogs.inserts))
	}
	entry := f.callLogs.inserts[0]
	if entry.Status != "success" || entry.Model != "override-model" || entry.ProviderID == nil || *entry.ProviderID != 2 {
		t.Fatalf("成功调用记录字段错误: %+v", entry)
	}
	if entry.UsageInputTokens == nil || *entry.UsageInputTokens != 10 ||
		entry.UsageOutputTokens == nil || *entry.UsageOutputTokens != 5 {
		t.Fatalf("用量记录错误: %+v", entry)
	}
	for _, want := range []string{"max_tokens", "search_round", "temperature", "top_p", "web_search"} {
		if !strings.Contains(string(entry.Params), want) {
			t.Fatalf("调用参数缺少 %q: %s", want, entry.Params)
		}
	}
	if !strings.Contains(entry.ResponseContent, "模型回复") {
		t.Fatalf("调用记录应保存响应内容: %q", entry.ResponseContent)
	}
}

func TestCallModelRoundSkipsLogWhenIDFails(t *testing.T) {
	state := &chatServerState{}
	chat := newChatServer(t, state, func(_ *chatServerState, _ map[string]any) map[string]any {
		return chatCompletionResponse(map[string]any{"role": "assistant", "content": "ok"}, "stop")
	})
	rt := contracts.Runtime{
		AIID:     1,
		Provider: contracts.ProviderConfig{ID: 2, BaseURL: chat.URL + "/v1", DefaultModel: "m", IsEnabled: true},
		Timeout:  5 * time.Second,
	}
	f := newFlowFixture()
	f.callLogs = &flowCallLogs{fakeCallLogs: &fakeCallLogs{}}
	svc := New(Deps{Clock: f.clk, IDs: &stuckIDs{err: errBoom}, Log: testLogger(), CallLogs: f.callLogs})
	resp, err := svc.callModelRound(context.Background(), rt, 1, callable.NewRequest(callable.User("hi")), 1, false)
	if err != nil || resp == nil {
		t.Fatalf("生成调用记录 ID 失败不应影响模型调用: resp=%+v err=%v", resp, err)
	}
	if len(f.callLogs.inserts) != 0 {
		t.Fatalf("ID 失败时不应写调用记录: %+v", f.callLogs.inserts)
	}
}

// newWebSearchDecisionService 构造启用联网搜索的决策调用环境，modelHandler 为聊天服务响应。
func newWebSearchDecisionService(
	t *testing.T,
	modelHandler func(state *chatServerState, request map[string]any) map[string]any,
) (*Service, *chatServerState, contracts.Runtime) {
	t.Helper()
	state := &chatServerState{}
	chat := newChatServer(t, state, modelHandler)
	f := newFlowFixture()
	f.ai.webSearch = contracts.WebSearchConfig{BaseURL: "https://search.example.com", APIKey: "search-key"}
	runtime := contracts.Runtime{
		AIID:     1,
		Provider: contracts.ProviderConfig{ID: 1, BaseURL: chat.URL + "/v1", APIKey: "k", DefaultModel: "test-model", IsEnabled: true},
		Model:    "test-model",
		Timeout:  5 * time.Second,
		Behavior: contracts.BehaviorSettings{AllowWebSearch: true},
	}
	f.ai.runtime = runtime
	return f.svc, state, runtime
}

func TestCallDecisionWebSearchDirectDecision(t *testing.T) {
	svc, state, rt := newWebSearchDecisionService(t, func(_ *chatServerState, _ map[string]any) map[string]any {
		return chatCompletionResponse(map[string]any{
			"role": "assistant", "content": `{"action":"noop","content":"","reason":"无需搜索"}`,
		}, "stop")
	})
	resp, err := svc.callDecisionModel(context.Background(), rt, 1, promptInput{Event: "科技新闻"},
		"system", "user", decisionFormat())
	if err != nil {
		t.Fatalf("联网搜索决策不应失败: %v", err)
	}
	if resp == nil || !strings.Contains(resp.Text, "noop") {
		t.Fatalf("应直接返回合法决策: %+v", resp)
	}
	if state.requests != 1 {
		t.Fatalf("模型直接给出决策时应只调用 1 次，实际 %d", state.requests)
	}
}

func TestCallDecisionWebSearchUnsupportedTool(t *testing.T) {
	// 覆盖“模型返回非 web_search 工具”的分支：第一轮返回非法工具，第二轮给出决策。
	unsupportedCalled := false
	svc, state, rt := newWebSearchDecisionService(t, func(_ *chatServerState, _ map[string]any) map[string]any {
		if !unsupportedCalled {
			unsupportedCalled = true
			return chatCompletionResponse(toolCallMessage("call_other", "fetch_url", `{"url":"https://example.com"}`), "tool_calls")
		}
		return chatCompletionResponse(map[string]any{
			"role": "assistant", "content": `{"action":"noop","content":"","reason":"忽略工具"}`,
		}, "stop")
	})
	resp, err := svc.callDecisionModel(context.Background(), rt, 1, promptInput{Event: "科技新闻"},
		"system", "user", decisionFormat())
	if err != nil {
		t.Fatalf("不支持工具应降级为决策而非报错: %v", err)
	}
	if resp == nil {
		t.Fatal("应返回最终决策")
	}
	if state.requests != 2 {
		t.Fatalf("非法工具轮后应进入最终轮，实际 %d 次", state.requests)
	}
}

func TestCallDecisionWithWebSearchModelError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	f := newFlowFixture()
	f.ai.runtime = contracts.Runtime{
		AIID:     1,
		Provider: contracts.ProviderConfig{ID: 1, BaseURL: server.URL + "/v1", DefaultModel: "m", IsEnabled: true},
		Model:    "m",
		Timeout:  5 * time.Second,
	}
	_, err := f.svc.callDecisionWithWebSearch(context.Background(), f.ai.runtime, 1,
		"system", "user", decisionFormat(),
		contracts.WebSearchConfig{BaseURL: "https://search.example.com", APIKey: "k"}, "主题")
	if err == nil {
		t.Fatal("模型错误应从联网搜索流程返回")
	}
}

func TestNormalizeAssistantToolCallIDsKeepsExistingID(t *testing.T) {
	message := callable.Message{Role: callable.RoleAssistant}
	message.Parts = append(message.Parts,
		callable.ToolCallPart{ID: "existing", Name: webSearchToolName, Arguments: `{"query":"a"}`},
		callable.ToolCallPart{Name: webSearchToolName, Arguments: `{"query":"b"}`},
	)
	normalized := normalizeAssistantToolCallIDs(message)
	calls := normalized.ToolCalls()
	if len(calls) != 2 || calls[0].ID != "existing" {
		t.Fatalf("已有 ID 不应被改写: %+v", calls)
	}
	if calls[1].ID == "" || calls[1].ID == "existing" {
		t.Fatalf("缺失 ID 应补齐为唯一 ID: %+v", calls)
	}
}
