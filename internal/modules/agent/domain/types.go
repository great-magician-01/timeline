// Package domain 定义 agent（AI 自主行为）领域的不变式与纯规则：
// 任务状态机、决策白名单、频率/冷却/时段/深度限制、动作与触发类型常量。
// 本包不依赖数据库、HTTP、日志或任何跨模块实现，只依赖 shared/model 与 contracts 的纯数据结构。
package domain

import (
	"encoding/json"
	"errors"
	"strings"
)

// 任务状态（与 ai_tasks.status 的 CHECK 约束一致）。
const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusSucceeded = "succeeded"
	TaskStatusNoop      = "noop"
	TaskStatusRejected  = "rejected"
	TaskStatusFailed    = "failed"
	TaskStatusCanceled  = "canceled"
	TaskStatusPaused    = "paused"
)

// 触发类型（与 ai_tasks.trigger_type 的 CHECK 约束一致）。
const (
	TriggerManual    = "manual"
	TriggerSchedule  = "schedule"
	TriggerMention   = "mention"
	TriggerComment   = "comment"
	TriggerProactive = "proactive"
	TriggerRetry     = "retry"
)

// AI 可执行动作（与 ai_tasks.action_type 一致）。
const (
	ActionOriginalPost = "original_post"
	ActionComment      = "comment"
	ActionReply        = "reply"
	ActionLike         = "like"
	ActionUnlike       = "unlike"
	ActionFollow       = "follow"
	ActionUnfollow     = "unfollow"
	ActionRepost       = "repost"
	ActionQuote        = "quote"
	ActionNoop         = "noop"
)

// 结果/目标引用类型。
const (
	RefPost    = "post"
	RefComment = "comment"
	RefUser    = "user"
	RefTask    = "ai_task"
	RefAI      = "ai"
)

// 任务失败类型（写入 ai_tasks.error 的前缀）。
const (
	ErrorTypeModel         = "model_error"
	ErrorTypeInvalidJSON   = "invalid_json"
	ErrorTypeInvalidAction = "invalid_action"
	ErrorTypeForbidden     = "forbidden"
	ErrorTypeInvalidTarget = "invalid_target"
	ErrorTypeRateLimited   = "rate_limited"
	ErrorTypeConfig        = "config_error"
	ErrorTypeDatabase      = "database_error"
	ErrorTypeNetwork       = "network_error"
)

// 候选范围类型（ai_behavior_settings.candidate_scope_type）。
const (
	ScopeNone     = "none"
	ScopeFollowed = "followed"
	ScopeTags     = "tags"
	ScopeAuthors  = "authors"
)

// 人工生成任务的模式（存于 ai_tasks.trigger_ref_type，避免新增列）。
const (
	GenerationModeDraft  = "draft"
	GenerationModeDirect = "direct"
)

// Decision 是模型输出的一次结构化决策。
type Decision struct {
	Action     string  `json:"action"`
	Content    string  `json:"content"`
	Reason     string  `json:"reason"`
	TargetType string  `json:"target_type"`
	TargetID   *string `json:"target_id"`
}

// ErrInvalidDecision 表示模型输出不是合法决策。
var ErrInvalidDecision = errors.New("模型输出不是合法决策")

// ValidAction 判断动作是否在白名单内。
func ValidAction(action string) bool {
	switch action {
	case ActionOriginalPost, ActionComment, ActionReply, ActionLike, ActionUnlike,
		ActionFollow, ActionUnfollow, ActionRepost, ActionQuote, ActionNoop:
		return true
	default:
		return false
	}
}

// ValidTrigger 判断触发类型是否合法。
func ValidTrigger(trigger string) bool {
	switch trigger {
	case TriggerManual, TriggerSchedule, TriggerMention, TriggerComment, TriggerProactive, TriggerRetry:
		return true
	default:
		return false
	}
}

// ValidCandidateScope 判断候选范围类型是否合法。
func ValidCandidateScope(scope string) bool {
	switch scope {
	case ScopeNone, ScopeFollowed, ScopeTags, ScopeAuthors:
		return true
	default:
		return false
	}
}

// TaskClaimable 判断任务状态是否值得尝试领取：pending/failed 直接可领，
// running 交给租约超时判断（重启恢复路径）。
func TaskClaimable(status string) bool {
	switch status {
	case TaskStatusPending, TaskStatusFailed, TaskStatusRunning:
		return true
	default:
		return false
	}
}

// RetryPending 判断失败任务是否还可重试。
func RetryPending(attempts, maxAttempts int) bool {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	return attempts < maxAttempts
}

// ParseDecision 解析模型输出：允许 Markdown 代码围栏，拒绝未知动作与空动作。
func ParseDecision(raw string) (Decision, error) {
	text := strings.TrimSpace(stripCodeFence(raw))
	if text == "" || text == "null" {
		return Decision{}, ErrInvalidDecision
	}
	var decision Decision
	decoder := json.NewDecoder(strings.NewReader(text))
	if err := decoder.Decode(&decision); err != nil {
		return Decision{}, ErrInvalidDecision
	}
	decision.Action = strings.TrimSpace(decision.Action)
	if decision.Action == "" || !ValidAction(decision.Action) {
		return Decision{}, ErrInvalidDecision
	}
	if decision.Action == ActionNoop {
		return decision, nil
	}
	// 只有产出正文的动作要求内容非空；点赞/关注等动作允许 content 为空。
	switch decision.Action {
	case ActionOriginalPost, ActionComment, ActionReply, ActionQuote:
		if strings.TrimSpace(decision.Content) == "" {
			return Decision{}, ErrInvalidDecision
		}
	}
	return decision, nil
}

// stripCodeFence 去掉 ```json ... ``` 围栏。
func stripCodeFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	trimmed = strings.TrimPrefix(trimmed, "```")
	if index := strings.IndexByte(trimmed, '\n'); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	if index := strings.LastIndex(trimmed, "```"); index >= 0 {
		trimmed = trimmed[:index]
	}
	return trimmed
}

// DecisionSchema 返回决策 JSON Schema（供模型结构化输出使用）。
func DecisionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					ActionOriginalPost, ActionComment, ActionReply, ActionLike, ActionUnlike,
					ActionFollow, ActionUnfollow, ActionRepost, ActionQuote, ActionNoop,
				},
			},
			"content":     map[string]any{"type": "string"},
			"reason":      map[string]any{"type": "string"},
			"target_type": map[string]any{"type": "string", "enum": []string{RefPost, RefComment, RefUser, ""}},
			"target_id":   map[string]any{"type": []string{"string", "null"}},
		},
		"required":             []string{"action", "content", "reason"},
		"additionalProperties": false,
	}
}

// ParseTargetID 解析决策目标 ID（十进制雪花 ID 字符串）。
func ParseTargetID(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, ErrInvalidDecision
	}
	var id int64
	for _, char := range trimmed {
		if char < '0' || char > '9' {
			return 0, ErrInvalidDecision
		}
		id = id*10 + int64(char-'0')
		if id < 0 {
			return 0, ErrInvalidDecision
		}
	}
	if id <= 0 {
		return 0, ErrInvalidDecision
	}
	return id, nil
}
