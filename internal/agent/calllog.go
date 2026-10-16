package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/great-magician-01/callable"

	"github.com/great-magician-01/timeline/internal/ai"
)

// 出站重试与超时约定与 internal/ai/client.go 保持一致（docs/WORKFLOWS.md 7.6）：
// 出站重试与任务层有限重试合并计算，避免重试风暴。钩子需要在本包注入，
// 因此这里自行按同一组参数构造 callable 客户端。
const (
	outboundRetries          = 2
	outboundRetryBackoffStep = 3 * time.Second
	// maxCallLogBytes 是请求/响应内容的单字段截断上限（默认 64KB）。
	maxCallLogBytes = 64 * 1024
	// callLogWriteTimeout 是调用记录写入的超时；写入失败只记日志。
	callLogWriteTimeout = 5 * time.Second
)

// callRecord 描述一次模型调用的归属信息（写入 ai_call_logs）。
type callRecord struct {
	TaskID     *int64
	AID        int64
	ProviderID int64
	Model      string
	Params     ai.GenerationParams
	Attempt    int
}

// callModel 调用一次模型并写入调用记录。
// 请求/响应内容通过 callable 的请求/响应钩子捕获；记录中绝不包含 API Key 或请求头。
// 返回的 resp 可能为 nil（调用失败时）。
func (s *Service) callModel(
	ctx context.Context,
	rt ai.Runtime,
	rec callRecord,
	systemPrompt, userPrompt string,
	format callable.ResponseFormat,
) (*callable.Response, error) {
	if rt.Provider.ID == 0 || strings.TrimSpace(rt.Provider.BaseURL) == "" {
		return nil, errProviderUnavailable
	}
	model := firstNonEmpty(rt.Model, rt.Provider.DefaultModel)
	if model == "" {
		return nil, errProviderUnavailable
	}
	if rec.Model == "" {
		rec.Model = model
	}
	if rec.ProviderID == 0 {
		rec.ProviderID = rt.Provider.ID
	}
	if rec.Attempt <= 0 {
		rec.Attempt = 1
	}

	timeout := rt.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	httpClient := &http.Client{Timeout: timeout}
	provider := callable.NewOpenAIProvider(rt.Provider.APIKey, rt.Provider.BaseURL,
		callable.WithRetries(outboundRetries),
		callable.WithRetryBackoff(outboundRetryBackoffStep, 3*outboundRetryBackoffStep),
		callable.WithHTTPClient(httpClient),
	)
	options := []callable.ClientOption{callable.WithModel(model)}
	if rt.Params.MaxTokens > 0 {
		options = append(options, callable.WithMaxTokens(rt.Params.MaxTokens))
	}
	if rt.Params.Temperature != nil {
		options = append(options, callable.WithTemperature(*rt.Params.Temperature))
	}
	if rt.Params.TopP != nil {
		options = append(options, callable.WithTopP(*rt.Params.TopP))
	}
	if len(rt.Params.Stop) > 0 {
		options = append(options, callable.WithStopSequences(rt.Params.Stop...))
	}

	var requestText string
	started := time.Now()
	options = append(options,
		callable.WithRequestHook(func(_ context.Context, req *callable.Request) {
			requestText = renderRequestContent(req)
		}),
		callable.WithResponseHook(func(hookCtx context.Context, _ *callable.Request, resp *callable.Response, err error) {
			s.writeCallLog(hookCtx, rec, requestText, resp, err, started)
		}),
	)
	client := callable.NewClient(provider, options...)

	request := callable.NewRequest(
		callable.System(systemPrompt),
		callable.User(userPrompt),
	).WithModel(model)
	if format.Name != "" || format.Schema != nil {
		request = request.WithResponseFormat(format)
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return client.Create(callCtx, request)
}

// errProviderUnavailable 表示该 AI 尚未配置可用的模型接口预设。
var errProviderUnavailable = errors.New("模型接口预设不可用")

// renderRequestContent 把请求消息渲染为可存档文本；只包含角色与文本片段，
// 不包含请求头、API Key 等敏感信息。
func renderRequestContent(req *callable.Request) string {
	if req == nil {
		return ""
	}
	var builder strings.Builder
	for _, message := range req.Messages {
		builder.WriteString("[")
		builder.WriteString(string(message.Role))
		builder.WriteString("]\n")
		for _, part := range message.Parts {
			if text, ok := part.(callable.TextPart); ok {
				builder.WriteString(text.Text)
			}
		}
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

// writeCallLog 写入一条调用记录；写入失败只记日志，不影响任务结果。
// 库内重试的每次尝试各自成条由调用方通过 Attempt 递增体现。
func (s *Service) writeCallLog(
	ctx context.Context,
	rec callRecord,
	requestText string,
	resp *callable.Response,
	callErr error,
	started time.Time,
) {
	pool := s.pools.Pool()
	if pool == nil {
		return
	}
	callLogID, err := s.ids.Next()
	if err != nil {
		s.log.Warn("生成调用记录 ID 失败", "err", err, "ai_id", rec.AID)
		return
	}

	status := "success"
	errorType := ""
	errorMessage := ""
	var httpStatus *int
	var apiErr *callable.APIError
	if callErr != nil {
		status = "error"
		errorMessage = callErr.Error()
		_, errorType, _ = classifyCallError(callErr)
		if errors.As(callErr, &apiErr) && apiErr.StatusCode > 0 {
			code := apiErr.StatusCode
			httpStatus = &code
		}
	}

	requestStored, requestTruncated := truncateBytes(requestText, maxCallLogBytes)
	responseText := ""
	if resp != nil {
		responseText = resp.Text
	}
	responseStored, responseTruncated := truncateBytes(responseText, maxCallLogBytes)
	errorStored, _ := truncateBytes(errorMessage, maxCallLogBytes)

	params := paramsSnapshot(rec)
	var inputTokens, outputTokens, reasoningTokens *int
	if resp != nil {
		input, output, reasoning := resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.ReasoningTokens
		inputTokens, outputTokens, reasoningTokens = &input, &output, &reasoning
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callLogWriteTimeout)
	defer cancel()
	if _, err := pool.Exec(writeCtx, `
		INSERT INTO ai_call_logs (
			id, task_id, ai_id, provider_id, model, params,
			request_content, response_content, status, http_status,
			error_type, error_message, usage_input_tokens, usage_output_tokens,
			usage_reasoning_tokens, attempt, latency_ms, truncated, started_at, finished_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, now())`,
		callLogID, rec.TaskID, nullInt64(rec.AID), nullInt64(rec.ProviderID), rec.Model, params,
		requestStored, responseStored, status, httpStatus,
		errorType, errorStored, inputTokens, outputTokens,
		reasoningTokens, rec.Attempt, time.Since(started).Milliseconds(),
		requestTruncated || responseTruncated, started,
	); err != nil {
		s.log.Warn("写入 AI 调用记录失败", "err", err, "ai_id", rec.AID, "task_id", rec.TaskID)
	}
}

// paramsSnapshot 生成生成参数快照（模型、尝试次数与生效的生成参数）。
// 返回值以 json.RawMessage 直接写入 JSONB 列，避免被当成 JSON 字符串二次编码。
func paramsSnapshot(rec callRecord) json.RawMessage {
	payload := map[string]any{
		"model":   rec.Model,
		"attempt": rec.Attempt,
	}
	if rec.ProviderID > 0 {
		payload["provider_id"] = rec.ProviderID
	}
	if rec.Params.MaxTokens > 0 {
		payload["max_tokens"] = rec.Params.MaxTokens
	}
	if rec.Params.Temperature != nil {
		payload["temperature"] = *rec.Params.Temperature
	}
	if rec.Params.TopP != nil {
		payload["top_p"] = *rec.Params.TopP
	}
	if len(rec.Params.Stop) > 0 {
		payload["stop"] = rec.Params.Stop
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

// nullInt64 把非正数写成 SQL NULL。
func nullInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

// truncateBytes 按字节截断并保证不破坏 UTF-8 编码，返回结果与是否发生截断。
func truncateBytes(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	cut := value[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		_, size := utf8.DecodeLastRuneInString(cut)
		if size <= 0 {
			break
		}
		cut = cut[:len(cut)-size]
	}
	return cut, true
}
