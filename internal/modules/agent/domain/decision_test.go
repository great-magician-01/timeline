package domain

import (
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

func TestParseDecision(t *testing.T) {
	valid := []string{
		`{"action":"noop","content":"","reason":"没有合适内容"}`,
		"```json\n{\"action\":\"like\",\"content\":\"\",\"reason\":\"有意思\",\"target_type\":\"post\",\"target_id\":\"123\"}\n```",
	}
	for _, raw := range valid {
		if _, err := ParseDecision(raw); err != nil {
			t.Fatalf("ParseDecision(%q) 应成功: %v", raw, err)
		}
	}
	invalid := []string{
		``,
		`null`,
		`{"action":""}`,
		`{"action":"write_sql","content":"x"}`,
		`{"action":"original_post","content":""}`,
		`not json`,
	}
	for _, raw := range invalid {
		if _, err := ParseDecision(raw); err == nil {
			t.Fatalf("ParseDecision(%q) 应失败", raw)
		}
	}
}

func TestActionAllowedForTrigger(t *testing.T) {
	behavior := contracts.BehaviorSettings{
		AllowOriginalPost:  true,
		AllowActiveComment: true,
		AllowPassiveReply:  true,
		AllowLike:          true,
	}
	if !ActionAllowedForTrigger(behavior, TriggerSchedule, ActionOriginalPost) {
		t.Fatal("定时原创应允许 original_post")
	}
	if ActionAllowedForTrigger(behavior, TriggerSchedule, ActionLike) {
		t.Fatal("定时原创不应允许 like")
	}
	if !ActionAllowedForTrigger(behavior, TriggerMention, ActionLike) {
		t.Fatal("提及触发应允许开启的动作")
	}
}

func TestLimits(t *testing.T) {
	if !WithinActiveHours(8, 23, 8) || !WithinActiveHours(8, 23, 22) {
		t.Fatal("启用时段内应允许")
	}
	if WithinActiveHours(8, 23, 23) {
		t.Fatal("结束小时应排除")
	}
	if !WithinActiveHours(22, 6, 23) || !WithinActiveHours(22, 6, 5) || WithinActiveHours(22, 6, 12) {
		t.Fatal("跨零点时段判断错误")
	}
	if UnderDailyLimit(3, 3) || !UnderDailyLimit(2, 3) || !UnderDailyLimit(100, 0) {
		t.Fatal("每日上限判断错误")
	}
	now := time.Now()
	if !CooldownElapsed(time.Time{}, now, 60) || CooldownElapsed(now, now, 60) {
		t.Fatal("冷却判断错误")
	}
	if ReplyDepthAllowed(2, 2) || !ReplyDepthAllowed(1, 2) || ReplyDepthAllowed(9, 0) {
		t.Fatal("回复深度判断错误（maxDepth<=0 应表示不参与回复）")
	}
}

func TestParseTargetID(t *testing.T) {
	if id, err := ParseTargetID("123"); err != nil || id != 123 {
		t.Fatalf("合法 ID 解析失败: %d %v", id, err)
	}
	for _, raw := range []string{"", "0", "-1", "1x"} {
		if _, err := ParseTargetID(raw); err == nil {
			t.Fatalf("非法 ID %q 应失败", raw)
		}
	}
}
