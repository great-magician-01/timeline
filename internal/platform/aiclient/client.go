// Package aiclient 是面向模型厂商的 OpenAI Chat Completions 兼容 HTTP 出站适配器。
//
// 本包从旧 internal/ai/client.go 机械迁移而来：库内置出站重试、单次 HTTP 尝试超时
// 与生成参数映射的逻辑保持不变，只把 ai 包内部类型（ProviderConfig/GenerationParams
// 及 Service 方法接收者）替换为本包中字段完全一致的等价类型，因此 modules/ai 后续
// 可以直接用类型转换适配，不需要重写请求组装逻辑。
//
// 具体厂商请求组装、重试、流式与错误类型由 github.com/great-magician-01/callable
// 负责；本包在其上提供稳定的平台边界：Request/Response 等厂商无关类型以别名形式
// 暴露，调用方（modules/ai、modules/agent）只依赖本包即可完成构造与调用。
//
// 分层约定（CODE_QUALITY_REFACTOR_PLAN.md §1、§2）：本包属于 platform 技术基座，
// 不包含业务规则，也不 import 任何旧 internal 包；同一来源的旧包保持只读。
package aiclient

import (
	"context"
	"net/http"
	"time"

	"github.com/great-magician-01/callable"
)

// 出站重试与任务层有限重试合并计算（docs/WORKFLOWS.md 7.6），避免重试风暴。
const (
	// OutboundRetries 是单次模型调用的库内出站重试次数。
	OutboundRetries = 2
	// OutboundRetryBackoffStep 是库内重试的首次退避；后续按 3 倍增长（3s、9s）。
	OutboundRetryBackoffStep = 3 * time.Second
	// DefaultRequestTimeout 是调用方未指定超时时的单次 HTTP 尝试超时。
	DefaultRequestTimeout = 60 * time.Second
	// ProviderTestTimeout 是管理后台测试模型预设连通性的整体超时。
	ProviderTestTimeout = 15 * time.Second
)

// 以下类型是 callable 厂商无关模型在本包的稳定别名：
// 迁移后的 modules 用同一套 Request/Response 组装与解析逻辑，不直接写第三方包名。
type (
	// Request 是单次模型调用。
	Request = callable.Request
	// Response 是单次模型调用的结果。
	Response = callable.Response
	// Message 是一条对话消息，可由 callable.System/User/Assistant 等构造。
	Message = callable.Message
	// ToolCall 是模型请求的一次工具调用。
	ToolCall = callable.ToolCall
	// StopReason 说明模型停止生成的原因。
	StopReason = callable.StopReason
	// Usage 是一次调用的 token 计费信息。
	Usage = callable.Usage
	// ModelInfo 是接口地址返回的模型列表中的一项。
	ModelInfo = callable.ModelInfo
	// APIError 是非 2xx 响应或传输层失败的脱敏错误，可按 StatusCode 分支处理。
	APIError = callable.APIError
	// ClientOption 是客户端选项（模型、生成参数、响应格式、请求/响应钩子）。
	ClientOption = callable.ClientOption
	// ProviderOption 是 provider 选项（HTTP 客户端、重试、附加请求头）。
	ProviderOption = callable.ProviderOption
)

// ProviderConfig 是模型接口预设的运行时视图。
//
// 字段与旧 internal/ai.ProviderConfig 完全一致（顺序、名称、类型相同），
// 迁移时可直接写 aiclient.ProviderConfig(providerConfig) 做类型转换。
// APIKey 是解密后的明文，仅供服务端内部构造调用客户端使用，
// 绝不出现在响应、日志或审计中。
type ProviderConfig struct {
	ID           int64
	Name         string
	BaseURL      string
	APIKey       string
	DefaultModel string
	IsEnabled    bool
}

// GenerationParams 对应 ai_model_settings.generation_params。
// 字段与旧 internal/ai.GenerationParams 完全一致，可直接类型转换。
// 零值（MaxTokens 为 0、指针为 nil、Stop 为空）表示未配置，不使用该参数。
type GenerationParams struct {
	MaxTokens   int
	Temperature *float64
	TopP        *float64
	Stop        []string
}

// ChatClient 是 OpenAI Chat Completions 兼容客户端（启用库内置出站重试与超时）。
//
// 本客户端的 timeout 只限制单次 HTTP 尝试；调用方还应在外层用
// context.WithTimeout 包裹整体调用。默认模型取 ProviderConfig.DefaultModel，
// 需要应用 model_override 时用 callable.NewRequest(...).WithModel(...)。
type ChatClient struct {
	client *callable.Client
}

// NewProvider 按预设构造 OpenAI Chat Completions 兼容 provider。
// timeout 是单次 HTTP 尝试的超时，<=0 时回退到 DefaultRequestTimeout；
// 额外的 ProviderOption 可用于自定义 HTTP 客户端、关闭库内重试或附加请求头
// （agent 调用记录需要自己控制重试节奏时使用）。后传入的选项覆盖默认值。
func NewProvider(p ProviderConfig, timeout time.Duration, opts ...ProviderOption) callable.Provider {
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	httpClient := &http.Client{Timeout: timeout}
	options := append([]ProviderOption{
		callable.WithRetries(OutboundRetries),
		callable.WithRetryBackoff(OutboundRetryBackoffStep, 3*OutboundRetryBackoffStep),
		callable.WithHTTPClient(httpClient),
	}, opts...)
	return callable.NewOpenAIProvider(p.APIKey, p.BaseURL, options...)
}

// NewChatClient 按预设与生成参数构造客户端（启用库内置重试与超时）。
// 构造逻辑等价于旧 internal/ai.Service.NewClient：
// 默认模型取 p.DefaultModel；未配置的生成参数不加入选项。
func NewChatClient(p ProviderConfig, params GenerationParams, timeout time.Duration, opts ...ClientOption) *ChatClient {
	return NewChatClientFromProvider(NewProvider(p, timeout), append(params.clientOptions(p.DefaultModel), opts...)...)
}

// NewChatClientFromProvider 用已构造的 provider 创建客户端。
// 供需要自定义 ProviderOption（例如关闭库内重试、注册请求/响应钩子）的调用方使用，
// 是 agent 调用记录场景迁移旧 callable.NewClient 构造代码的入口。
func NewChatClientFromProvider(provider callable.Provider, opts ...ClientOption) *ChatClient {
	return &ChatClient{client: callable.NewClient(provider, opts...)}
}

// Create 执行一次非流式模型调用。
func (c *ChatClient) Create(ctx context.Context, req *Request) (*Response, error) {
	return c.client.Create(ctx, req)
}

// ListModels 读取接口地址可用的模型列表（GET {BaseURL}/models）。
func (c *ChatClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return c.client.ListModels(ctx)
}

// Underlying 返回底层 callable 客户端。
//
// 迁移期供仍需要 callable 专属能力（工具循环、流式回调、自定义钩子）的调用方
// 过渡使用；新代码优先使用本包方法，以避免业务包直接依赖第三方类型。
func (c *ChatClient) Underlying() *callable.Client { return c.client }

// clientOptions 把生成参数映射为 callable 客户端选项。
// 与旧 internal/ai.client.go 行为一致：零值/未配置参数不加入选项，使用接口默认值。
func (p GenerationParams) clientOptions(model string) []ClientOption {
	opts := []ClientOption{callable.WithModel(model)}
	if p.MaxTokens > 0 {
		opts = append(opts, callable.WithMaxTokens(p.MaxTokens))
	}
	if p.Temperature != nil {
		opts = append(opts, callable.WithTemperature(*p.Temperature))
	}
	if p.TopP != nil {
		opts = append(opts, callable.WithTopP(*p.TopP))
	}
	if len(p.Stop) > 0 {
		opts = append(opts, callable.WithStopSequences(p.Stop...))
	}
	return opts
}
