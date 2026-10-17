// Package http 是 ai 模块的 Gin handler 层：只做参数解析、调用 app 用例与 DTO 组装，
// SQL/事务/领域规则都在 app 与 repo。JSON 形状与旧 internal/ai 的对外契约保持一致。
package http

import (
	"encoding/json"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/ai/app"
	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/view"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// providerListItem 是预设列表项；只暴露密钥掩码，绝不返回明文或密文。
type providerListItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	DefaultModel string `json:"default_model"`
	IsEnabled    bool   `json:"is_enabled"`
	APIKeyMasked string `json:"api_key_masked"`
	AICount      int    `json:"ai_count"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func providerListView(item app.ProviderItem) providerListItem {
	return providerListItem{
		ID:           httpx.FormatID(item.Provider.ID),
		Name:         item.Provider.Name,
		BaseURL:      item.Provider.BaseURL,
		DefaultModel: item.Provider.DefaultModel,
		IsEnabled:    item.Provider.IsEnabled,
		APIKeyMasked: item.APIKeyMasked,
		AICount:      item.AICount,
		CreatedAt:    httpx.FormatTime(item.Provider.CreatedAt),
		UpdatedAt:    httpx.FormatTime(item.Provider.UpdatedAt),
	}
}

// aiTaskBrief 是 AI 详情页的近期任务摘要。
type aiTaskBrief struct {
	ID          string `json:"id"`
	TriggerType string `json:"trigger_type"`
	ActionType  string `json:"action_type"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

// aiMemoryCounts 是 AI 详情页的记忆条数概览。
type aiMemoryCounts struct {
	Recent   int `json:"recent"`
	LongTerm int `json:"long_term"`
}

// aiProfileResponse 是 AI 身份资料的响应形态。
type aiProfileResponse struct {
	Background     string `json:"background"`
	Personality    string `json:"personality"`
	CoreValues     string `json:"core_values"`
	LanguageStyle  string `json:"language_style"`
	Topics         string `json:"topics"`
	TargetLanguage string `json:"target_language"`
	Taboos         string `json:"taboos"`
	SystemPrompt   string `json:"system_prompt"`
	IsEnabled      bool   `json:"is_enabled"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// aiModelResponse 是 AI 模型设置的响应形态。
type aiModelResponse struct {
	ProviderID            *string         `json:"provider_id"`
	ProviderName          string          `json:"provider_name"`
	ModelOverride         string          `json:"model_override"`
	Model                 string          `json:"model"`
	GenerationParams      json.RawMessage `json:"generation_params"`
	RequestTimeoutSeconds int             `json:"request_timeout_seconds"`
	UpdatedAt             string          `json:"updated_at"`
}

// aiBehaviorResponse 是 AI 行为策略的响应形态。
type aiBehaviorResponse struct {
	AllowOriginalPost    bool            `json:"allow_original_post"`
	AllowActiveComment   bool            `json:"allow_active_comment"`
	AllowPassiveReply    bool            `json:"allow_passive_reply"`
	AllowLike            bool            `json:"allow_like"`
	AllowFollow          bool            `json:"allow_follow"`
	AllowUnfollow        bool            `json:"allow_unfollow"`
	AllowRepost          bool            `json:"allow_repost"`
	AllowQuote           bool            `json:"allow_quote"`
	AllowAIToAI          bool            `json:"allow_ai_to_ai"`
	AllowWebSearch       bool            `json:"allow_web_search"`
	CandidateScopeType   string          `json:"candidate_scope_type"`
	CandidateScopeParams json.RawMessage `json:"candidate_scope_params"`
	RateLimits           json.RawMessage `json:"rate_limits"`
	CooldownSeconds      int             `json:"cooldown_seconds"`
	ActiveHoursStart     int             `json:"active_hours_start"`
	ActiveHoursEnd       int             `json:"active_hours_end"`
	Schedule             json.RawMessage `json:"schedule"`
	MaxReplyDepth        int             `json:"max_reply_depth"`
	UpdatedAt            string          `json:"updated_at"`
}

// aiListItem 是 AI 列表项。
type aiListItem struct {
	User         view.UserBriefResponse `json:"user"`
	IsEnabled    bool                   `json:"is_enabled"`
	ProviderID   *string                `json:"provider_id"`
	ProviderName string                 `json:"provider_name"`
	Model        string                 `json:"model"`
	TodayActions int                    `json:"today_actions"`
	CreatedAt    string                 `json:"created_at"`
}

// aiDetailResponse 是 AI 详情。
type aiDetailResponse struct {
	ID           string                 `json:"id"`
	User         view.UserBriefResponse `json:"user"`
	Bio          string                 `json:"bio"`
	Profile      aiProfileResponse      `json:"profile"`
	Model        aiModelResponse        `json:"model"`
	Behavior     aiBehaviorResponse     `json:"behavior"`
	RecentTasks  []aiTaskBrief          `json:"recent_tasks"`
	MemoryCounts aiMemoryCounts         `json:"memory_counts"`
	CreatedAt    string                 `json:"created_at"`
	UpdatedAt    string                 `json:"updated_at"`
}

// memoryResponse 是记忆的管理端 DTO。
type memoryResponse struct {
	ID             string   `json:"id"`
	MemoryType     string   `json:"memory_type"`
	Status         string   `json:"status"`
	Content        string   `json:"content"`
	Category       string   `json:"category"`
	Keywords       []string `json:"keywords"`
	Importance     int      `json:"importance"`
	SourceType     string   `json:"source_type"`
	SourceRefType  string   `json:"source_ref_type"`
	SourceRefID    *string  `json:"source_ref_id"`
	InferenceLevel string   `json:"inference_level"`
	SupersededByID *string  `json:"superseded_by_id"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
	LastUsedAt     *string  `json:"last_used_at"`
}

// modelView 组装模型设置响应。
func modelView(settings domain.ModelSettings) aiModelResponse {
	var providerID *string
	if settings.ProviderID != nil {
		value := httpx.FormatID(*settings.ProviderID)
		providerID = &value
	}
	timeout := settings.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	return aiModelResponse{
		ProviderID:            providerID,
		ProviderName:          settings.ProviderName,
		ModelOverride:         settings.ModelOverride,
		Model:                 settings.Model,
		GenerationParams:      domain.JSONOrEmpty(settings.RawParams),
		RequestTimeoutSeconds: timeout,
		UpdatedAt:             formatTimeOrEmpty(settings.UpdatedAt),
	}
}

// behaviorView 组装行为策略响应。
func behaviorView(settings domain.BehaviorSettings, updatedAt time.Time) aiBehaviorResponse {
	return aiBehaviorResponse{
		AllowOriginalPost:    settings.AllowOriginalPost,
		AllowActiveComment:   settings.AllowActiveComment,
		AllowPassiveReply:    settings.AllowPassiveReply,
		AllowLike:            settings.AllowLike,
		AllowFollow:          settings.AllowFollow,
		AllowUnfollow:        settings.AllowUnfollow,
		AllowRepost:          settings.AllowRepost,
		AllowQuote:           settings.AllowQuote,
		AllowAIToAI:          settings.AllowAIToAI,
		AllowWebSearch:       settings.AllowWebSearch,
		CandidateScopeType:   settings.CandidateScopeType,
		CandidateScopeParams: domain.JSONOrEmpty(settings.CandidateScopeParams),
		RateLimits:           domain.JSONOrEmpty(settings.RateLimits),
		CooldownSeconds:      settings.CooldownSeconds,
		ActiveHoursStart:     settings.ActiveHoursStart,
		ActiveHoursEnd:       settings.ActiveHoursEnd,
		Schedule:             domain.JSONOrEmpty(settings.Schedule),
		MaxReplyDepth:        settings.MaxReplyDepth,
		UpdatedAt:            formatTimeOrEmpty(updatedAt),
	}
}

// profileView 组装身份资料响应。
func profileView(profile domain.Profile) aiProfileResponse {
	return aiProfileResponse{
		Background:     profile.Background,
		Personality:    profile.Personality,
		CoreValues:     profile.CoreValues,
		LanguageStyle:  profile.LanguageStyle,
		Topics:         profile.Topics,
		TargetLanguage: profile.TargetLanguage,
		Taboos:         profile.Taboos,
		SystemPrompt:   profile.SystemPrompt,
		IsEnabled:      profile.IsEnabled,
		CreatedAt:      formatTimeOrEmpty(profile.CreatedAt),
		UpdatedAt:      formatTimeOrEmpty(profile.UpdatedAt),
	}
}

// detailView 组装 AI 详情响应。
func detailView(detail app.AIDetail) aiDetailResponse {
	tasks := make([]aiTaskBrief, 0, len(detail.RecentTasks))
	for _, task := range detail.RecentTasks {
		tasks = append(tasks, aiTaskBrief{
			ID:          httpx.FormatID(task.ID),
			TriggerType: task.TriggerType,
			ActionType:  task.ActionType,
			Status:      task.Status,
			CreatedAt:   httpx.FormatTime(task.CreatedAt),
		})
	}
	return aiDetailResponse{
		ID:           httpx.FormatID(detail.User.ID),
		User:         view.UserBriefResponseFor(detail.User),
		Bio:          detail.Bio,
		Profile:      profileView(detail.Profile),
		Model:        modelView(detail.Model),
		Behavior:     behaviorView(detail.Behavior, detail.BehaviorAt),
		RecentTasks:  tasks,
		MemoryCounts: aiMemoryCounts{Recent: detail.MemoryCounts.Recent, LongTerm: detail.MemoryCounts.LongTerm},
		CreatedAt:    httpx.FormatTime(detail.CreatedAt),
		UpdatedAt:    httpx.FormatTime(detail.UpdatedAt),
	}
}

// memoryView 组装记忆响应。
func memoryView(record domain.MemoryRecord) memoryResponse {
	memory := record.Memory
	keywords := memory.Keywords
	if keywords == nil {
		keywords = []string{}
	}
	return memoryResponse{
		ID:             httpx.FormatID(memory.ID),
		MemoryType:     memory.MemoryType,
		Status:         memory.Status,
		Content:        memory.Content,
		Category:       memory.Category,
		Keywords:       keywords,
		Importance:     memory.Importance,
		SourceType:     memory.SourceType,
		SourceRefType:  memory.SourceRefType,
		SourceRefID:    formatIDPtr(memory.SourceRefID),
		InferenceLevel: memory.InferenceLevel,
		SupersededByID: formatIDPtr(record.SupersededByID),
		CreatedAt:      httpx.FormatTime(memory.CreatedAt),
		UpdatedAt:      httpx.FormatTime(memory.UpdatedAt),
		LastUsedAt:     formatTimePtr(memory.LastUsedAt),
	}
}

// formatTimeOrEmpty 输出时间；零值时间输出空串（表示从未写入）。
func formatTimeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return httpx.FormatTime(t)
}

// formatTimePtr 输出可空时间。
func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := format.FormatTime(*t)
	return &formatted
}

// formatIDPtr 输出可空雪花 ID 字符串。
func formatIDPtr(id *int64) *string {
	if id == nil {
		return nil
	}
	formatted := format.FormatID(*id)
	return &formatted
}

// userBriefView 保留给调用方直接构造作者摘要（避免漏用共享 DTO）。
func userBriefView(brief model.UserBrief) view.UserBriefResponse {
	return view.UserBriefResponseFor(brief)
}
