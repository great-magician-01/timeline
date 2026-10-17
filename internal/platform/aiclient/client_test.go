package aiclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/great-magician-01/callable"
)

// TestNewChatClientWiring 只验证客户端可以按预设与生成参数构造，不做网络调用。
func TestNewChatClientWiring(t *testing.T) {
	client := NewChatClient(ProviderConfig{
		ID: 1, Name: "本地 mock", BaseURL: "http://127.0.0.1:9/v1",
		APIKey: "sk-mock", DefaultModel: "mock-model", IsEnabled: true,
	}, GenerationParams{
		MaxTokens: 64, Temperature: floatPtr(0.2), TopP: floatPtr(0.8), Stop: []string{"END"},
	}, time.Second)
	if client == nil || client.Underlying() == nil {
		t.Fatal("NewChatClient 不应返回 nil")
	}

	// 零值超时应回退到默认值，空生成参数不应影响构造。
	if client := NewChatClient(ProviderConfig{BaseURL: "http://127.0.0.1:9/v1"}, GenerationParams{}, 0); client == nil {
		t.Fatal("NewChatClient 零超时应回退默认值")
	}

	// 自定义 ProviderOption 与 ClientOption 的组合构造路径（agent 调用记录使用）。
	provider := NewProvider(ProviderConfig{BaseURL: "http://127.0.0.1:9/v1", APIKey: "sk-mock"},
		0, callable.WithRetries(0))
	client = NewChatClientFromProvider(provider)
	if client == nil || client.Underlying() == nil {
		t.Fatal("NewChatClientFromProvider 不应返回 nil")
	}
}

// TestChatClientCreate 用本地 httptest 服务验证请求线路：端点、鉴权头、
// 默认模型与生成参数都按预设写入请求体，响应文本能正确解析。
func TestChatClientCreate(t *testing.T) {
	var gotPath, gotAuth string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取请求体: %v", err)
		}
		if err := json.Unmarshal(body, &gotPayload); err != nil {
			t.Errorf("请求体不是合法 JSON: %v（%s）", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","object":"chat.completion","model":"mock-model",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"你好，世界"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`)
	}))
	defer server.Close()

	client := NewChatClient(ProviderConfig{
		BaseURL: server.URL + "/v1", APIKey: "sk-mock", DefaultModel: "mock-model",
	}, GenerationParams{
		MaxTokens: 64, Temperature: floatPtr(0.2), TopP: floatPtr(0.8), Stop: []string{"END"},
	}, time.Second)

	resp, err := client.Create(context.Background(), callable.NewRequest(
		callable.System("系统提示"), callable.User("你好"),
	))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resp.Text != "你好，世界" {
		t.Fatalf("响应文本不符合预期: %q", resp.Text)
	}

	if gotPath != "/v1/chat/completions" {
		t.Errorf("请求路径错误: %q", gotPath)
	}
	if gotAuth != "Bearer sk-mock" {
		t.Errorf("鉴权头错误: %q", gotAuth)
	}
	if gotPayload["model"] != "mock-model" {
		t.Errorf("默认模型未写入请求体: %v", gotPayload["model"])
	}
	// 非官方 OpenAI 兼容接口使用 max_tokens（官方地址才会改用 max_completion_tokens）。
	maxTokens, hasMaxTokens := gotPayload["max_tokens"]
	if !hasMaxTokens {
		maxTokens, hasMaxTokens = gotPayload["max_completion_tokens"]
	}
	if !hasMaxTokens || maxTokens != float64(64) {
		t.Errorf("max_tokens 未按生成参数写入: %v", gotPayload)
	}
	if gotPayload["temperature"] != 0.2 {
		t.Errorf("temperature 未按生成参数写入: %v", gotPayload["temperature"])
	}
	if gotPayload["top_p"] != 0.8 {
		t.Errorf("top_p 未按生成参数写入: %v", gotPayload["top_p"])
	}
	stop, _ := gotPayload["stop"].([]any)
	if len(stop) != 1 || stop[0] != "END" {
		t.Errorf("stop 未按生成参数写入: %v", gotPayload["stop"])
	}
}

// TestChatClientListModels 验证模型列表接口的路径与解析。
func TestChatClientListModels(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"model-a"},{"id":"model-b"}]}`)
	}))
	defer server.Close()

	client := NewChatClient(ProviderConfig{
		BaseURL: server.URL + "/v1", APIKey: "sk-mock", DefaultModel: "model-a",
	}, GenerationParams{}, time.Second)

	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Errorf("模型列表路径错误: %q", gotPath)
	}
	names := make([]string, 0, len(models))
	for _, info := range models {
		names = append(names, info.ID)
	}
	if len(names) != 2 || names[0] != "model-a" || names[1] != "model-b" {
		t.Fatalf("模型列表解析错误: %v", names)
	}
}

// TestChatClientCreateAPIError 验证非 2xx 响应返回可判定的 APIError。
func TestChatClientCreateAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"type":"authentication_error","message":"invalid api key"}}`)
	}))
	defer server.Close()

	// 通过自定义 ProviderOption 关闭库内重试：401 本身不可重试，
	// 这里同时验证 NewProvider/NewChatClientFromProvider 这条迁移路径。
	provider := NewProvider(ProviderConfig{
		BaseURL: server.URL + "/v1", APIKey: "sk-bad", DefaultModel: "mock-model",
	}, time.Second, callable.WithRetries(0))
	client := NewChatClientFromProvider(provider)

	_, err := client.Create(context.Background(), callable.NewRequest(callable.User("你好")))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *APIError，实际: %v", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized || apiErr.Message != "invalid api key" {
		t.Fatalf("APIError 不符合预期: %+v", apiErr)
	}
}

func floatPtr(v float64) *float64 { return &v }
