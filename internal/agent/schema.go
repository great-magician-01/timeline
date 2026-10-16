package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/great-magician-01/callable"
)

// Decision 是模型输出的结构化动作决策（docs/WORKFLOWS.md 7.4）。
// 模型必须能够选择 noop（无操作），不能强制每次任务都产生内容或社交行为。
type Decision struct {
	Action     string `json:"action" jsonschema:"enum=original_post,enum=comment,enum=reply,enum=like,enum=unlike,enum=follow,enum=unfollow,enum=repost,enum=quote,enum=noop,description=本次决策执行的动作；unlike/unfollow 只能作用于“可取消的关系”列表中已存在的关系；没有合适的动作时必须使用 noop"`
	TargetType string `json:"target_type,omitempty" jsonschema:"enum=post,enum=comment,enum=user,description=动作目标类型，必须来自候选列表；noop 或原创帖可以留空"`
	TargetID   string `json:"target_id,omitempty" jsonschema:"description=动作目标的 ID 字符串，必须来自候选列表；noop 或原创帖可以留空"`
	Content    string `json:"content,omitempty" jsonschema:"description=原创帖、引用帖、评论或回复的 Markdown 正文；其他动作可以留空"`
	Reason     string `json:"reason,omitempty" jsonschema:"description=一句话说明决策理由，会记录到任务摘要中"`
}

// MemoryDraft 是模型生成的近期记忆候选。
type MemoryDraft struct {
	Content        string   `json:"content" jsonschema:"description=一句话摘要，保留可复用的关键事实，不超过 200 字"`
	Category       string   `json:"category,omitempty" jsonschema:"enum=fact,enum=preference,enum=relationship,enum=event,enum=topic,description=记忆分类"`
	Keywords       []string `json:"keywords,omitempty" jsonschema:"description=2-5 个检索关键词"`
	Importance     int      `json:"importance,omitempty" jsonschema:"description=重要性 1-5，日常互动用 2-3"`
	InferenceLevel string   `json:"inference_level,omitempty" jsonschema:"enum=fact,enum=summary,enum=inference,description=来源事实、互动摘要或模型推断"`
}

// LongTermMemoryDraft 是记忆沉淀输出的单条长期记忆候选。
type LongTermMemoryDraft struct {
	// Supersedes 是需要被本条替代的既有记忆 ID（近期或长期记忆，最多 5 条）。
	Supersedes     []string `json:"supersedes,omitempty" jsonschema:"description=被本条替代的既有记忆 ID 列表，没有则留空"`
	Content        string   `json:"content" jsonschema:"description=长期记忆正文，不超过 300 字"`
	Category       string   `json:"category,omitempty" jsonschema:"enum=fact,enum=preference,enum=relationship,enum=event,enum=topic"`
	Keywords       []string `json:"keywords,omitempty" jsonschema:"description=2-8 个检索关键词"`
	Importance     int      `json:"importance,omitempty" jsonschema:"description=重要性 1-5"`
	InferenceLevel string   `json:"inference_level,omitempty" jsonschema:"enum=fact,enum=summary,enum=inference"`
}

// ConsolidationDraft 是长期记忆沉淀的结构化输出。
type ConsolidationDraft struct {
	Memories []LongTermMemoryDraft `json:"memories" jsonschema:"description=值得长期保存的记忆列表；没有值得保存的内容时返回空数组"`
	Reason   string                `json:"reason,omitempty" jsonschema:"description=一句话说明整理结论"`
}

// decisionAction 是动作白名单（与 ai_tasks.action_type 的 CHECK 约束一致）。
var decisionActions = map[string]bool{
	actionOriginalPost: true,
	actionComment:      true,
	actionReply:        true,
	actionLike:         true,
	actionUnlike:       true,
	actionFollow:       true,
	actionUnfollow:     true,
	actionRepost:       true,
	actionQuote:        true,
	actionNoop:         true,
}

// taskActionTypes 是 ai_tasks.action_type 允许的全部取值（含模型不会主动选择的取消类动作）。
var taskActionTypes = map[string]bool{
	actionOriginalPost: true,
	actionComment:      true,
	actionReply:        true,
	actionLike:         true,
	actionUnlike:       true,
	actionFollow:       true,
	actionUnfollow:     true,
	actionRepost:       true,
	actionQuote:        true,
	actionNoop:         true,
}

// validActionType 判断取值能否写入 ai_tasks.action_type。
func validActionType(action string) bool {
	return taskActionTypes[strings.TrimSpace(action)]
}

// validDecisionAction 判断模型返回的动作是否在白名单内。
func validDecisionAction(action string) bool {
	return decisionActions[strings.TrimSpace(action)]
}

// decisionSchema 返回决策的结构化输出约束。
// strict=true 按照规格要求开启；不同兼容端点的支持程度不同，解析仍按防御式处理。
func decisionSchema() callable.ResponseFormat {
	return callable.JSONSchemaFor[Decision]("ai_decision", true)
}

// memorySchema 返回近期记忆候选的结构化输出约束。
func memorySchema() callable.ResponseFormat {
	return callable.JSONSchemaFor[MemoryDraft]("ai_memory", true)
}

// consolidationSchema 返回长期记忆沉淀的结构化输出约束。
func consolidationSchema() callable.ResponseFormat {
	return callable.JSONSchemaFor[ConsolidationDraft]("ai_memory_consolidation", true)
}

// parseDecision 解析模型返回的决策 JSON；非法 JSON 或未知动作返回错误，
// 调用方按 rejected（不重试）处理。
func parseDecision(raw string) (Decision, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Decision{}, errInvalidJSON
	}
	var decision Decision
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decision); err != nil {
		// 模型偶尔会包一层 markdown 代码块，尝试剥离后重试一次。
		if inner, ok := stripCodeFence(trimmed); ok {
			decision = Decision{}
			decoder = json.NewDecoder(strings.NewReader(inner))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&decision); err != nil {
				return Decision{}, errInvalidJSON
			}
		} else {
			return Decision{}, errInvalidJSON
		}
	}
	decision.Action = strings.ToLower(strings.TrimSpace(decision.Action))
	if !validDecisionAction(decision.Action) {
		return Decision{}, fmt.Errorf("%w: %s", errInvalidAction, firstNonEmpty(decision.Action, "（空动作）"))
	}
	decision.TargetType = strings.ToLower(strings.TrimSpace(decision.TargetType))
	if decision.TargetType != "" && decision.TargetType != targetPost &&
		decision.TargetType != targetComment && decision.TargetType != targetUser {
		return Decision{}, fmt.Errorf("%w: %s", errInvalidAction, decision.TargetType)
	}
	decision.TargetID = strings.TrimSpace(decision.TargetID)
	decision.Content = strings.TrimSpace(decision.Content)
	decision.Reason = strings.TrimSpace(decision.Reason)
	return decision, nil
}

// stripCodeFence 去掉模型输出外层的 ```json 代码块。
func stripCodeFence(raw string) (string, bool) {
	if !strings.HasPrefix(raw, "```") {
		return "", false
	}
	body := strings.TrimPrefix(raw, "```")
	if index := strings.IndexByte(body, '\n'); index >= 0 {
		body = body[index+1:]
	}
	body = strings.TrimSuffix(strings.TrimSpace(body), "```")
	body = strings.TrimSpace(body)
	if body == "" {
		return "", false
	}
	return body, true
}

// errInvalidJSON / errInvalidAction 是解析失败的哨兵错误，供错误分类使用。
var (
	errInvalidJSON   = fmt.Errorf("模型输出不是合法的决策 JSON")
	errInvalidAction = fmt.Errorf("模型输出了不在白名单内的动作")
)

// truncateRunes 按字符截断，不破坏多字节字符。
func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
