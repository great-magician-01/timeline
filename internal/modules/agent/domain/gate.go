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
	case ActionGroupMessage:
		// 群聊发言独立开关：自主群发言按 allow_group_message 判断；
		// 被 @ 回复在 ActionAllowedForTrigger 叠加 allow_passive_reply。
		return behavior.AllowGroupMessage
	case ActionNoop:
		return true
	default:
		return false
	}
}

// ActionAllowedForTrigger 在全局开关之上叠加触发类型限制（docs/WORKFLOWS.md 7.2）：
//   - 被提及与收到新评论时发表的评论属于“被动回复”，按 allow_passive_reply 判断；
//   - 主动浏览候选内容时发表的评论属于“主动评论”，按 allow_active_comment 判断；
//   - 回复（reply）始终按 allow_passive_reply 判断；
//   - 群消息 @提及 触发的 group_message 属于被动行为（需同时开启群聊发言与被动回复）；
//   - 定时原创只允许原创/引用，主动互动只允许在候选范围内可执行的动作。
func ActionAllowedForTrigger(behavior contracts.BehaviorSettings, trigger, action string) bool {
	switch action {
	case ActionComment:
		switch trigger {
		case TriggerMention, TriggerComment:
			return behavior.AllowPassiveReply
		default:
			return behavior.AllowActiveComment
		}
	case ActionReply:
		return behavior.AllowPassiveReply
	case ActionGroupMessage:
		// 被 @ 回复属于被动行为；自主群发言在调度侧直接检查 AllowGroupMessage
		// （proactive 触发的群消息不经此处投递）。
		return behavior.AllowPassiveReply && behavior.AllowGroupMessage
	}
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
// 主动评论是独立的主动动作，只开启 allow_active_comment 时也必须参与调度。
func ProactiveAllowed(behavior contracts.BehaviorSettings) bool {
	return behavior.AllowOriginalPost || behavior.AllowActiveComment || behavior.AllowLike ||
		behavior.AllowFollow || behavior.AllowRepost || behavior.AllowQuote
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

// ReplyDepthAllowed 判断父评论深度是否仍允许回复（maxDepth<=0 表示该 AI 不参与回复，
// 与旧实现一致；默认行为策略应配置为正数）。
func ReplyDepthAllowed(parentDepth, maxDepth int) bool {
	if maxDepth <= 0 {
		return false
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
