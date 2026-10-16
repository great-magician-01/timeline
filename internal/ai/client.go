package ai

import (
	"net/http"
	"time"

	"github.com/great-magician-01/callable"
)

// 出站重试与任务层有限重试合并计算（docs/WORKFLOWS.md 7.6），避免重试风暴。
const (
	outboundRetries          = 2
	defaultRequestTimeout    = 60 * time.Second
	providerTestTimeout      = 15 * time.Second
	outboundRetryBackoffStep = 3 * time.Second
)

// NewClient 按预设与生成参数构造 callable 客户端（启用库内置重试与超时）。
//
// 使用 OpenAI Chat Completions 兼容路径；timeout 同时作为单次 HTTP 尝试的超时
// （调用方还应在外层用 context.WithTimeout 包裹整体调用）。客户端默认模型取
// p.DefaultModel，调用方可用 callable.NewRequest(...).WithModel(rt.Model) 应用
// model_override，或在传入前把 Runtime.Model 覆盖到 ProviderConfig.DefaultModel。
func (s *Service) NewClient(p ProviderConfig, params GenerationParams, timeout time.Duration) *callable.Client {
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	httpClient := &http.Client{Timeout: timeout}
	provider := callable.NewOpenAIProvider(p.APIKey, p.BaseURL,
		callable.WithRetries(outboundRetries),
		callable.WithRetryBackoff(outboundRetryBackoffStep, 3*outboundRetryBackoffStep),
		callable.WithHTTPClient(httpClient),
	)
	opts := []callable.ClientOption{callable.WithModel(p.DefaultModel)}
	if params.MaxTokens > 0 {
		opts = append(opts, callable.WithMaxTokens(params.MaxTokens))
	}
	if params.Temperature != nil {
		opts = append(opts, callable.WithTemperature(*params.Temperature))
	}
	if params.TopP != nil {
		opts = append(opts, callable.WithTopP(*params.TopP))
	}
	if len(params.Stop) > 0 {
		opts = append(opts, callable.WithStopSequences(params.Stop...))
	}
	return callable.NewClient(provider, opts...)
}
