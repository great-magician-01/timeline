// Package domain 实现 ai 模块的纯规则：模型预设、AI 身份/模型/行为配置、分层记忆的
// 校验、规范化与运行时快照类型。本包不访问数据库、不依赖 gin/pgx，也不 import 其他
// 模块；错误语义由调用方（app）映射为 platform/apperr。
package domain

import (
	"encoding/json"
	"time"
)

// ProviderConfig 是模型接口预设的运行时视图。
// APIKey 是解密后的明文，仅供服务端内部构造调用客户端使用，绝不出现在响应、日志或审计中。
type ProviderConfig struct {
	ID           int64
	Name         string
	BaseURL      string
	APIKey       string
	DefaultModel string
	IsEnabled    bool
}

// GenerationParams 对应 ai_model_settings.generation_params；
// 零值（MaxTokens 为 0、指针为 nil、Stop 为空）表示未配置，不使用该参数。
type GenerationParams struct {
	MaxTokens   int
	Temperature *float64
	TopP        *float64
	Stop        []string
}

// BehaviorSettings 与 ai_behavior_settings 列一一对应。
// CandidateScopeParams / RateLimits / Schedule 保留原始 JSONB，由调用方按需解析。
type BehaviorSettings struct {
	AllowOriginalPost  bool
	AllowActiveComment bool
	AllowPassiveReply  bool
	AllowLike          bool
	AllowFollow        bool
	AllowUnfollow      bool
	AllowRepost        bool
	AllowQuote         bool
	AllowAIToAI        bool
	AllowWebSearch     bool

	CandidateScopeType   string
	CandidateScopeParams json.RawMessage
	RateLimits           json.RawMessage
	CooldownSeconds      int
	ActiveHoursStart     int
	ActiveHoursEnd       int
	Schedule             json.RawMessage
	MaxReplyDepth        int
}

// DefaultBehaviorSettings 与 ai_behavior_settings 的数据库默认值保持一致。
func DefaultBehaviorSettings() BehaviorSettings {
	return BehaviorSettings{
		AllowPassiveReply:    true,
		CandidateScopeType:   "none",
		CandidateScopeParams: json.RawMessage(`{}`),
		RateLimits:           json.RawMessage(`{}`),
		CooldownSeconds:      900,
		ActiveHoursStart:     8,
		ActiveHoursEnd:       23,
		Schedule:             json.RawMessage(`{}`),
		MaxReplyDepth:        2,
	}
}

// DailyLimit 从率限制 JSON 中取某个动作的每日上限；0 或缺失表示不限制。
// action 使用 ai_tasks.action_type 取值；"total"（或空串）读取 total 上限，
// reply 未单独配置时回退到 daily_comment。
func (b BehaviorSettings) DailyLimit(action string) int {
	if len(b.RateLimits) == 0 {
		return 0
	}
	limits := map[string]int{}
	if err := json.Unmarshal(b.RateLimits, &limits); err != nil {
		return 0
	}
	key := "daily_" + action
	if action == "" || action == "total" {
		key = "daily_total"
	}
	if value, ok := limits[key]; ok && value > 0 {
		return value
	}
	if action == "reply" {
		if value, ok := limits["daily_comment"]; ok && value > 0 {
			return value
		}
	}
	return 0
}

// Profile 是 ai_profiles 的运行时视图。
type Profile struct {
	UserID         int64
	Background     string
	Personality    string
	CoreValues     string
	LanguageStyle  string
	Topics         string
	TargetLanguage string
	Taboos         string
	SystemPrompt   string
	IsEnabled      bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Runtime 是一次 AI 决策所需的全部配置快照。
// Model 是合并 model_override 之后的实际模型名；Provider.APIKey 只在服务端内存中使用。
type Runtime struct {
	AIID     int64
	Username string
	Nickname string
	Profile  Profile
	Provider ProviderConfig
	Model    string
	Params   GenerationParams
	Timeout  time.Duration
	Behavior BehaviorSettings
}

// Memory 是 ai_memories 的运行时视图；检索函数只返回 status='active' 的记忆。
type Memory struct {
	ID             int64
	AID            int64
	MemoryType     string
	Status         string
	Content        string
	Category       string
	Keywords       []string
	Importance     int
	SourceType     string
	SourceRefType  string
	SourceRefID    *int64
	InferenceLevel string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastUsedAt     *time.Time
}

// MemoryRecord 在 Memory 之外保留 superseded_by_id（仅管理端 DTO 使用，
// 跨模块 Memory 视图不需要该字段）。
type MemoryRecord struct {
	Memory         Memory
	SupersededByID *int64
}

// WebSearchConfig 是执行联网搜索所需的全局配置；APIKey 只在服务端内存使用。
type WebSearchConfig struct {
	BaseURL string
	APIKey  string
}

// Configured 表示搜索能力是否可用（BaseURL 与 APIKey 同时存在）。
func (c WebSearchConfig) Configured() bool {
	return c.BaseURL != "" && c.APIKey != ""
}

// Provider 是 model_providers 的行结构；APIKeyCipher 只在本模块内部流转。
type Provider struct {
	ID           int64
	Name         string
	BaseURL      string
	APIKeyCipher string
	DefaultModel string
	IsEnabled    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ModelSettings 是 ai_model_settings 的读取快照（含联表得到的预设名与实际模型）。
// Provider* 字段来自同一次 LEFT JOIN model_providers：预设行缺失或已逻辑删除时
// ProviderAvailable=false（等价于单独调用 GetProvider 返回不存在），由调用方按不可用处理。
type ModelSettings struct {
	ProviderID     *int64
	ProviderName   string
	ModelOverride  string
	RawParams      json.RawMessage
	TimeoutSeconds int
	Model          string
	UpdatedAt      time.Time

	// 预设运行时字段（与 model_providers 行一一对应，仅运行时装配使用）。
	ProviderAvailable    bool
	ProviderBaseURL      string
	ProviderAPIKeyCipher string
	ProviderDefaultModel string
	ProviderIsEnabled    bool
}

// MemorySource 是 ai_memory_sources 的一条来源关联。
type MemorySource struct {
	ID         int64
	MemoryID   int64
	SourceType string
	SourceID   int64
	CreatedAt  time.Time
}
