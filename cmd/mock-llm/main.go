// mock-llm 是验收用的本地 OpenAI 兼容 mock 服务（docs/ACCEPTANCE.md §2.2）：
// 固定返回预定的结构化决策与内容，让 AI 相关验收与冒烟测试可重复、不依赖真实模型。
//
// 用法：
//
//	go run ./cmd/mock-llm                 # 监听 127.0.0.1:8099
//	MOCK_LLM_ADDR=127.0.0.1:9000 go run ./cmd/mock-llm
//	MOCK_LLM_MODE=noop go run ./cmd/mock-llm
//
// 在管理后台创建接口预设时填写 base_url = http://127.0.0.1:8099/v1、任意 api_key、
// 模型名任意。MOCK_LLM_MODE 支持：
//
//	normal（默认）  按请求的 JSON Schema 名称返回合法结构化结果
//	noop           决策固定返回“无操作”
//	invalid_json   返回无法解析的正文，用于验证 rejected 状态
//	error          返回 500，用于验证 failed 与重试
//	timeout        挂起 30 秒，用于验证超时
//	search            首次工具轮返回 web_search 调用，Search 接口返回 mock 结果
//	search_twice      连续两轮工具调用，用于验证搜索轮次上限
//	search_error      Search 接口返回 404，用于验证搜索失败降级
//	search_auth_error Search 接口返回 401，用于验证搜索鉴权失败降级
//
// 该二进制只用于本地验收，不属于生产部署。
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultAddr     = "127.0.0.1:8099"
	mockModelName   = "mock-model"
	publicPostTitle = "mock 生成的原创帖"
)

type chatRequest struct {
	Model          string           `json:"model"`
	Messages       []chatMessage    `json:"messages"`
	ResponseFormat *responseFormat  `json:"response_format"`
	Tools          []toolDefinition `json:"tools"`
}

type toolDefinition struct {
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string `json:"type"`
	JSONSchema *struct {
		Name string `json:"name"`
	} `json:"json_schema"`
}

func main() {
	addr := envOr("MOCK_LLM_ADDR", defaultAddr)
	mode := envOr("MOCK_LLM_MODE", "normal")

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": mockModelName, "object": "model", "owned_by": "mock"}},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		handleCompletion(w, r, mode)
	})
	mux.HandleFunc("/v1/tools/search", func(w http.ResponseWriter, r *http.Request) {
		handleSearch(w, r, mode)
	})

	log.Printf("mock-llm 启动：addr=%s mode=%s（base_url 请填写 http://%s/v1）", addr, mode, addr)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("mock-llm 退出: %v", err)
	}
}

func handleCompletion(w http.ResponseWriter, r *http.Request, mode string) {
	switch mode {
	case "timeout":
		time.Sleep(30 * time.Second)
	case "error":
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]any{"message": "mock 服务模拟的接口错误", "type": "server_error"},
		})
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"message": "请求体不是合法 JSON", "type": "invalid_request_error"},
		})
		return
	}

	schemaName := ""
	if req.ResponseFormat != nil && req.ResponseFormat.JSONSchema != nil {
		schemaName = req.ResponseFormat.JSONSchema.Name
	}

	if isSearchMode(mode) && hasWebSearchTool(req.Tools) {
		toolRounds := countToolMessages(req.Messages)
		limit := 1
		if mode == "search_twice" {
			limit = 2
		}
		if toolRounds < limit {
			writeToolCallCompletion(w, fmt.Sprintf("call_mock_search_%d", toolRounds+1))
			return
		}
		writeChatCompletion(w, `{"action":"noop","reason":"mock 搜索已完成，本次不执行动作"}`)
		return
	}

	content := buildContent(mode, schemaName, lastUserMessage(req.Messages))
	writeChatCompletion(w, content)
}

func writeChatCompletion(w http.ResponseWriter, content string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl-mock",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   mockModelName,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":     128,
			"completion_tokens": 64,
			"total_tokens":      192,
		},
	})
}

func hasWebSearchTool(tools []toolDefinition) bool {
	for _, tool := range tools {
		if tool.Function.Name == "web_search" {
			return true
		}
	}
	return false
}

func countToolMessages(messages []chatMessage) int {
	count := 0
	for _, message := range messages {
		if message.Role == "tool" {
			count++
		}
	}
	return count
}

func writeToolCallCompletion(w http.ResponseWriter, callID string) {
	arguments := mustJSON(map[string]string{"query": "mock 公开科技新闻"})
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl-mock-tool",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   mockModelName,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]any{{
					"id":   callID,
					"type": "function",
					"function": map[string]any{
						"name":      "web_search",
						"arguments": arguments,
					},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 128, "completion_tokens": 32, "total_tokens": 160},
	})
}

func isSearchMode(mode string) bool {
	return mode == "search" || mode == "search_twice" || mode == "search_error" || mode == "search_auth_error"
}

func handleSearch(w http.ResponseWriter, r *http.Request, mode string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": map[string]any{"message": "只支持 POST", "type": "invalid_request_error"},
		})
		return
	}
	switch mode {
	case "search_error":
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{"message": "mock 搜索接口不可用", "type": "not_found"},
		})
		return
	case "search_auth_error":
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"message": "mock 搜索鉴权失败", "type": "authentication_error"},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": []map[string]any{
			{
				"title":     "mock 搜索新闻",
				"url":       "https://example.com/mock-news",
				"summary":   "这是 mock 搜索接口返回的公开摘要，用于验证工具调用与结果回传。",
				"site_name": "Mock",
				"date":      "2026-10-18T00:00:00Z",
			},
			{
				"title":     "第二条 mock 结果",
				"url":       "https://example.com/mock-news-2",
				"summary":   "第二条公开摘要，验证多条结果裁剪。",
				"site_name": "Mock",
				"date":      "2026-10-18T00:00:00Z",
			},
		},
	})
}

// buildContent 按 schema 名称给出符合结构的结果；无法识别 schema 时返回纯文本。
func buildContent(mode, schemaName, userPrompt string) string {
	if mode == "invalid_json" {
		return "这不是 JSON，用于验证解析失败后的 rejected 状态。"
	}
	switch schemaName {
	case "ai_decision":
		return decisionJSON(mode, userPrompt)
	case "ai_memory":
		return mustJSON(map[string]any{
			"content":         "mock 记录：本次互动已完成，可作为后续上下文参考。",
			"category":        "event",
			"keywords":        []string{"mock", "互动"},
			"importance":      2,
			"inference_level": "summary",
		})
	case "ai_memory_consolidation":
		return mustJSON(map[string]any{
			"memories": []map[string]any{{
				"content":         "mock 长期记忆：持续关注自我介绍类内容。",
				"category":        "topic",
				"keywords":        []string{"mock", "长期"},
				"importance":      3,
				"inference_level": "summary",
			}},
			"reason": "mock 整理完成",
		})
	default:
		return "mock 文本回复：" + userPrompt
	}
}

// decisionJSON 根据提示词中出现的候选类型选择动作，保证不同触发路径都能被验证。
func decisionJSON(mode, userPrompt string) string {
	if mode == "noop" {
		return `{"action":"noop","reason":"mock 模式：本次不执行任何动作"}`
	}
	switch {
	case strings.Contains(userPrompt, "被提及") || strings.Contains(userPrompt, "收到了新的评论"):
		return mustJSON(map[string]any{
			"action":      "comment",
			"target_type": "post",
			"target_id":   sourceID(userPrompt),
			"content":     "mock 回复：谢谢提到我，我的看法是我们应该多观察再下结论。",
			"reason":      "mock 模式：回应提及",
		})
	case strings.Contains(userPrompt, "[候选评论]") || strings.Contains(userPrompt, "评论候选"):
		return mustJSON(map[string]any{
			"action":      "comment",
			"target_type": "comment",
			"target_id":   firstCandidateID(userPrompt),
			"content":     "mock 评论：这个观点很有意思，我也有类似的观察。",
			"reason":      "mock 模式：对公开内容发表评论",
		})
	case strings.Contains(userPrompt, "[候选帖子]") || strings.Contains(userPrompt, "帖子候选"):
		return mustJSON(map[string]any{
			"action":      "comment",
			"target_type": "post",
			"target_id":   firstCandidateID(userPrompt),
			"content":     "mock 评论：读完了，很受启发。",
			"reason":      "mock 模式：评论候选帖子",
		})
	default:
		return mustJSON(map[string]any{
			"action":  "original_post",
			"content": "## " + publicPostTitle + "\n\n这是 mock 服务生成的 Markdown 内容，用于验证 AI 发布链路与安全清洗。\n\n- 支持列表\n- 支持 #话题# 与 @提及",
			"reason":  "mock 模式：生成原创内容",
		})
	}
}

// firstCandidateID 从提示词里抓取候选目标 ID：优先识别 "id=" 前缀，其次抓取第一个
// 18-19 位数字（雪花 ID）；抓不到时返回空串，由后端按“目标非法”走 rejected 分支，
// 这也是一种可验证的结果。
func firstCandidateID(prompt string) string {
	for _, field := range strings.Fields(prompt) {
		field = strings.Trim(field, "[](),;:")
		for _, prefix := range []string{"id=", "ID=", "target_id="} {
			if strings.HasPrefix(field, prefix) {
				return strings.TrimPrefix(field, prefix)
			}
		}
	}
	return firstSnowflake(prompt)
}

// sourceID 解析提示词中「来源 ID：<id>」后面的雪花 ID。
func sourceID(prompt string) string {
	const marker = "来源 ID："
	index := strings.Index(prompt, marker)
	if index < 0 {
		return firstSnowflake(prompt)
	}
	return firstSnowflake(prompt[index+len(marker):])
}

// firstSnowflake 返回提示词中第一个 18-19 位连续数字，作为候选目标 ID 的兜底解析。
func firstSnowflake(prompt string) string {
	digits := 0
	start := -1
	for index, r := range prompt {
		if r >= '0' && r <= '9' {
			if digits == 0 {
				start = index
			}
			digits++
			continue
		}
		if digits >= 18 {
			return prompt[start : start+digits]
		}
		digits = 0
		start = -1
	}
	if digits >= 18 {
		return prompt[start : start+digits]
	}
	return ""
}

func lastUserMessage(messages []chatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		fmt.Fprintln(os.Stderr, "写入响应失败:", err)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
