package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/model"
)

// 用户名规则与人类账号完全一致（docs/DATA_MODEL.md 6.1）。
var aiUsernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{2,31}$`)

// 身份字段长度上限；system_prompt 允许更长。
const (
	maxNicknameRunes      = 50
	maxBioRunes           = 500
	maxIdentityFieldRunes = 1000
	maxSystemPromptRunes  = 8000
	maxLanguageTagRunes   = 20
	maxModelOverrideRunes = 200
	maxTokensLimit        = 100000
	maxStopSequences      = 8
	maxStopSequenceRunes  = 64
	maxDailyLimit         = 1000
	maxCooldownSeconds    = 604800
)

// validRateLimitActions 是 rate_limits 允许的键（与 docs 约定一致）。
var validRateLimitActions = map[string]bool{
	"daily_original_post": true,
	"daily_comment":       true,
	"daily_like":          true,
	"daily_unlike":        true,
	"daily_follow":        true,
	"daily_unfollow":      true,
	"daily_repost":        true,
	"daily_quote":         true,
	"daily_total":         true,
}

var validCandidateScopeTypes = map[string]bool{
	"none":          true,
	"global_public": true,
	"followed":      true,
	"tags":          true,
	"authors":       true,
}

// behaviorInput 是行为策略的补丁结构：nil / 空值表示不变更（创建时表示使用默认值）。
type behaviorInput struct {
	AllowOriginalPost    *bool           `json:"allow_original_post"`
	AllowActiveComment   *bool           `json:"allow_active_comment"`
	AllowPassiveReply    *bool           `json:"allow_passive_reply"`
	AllowLike            *bool           `json:"allow_like"`
	AllowFollow          *bool           `json:"allow_follow"`
	AllowUnfollow        *bool           `json:"allow_unfollow"`
	AllowRepost          *bool           `json:"allow_repost"`
	AllowQuote           *bool           `json:"allow_quote"`
	AllowAIToAI          *bool           `json:"allow_ai_to_ai"`
	AllowWebSearch       *bool           `json:"allow_web_search"`
	CandidateScopeType   *string         `json:"candidate_scope_type"`
	CandidateScopeParams json.RawMessage `json:"candidate_scope_params"`
	RateLimits           json.RawMessage `json:"rate_limits"`
	CooldownSeconds      *int            `json:"cooldown_seconds"`
	ActiveHoursStart     *int            `json:"active_hours_start"`
	ActiveHoursEnd       *int            `json:"active_hours_end"`
	Schedule             json.RawMessage `json:"schedule"`
	MaxReplyDepth        *int            `json:"max_reply_depth"`
}

// aiCreateInput 是创建 AI 用户的请求体。
type aiCreateInput struct {
	Username         string          `json:"username"`
	Nickname         string          `json:"nickname"`
	Bio              string          `json:"bio"`
	Background       string          `json:"background"`
	Personality      string          `json:"personality"`
	CoreValues       string          `json:"core_values"`
	LanguageStyle    string          `json:"language_style"`
	Topics           string          `json:"topics"`
	TargetLanguage   string          `json:"target_language"`
	Taboos           string          `json:"taboos"`
	SystemPrompt     string          `json:"system_prompt"`
	ProviderID       string          `json:"provider_id"`
	ModelOverride    string          `json:"model_override"`
	GenerationParams json.RawMessage `json:"generation_params"`
	Behavior         behaviorInput   `json:"behavior"`
	IsEnabled        *bool           `json:"is_enabled"`
}

// aiUpdateInput 是更新 AI 资料与身份的请求体（不含模型与行为策略）。
type aiUpdateInput struct {
	Username       *string `json:"username"`
	Nickname       *string `json:"nickname"`
	Bio            *string `json:"bio"`
	Background     *string `json:"background"`
	Personality    *string `json:"personality"`
	CoreValues     *string `json:"core_values"`
	LanguageStyle  *string `json:"language_style"`
	Topics         *string `json:"topics"`
	TargetLanguage *string `json:"target_language"`
	Taboos         *string `json:"taboos"`
	SystemPrompt   *string `json:"system_prompt"`
}

// 响应结构。

type aiListItem struct {
	User         model.UserBriefResponse `json:"user"`
	IsEnabled    bool                    `json:"is_enabled"`
	ProviderID   *string                 `json:"provider_id"`
	ProviderName string                  `json:"provider_name"`
	Model        string                  `json:"model"`
	TodayActions int                     `json:"today_actions"`
	CreatedAt    string                  `json:"created_at"`
}

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

type aiModelResponse struct {
	ProviderID            *string         `json:"provider_id"`
	ProviderName          string          `json:"provider_name"`
	ModelOverride         string          `json:"model_override"`
	Model                 string          `json:"model"`
	GenerationParams      json.RawMessage `json:"generation_params"`
	RequestTimeoutSeconds int             `json:"request_timeout_seconds"`
	UpdatedAt             string          `json:"updated_at"`
}

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

type aiTaskBrief struct {
	ID          string `json:"id"`
	TriggerType string `json:"trigger_type"`
	ActionType  string `json:"action_type"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

type aiMemoryCounts struct {
	Recent   int `json:"recent"`
	LongTerm int `json:"long_term"`
}

type aiDetailResponse struct {
	ID           string                  `json:"id"`
	User         model.UserBriefResponse `json:"user"`
	Bio          string                  `json:"bio"`
	Profile      aiProfileResponse       `json:"profile"`
	Model        aiModelResponse         `json:"model"`
	Behavior     aiBehaviorResponse      `json:"behavior"`
	RecentTasks  []aiTaskBrief           `json:"recent_tasks"`
	MemoryCounts aiMemoryCounts          `json:"memory_counts"`
	CreatedAt    string                  `json:"created_at"`
	UpdatedAt    string                  `json:"updated_at"`
}

// aiModelSettings 是 ai_model_settings 的读取快照。
type aiModelSettings struct {
	ProviderID     *int64
	ProviderName   string
	ModelOverride  string
	RawParams      json.RawMessage
	TimeoutSeconds int
	Model          string
	UpdatedAt      time.Time
}

func (m aiModelSettings) response() aiModelResponse {
	var providerID *string
	if m.ProviderID != nil {
		value := api.FormatID(*m.ProviderID)
		providerID = &value
	}
	updatedAt := ""
	if !m.UpdatedAt.IsZero() {
		updatedAt = api.FormatTime(m.UpdatedAt)
	}
	timeout := m.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	return aiModelResponse{
		ProviderID:            providerID,
		ProviderName:          m.ProviderName,
		ModelOverride:         m.ModelOverride,
		Model:                 m.Model,
		GenerationParams:      jsonOrEmpty(m.RawParams),
		RequestTimeoutSeconds: timeout,
		UpdatedAt:             updatedAt,
	}
}

func behaviorResponseFrom(settings BehaviorSettings, updatedAt time.Time) aiBehaviorResponse {
	updated := ""
	if !updatedAt.IsZero() {
		updated = api.FormatTime(updatedAt)
	}
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
		CandidateScopeParams: jsonOrEmpty(settings.CandidateScopeParams),
		RateLimits:           jsonOrEmpty(settings.RateLimits),
		CooldownSeconds:      settings.CooldownSeconds,
		ActiveHoursStart:     settings.ActiveHoursStart,
		ActiveHoursEnd:       settings.ActiveHoursEnd,
		Schedule:             jsonOrEmpty(settings.Schedule),
		MaxReplyDepth:        settings.MaxReplyDepth,
		UpdatedAt:            updated,
	}
}

// ── 纯校验逻辑（不访问数据库，便于单元测试） ──

// parseProviderIDValue 解析请求中的 provider_id（雪花 ID 字符串）。
func parseProviderIDValue(raw string) (int64, error) {
	id, err := api.ParseID(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("请选择有效的模型接口预设")
	}
	return id, nil
}

// parseGenerationParamsInput 严格解析 HTTP 输入的生成参数：拒绝未知键、非法类型与越界值。
func parseGenerationParamsInput(raw json.RawMessage) (GenerationParams, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return GenerationParams{}, nil
	}
	var payload struct {
		MaxTokens   *int      `json:"max_tokens"`
		Temperature *float64  `json:"temperature"`
		TopP        *float64  `json:"top_p"`
		Stop        *[]string `json:"stop"`
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return GenerationParams{}, fmt.Errorf("生成参数只支持 max_tokens、temperature、top_p、stop")
	}
	params := GenerationParams{}
	if payload.MaxTokens != nil {
		if *payload.MaxTokens < 1 || *payload.MaxTokens > maxTokensLimit {
			return GenerationParams{}, fmt.Errorf("max_tokens 必须是 1-%d 的整数", maxTokensLimit)
		}
		params.MaxTokens = *payload.MaxTokens
	}
	if payload.Temperature != nil {
		if *payload.Temperature < 0 || *payload.Temperature > 2 {
			return GenerationParams{}, fmt.Errorf("temperature 必须在 0-2 之间")
		}
		params.Temperature = payload.Temperature
	}
	if payload.TopP != nil {
		if *payload.TopP < 0 || *payload.TopP > 1 {
			return GenerationParams{}, fmt.Errorf("top_p 必须在 0-1 之间")
		}
		params.TopP = payload.TopP
	}
	if payload.Stop != nil {
		if len(*payload.Stop) > maxStopSequences {
			return GenerationParams{}, fmt.Errorf("stop 最多 %d 条", maxStopSequences)
		}
		for _, sequence := range *payload.Stop {
			if strings.TrimSpace(sequence) == "" {
				return GenerationParams{}, fmt.Errorf("stop 序列不能为空")
			}
			if utf8.RuneCountInString(sequence) > maxStopSequenceRunes {
				return GenerationParams{}, fmt.Errorf("单条 stop 序列不能超过 %d 个字符", maxStopSequenceRunes)
			}
		}
		params.Stop = *payload.Stop
	}
	return params, nil
}

// generationParamsJSON 把生成参数编码为落库用的 JSONB（未配置的键不落库）。
func generationParamsJSON(params GenerationParams) (json.RawMessage, error) {
	payload := map[string]any{}
	if params.MaxTokens > 0 {
		payload["max_tokens"] = params.MaxTokens
	}
	if params.Temperature != nil {
		payload["temperature"] = *params.Temperature
	}
	if params.TopP != nil {
		payload["top_p"] = *params.TopP
	}
	if len(params.Stop) > 0 {
		payload["stop"] = params.Stop
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化生成参数失败: %w", err)
	}
	return json.RawMessage(encoded), nil
}

// jsonIsObject 判断 JSONB 值是否为对象（空值与 null 视为合法占位）。
func jsonIsObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return true
	}
	var object map[string]json.RawMessage
	return json.Unmarshal([]byte(trimmed), &object) == nil
}

// hasNonEmptyStringArray 判断 JSONB 对象中某个键是否是非空的字符串数组。
func hasNonEmptyStringArray(raw json.RawMessage, key string) bool {
	if len(raw) == 0 {
		return false
	}
	var object map[string][]string
	if err := json.Unmarshal(raw, &object); err != nil {
		return false
	}
	values, ok := object[key]
	if !ok {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// validateRateLimits 校验每日上限 JSONB：只允许约定的键，取值为 0-1000 的整数。
func validateRateLimits(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	limits := map[string]int{}
	if err := json.Unmarshal([]byte(trimmed), &limits); err != nil {
		return "每日上限必须是 JSON 对象，取值为 0-1000 的整数（0 表示不限制）"
	}
	for key, value := range limits {
		if !validRateLimitActions[key] {
			return "不支持的每日上限字段：" + key
		}
		if value < 0 || value > maxDailyLimit {
			return "每个每日上限必须在 0-1000 之间（0 表示不限制）"
		}
	}
	return ""
}

// validateBehaviorSettings 校验补丁应用之后的完整行为设置。
func validateBehaviorSettings(settings BehaviorSettings) map[string]string {
	details := map[string]string{}
	if !validCandidateScopeTypes[settings.CandidateScopeType] {
		details["candidate_scope_type"] = "候选范围只能是 none、global_public、followed、tags 或 authors"
	} else {
		switch settings.CandidateScopeType {
		case "tags":
			if !hasNonEmptyStringArray(settings.CandidateScopeParams, "tags") {
				details["candidate_scope_params"] = "候选范围为 tags 时必须提供非空的 tags 数组"
			}
		case "authors":
			if !hasNonEmptyStringArray(settings.CandidateScopeParams, "usernames") {
				details["candidate_scope_params"] = "候选范围为 authors 时必须提供非空的 usernames 数组"
			}
		}
	}
	if _, exists := details["candidate_scope_params"]; !exists && !jsonIsObject(settings.CandidateScopeParams) {
		details["candidate_scope_params"] = "候选范围参数必须是 JSON 对象"
	}
	if !jsonIsObject(settings.Schedule) {
		details["schedule"] = "定时策略必须是 JSON 对象"
	}
	if message := validateRateLimits(settings.RateLimits); message != "" {
		details["rate_limits"] = message
	}
	if settings.CooldownSeconds < 0 || settings.CooldownSeconds > maxCooldownSeconds {
		details["cooldown_seconds"] = "冷却时间必须在 0-604800 秒之间"
	}
	if settings.ActiveHoursStart < 0 || settings.ActiveHoursStart > 23 {
		details["active_hours_start"] = "启用时间段的开始小时必须在 0-23 之间"
	}
	if settings.ActiveHoursEnd < 0 || settings.ActiveHoursEnd > 23 {
		details["active_hours_end"] = "启用时间段的结束小时必须在 0-23 之间"
	}
	if settings.MaxReplyDepth < 0 || settings.MaxReplyDepth > 5 {
		details["max_reply_depth"] = "回复深度必须在 0-5 之间"
	}
	return details
}

// applyBehaviorPatch 以 base 为底应用补丁。
func applyBehaviorPatch(base BehaviorSettings, in behaviorInput) BehaviorSettings {
	out := base
	if in.AllowOriginalPost != nil {
		out.AllowOriginalPost = *in.AllowOriginalPost
	}
	if in.AllowActiveComment != nil {
		out.AllowActiveComment = *in.AllowActiveComment
	}
	if in.AllowPassiveReply != nil {
		out.AllowPassiveReply = *in.AllowPassiveReply
	}
	if in.AllowLike != nil {
		out.AllowLike = *in.AllowLike
	}
	if in.AllowFollow != nil {
		out.AllowFollow = *in.AllowFollow
	}
	if in.AllowUnfollow != nil {
		out.AllowUnfollow = *in.AllowUnfollow
	}
	if in.AllowRepost != nil {
		out.AllowRepost = *in.AllowRepost
	}
	if in.AllowQuote != nil {
		out.AllowQuote = *in.AllowQuote
	}
	if in.AllowAIToAI != nil {
		out.AllowAIToAI = *in.AllowAIToAI
	}
	if in.AllowWebSearch != nil {
		out.AllowWebSearch = *in.AllowWebSearch
	}
	if in.CandidateScopeType != nil {
		out.CandidateScopeType = strings.TrimSpace(*in.CandidateScopeType)
	}
	if len(in.CandidateScopeParams) > 0 {
		out.CandidateScopeParams = append(json.RawMessage(nil), in.CandidateScopeParams...)
	}
	if len(in.RateLimits) > 0 {
		out.RateLimits = append(json.RawMessage(nil), in.RateLimits...)
	}
	if in.CooldownSeconds != nil {
		out.CooldownSeconds = *in.CooldownSeconds
	}
	if in.ActiveHoursStart != nil {
		out.ActiveHoursStart = *in.ActiveHoursStart
	}
	if in.ActiveHoursEnd != nil {
		out.ActiveHoursEnd = *in.ActiveHoursEnd
	}
	if len(in.Schedule) > 0 {
		out.Schedule = append(json.RawMessage(nil), in.Schedule...)
	}
	if in.MaxReplyDepth != nil {
		out.MaxReplyDepth = *in.MaxReplyDepth
	}
	return out
}

// patchAndValidateBehavior 应用补丁并校验最终设置。
func patchAndValidateBehavior(base BehaviorSettings, in behaviorInput) (BehaviorSettings, map[string]string) {
	settings := applyBehaviorPatch(base, in)
	return settings, validateBehaviorSettings(settings)
}

// validateAIUpdate 校验资料更新请求（纯逻辑，不访问数据库）。
func validateAIUpdate(in aiUpdateInput) *api.Failure {
	details := map[string]string{}
	if in.Username != nil {
		if !aiUsernamePattern.MatchString(strings.TrimSpace(*in.Username)) {
			details["username"] = "用户名需为 3-32 位，以字母开头，只能包含字母、数字和下划线"
		}
	}
	if in.Nickname != nil {
		runes := utf8.RuneCountInString(strings.TrimSpace(*in.Nickname))
		if runes < 1 || runes > maxNicknameRunes {
			details["nickname"] = "昵称需为 1-50 个字符"
		}
	}
	if in.Bio != nil && utf8.RuneCountInString(strings.TrimSpace(*in.Bio)) > maxBioRunes {
		details["bio"] = "简介不能超过 500 个字符"
	}
	identityFields := map[string]*string{
		"background":     in.Background,
		"personality":    in.Personality,
		"core_values":    in.CoreValues,
		"language_style": in.LanguageStyle,
		"topics":         in.Topics,
		"taboos":         in.Taboos,
	}
	for field, value := range identityFields {
		if value != nil && utf8.RuneCountInString(*value) > maxIdentityFieldRunes {
			details[field] = fmt.Sprintf("该字段不能超过 %d 个字符", maxIdentityFieldRunes)
		}
	}
	if in.SystemPrompt != nil && utf8.RuneCountInString(*in.SystemPrompt) > maxSystemPromptRunes {
		details["system_prompt"] = fmt.Sprintf("自定义提示词不能超过 %d 个字符", maxSystemPromptRunes)
	}
	if in.TargetLanguage != nil && utf8.RuneCountInString(strings.TrimSpace(*in.TargetLanguage)) > maxLanguageTagRunes {
		details["target_language"] = "目标语言标识不能超过 20 个字符"
	}
	if len(details) > 0 {
		return api.ErrValidation(firstDetail(details)).WithDetails(details)
	}
	return nil
}

// validateAICreate 校验创建 AI 的请求（纯逻辑，不访问数据库）。
func validateAICreate(in aiCreateInput) *api.Failure {
	details := map[string]string{}
	username := strings.TrimSpace(in.Username)
	nickname := strings.TrimSpace(in.Nickname)
	if nickname == "" {
		nickname = username
	}
	if !aiUsernamePattern.MatchString(username) {
		details["username"] = "用户名需为 3-32 位，以字母开头，只能包含字母、数字和下划线"
	}
	if runes := utf8.RuneCountInString(nickname); runes < 1 || runes > maxNicknameRunes {
		details["nickname"] = "昵称需为 1-50 个字符"
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.Bio)) > maxBioRunes {
		details["bio"] = "简介不能超过 500 个字符"
	}
	identityFields := map[string]string{
		"background":     in.Background,
		"personality":    in.Personality,
		"core_values":    in.CoreValues,
		"language_style": in.LanguageStyle,
		"topics":         in.Topics,
		"taboos":         in.Taboos,
	}
	for field, value := range identityFields {
		if utf8.RuneCountInString(value) > maxIdentityFieldRunes {
			details[field] = fmt.Sprintf("该字段不能超过 %d 个字符", maxIdentityFieldRunes)
		}
	}
	if utf8.RuneCountInString(in.SystemPrompt) > maxSystemPromptRunes {
		details["system_prompt"] = fmt.Sprintf("自定义提示词不能超过 %d 个字符", maxSystemPromptRunes)
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.TargetLanguage)) > maxLanguageTagRunes {
		details["target_language"] = "目标语言标识不能超过 20 个字符"
	}
	if utf8.RuneCountInString(strings.TrimSpace(in.ModelOverride)) > maxModelOverrideRunes {
		details["model_override"] = "模型覆盖名不能超过 200 个字符"
	}
	if _, err := parseProviderIDValue(in.ProviderID); err != nil {
		details["provider_id"] = err.Error()
	}
	if len(in.GenerationParams) > 0 {
		if _, err := parseGenerationParamsInput(in.GenerationParams); err != nil {
			details["generation_params"] = err.Error()
		}
	}
	if _, behaviorDetails := patchAndValidateBehavior(defaultBehaviorSettings(), in.Behavior); len(behaviorDetails) > 0 {
		for key, message := range behaviorDetails {
			details[key] = message
		}
	}
	if len(details) > 0 {
		return api.ErrValidation(firstDetail(details)).WithDetails(details)
	}
	return nil
}

// ── 数据库读取 ──

// ensureAIUser 确认目标是未停用的 AI 用户；否则 404 not_found。
func ensureAIUser(ctx context.Context, q access.Querier, aiID int64) error {
	var isAI bool
	err := q.QueryRow(ctx, `SELECT is_ai FROM users WHERE id = $1`, aiID).Scan(&isAI)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ErrNotFound("AI 用户不存在")
	}
	if err != nil {
		return err
	}
	if !isAI {
		return api.ErrNotFound("AI 用户不存在")
	}
	return nil
}

func loadAIProfile(ctx context.Context, q access.Querier, aiID int64) (aiProfileResponse, error) {
	profile := aiProfileResponse{TargetLanguage: "zh-CN"}
	var createdAt, updatedAt time.Time
	err := q.QueryRow(ctx, `
		SELECT background, personality, core_values, language_style, topics,
		       target_language, taboos, system_prompt, is_enabled, created_at, updated_at
		FROM ai_profiles WHERE user_id = $1`, aiID,
	).Scan(&profile.Background, &profile.Personality, &profile.CoreValues, &profile.LanguageStyle,
		&profile.Topics, &profile.TargetLanguage, &profile.Taboos, &profile.SystemPrompt,
		&profile.IsEnabled, &createdAt, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return profile, nil
	}
	if err != nil {
		return aiProfileResponse{}, err
	}
	profile.CreatedAt = api.FormatTime(createdAt)
	profile.UpdatedAt = api.FormatTime(updatedAt)
	return profile, nil
}

func loadAIModelSettings(ctx context.Context, q access.Querier, aiID int64) (aiModelSettings, error) {
	settings := aiModelSettings{RawParams: json.RawMessage(`{}`), TimeoutSeconds: 60}
	var rawParams []byte
	var updatedAt time.Time
	err := q.QueryRow(ctx, `
		SELECT ms.provider_id, COALESCE(pr.name, ''), ms.model_override, ms.generation_params,
		       ms.request_timeout_seconds, ms.updated_at,
		       COALESCE(NULLIF(ms.model_override, ''), COALESCE(pr.default_model, ''))
		FROM ai_model_settings ms
		LEFT JOIN model_providers pr ON pr.id = ms.provider_id
		WHERE ms.ai_id = $1`, aiID,
	).Scan(&settings.ProviderID, &settings.ProviderName, &settings.ModelOverride, &rawParams,
		&settings.TimeoutSeconds, &updatedAt, &settings.Model)
	if errors.Is(err, pgx.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return aiModelSettings{}, err
	}
	settings.RawParams = jsonOrEmpty(rawParams)
	settings.UpdatedAt = updatedAt
	return settings, nil
}

// loadAIBehavior 读取行为设置；不存在时返回与数据库默认值一致的设置。
func loadAIBehavior(ctx context.Context, q access.Querier, aiID int64) (BehaviorSettings, time.Time, error) {
	var settings BehaviorSettings
	var scopeParams, rateLimits, schedule []byte
	var updatedAt time.Time
	err := q.QueryRow(ctx, `
		SELECT allow_original_post, allow_active_comment, allow_passive_reply, allow_like,
		       allow_follow, allow_unfollow, allow_repost, allow_quote,
		       candidate_scope_type, candidate_scope_params, allow_ai_to_ai, rate_limits,
		       cooldown_seconds, active_hours_start, active_hours_end, schedule,
		       max_reply_depth, allow_web_search, updated_at
		FROM ai_behavior_settings WHERE ai_id = $1`, aiID,
	).Scan(&settings.AllowOriginalPost, &settings.AllowActiveComment, &settings.AllowPassiveReply,
		&settings.AllowLike, &settings.AllowFollow, &settings.AllowUnfollow, &settings.AllowRepost,
		&settings.AllowQuote, &settings.CandidateScopeType, &scopeParams, &settings.AllowAIToAI,
		&rateLimits, &settings.CooldownSeconds, &settings.ActiveHoursStart, &settings.ActiveHoursEnd,
		&schedule, &settings.MaxReplyDepth, &settings.AllowWebSearch, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultBehaviorSettings(), time.Time{}, nil
	}
	if err != nil {
		return BehaviorSettings{}, time.Time{}, err
	}
	settings.CandidateScopeParams = jsonOrEmpty(scopeParams)
	settings.RateLimits = jsonOrEmpty(rateLimits)
	settings.Schedule = jsonOrEmpty(schedule)
	return settings, updatedAt, nil
}

func loadAIRecentTasks(ctx context.Context, q access.Querier, aiID int64) ([]aiTaskBrief, error) {
	rows, err := q.Query(ctx, `
		SELECT id, trigger_type, action_type, status, created_at
		FROM ai_tasks WHERE ai_id = $1
		ORDER BY id DESC LIMIT 5`, aiID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make([]aiTaskBrief, 0, 5)
	for rows.Next() {
		var (
			id        int64
			createdAt time.Time
			task      aiTaskBrief
		)
		if err := rows.Scan(&id, &task.TriggerType, &task.ActionType, &task.Status, &createdAt); err != nil {
			return nil, err
		}
		task.ID = api.FormatID(id)
		task.CreatedAt = api.FormatTime(createdAt)
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func loadAIMemoryCounts(ctx context.Context, q access.Querier, aiID int64) (aiMemoryCounts, error) {
	rows, err := q.Query(ctx, `
		SELECT memory_type, COUNT(*)
		FROM ai_memories
		WHERE ai_id = $1 AND status = 'active'
		GROUP BY memory_type`, aiID)
	if err != nil {
		return aiMemoryCounts{}, err
	}
	defer rows.Close()
	var counts aiMemoryCounts
	for rows.Next() {
		var memoryType string
		var count int
		if err := rows.Scan(&memoryType, &count); err != nil {
			return aiMemoryCounts{}, err
		}
		switch memoryType {
		case memoryTypeRecent:
			counts.Recent = count
		case memoryTypeLongTerm:
			counts.LongTerm = count
		}
	}
	return counts, rows.Err()
}

// loadAIDetail 组装 AI 详情：资料全字段、模型设置、行为设置、近期任务与记忆条数。
func loadAIDetail(ctx context.Context, q access.Querier, aiID int64) (aiDetailResponse, error) {
	user, err := auth.UserByID(ctx, q, aiID, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return aiDetailResponse{}, api.ErrNotFound("AI 用户不存在")
	}
	if err != nil {
		return aiDetailResponse{}, err
	}
	if !user.IsAI {
		return aiDetailResponse{}, api.ErrNotFound("AI 用户不存在")
	}
	var detail aiDetailResponse
	detail.ID = api.FormatID(user.ID)
	detail.User = model.UserBriefFromAuth(user).Response()
	detail.Bio = user.Bio
	detail.CreatedAt = api.FormatTime(user.CreatedAt)
	detail.UpdatedAt = api.FormatTime(user.UpdatedAt)

	profile, err := loadAIProfile(ctx, q, aiID)
	if err != nil {
		return aiDetailResponse{}, err
	}
	detail.Profile = profile
	modelSettings, err := loadAIModelSettings(ctx, q, aiID)
	if err != nil {
		return aiDetailResponse{}, err
	}
	detail.Model = modelSettings.response()
	behavior, behaviorUpdatedAt, err := loadAIBehavior(ctx, q, aiID)
	if err != nil {
		return aiDetailResponse{}, err
	}
	detail.Behavior = behaviorResponseFrom(behavior, behaviorUpdatedAt)
	tasks, err := loadAIRecentTasks(ctx, q, aiID)
	if err != nil {
		return aiDetailResponse{}, err
	}
	detail.RecentTasks = tasks
	counts, err := loadAIMemoryCounts(ctx, q, aiID)
	if err != nil {
		return aiDetailResponse{}, err
	}
	detail.MemoryCounts = counts
	return detail, nil
}

// ── Handler ──

// ListAIs 返回 AI 用户列表；今日动作数使用一次批量查询。
func (s *Service) ListAIs(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	limit, err := api.PageLimit(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursor, err := api.TrimPageCursor(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	where := []string{"u.is_ai = TRUE"}
	args := make([]any, 0, 4)
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		if utf8.RuneCountInString(q) > 100 {
			api.Error(c, http.StatusBadRequest, "invalid_request", "搜索关键词过长")
			return
		}
		placeholder := addArg(q)
		where = append(where, fmt.Sprintf(
			"(STRPOS(LOWER(u.username), LOWER(%s)) > 0 OR STRPOS(LOWER(u.nickname), LOWER(%s)) > 0)",
			placeholder, placeholder))
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		switch status {
		case "enabled":
			where = append(where, "COALESCE(p.is_enabled, FALSE)")
		case "disabled":
			where = append(where, "NOT COALESCE(p.is_enabled, FALSE)")
		default:
			api.Error(c, http.StatusBadRequest, "invalid_request", "status 只能是 enabled 或 disabled")
			return
		}
	}
	if cursor > 0 {
		where = append(where, "u.id < "+addArg(cursor))
	}
	args = append(args, limit+1)
	limitPlaceholder := "$" + strconv.Itoa(len(args))

	rows, err := pool.Query(c.Request.Context(), `
		SELECT u.id, u.username, u.nickname, u.avatar_url, u.status,
		       COALESCE(p.is_enabled, FALSE), COALESCE(p.created_at, u.created_at),
		       ms.provider_id, COALESCE(pr.name, ''),
		       COALESCE(NULLIF(ms.model_override, ''), COALESCE(pr.default_model, ''))
		FROM users u
		LEFT JOIN ai_profiles p ON p.user_id = u.id
		LEFT JOIN ai_model_settings ms ON ms.ai_id = u.id
		LEFT JOIN model_providers pr ON pr.id = ms.provider_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY u.id DESC LIMIT `+limitPlaceholder, args...)
	if err != nil {
		s.internalError(c, "查询 AI 列表失败", err)
		return
	}
	defer rows.Close()

	type aiListRow struct {
		user         model.UserBrief
		isEnabled    bool
		providerID   *int64
		providerName string
		model        string
		createdAt    time.Time
	}
	records := make([]aiListRow, 0, limit+1)
	for rows.Next() {
		var (
			record aiListRow
			status string
		)
		if err := rows.Scan(&record.user.ID, &record.user.Username, &record.user.Nickname,
			&record.user.AvatarURL, &status, &record.isEnabled, &record.createdAt,
			&record.providerID, &record.providerName, &record.model); err != nil {
			s.internalError(c, "读取 AI 列表失败", err)
			return
		}
		record.user.IsAI = true
		record.user.IsSuspended = status != auth.StatusActive
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历 AI 列表失败", err)
		return
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	ids := make([]int64, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.user.ID)
	}
	todayActions := map[int64]int{}
	if len(ids) > 0 {
		countRows, err := pool.Query(c.Request.Context(), `
			SELECT ai_id, COUNT(*) FROM ai_tasks
			WHERE ai_id = ANY($1) AND status = 'succeeded' AND created_at::date = CURRENT_DATE
			GROUP BY ai_id`, ids)
		if err != nil {
			s.internalError(c, "统计 AI 今日动作失败", err)
			return
		}
		defer countRows.Close()
		for countRows.Next() {
			var aiID int64
			var count int
			if err := countRows.Scan(&aiID, &count); err != nil {
				s.internalError(c, "读取 AI 今日动作失败", err)
				return
			}
			todayActions[aiID] = count
		}
		if err := countRows.Err(); err != nil {
			s.internalError(c, "遍历 AI 今日动作失败", err)
			return
		}
	}

	items := make([]aiListItem, 0, len(records))
	for _, record := range records {
		var providerID *string
		if record.providerID != nil {
			value := api.FormatID(*record.providerID)
			providerID = &value
		}
		items = append(items, aiListItem{
			User:         record.user.Response(),
			IsEnabled:    record.isEnabled,
			ProviderID:   providerID,
			ProviderName: record.providerName,
			Model:        record.model,
			TodayActions: todayActions[record.user.ID],
			CreatedAt:    api.FormatTime(record.createdAt),
		})
	}
	var lastID int64
	if len(records) > 0 {
		lastID = records[len(records)-1].user.ID
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// CreateAI 创建 AI 用户：账号、身份资料、模型设置、行为策略与审计在同一事务内完成。
func (s *Service) CreateAI(c *gin.Context) {
	var in aiCreateInput
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	if failure := validateAICreate(in); failure != nil {
		api.ErrorWithDetails(c, failure.Status, failure.Code, failure.Message, failure.Details)
		return
	}
	username := strings.TrimSpace(in.Username)
	nickname := strings.TrimSpace(in.Nickname)
	if nickname == "" {
		nickname = username
	}
	providerID, err := parseProviderIDValue(in.ProviderID)
	if err != nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", err.Error(),
			gin.H{"provider_id": err.Error()})
		return
	}
	params, err := parseGenerationParamsInput(in.GenerationParams)
	if err != nil {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", err.Error(),
			gin.H{"generation_params": err.Error()})
		return
	}
	paramsJSON, err := generationParamsJSON(params)
	if err != nil {
		s.internalError(c, "序列化生成参数失败", err)
		return
	}
	behavior, behaviorDetails := patchAndValidateBehavior(defaultBehaviorSettings(), in.Behavior)
	if len(behaviorDetails) > 0 {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", firstDetail(behaviorDetails), behaviorDetails)
		return
	}
	isEnabled := false
	if in.IsEnabled != nil {
		isEnabled = *in.IsEnabled
	}
	targetLanguage := strings.TrimSpace(in.TargetLanguage)
	if targetLanguage == "" {
		targetLanguage = "zh-CN"
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启创建 AI 事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var usernameTaken bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE LOWER(username) = LOWER($1))`, username,
	).Scan(&usernameTaken); err != nil {
		s.internalError(c, "检查 AI 用户名冲突失败", err)
		return
	}
	if usernameTaken {
		api.ErrorWithDetails(c, http.StatusConflict, "duplicate", "用户名已被占用",
			gin.H{"username": "用户名已被占用"})
		return
	}
	providerOK, err := enabledProviderExists(ctx, tx, providerID)
	if err != nil {
		s.internalError(c, "校验模型接口预设失败", err)
		return
	}
	if !providerOK {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "模型接口预设不存在或未启用",
			gin.H{"provider_id": "模型接口预设不存在或未启用"})
		return
	}
	aiID, err := s.ids.Next()
	if err != nil {
		s.internalError(c, "生成 AI 用户 ID 失败", err)
		return
	}
	// AI 账号没有邮箱与密码（users_identity_check 约束）。
	if _, err = tx.Exec(ctx, `
		INSERT INTO users (
			id, username, email, password_hash, nickname, avatar_url, bio,
			role, is_ai, status, require_follow_approval, token_version
		) VALUES ($1, $2, NULL, NULL, $3, '', $4, 'user', TRUE, 'active', FALSE, 1)`,
		aiID, username, nickname, strings.TrimSpace(in.Bio),
	); err != nil {
		if isUniqueViolation(err) {
			api.ErrorWithDetails(c, http.StatusConflict, "duplicate", "用户名已被占用",
				gin.H{"username": "用户名已被占用"})
			return
		}
		s.internalError(c, "创建 AI 用户失败", err)
		return
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO ai_profiles (
			user_id, background, personality, core_values, language_style, topics,
			target_language, taboos, system_prompt, is_enabled
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		aiID, in.Background, in.Personality, in.CoreValues, in.LanguageStyle, in.Topics,
		targetLanguage, in.Taboos, in.SystemPrompt, isEnabled,
	); err != nil {
		s.internalError(c, "创建 AI 身份资料失败", err)
		return
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO ai_model_settings (ai_id, provider_id, model_override, generation_params, request_timeout_seconds)
		VALUES ($1, $2, $3, $4, 60)`,
		aiID, providerID, strings.TrimSpace(in.ModelOverride), paramsJSON,
	); err != nil {
		s.internalError(c, "创建 AI 模型设置失败", err)
		return
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO ai_behavior_settings (
			ai_id, allow_original_post, allow_active_comment, allow_passive_reply, allow_like,
			allow_follow, allow_unfollow, allow_repost, allow_quote, candidate_scope_type,
			candidate_scope_params, allow_ai_to_ai, rate_limits, cooldown_seconds,
			active_hours_start, active_hours_end, schedule, max_reply_depth, allow_web_search
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
		aiID, behavior.AllowOriginalPost, behavior.AllowActiveComment, behavior.AllowPassiveReply,
		behavior.AllowLike, behavior.AllowFollow, behavior.AllowUnfollow, behavior.AllowRepost,
		behavior.AllowQuote, behavior.CandidateScopeType, jsonOrEmpty(behavior.CandidateScopeParams),
		behavior.AllowAIToAI, jsonOrEmpty(behavior.RateLimits), behavior.CooldownSeconds,
		behavior.ActiveHoursStart, behavior.ActiveHoursEnd, jsonOrEmpty(behavior.Schedule),
		behavior.MaxReplyDepth, behavior.AllowWebSearch,
	); err != nil {
		s.internalError(c, "创建 AI 行为设置失败", err)
		return
	}
	if err = s.writeAudit(ctx, tx, adminUser.ID, audit.ActionAICreate, targetAI, &aiID, gin.H{
		"username":    username,
		"provider_id": api.FormatID(providerID),
	}); err != nil {
		s.internalError(c, "写入 AI 创建审计失败", err)
		return
	}
	detail, err := loadAIDetail(ctx, tx, aiID)
	if err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "读取新建 AI 详情失败", err)
		}
		return
	}
	if !s.commitTx(c, tx, "提交创建 AI 事务失败") {
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ai": detail})
}

// GetAI 返回 AI 详情；非 AI 用户 id 返回 404。
func (s *Service) GetAI(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	detail, err := loadAIDetail(c.Request.Context(), pool, aiID)
	if err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "读取 AI 详情失败", err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"ai": detail})
}

// UpdateAI 更新 AI 资料与身份字段（不含模型与行为策略）。
func (s *Service) UpdateAI(c *gin.Context) {
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	var in aiUpdateInput
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	if failure := validateAIUpdate(in); failure != nil {
		api.ErrorWithDetails(c, failure.Status, failure.Code, failure.Message, failure.Details)
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启更新 AI 事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := auth.UserByID(ctx, tx, aiID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "AI 用户不存在")
		return
	}
	if err != nil {
		s.internalError(c, "读取待更新 AI 失败", err)
		return
	}
	if !current.IsAI {
		api.Error(c, http.StatusNotFound, "not_found", "AI 用户不存在")
		return
	}
	username := current.Username
	if in.Username != nil {
		username = strings.TrimSpace(*in.Username)
		if !strings.EqualFold(username, current.Username) {
			var taken bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM users WHERE LOWER(username) = LOWER($1) AND id <> $2
				)`, username, aiID).Scan(&taken); err != nil {
				s.internalError(c, "检查 AI 用户名冲突失败", err)
				return
			}
			if taken {
				api.ErrorWithDetails(c, http.StatusConflict, "duplicate", "用户名已被占用",
					gin.H{"username": "用户名已被占用"})
				return
			}
		}
	}
	nickname := current.Nickname
	if in.Nickname != nil {
		nickname = strings.TrimSpace(*in.Nickname)
	}
	bio := current.Bio
	if in.Bio != nil {
		bio = strings.TrimSpace(*in.Bio)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE users SET username = $2, nickname = $3, bio = $4, updated_at = now()
		WHERE id = $1`, aiID, username, nickname, bio,
	); err != nil {
		if isUniqueViolation(err) {
			api.ErrorWithDetails(c, http.StatusConflict, "duplicate", "用户名已被占用",
				gin.H{"username": "用户名已被占用"})
			return
		}
		s.internalError(c, "更新 AI 账号资料失败", err)
		return
	}
	profile, err := loadAIProfile(ctx, tx, aiID)
	if err != nil {
		s.internalError(c, "读取 AI 身份资料失败", err)
		return
	}
	if in.Background != nil {
		profile.Background = *in.Background
	}
	if in.Personality != nil {
		profile.Personality = *in.Personality
	}
	if in.CoreValues != nil {
		profile.CoreValues = *in.CoreValues
	}
	if in.LanguageStyle != nil {
		profile.LanguageStyle = *in.LanguageStyle
	}
	if in.Topics != nil {
		profile.Topics = *in.Topics
	}
	if in.Taboos != nil {
		profile.Taboos = *in.Taboos
	}
	if in.SystemPrompt != nil {
		profile.SystemPrompt = *in.SystemPrompt
	}
	if in.TargetLanguage != nil {
		profile.TargetLanguage = strings.TrimSpace(*in.TargetLanguage)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO ai_profiles (
			user_id, background, personality, core_values, language_style, topics,
			target_language, taboos, system_prompt, is_enabled
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (user_id) DO UPDATE SET
			background = EXCLUDED.background,
			personality = EXCLUDED.personality,
			core_values = EXCLUDED.core_values,
			language_style = EXCLUDED.language_style,
			topics = EXCLUDED.topics,
			target_language = EXCLUDED.target_language,
			taboos = EXCLUDED.taboos,
			system_prompt = EXCLUDED.system_prompt,
			updated_at = now()`,
		aiID, profile.Background, profile.Personality, profile.CoreValues, profile.LanguageStyle,
		profile.Topics, profile.TargetLanguage, profile.Taboos, profile.SystemPrompt, profile.IsEnabled,
	); err != nil {
		s.internalError(c, "更新 AI 身份资料失败", err)
		return
	}
	if err = s.writeAudit(ctx, tx, adminUser.ID, audit.ActionAIUpdate, targetAI, &aiID,
		gin.H{"username": username}); err != nil {
		s.internalError(c, "写入 AI 更新审计失败", err)
		return
	}
	detail, err := loadAIDetail(ctx, tx, aiID)
	if err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "读取更新后的 AI 详情失败", err)
		}
		return
	}
	if !s.commitTx(c, tx, "提交更新 AI 事务失败") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ai": detail})
}

// UpdateAIModel 更新接口预设、模型覆盖、生成参数与请求超时。
func (s *Service) UpdateAIModel(c *gin.Context) {
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	var in struct {
		ProviderID            *string         `json:"provider_id"`
		ModelOverride         *string         `json:"model_override"`
		GenerationParams      json.RawMessage `json:"generation_params"`
		RequestTimeoutSeconds *int            `json:"request_timeout_seconds"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	if in.RequestTimeoutSeconds != nil && (*in.RequestTimeoutSeconds < 5 || *in.RequestTimeoutSeconds > 600) {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "请求超时必须在 5-600 秒之间",
			gin.H{"request_timeout_seconds": "请求超时必须在 5-600 秒之间"})
		return
	}
	paramsProvided := len(in.GenerationParams) > 0
	var params GenerationParams
	if paramsProvided {
		parsed, err := parseGenerationParamsInput(in.GenerationParams)
		if err != nil {
			api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", err.Error(),
				gin.H{"generation_params": err.Error()})
			return
		}
		params = parsed
	}
	var providerID *int64
	if in.ProviderID != nil {
		parsed, err := parseProviderIDValue(*in.ProviderID)
		if err != nil {
			api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", err.Error(),
				gin.H{"provider_id": err.Error()})
			return
		}
		providerID = &parsed
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启更新 AI 模型事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := ensureAIUser(ctx, tx, aiID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验 AI 用户失败", err)
		}
		return
	}
	current, err := loadAIModelSettings(ctx, tx, aiID)
	if err != nil {
		s.internalError(c, "读取 AI 模型设置失败", err)
		return
	}
	finalProviderID := current.ProviderID
	if providerID != nil {
		providerOK, err := enabledProviderExists(ctx, tx, *providerID)
		if err != nil {
			s.internalError(c, "校验模型接口预设失败", err)
			return
		}
		if !providerOK {
			api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "模型接口预设不存在或未启用",
				gin.H{"provider_id": "模型接口预设不存在或未启用"})
			return
		}
		finalProviderID = providerID
	}
	modelOverride := current.ModelOverride
	if in.ModelOverride != nil {
		modelOverride = strings.TrimSpace(*in.ModelOverride)
		if utf8.RuneCountInString(modelOverride) > maxModelOverrideRunes {
			api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "模型覆盖名不能超过 200 个字符",
				gin.H{"model_override": "模型覆盖名不能超过 200 个字符"})
			return
		}
	}
	if !paramsProvided {
		currentParams, err := GenerationParamsFromJSON(current.RawParams)
		if err != nil {
			s.internalError(c, "解析既有生成参数失败", err)
			return
		}
		params = currentParams
	}
	paramsJSON, err := generationParamsJSON(params)
	if err != nil {
		s.internalError(c, "序列化生成参数失败", err)
		return
	}
	timeout := current.TimeoutSeconds
	if in.RequestTimeoutSeconds != nil {
		timeout = *in.RequestTimeoutSeconds
	}
	if timeout <= 0 {
		timeout = 60
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO ai_model_settings (ai_id, provider_id, model_override, generation_params, request_timeout_seconds)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (ai_id) DO UPDATE SET
			provider_id = EXCLUDED.provider_id,
			model_override = EXCLUDED.model_override,
			generation_params = EXCLUDED.generation_params,
			request_timeout_seconds = EXCLUDED.request_timeout_seconds,
			updated_at = now()`,
		aiID, finalProviderID, modelOverride, paramsJSON, timeout,
	); err != nil {
		s.internalError(c, "更新 AI 模型设置失败", err)
		return
	}
	providerDetail := ""
	if finalProviderID != nil {
		providerDetail = api.FormatID(*finalProviderID)
	}
	if err = s.writeAudit(ctx, tx, adminUser.ID, audit.ActionAIModelUpdate, targetAI, &aiID, gin.H{
		"provider_id": providerDetail,
	}); err != nil {
		s.internalError(c, "写入 AI 模型更新审计失败", err)
		return
	}
	updated, err := loadAIModelSettings(ctx, tx, aiID)
	if err != nil {
		s.internalError(c, "读取更新后的 AI 模型设置失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交更新 AI 模型事务失败") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"model": updated.response()})
}

// UpdateAIBehavior 更新行为开关、候选范围、AI 间互动、频率与冷却。
func (s *Service) UpdateAIBehavior(c *gin.Context) {
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	var in behaviorInput
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启更新 AI 行为事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := ensureAIUser(ctx, tx, aiID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验 AI 用户失败", err)
		}
		return
	}
	current, _, err := loadAIBehavior(ctx, tx, aiID)
	if err != nil {
		s.internalError(c, "读取 AI 行为设置失败", err)
		return
	}
	final, details := patchAndValidateBehavior(current, in)
	if len(details) > 0 {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", firstDetail(details), details)
		return
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO ai_behavior_settings (
			ai_id, allow_original_post, allow_active_comment, allow_passive_reply, allow_like,
			allow_follow, allow_unfollow, allow_repost, allow_quote, candidate_scope_type,
			candidate_scope_params, allow_ai_to_ai, rate_limits, cooldown_seconds,
			active_hours_start, active_hours_end, schedule, max_reply_depth, allow_web_search
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (ai_id) DO UPDATE SET
			allow_original_post = EXCLUDED.allow_original_post,
			allow_active_comment = EXCLUDED.allow_active_comment,
			allow_passive_reply = EXCLUDED.allow_passive_reply,
			allow_like = EXCLUDED.allow_like,
			allow_follow = EXCLUDED.allow_follow,
			allow_unfollow = EXCLUDED.allow_unfollow,
			allow_repost = EXCLUDED.allow_repost,
			allow_quote = EXCLUDED.allow_quote,
			candidate_scope_type = EXCLUDED.candidate_scope_type,
			candidate_scope_params = EXCLUDED.candidate_scope_params,
			allow_ai_to_ai = EXCLUDED.allow_ai_to_ai,
			rate_limits = EXCLUDED.rate_limits,
			cooldown_seconds = EXCLUDED.cooldown_seconds,
			active_hours_start = EXCLUDED.active_hours_start,
			active_hours_end = EXCLUDED.active_hours_end,
			schedule = EXCLUDED.schedule,
			max_reply_depth = EXCLUDED.max_reply_depth,
			allow_web_search = EXCLUDED.allow_web_search,
			updated_at = now()`,
		aiID, final.AllowOriginalPost, final.AllowActiveComment, final.AllowPassiveReply,
		final.AllowLike, final.AllowFollow, final.AllowUnfollow, final.AllowRepost, final.AllowQuote,
		final.CandidateScopeType, jsonOrEmpty(final.CandidateScopeParams), final.AllowAIToAI,
		jsonOrEmpty(final.RateLimits), final.CooldownSeconds, final.ActiveHoursStart,
		final.ActiveHoursEnd, jsonOrEmpty(final.Schedule), final.MaxReplyDepth, final.AllowWebSearch,
	); err != nil {
		s.internalError(c, "更新 AI 行为设置失败", err)
		return
	}
	if err = s.writeAudit(ctx, tx, adminUser.ID, audit.ActionAIBehaviorUpdate, targetAI, &aiID,
		gin.H{"candidate_scope_type": final.CandidateScopeType, "allow_web_search": final.AllowWebSearch}); err != nil {
		s.internalError(c, "写入 AI 行为更新审计失败", err)
		return
	}
	updated, updatedAt, err := loadAIBehavior(ctx, tx, aiID)
	if err != nil {
		s.internalError(c, "读取更新后的 AI 行为设置失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交更新 AI 行为事务失败") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"behavior": behaviorResponseFrom(updated, updatedAt)})
}

// EnableAI 启用单个 AI。
func (s *Service) EnableAI(c *gin.Context) {
	s.setAIEnabled(c, true)
}

// DisableAI 停用单个 AI：未开始的后台作业暂停，待执行的 AI 任务取消。
func (s *Service) DisableAI(c *gin.Context) {
	s.setAIEnabled(c, false)
}

func (s *Service) setAIEnabled(c *gin.Context, enabled bool) {
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启 AI 启停事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := ensureAIUser(ctx, tx, aiID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验 AI 用户失败", err)
		}
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ai_profiles (user_id, is_enabled) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET is_enabled = EXCLUDED.is_enabled, updated_at = now()`,
		aiID, enabled,
	); err != nil {
		s.internalError(c, "更新 AI 启用状态失败", err)
		return
	}
	if !enabled {
		if _, err := tx.Exec(ctx, `
			UPDATE background_jobs SET status = 'paused', updated_at = now()
			WHERE ref_type = 'ai' AND ref_id = $1 AND status = 'pending'`, aiID); err != nil {
			s.internalError(c, "暂停 AI 后台作业失败", err)
			return
		}
		if _, err := tx.Exec(ctx, `
			UPDATE ai_tasks SET status = 'canceled', finished_at = now()
			WHERE ai_id = $1 AND status = 'pending'`, aiID); err != nil {
			s.internalError(c, "取消 AI 待执行任务失败", err)
			return
		}
	}
	action := audit.ActionAIEnable
	if !enabled {
		action = audit.ActionAIDisable
	}
	if err := s.writeAudit(ctx, tx, adminUser.ID, action, targetAI, &aiID, gin.H{}); err != nil {
		s.internalError(c, "写入 AI 启停审计失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交 AI 启停事务失败") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": api.FormatID(aiID), "is_enabled": enabled})
}
