package domain

import (
	"encoding/json"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// ActionAllowed 判断行为策略是否开启某个动作。
func ActionAllowed(behavior contracts.BehaviorSettings, action string) bool {
	switch action {
	case ActionOriginalPost:
		return behavior.AllowOriginalPost
	case ActionComment:
		return behavior.AllowActiveComment
	case ActionReply:
		return behavior.AllowPassiveReply
	case ActionLike:
		return behavior.AllowLike
	case ActionUnlike:
		return behavior.AllowLike
	case ActionFollow:
		return behavior.AllowFollow
	case ActionUnfollow:
		return behavior.AllowUnfollow
	case ActionRepost:
		return behavior.AllowRepost
	case ActionQuote:
		return behavior.AllowQuote
	case ActionNoop:
		return true
	default:
		return false
	}
}

// ActionAllowedForTrigger 在全局开关之上叠加触发类型限制：
// 定时原创只允许原创/引用；提及与被动评论只允许回复/点赞/关注等被动动作。
func ActionAllowedForTrigger(behavior contracts.BehaviorSettings, trigger, action string) bool {
	if !ActionAllowed(behavior, action) {
		return false
	}
	switch trigger {
	case TriggerSchedule:
		return action == ActionOriginalPost || action == ActionQuote
	case TriggerProactive:
		return action == ActionOriginalPost || action == ActionQuote || action == ActionLike ||
			action == ActionUnlike || action == ActionFollow || action == ActionUnfollow || action == ActionRepost
	default:
		return true
	}
}

// ProactiveAllowed 判断是否允许主动互动（至少一个主动动作开启）。
func ProactiveAllowed(behavior contracts.BehaviorSettings) bool {
	return behavior.AllowOriginalPost || behavior.AllowLike || behavior.AllowFollow ||
		behavior.AllowRepost || behavior.AllowQuote
}

// WithinActiveHours 判断小时是否落在启用时段内（start==end 视为全天生效）。
func WithinActiveHours(start, end, hour int) bool {
	if start == end {
		return true
	}
	if start < end {
		return hour >= start && hour < end
	}
	// 跨零点：例如 22 点至次日 6 点。
	return hour >= start || hour < end
}

// UnderDailyLimit 判断当日计数是否仍在上限内（limit<=0 表示不限制）。
func UnderDailyLimit(count, limit int) bool {
	if limit <= 0 {
		return true
	}
	return count < limit
}

// CooldownElapsed 判断距上次成功是否已过冷却时间（零值 last 视为从未发生）。
func CooldownElapsed(last, now time.Time, cooldownSeconds int) bool {
	if last.IsZero() || cooldownSeconds <= 0 {
		return true
	}
	return now.Sub(last) >= time.Duration(cooldownSeconds)*time.Second
}

// ReplyDepthAllowed 判断父评论深度是否仍允许回复（maxDepth<=0 表示不限制）。
func ReplyDepthAllowed(parentDepth, maxDepth int) bool {
	if maxDepth <= 0 {
		return true
	}
	return parentDepth < maxDepth
}

// DailyLimit 读取行为策略中的每日动作上限。
func DailyLimit(behavior contracts.BehaviorSettings, action string) int {
	return behavior.DailyLimit(action)
}

// ScheduleMinutes 读取 schedule JSON 中的分钟间隔配置；缺失或非法时使用 fallback。
func ScheduleMinutes(raw json.RawMessage, key string, fallback int) int {
	trimmed := string(raw)
	if trimmed == "" || trimmed == "null" {
		return fallback
	}
	values := map[string]any{}
	if err := json.Unmarshal(raw, &values); err != nil {
		return fallback
	}
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return fallback
		}
		return int(parsed)
	default:
		return fallback
	}
}

// ClassifyCallError 把模型调用错误归类为任务失败类型（用于审计与重试判断）。
func ClassifyCallError(err error) (status, errorType, message string) {
	if err == nil {
		return TaskStatusSucceeded, "", ""
	}
	return TaskStatusFailed, ErrorTypeModel, "模型调用失败"
}
