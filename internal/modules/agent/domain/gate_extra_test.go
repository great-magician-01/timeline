package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// allActions 是行为策略里全部可配置动作；用于遍历 ActionAllowed 的开关映射。
var allActions = []string{
	ActionOriginalPost, ActionComment, ActionReply, ActionLike, ActionUnlike,
	ActionFollow, ActionUnfollow, ActionRepost, ActionQuote,
}

// fullBehavior 返回所有动作都开启的行为策略。
func fullBehavior() contracts.BehaviorSettings {
	return contracts.BehaviorSettings{
		AllowOriginalPost:  true,
		AllowActiveComment: true,
		AllowPassiveReply:  true,
		AllowLike:          true,
		AllowFollow:        true,
		AllowUnfollow:      true,
		AllowRepost:        true,
		AllowQuote:         true,
	}
}

// TestActionAllowedMatrix 校验 ActionAllowed 的开关映射：like/unlike 共用 allow_like，
// noop 恒允许，未知动作恒拒绝。
func TestActionAllowedMatrix(t *testing.T) {
	enabled := fullBehavior()
	for _, action := range allActions {
		if !ActionAllowed(enabled, action) {
			t.Errorf("全部开启时动作 %s 应允许", action)
		}
	}
	if !ActionAllowed(contracts.BehaviorSettings{}, ActionNoop) {
		t.Fatal("noop 必须始终允许")
	}
	if ActionAllowed(enabled, "unknown") {
		t.Fatal("未知动作必须拒绝")
	}

	cases := []struct {
		name     string
		behavior contracts.BehaviorSettings
		action   string
		want     bool
	}{
		{"原创关闭", contracts.BehaviorSettings{}, ActionOriginalPost, false},
		{"主动评论关闭", contracts.BehaviorSettings{}, ActionComment, false},
		{"被动回复关闭", contracts.BehaviorSettings{}, ActionReply, false},
		{"点赞关闭影响 unlike", contracts.BehaviorSettings{AllowLike: false}, ActionUnlike, false},
		{"点赞开启影响 unlike", contracts.BehaviorSettings{AllowLike: true}, ActionUnlike, true},
		{"关注开启", contracts.BehaviorSettings{AllowFollow: true}, ActionFollow, true},
		{"取关独立开关", contracts.BehaviorSettings{AllowFollow: true, AllowUnfollow: false}, ActionUnfollow, false},
		{"转发开启", contracts.BehaviorSettings{AllowRepost: true}, ActionRepost, true},
		{"引用开启", contracts.BehaviorSettings{AllowQuote: true}, ActionQuote, true},
	}
	for _, tc := range cases {
		if got := ActionAllowed(tc.behavior, tc.action); got != tc.want {
			t.Errorf("%s: ActionAllowed(%s) = %v，期望 %v", tc.name, tc.action, got, tc.want)
		}
	}
}

// TestActionAllowedForTriggerMatrix 校验触发类型叠加限制（docs/WORKFLOWS.md 7.2）：
// 提及/新评论按被动回复、主动浏览按主动评论；定时只允许原创/引用；主动互动只允许候选动作。
func TestActionAllowedForTriggerMatrix(t *testing.T) {
	behavior := fullBehavior()
	cases := []struct {
		name    string
		trigger string
		action  string
		want    bool
	}{
		{"提及触发评论按被动回复", TriggerMention, ActionComment, true},
		{"新评论触发评论按被动回复", TriggerComment, ActionComment, true},
		{"主动触发评论按主动评论", TriggerProactive, ActionComment, true},
		{"回复恒按被动回复", TriggerManual, ActionReply, true},
		{"定时允许原创", TriggerSchedule, ActionOriginalPost, true},
		{"定时允许引用", TriggerSchedule, ActionQuote, true},
		// 评论/回复走独立的触发语义（主动评论/被动回复开关），不再叠加触发范围限制。
		{"定时触发评论按主动评论开关", TriggerSchedule, ActionComment, true},
		{"定时不允许点赞", TriggerSchedule, ActionLike, false},
		{"主动互动允许点赞", TriggerProactive, ActionLike, true},
		{"主动互动允许转发", TriggerProactive, ActionRepost, true},
		{"主动互动触发回复按被动回复开关", TriggerProactive, ActionReply, true},
		{"手动触发默认放行", TriggerManual, ActionLike, true},
		{"未知动作拒绝", TriggerManual, "unknown", false},
	}
	for _, tc := range cases {
		if got := ActionAllowedForTrigger(behavior, tc.trigger, tc.action); got != tc.want {
			t.Errorf("%s: ActionAllowedForTrigger(%s,%s) = %v，期望 %v",
				tc.name, tc.trigger, tc.action, got, tc.want)
		}
	}

	// 关闭被动回复后，提及/评论触发的回复与评论都被拒绝。
	passiveOff := fullBehavior()
	passiveOff.AllowPassiveReply = false
	if ActionAllowedForTrigger(passiveOff, TriggerMention, ActionComment) {
		t.Fatal("关闭被动回复后提及触发评论应拒绝")
	}
	if ActionAllowedForTrigger(passiveOff, TriggerComment, ActionReply) {
		t.Fatal("关闭被动回复后回复应拒绝")
	}
}

// TestProactiveAllowed 校验主动互动开关的聚合判断（任一主动动作开启即可参与调度）。
func TestProactiveAllowed(t *testing.T) {
	if ProactiveAllowed(contracts.BehaviorSettings{}) {
		t.Fatal("全部关闭时不应允许主动互动")
	}
	single := []contracts.BehaviorSettings{
		{AllowOriginalPost: true},
		{AllowActiveComment: true},
		{AllowLike: true},
		{AllowFollow: true},
		{AllowRepost: true},
		{AllowQuote: true},
	}
	for i, behavior := range single {
		if !ProactiveAllowed(behavior) {
			t.Errorf("第 %d 个单项开启应允许主动互动", i+1)
		}
	}
	// 仅开启被动回复/取关不构成主动互动。
	if ProactiveAllowed(contracts.BehaviorSettings{AllowPassiveReply: true, AllowUnfollow: true}) {
		t.Fatal("被动回复/取关不应视为主动互动")
	}
}

// TestWithinActiveHours 校验启用时段判断：start==end 为全天，跨零点区间取并集。
func TestWithinActiveHours(t *testing.T) {
	cases := []struct {
		name       string
		start, end int
		hour       int
		want       bool
	}{
		{"start==end 全天生效", 9, 9, 3, true},
		{"普通区间内", 9, 18, 12, true},
		{"普通区间左边界", 9, 18, 9, true},
		{"普通区间右边界不含", 9, 18, 18, false},
		{"普通区间外", 9, 18, 8, false},
		{"跨零点深夜", 22, 6, 23, true},
		{"跨零点凌晨", 22, 6, 5, true},
		{"跨零点边界 22", 22, 6, 22, true},
		{"跨零点边界 6 不含", 22, 6, 6, false},
		{"跨零点白天不生效", 22, 6, 12, false},
	}
	for _, tc := range cases {
		if got := WithinActiveHours(tc.start, tc.end, tc.hour); got != tc.want {
			t.Errorf("%s: WithinActiveHours(%d,%d,%d) = %v，期望 %v",
				tc.name, tc.start, tc.end, tc.hour, got, tc.want)
		}
	}
}

// TestUnderDailyLimit 校验每日上限判断：limit<=0 不限制，达到上限即拒绝。
func TestUnderDailyLimit(t *testing.T) {
	cases := []struct {
		count, limit int
		want         bool
	}{
		{0, 0, true},
		{100, -1, true},
		{0, 3, true},
		{2, 3, true},
		{3, 3, false},
		{4, 3, false},
	}
	for _, tc := range cases {
		if got := UnderDailyLimit(tc.count, tc.limit); got != tc.want {
			t.Errorf("UnderDailyLimit(%d,%d) = %v，期望 %v", tc.count, tc.limit, got, tc.want)
		}
	}
}

// TestCooldownElapsed 校验冷却判断：零值 last/非正冷却视为已过，其余按秒比较。
func TestCooldownElapsed(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if !CooldownElapsed(time.Time{}, now, 60) {
		t.Fatal("从未发生时视为已过冷却")
	}
	if !CooldownElapsed(now.Add(-time.Minute), now, 0) {
		t.Fatal("冷却<=0 视为无冷却")
	}
	if CooldownElapsed(now.Add(-30*time.Second), now, 60) {
		t.Fatal("未到冷却时间应拒绝")
	}
	if !CooldownElapsed(now.Add(-time.Minute), now, 60) {
		t.Fatal("恰好达到冷却时间应放行")
	}
}

// TestReplyDepthAllowed 校验回复深度：maxDepth<=0 表示不参与回复。
func TestReplyDepthAllowed(t *testing.T) {
	cases := []struct {
		parent, max int
		want        bool
	}{
		{0, 0, false},
		{0, -1, false},
		{0, 3, true},
		{2, 3, true},
		{3, 3, false},
		{4, 3, false},
	}
	for _, tc := range cases {
		if got := ReplyDepthAllowed(tc.parent, tc.max); got != tc.want {
			t.Errorf("ReplyDepthAllowed(%d,%d) = %v，期望 %v", tc.parent, tc.max, got, tc.want)
		}
	}
}

// TestScheduleMinutes 校验调度间隔解析：空/null/非法 JSON/缺失/非数字回退 fallback。
func TestScheduleMinutes(t *testing.T) {
	cases := []struct {
		name     string
		raw      json.RawMessage
		key      string
		fallback int
		want     int
	}{
		{"空配置回退", nil, "post_interval_minutes", 30, 30},
		{"null 回退", json.RawMessage("null"), "post_interval_minutes", 30, 30},
		{"非法 JSON 回退", json.RawMessage("{bad"), "post_interval_minutes", 30, 30},
		{"缺失键回退", json.RawMessage(`{"other":10}`), "post_interval_minutes", 30, 30},
		{"数字正常读取", json.RawMessage(`{"post_interval_minutes":45}`), "post_interval_minutes", 30, 45},
		{"字符串类型回退", json.RawMessage(`{"post_interval_minutes":"45"}`), "post_interval_minutes", 30, 30},
		{"布尔类型回退", json.RawMessage(`{"post_interval_minutes":true}`), "post_interval_minutes", 30, 30},
	}
	for _, tc := range cases {
		if got := ScheduleMinutes(tc.raw, tc.key, tc.fallback); got != tc.want {
			t.Errorf("%s: ScheduleMinutes = %d，期望 %d", tc.name, got, tc.want)
		}
	}
}

// TestValidTrigger 校验触发类型白名单：六种合法触发，未知/空值拒绝。
func TestValidTrigger(t *testing.T) {
	for _, trigger := range []string{
		TriggerManual, TriggerSchedule, TriggerMention, TriggerComment, TriggerProactive, TriggerRetry,
	} {
		if !ValidTrigger(trigger) {
			t.Errorf("合法触发类型 %q 被拒绝", trigger)
		}
	}
	for _, trigger := range []string{"", "unknown", "MANUAL", "manual "} {
		if ValidTrigger(trigger) {
			t.Errorf("非法触发类型 %q 被接受", trigger)
		}
	}
}

// TestRetryPending 校验失败任务重试上限：maxAttempts<=0 回退 3，attempts 小于上限才可重试。
func TestRetryPending(t *testing.T) {
	cases := []struct {
		attempts, max int
		want          bool
	}{
		{0, 3, true},
		{2, 3, true},
		{3, 3, false},
		{4, 3, false},
		{2, 0, true},
		{3, 0, false},
		{0, 5, true},
	}
	for _, tc := range cases {
		if got := RetryPending(tc.attempts, tc.max); got != tc.want {
			t.Errorf("RetryPending(%d,%d) = %v，期望 %v", tc.attempts, tc.max, got, tc.want)
		}
	}
}

// TestDecisionSchema 校验模型结构化输出的 JSON Schema 覆盖全部动作与目标类型，
// 且必填字段与 additionalProperties=false 约束存在（防止模型返回任意字段）。
func TestDecisionSchema(t *testing.T) {
	schema := DecisionSchema()
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v，期望 object", schema["type"])
	}
	if admitted, _ := schema["additionalProperties"].(bool); admitted {
		t.Fatal("additionalProperties 必须为 false")
	}
	required, _ := schema["required"].([]string)
	wantRequired := map[string]bool{"action": false, "content": false, "reason": false}
	for _, field := range required {
		if _, ok := wantRequired[field]; ok {
			wantRequired[field] = true
		}
	}
	for field, found := range wantRequired {
		if !found {
			t.Errorf("schema required 缺少字段 %s", field)
		}
	}

	properties, _ := schema["properties"].(map[string]any)
	action, _ := properties["action"].(map[string]any)
	enum, _ := action["enum"].([]string)
	if len(enum) != len(allActions)+1 {
		t.Fatalf("动作枚举数量 = %d，期望 %d（含 noop）", len(enum), len(allActions)+1)
	}
	seen := map[string]bool{}
	for _, value := range enum {
		seen[value] = true
		if !ValidAction(value) {
			t.Errorf("schema 枚举包含非法动作 %q", value)
		}
	}
	for _, actionValue := range allActions {
		if !seen[actionValue] {
			t.Errorf("schema 枚举缺少动作 %q", actionValue)
		}
	}
	if !seen[ActionNoop] {
		t.Error("schema 枚举缺少 noop")
	}
}
