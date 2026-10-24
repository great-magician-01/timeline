package domain

import (
	"encoding/json"
	"strings"
)

// 行为策略约束（与 ai_behavior_settings 的 CHECK 约束一致）。
const (
	MaxDailyLimit      = 1000
	MaxCooldownSeconds = 604800
)

// validRateLimitActions 是 rate_limits 允许的键。
var validRateLimitActions = map[string]bool{
	"daily_original_post": true,
	"daily_comment":       true,
	"daily_like":          true,
	"daily_unlike":        true,
	"daily_follow":        true,
	"daily_unfollow":      true,
	"daily_repost":        true,
	"daily_quote":         true,
	"daily_group_message": true,
	"daily_total":         true,
}

// ValidCandidateScopeTypes 是候选范围合法取值。
var ValidCandidateScopeTypes = map[string]bool{
	"none":          true,
	"global_public": true,
	"followed":      true,
	"tags":          true,
	"authors":       true,
}

// BehaviorPatch 是行为策略的补丁：nil/空值表示不变更（创建时表示使用默认值）。
// 与 HTTP 请求体形状一致（JSON tag 在 http/inputs.go，这里只保留纯数据）。
type BehaviorPatch struct {
	AllowOriginalPost    *bool
	AllowActiveComment   *bool
	AllowPassiveReply    *bool
	AllowLike            *bool
	AllowFollow          *bool
	AllowUnfollow        *bool
	AllowRepost          *bool
	AllowQuote           *bool
	AllowAIToAI          *bool
	AllowWebSearch       *bool
	AllowGroupMessage    *bool
	CandidateScopeType   *string
	CandidateScopeParams json.RawMessage
	RateLimits           json.RawMessage
	CooldownSeconds      *int
	ActiveHoursStart     *int
	ActiveHoursEnd       *int
	Schedule             json.RawMessage
	MaxReplyDepth        *int
}

// ApplyBehaviorPatch 以 base 为底应用补丁。
func ApplyBehaviorPatch(base BehaviorSettings, in BehaviorPatch) BehaviorSettings {
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
	if in.AllowGroupMessage != nil {
		out.AllowGroupMessage = *in.AllowGroupMessage
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

// PatchAndValidateBehavior 应用补丁并校验最终设置。
func PatchAndValidateBehavior(base BehaviorSettings, in BehaviorPatch) (BehaviorSettings, map[string]string) {
	settings := ApplyBehaviorPatch(base, in)
	return settings, ValidateBehaviorSettings(settings)
}

// ValidateBehaviorSettings 校验补丁应用之后的完整行为设置，返回字段名到错误文案的映射。
func ValidateBehaviorSettings(settings BehaviorSettings) map[string]string {
	details := map[string]string{}
	if !ValidCandidateScopeTypes[settings.CandidateScopeType] {
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
	if _, exists := details["candidate_scope_params"]; !exists && !JSONIsObject(settings.CandidateScopeParams) {
		details["candidate_scope_params"] = "候选范围参数必须是 JSON 对象"
	}
	if !JSONIsObject(settings.Schedule) {
		details["schedule"] = "定时策略必须是 JSON 对象"
	}
	if message := ValidateRateLimits(settings.RateLimits); message != "" {
		details["rate_limits"] = message
	}
	if settings.CooldownSeconds < 0 || settings.CooldownSeconds > MaxCooldownSeconds {
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

// ValidateRateLimits 校验每日上限 JSONB：只允许约定的键，取值为 0-1000 的整数。
func ValidateRateLimits(raw json.RawMessage) string {
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
		if value < 0 || value > MaxDailyLimit {
			return "每个每日上限必须在 0-1000 之间（0 表示不限制）"
		}
	}
	return ""
}

// JSONOrEmpty 保证 JSONB 字段对外输出对象而不是 null。
func JSONOrEmpty(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(trimmed)
}

// JSONIsObject 判断 JSONB 值是否为对象（空值与 null 视为合法占位）。
func JSONIsObject(raw json.RawMessage) bool {
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
