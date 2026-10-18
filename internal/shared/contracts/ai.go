package contracts

import (
	"context"
	"encoding/json"
	"time"
)

// ProviderConfig 是模型接口预设的运行时视图。
// APIKey 是解密后的明文，仅供服务端内部构造调用客户端使用，绝不出现在响应、日志或审计中。
// 消费者：agent（决策模型调用）。
type ProviderConfig struct {
	ID           int64
	Name         string
	BaseURL      string
	APIKey       string
	DefaultModel string
	IsEnabled    bool
}

// GenerationParams 对应 ai_model_settings.generation_params；零值表示未配置。
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

// DailyLimit 从率限制 JSON 中取某个动作的每日上限；0 或缺失表示不限制。
// action 使用 ai_tasks.action_type 取值；"total"（或空串）读取 total 上限，
// reply 未单独配置时回退到 daily_comment。消费者：agent（限额判断）。
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
}

// Runtime 是一次 AI 决策所需的全部配置快照。
// Model 是合并 model_override 后的实际模型名；Provider.APIKey 只在服务端内存中使用。
// 消费者：agent（上下文组装、模型调用、动作门槛）。
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
// 消费者：agent（上下文注入、沉淀与失效）。
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

// WebSearchConfig 是执行联网搜索所需的全局配置；APIKey 只在服务端内存使用。
// 消费者：agent（受控联网搜索工具）。
type WebSearchConfig struct {
	BaseURL string
	APIKey  string
}

// Configured 表示搜索能力是否可用（BaseURL 与 APIKey 同时存在）。
func (c WebSearchConfig) Configured() bool {
	return c.BaseURL != "" && c.APIKey != ""
}

// SearchLongTermInput 长期记忆检索条件。消费者：agent。
type SearchLongTermInput struct {
	AIID       int64
	Categories []string
	Keywords   []string
	Limit      int
}

// InvalidateBySourceInput 来源内容变为非公开/删除时失效相关记忆。消费者：agent。
type InvalidateBySourceInput struct {
	AIID       int64
	SourceType string
	SourceID   int64
	At         time.Time
}

// CreateMemoryInput 写入一条 AI 记忆（近期/长期）。消费者：agent（记忆形成/沉淀）。
type CreateMemoryInput struct {
	AIID           int64
	MemoryType     string // "recent" / "long_term"
	Content        string
	Category       string
	Keywords       []string
	Importance     int
	SourceType     string
	SourceRefType  string
	SourceRefID    *int64
	InferenceLevel string
	At             time.Time
}

// MemorySourceInput 是记忆与来源内容的关联。消费者：agent。
type MemorySourceInput struct {
	MemoryID   int64
	SourceType string
	SourceID   int64
	At         time.Time
}

// MemorySourceRef 是记忆来源关联的轻量引用（只包含可复核的公开内容来源：post/comment）。
// 消费者：agent（长期记忆沉淀时聚合来源、来源失效复核）。
type MemorySourceRef struct {
	SourceType string
	SourceID   int64
}

// SupersedeMemoryInput 把一批记忆标记为被新记忆取代。消费者：agent（长期记忆沉淀）。
type SupersedeMemoryInput struct {
	AIID           int64
	MemoryIDs      []int64
	SupersededByID int64
	At             time.Time
}

// InvalidateMemoriesBySourcesInput 来源内容失效时批量停用相关记忆。消费者：agent。
type InvalidateMemoriesBySourcesInput struct {
	AIID    int64
	Sources []MemorySourceInput
	At      time.Time
}

// AIService 是 ai 模块对外发布的全部跨模块能力。
//
// 消费者：agent（运行时/记忆/搜索配置）、admin（预测密文能力）。
// 实现方：modules/ai/app 的 App 类型（组合根注入）。
type AIService interface {
	// RuntimeByAI 组装一次 AI 决策所需的配置快照（含解密后的预设 API Key）。
	// AI 用户不存在时返回领域 NotFound。消费者：agent。
	RuntimeByAI(ctx context.Context, aiID int64) (Runtime, error)

	// EnabledAIIDs 返回所有启用中的 AI 用户 ID（调度器每轮读取）。消费者：agent。
	EnabledAIIDs(ctx context.Context) ([]int64, error)

	// RecentMemories 读取有效近期记忆（按 ID 倒序）。消费者：agent。
	RecentMemories(ctx context.Context, aiID int64, limit int) ([]Memory, error)

	// SearchLongTerm 按分类/关键词检索有效长期记忆。消费者：agent。
	SearchLongTerm(ctx context.Context, in SearchLongTermInput) ([]Memory, error)

	// TouchMemories 更新记忆最近使用时间。消费者：agent。
	TouchMemories(ctx context.Context, memoryIDs []int64, at time.Time) error

	// CountRecentMemories 统计有效近期记忆数量（沉淀触发判断）。消费者：agent。
	CountRecentMemories(ctx context.Context, aiID int64) (int, error)

	// InvalidateBySource 来源内容失效时停用相关记忆。消费者：agent。
	InvalidateBySource(ctx context.Context, in InvalidateBySourceInput) error

	// CreateMemory 写入一条记忆并返回写入后的记忆行。消费者：agent。
	CreateMemory(ctx context.Context, in CreateMemoryInput) (Memory, error)

	// AddMemorySources 写入记忆与来源内容的关联。消费者：agent。
	AddMemorySources(ctx context.Context, memoryID int64, sources []MemorySourceInput) error

	// ListMemorySources 读取一批记忆已记录的内容来源（post/comment 去重）。消费者：agent（沉淀）。
	ListMemorySources(ctx context.Context, memoryIDs []int64) ([]MemorySourceRef, error)

	// ListActiveMemorySourceRefs 按 keyset 游标读取某 AI 生效记忆引用的内容来源（去重、有限条数）。
	// after 为零值表示从头开始；实现返回按 (source_type, source_id) 升序、严格大于 after 的来源。
	// 消费者：agent（来源失效复核：来源删除或转非公开时停用相关记忆）。调用方用返回的最后一条
	// 推进游标，取尽后归零重新轮转，避免每轮重复扫描同一批来源。
	ListActiveMemorySourceRefs(ctx context.Context, aiID int64, after MemorySourceRef, limit int) ([]MemorySourceRef, error)

	// SupersedeMemories 把一批记忆标记为被新记忆取代。消费者：agent。
	SupersedeMemories(ctx context.Context, in SupersedeMemoryInput) error

	// InvalidateMemoriesBySources 批量按来源失效记忆，返回受影响数量。消费者：agent。
	InvalidateMemoriesBySources(ctx context.Context, in InvalidateMemoriesBySourcesInput) (int, error)

	// WebSearchConfig 读取并解密全局联网搜索配置。消费者：agent。
	WebSearchConfig(ctx context.Context) (WebSearchConfig, error)
}

// SecretCipher 是 ai 模块发布的敏感配置加密能力（admin 不接触主密钥）。
// 消费者：admin（保存/校验全局网页搜索密钥）。
type SecretCipher interface {
	// EncryptSecret 使用服务持有的 32 字节密钥加密敏感配置。
	EncryptSecret(plain string) (string, error)
	// CanDecryptSecret 只验证密文能否用当前主密钥解开（不返回明文）；空串视为可解密。
	CanDecryptSecret(encoded string) bool
}
