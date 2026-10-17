package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/great-magician-01/callable"

	"github.com/great-magician-01/timeline/internal/ai"
)

func TestRenderRequestContentIncludesToolsButNotThinking(t *testing.T) {
	request := callable.NewRequest(
		callable.System("系统提示"),
		callable.User("用户上下文"),
		callable.Assistant(
			callable.TextPart{Text: "先搜索"},
			callable.ToolCallPart{ID: "call_1", Name: webSearchToolName, Arguments: `{"query":"公开新闻"}`},
			callable.ThinkingPart{Text: "不应记录的推理内容"},
		),
		callable.ToolResults(callable.ToolResultPart{
			ToolCallID: "call_1",
			Name:       webSearchToolName,
			Content:    `{"error":{"code":"web_search_not_supported"}}`,
			IsError:    true,
		}),
	)
	content := renderRequestContent(request)
	for _, needle := range []string{
		"系统提示", "用户上下文", "[tool_call id=call_1 name=web_search]",
		`{"query":"公开新闻"}`, "[tool_result tool_call_id=call_1 name=web_search is_error=true]",
		"web_search_not_supported",
	} {
		if !strings.Contains(content, needle) {
			t.Fatalf("请求记录缺少 %q:\n%s", needle, content)
		}
	}
	if strings.Contains(content, "不应记录的推理内容") {
		t.Fatalf("请求记录不应包含 ThinkingPart:\n%s", content)
	}
}

func TestRenderResponseContentIncludesToolCalls(t *testing.T) {
	content := renderResponseContent(&callable.Response{
		Text: "准备搜索",
		ToolCalls: []callable.ToolCall{
			{ID: "call_1", Name: webSearchToolName, Arguments: `{"query":"新闻"}`},
		},
	})
	if !strings.Contains(content, "准备搜索") ||
		!strings.Contains(content, "[tool_call id=call_1 name=web_search]") ||
		!strings.Contains(content, `{"query":"新闻"}`) {
		t.Fatalf("响应记录不完整: %s", content)
	}
	if got := renderResponseContent(nil); got != "" {
		t.Fatalf("nil 响应应返回空文本: %q", got)
	}
}

func TestParamsSnapshotIncludesSearchRoundMetadata(t *testing.T) {
	temperature := 0.5
	raw := paramsSnapshot(callRecord{
		Model:     "mock-model",
		Params:    ai.GenerationParams{Temperature: &temperature},
		Attempt:   2,
		Round:     3,
		WebSearch: true,
	})
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("参数快照不是合法 JSON: %v", err)
	}
	if payload["round"] != float64(3) || payload["web_search"] != true || payload["attempt"] != float64(2) {
		t.Fatalf("搜索轮次元数据缺失: %#v", payload)
	}
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("参数快照不应包含敏感信息: %s", raw)
	}
}
