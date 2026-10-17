// Package websearch 封装 DeepSeek Search API。
//
// 搜索接口与聊天模型接口相互独立：调用方显式传入全局搜索 Base URL 与独立
// API Key，因此任意模型厂商的 AI 都可以使用。本包只提供固定端点的只读搜索，
// 不开放 Fetch、任意 URL 或请求头透传。
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultBaseURL 是 DeepSeek 官方 API 根地址；管理后台可配置兼容网关。
	DefaultBaseURL = "https://api.deepseek.com"

	// QueryRuneLimit 是 DeepSeek Search 对 query 的长度上限。
	QueryRuneLimit = 128
	// resultLimit 固定向搜索接口请求并返回的结果条数，避免上下文与费用失控。
	resultLimit = 5

	maxBaseURLBytes      = 512
	maxRawResponseBytes  = 1 << 20
	maxToolResultBytes   = 8 << 10
	maxTitleRunes        = 200
	maxURLRunes          = 1024
	maxSummaryRunes      = 800
	maxSiteNameRunes     = 100
	maxDateRunes         = 40
	maxSearchAttempts    = 3
	retryBackoffStep     = 3 * time.Second
	retryBackoffMax      = 9 * time.Second
	defaultSearchTimeout = 15 * time.Second
)

// Result 是提供给模型的单条搜索结果；不包含网页正文与图标。
type Result struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Summary  string `json:"summary,omitempty"`
	SiteName string `json:"site_name,omitempty"`
	Date     string `json:"date,omitempty"`
}

// Output 是工具结果中的搜索输出。
type Output struct {
	Query     string   `json:"query"`
	Untrusted bool     `json:"untrusted"`
	Truncated bool     `json:"truncated,omitempty"`
	Results   []Result `json:"results"`
}

// Error 是经过脱敏的搜索接口错误：只保留安全错误码、HTTP 状态与固定文案，
// 不包含上游响应正文、完整 URL 或 Authorization。
type Error struct {
	Code       string
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Client 执行固定端点的搜索请求，并负责有限重试与结果裁剪。
type Client struct {
	httpClient *http.Client
	sleep      func(context.Context, time.Duration) error
}

// NewClient 创建一个带超时与禁止重定向的搜索客户端。
func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = defaultSearchTimeout
	}
	return newClient(&http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			// 绝不跟随重定向，避免 Authorization 被带到其他 host。
			return http.ErrUseLastResponse
		},
	})
}

func newClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: defaultSearchTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Client{httpClient: httpClient, sleep: sleepContext}
}

// NormalizeBaseURL 校验并规范化全局搜索 Base URL。
// 只接受 http/https、非空 host，且不允许 userinfo、query 或 fragment。
func NormalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("搜索 Base URL 不能为空")
	}
	if len(trimmed) > maxBaseURLBytes {
		return "", fmt.Errorf("搜索 Base URL 不能超过 %d 字节", maxBaseURLBytes)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return "", fmt.Errorf("搜索 Base URL 必须是有效的绝对地址")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("搜索 Base URL 只支持 http 或 https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", fmt.Errorf("搜索 Base URL 不能包含凭据、查询参数或片段")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed.String(), nil
}

// SearchURL 从 Base URL 派生 DeepSeek Search 端点。
// 根路径会补 /v1；已以 /v1 结尾的路径直接追加 /tools/search；
// 其他路径视为网关前缀并补 /v1。
func SearchURL(baseURL string) (string, error) {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", fmt.Errorf("搜索 Base URL 无效")
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case path == "":
		path = "/v1"
	case !strings.HasSuffix(path, "/v1"):
		path += "/v1"
	}
	parsed.Path = path + "/tools/search"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

// rawResult 是搜索响应中本包使用的字段；未知字段（正文、图标、评分等）忽略。
type rawResult struct {
	Title    string  `json:"title"`
	URL      string  `json:"url"`
	Snippet  string  `json:"snippet"`
	Summary  string  `json:"summary"`
	SiteName string  `json:"site_name"`
	Date     *string `json:"date"`
}

// Search 调用 Search API 并返回裁剪后的结果。
// 调用方必须传入全局独立搜索密钥；本包从不读取模型接口预设的密钥。
func (c *Client) Search(ctx context.Context, baseURL, apiKey, query string) (Output, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Output{}, &Error{Code: "invalid_query", Message: "搜索关键词不能为空"}
	}
	if utf8.RuneCountInString(query) > QueryRuneLimit {
		return Output{}, &Error{Code: "invalid_query", Message: fmt.Sprintf("搜索关键词不能超过 %d 个字符", QueryRuneLimit)}
	}
	if strings.TrimSpace(apiKey) == "" {
		return Output{}, &Error{Code: "web_search_not_configured", Message: "全局网页搜索密钥尚未配置"}
	}
	endpoint, err := SearchURL(baseURL)
	if err != nil {
		return Output{}, &Error{Code: "web_search_config_invalid", Message: "网页搜索 Base URL 无效"}
	}
	if c == nil {
		c = NewClient(0)
	}
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = NewClient(0).httpClient
	}
	sleep := c.sleep
	if sleep == nil {
		sleep = sleepContext
	}

	body, err := json.Marshal(struct {
		Query       string `json:"query"`
		Count       int    `json:"count"`
		NeedContent bool   `json:"need_content"`
	}{Query: query, Count: resultLimit, NeedContent: false})
	if err != nil {
		return Output{}, &Error{Code: "web_search_failed", Message: "网页搜索请求失败"}
	}

	var lastErr error
	for attempt := 1; attempt <= maxSearchAttempts; attempt++ {
		output, callErr := c.searchOnce(ctx, httpClient, endpoint, apiKey, body)
		if callErr == nil {
			output.Query = query
			output.Untrusted = true
			return output, nil
		}
		lastErr = callErr
		if !retryable(callErr) || attempt >= maxSearchAttempts {
			break
		}
		if err := sleep(ctx, retryDelay(attempt)); err != nil {
			return Output{}, err
		}
	}
	return Output{}, lastErr
}

func (c *Client) searchOnce(
	ctx context.Context,
	httpClient *http.Client,
	endpoint, apiKey string,
	body []byte,
) (Output, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Output{}, &Error{Code: "web_search_failed", Message: "网页搜索请求失败"}
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := httpClient.Do(request)
	if err != nil {
		// 任务上下文的取消/截止时间不可重试；HTTP 客户端自身的单次超时
		// 属于瞬态错误，交给外层按 3s/9s 退避重试。
		if ctx.Err() != nil {
			return Output{}, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return Output{}, &Error{Code: "web_search_timeout", Message: "网页搜索请求超时"}
		}
		return Output{}, &Error{Code: "web_search_unavailable", Message: "网页搜索服务暂时不可用"}
	}
	defer response.Body.Close()

	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxRawResponseBytes+1))
	if readErr != nil {
		// 读取失败通常是连接截断，Status 不携带上游状态，也不能当成永久成功。
		return Output{}, &Error{Code: "web_search_unavailable", Message: "网页搜索服务暂时不可用"}
	}
	if len(raw) > maxRawResponseBytes {
		return Output{}, &Error{Code: "web_search_response_too_large", StatusCode: response.StatusCode, Message: "网页搜索返回内容过大"}
	}
	if response.StatusCode != http.StatusOK {
		return Output{}, statusError(response.StatusCode)
	}

	var payload struct {
		Results []rawResult `json:"results"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Output{}, &Error{Code: "web_search_invalid_response", StatusCode: response.StatusCode, Message: "网页搜索返回格式无效"}
	}
	return buildOutput(payload.Results), nil
}

func buildOutput(rawResults []rawResult) Output {
	output := Output{Results: make([]Result, 0, resultLimit)}
	for _, item := range rawResults {
		if len(output.Results) >= resultLimit {
			output.Truncated = true
			break
		}
		summary := strings.TrimSpace(item.Summary)
		if summary == "" {
			summary = strings.TrimSpace(item.Snippet)
		}
		result := Result{
			Title:    truncateRunes(strings.TrimSpace(item.Title), maxTitleRunes),
			URL:      truncateRunes(strings.TrimSpace(item.URL), maxURLRunes),
			Summary:  truncateRunes(summary, maxSummaryRunes),
			SiteName: truncateRunes(strings.TrimSpace(item.SiteName), maxSiteNameRunes),
		}
		if item.Date != nil {
			result.Date = truncateRunes(strings.TrimSpace(*item.Date), maxDateRunes)
		}
		if result.Title != strings.TrimSpace(item.Title) || result.URL != strings.TrimSpace(item.URL) ||
			result.Summary != summary || result.SiteName != strings.TrimSpace(item.SiteName) {
			output.Truncated = true
		}
		output.Results = append(output.Results, result)
	}
	// 逐字段裁剪后仍可能因多字节文本超过预算；从尾部移除结果，保持合法 JSON。
	for len(output.Results) > 0 {
		encoded, err := json.Marshal(output)
		if err == nil && len(encoded) <= maxToolResultBytes {
			break
		}
		output.Results = output.Results[:len(output.Results)-1]
		output.Truncated = true
	}
	if output.Results == nil {
		output.Results = []Result{}
	}
	return output
}

func statusError(status int) *Error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &Error{Code: "web_search_unauthorized", StatusCode: status, Message: "网页搜索鉴权失败"}
	case http.StatusPaymentRequired:
		return &Error{Code: "web_search_insufficient_balance", StatusCode: status, Message: "网页搜索账户余额不足"}
	case http.StatusTooManyRequests:
		return &Error{Code: "web_search_rate_limited", StatusCode: status, Message: "网页搜索请求过于频繁"}
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return &Error{Code: "web_search_not_supported", StatusCode: status, Message: "当前搜索服务未提供兼容接口"}
	default:
		return &Error{Code: "web_search_failed", StatusCode: status, Message: "网页搜索请求失败"}
	}
}

func retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var searchErr *Error
	if !errors.As(err, &searchErr) {
		return false
	}
	if searchErr.StatusCode == http.StatusNotImplemented {
		// 501 是“端点未实现”的永久错误，不能按普通 5xx 重试。
		return false
	}
	return searchErr.StatusCode == 0 || searchErr.StatusCode == http.StatusRequestTimeout ||
		searchErr.StatusCode == http.StatusTooManyRequests || searchErr.StatusCode >= 500
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := retryBackoffStep
	for index := 1; index < attempt; index++ {
		delay *= 3
		if delay >= retryBackoffMax {
			return retryBackoffMax
		}
	}
	return delay
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}
