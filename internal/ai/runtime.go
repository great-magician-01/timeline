package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
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

// GenerationParams 对应 ai_model_settings.generation_params。
// 零值（MaxTokens 为 0、指针为 nil、Stop 为空）表示未配置，不使用该参数。
type GenerationParams struct {
	MaxTokens   int
	Temperature *float64
	TopP        *float64
	Stop        []string
}

// GenerationParamsFromJSON 把 JSONB 解析为生成参数结构：缺失键使用零值，
// 非法类型（如字符串形式的 max_tokens）返回错误；未知键忽略以兼容后续扩展。
func GenerationParamsFromJSON(raw []byte) (GenerationParams, error) {
	var params GenerationParams
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return params, nil
	}
	var payload struct {
		MaxTokens   *int      `json:"max_tokens"`
		Temperature *float64  `json:"temperature"`
		TopP        *float64  `json:"top_p"`
		Stop        *[]string `json:"stop"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return GenerationParams{}, fmt.Errorf("生成参数格式无效: %w", err)
	}
	if payload.MaxTokens != nil {
		params.MaxTokens = *payload.MaxTokens
	}
	if payload.Temperature != nil {
		params.Temperature = payload.Temperature
	}
	if payload.TopP != nil {
		params.TopP = payload.TopP
	}
	if payload.Stop != nil {
		params.Stop = *payload.Stop
	}
	return params, nil
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
// action 使用 ai_tasks.action_type 取值（original_post/comment/reply/like/follow/
// unfollow/repost/quote），"total"（或空串）读取 total 上限；
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
}

// Runtime 是一次 AI 决策所需的全部配置快照。
//
// Model 是合并了 model_override 之后的实际模型名；调用方可直接使用
// callable.NewRequest(...).WithModel(rt.Model)，或把 rt.Model 覆盖到
// Provider.DefaultModel 后再交给 NewClient。
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

// defaultBehaviorSettings 与 ai_behavior_settings 的数据库默认值保持一致。
func defaultBehaviorSettings() BehaviorSettings {
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

// jsonOrEmpty 保证 JSONB 字段对外输出对象而不是 null。
func jsonOrEmpty(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(trimmed)
}

// ProviderByID 读取预设（含解密后的明文 API Key，仅供内部使用）。
// 已逻辑删除的预设视为不存在（404 not_found）；停用的预设仍会返回，
// 由调用方通过 IsEnabled 判断是否可用。
func (s *Service) ProviderByID(ctx context.Context, q access.Querier, providerID int64) (ProviderConfig, error) {
	var provider ProviderConfig
	var cipher string
	err := q.QueryRow(ctx, `
		SELECT id, name, base_url, api_key_cipher, default_model, is_enabled
		FROM model_providers
		WHERE id = $1 AND NOT is_deleted`, providerID,
	).Scan(&provider.ID, &provider.Name, &provider.BaseURL, &cipher, &provider.DefaultModel, &provider.IsEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderConfig{}, api.ErrNotFound("模型接口预设不存在")
	}
	if err != nil {
		return ProviderConfig{}, err
	}
	apiKey, err := DecryptSecret(s.secretKey, cipher)
	if err != nil {
		// 错误信息只带预设 ID，不包含密文或明文。
		return ProviderConfig{}, fmt.Errorf("解密模型接口预设 %d 的 API Key 失败: %w", provider.ID, err)
	}
	provider.APIKey = apiKey
	return provider, nil
}

// RuntimeByAI 组装一次 AI 决策所需的配置快照。
// 未找到 AI 用户时返回 404 not_found；其余缺失项（资料、模型设置、行为设置、
// 预设）使用安全默认值或零值，由调用方依据 IsEnabled / Provider.ID 判断可用性。
func (s *Service) RuntimeByAI(ctx context.Context, q access.Querier, aiID int64) (Runtime, error) {
	rt := Runtime{AIID: aiID}

	var isAI bool
	err := q.QueryRow(ctx, `SELECT username, nickname, is_ai FROM users WHERE id = $1`, aiID).
		Scan(&rt.Username, &rt.Nickname, &isAI)
	if errors.Is(err, pgx.ErrNoRows) {
		return Runtime{}, api.ErrNotFound("AI 用户不存在")
	}
	if err != nil {
		return Runtime{}, err
	}
	if !isAI {
		return Runtime{}, api.ErrNotFound("AI 用户不存在")
	}

	rt.Profile = Profile{UserID: aiID, TargetLanguage: "zh-CN"}
	err = q.QueryRow(ctx, `
		SELECT background, personality, core_values, language_style, topics,
		       target_language, taboos, system_prompt, is_enabled
		FROM ai_profiles WHERE user_id = $1`, aiID,
	).Scan(&rt.Profile.Background, &rt.Profile.Personality, &rt.Profile.CoreValues,
		&rt.Profile.LanguageStyle, &rt.Profile.Topics, &rt.Profile.TargetLanguage,
		&rt.Profile.Taboos, &rt.Profile.SystemPrompt, &rt.Profile.IsEnabled)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Runtime{}, err
	}

	rt.Params = GenerationParams{}
	rt.Timeout = 60 * time.Second
	var providerID *int64
	var modelOverride string
	var rawParams []byte
	var timeoutSeconds int
	err = q.QueryRow(ctx, `
		SELECT provider_id, model_override, generation_params, request_timeout_seconds
		FROM ai_model_settings WHERE ai_id = $1`, aiID,
	).Scan(&providerID, &modelOverride, &rawParams, &timeoutSeconds)
	if err == nil {
		if timeoutSeconds >= 5 && timeoutSeconds <= 600 {
			rt.Timeout = time.Duration(timeoutSeconds) * time.Second
		}
		params, parseErr := GenerationParamsFromJSON(rawParams)
		if parseErr != nil {
			return Runtime{}, parseErr
		}
		rt.Params = params
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Runtime{}, err
	}

	if providerID != nil {
		var provider ProviderConfig
		var cipher string
		err = q.QueryRow(ctx, `
			SELECT id, name, base_url, api_key_cipher, default_model, is_enabled
			FROM model_providers
			WHERE id = $1 AND NOT is_deleted`, *providerID,
		).Scan(&provider.ID, &provider.Name, &provider.BaseURL, &cipher, &provider.DefaultModel, &provider.IsEnabled)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// 预留零值：调用方可见 Provider.ID == 0，据此判定预设不可用。
		case err != nil:
			return Runtime{}, err
		default:
			apiKey, decryptErr := DecryptSecret(s.secretKey, cipher)
			if decryptErr != nil {
				return Runtime{}, fmt.Errorf("解密模型接口预设 %d 的 API Key 失败: %w", provider.ID, decryptErr)
			}
			provider.APIKey = apiKey
			rt.Provider = provider
			rt.Model = provider.DefaultModel
		}
	}
	if modelOverride != "" {
		rt.Model = modelOverride
	}

	behavior, _, err := loadAIBehavior(ctx, q, aiID)
	if err != nil {
		return Runtime{}, err
	}
	rt.Behavior = behavior
	return rt, nil
}

// EnabledAIIDs 返回所有启用中的 AI 用户 id（ai_profiles.is_enabled 且用户未停用）。
func (s *Service) EnabledAIIDs(ctx context.Context, q access.Querier) ([]int64, error) {
	rows, err := q.Query(ctx, `
		SELECT u.id
		FROM users u
		JOIN ai_profiles p ON p.user_id = u.id AND p.is_enabled
		WHERE u.is_ai AND u.status = 'active'
		ORDER BY u.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
