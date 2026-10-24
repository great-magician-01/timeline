package app

// 群聊 AI 行为用例测试：@提及 触发的投递门控与幂等、自主发言调度、
// 群上下文组装、group_message 动作门控与代发。全部使用 fake 端口。

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// allowGroup 返回开启群聊发言（含被动回复）的运行时。
func allowGroup(rt contracts.Runtime) contracts.Runtime {
	rt.Behavior.AllowPassiveReply = true
	rt.Behavior.AllowGroupMessage = true
	return rt
}

func TestOnGroupMentionGate(t *testing.T) {
	base := func() *flowFixture {
		f := newFlowFixture()
		f.enableAI(7)
		f.ai.runtime = allowGroup(f.ai.runtime)
		f.groups.members["100:7"] = true
		return f
	}

	t.Run("全部条件满足时投递任务（幂等键含消息与 AI）", func(t *testing.T) {
		f := base()
		err := f.svc.OnGroupMention(context.Background(), contracts.GroupMentionSignal{
			AIID: 7, GroupID: 100, MessageID: 55, MentionedBy: 3,
		})
		if err != nil {
			t.Fatalf("OnGroupMention 失败：%v", err)
		}
		if len(f.tasks.inserted) != 1 {
			t.Fatalf("应投递 1 个任务，实际 %d", len(f.tasks.inserted))
		}
		task := f.tasks.inserted[0]
		if task.TriggerType != domain.TriggerGroupMention || task.ActionType != domain.ActionGroupMessage {
			t.Fatalf("任务类型错误：%+v", task)
		}
		if task.TriggerRefType != domain.RefGroup || task.TriggerRefID == nil || *task.TriggerRefID != 100 {
			t.Fatalf("触发来源应为群：%+v", task)
		}
		want := "group_mention:55:7"
		if task.IdempotencyKey != want {
			t.Fatalf("幂等键 = %q，期望 %q", task.IdempotencyKey, want)
		}
	})

	t.Run("重复信号幂等不重复投递", func(t *testing.T) {
		f := base()
		signal := contracts.GroupMentionSignal{AIID: 7, GroupID: 100, MessageID: 55, MentionedBy: 3}
		for i := 0; i < 2; i++ {
			if err := f.svc.OnGroupMention(context.Background(), signal); err != nil {
				t.Fatalf("第 %d 次投递失败：%v", i+1, err)
			}
		}
		if len(f.tasks.inserted) != 1 {
			t.Fatalf("重复信号应幂等，实际插入 %d 条任务", len(f.tasks.inserted))
		}
	})

	t.Run("全局开关关闭不投递", func(t *testing.T) {
		f := base()
		f.settings.values.AIGlobalEnabled = false
		if err := f.svc.OnGroupMention(context.Background(), contracts.GroupMentionSignal{AIID: 7, GroupID: 100, MessageID: 55}); err != nil {
			t.Fatalf("OnGroupMention 失败：%v", err)
		}
		if len(f.tasks.inserted) != 0 {
			t.Fatal("全局关闭时不应投递")
		}
	})

	t.Run("AI 停用或未开启群聊发言不投递", func(t *testing.T) {
		disabled := base()
		disabled.ai.runtime.Profile.IsEnabled = false
		if err := disabled.svc.OnGroupMention(context.Background(), contracts.GroupMentionSignal{AIID: 7, GroupID: 100, MessageID: 55}); err != nil {
			t.Fatalf("OnGroupMention 失败：%v", err)
		}
		if len(disabled.tasks.inserted) != 0 {
			t.Fatal("AI 停用不应投递")
		}

		noSwitch := base()
		noSwitch.ai.runtime.Behavior.AllowGroupMessage = false
		if err := noSwitch.svc.OnGroupMention(context.Background(), contracts.GroupMentionSignal{AIID: 7, GroupID: 100, MessageID: 55}); err != nil {
			t.Fatalf("OnGroupMention 失败：%v", err)
		}
		if len(noSwitch.tasks.inserted) != 0 {
			t.Fatal("未开启 allow_group_message 不应投递")
		}
	})

	t.Run("非本群成员不投递", func(t *testing.T) {
		f := base()
		f.groups.members["100:7"] = false
		if err := f.svc.OnGroupMention(context.Background(), contracts.GroupMentionSignal{AIID: 7, GroupID: 100, MessageID: 55}); err != nil {
			t.Fatalf("OnGroupMention 失败：%v", err)
		}
		if len(f.tasks.inserted) != 0 {
			t.Fatal("非成员不应投递")
		}
	})
}

func TestScheduleGroupMessage(t *testing.T) {
	rt := func() contracts.Runtime {
		f := newFlowFixture()
		return allowGroup(f.enableAI(7))
	}

	t.Run("每个间隔桶只投递一个群（幂等键含桶序号）", func(t *testing.T) {
		f := newFlowFixture()
		current := rt()
		f.ai.runtime = current
		f.groups.groupsOf[7] = []int64{1, 2, 3, 4, 5, 6, 7, 8}
		f.svc.scheduleGroupMessage(context.Background(), current, f.clk.Now())
		if len(f.tasks.inserted) != 1 {
			t.Fatalf("每个桶应只投递 1 个任务，实际 %d", len(f.tasks.inserted))
		}
		task := f.tasks.inserted[0]
		if task.TriggerType != domain.TriggerGroupSchedule || task.ActionType != domain.ActionGroupMessage {
			t.Fatalf("任务类型错误：%+v", task)
		}
		if !strings.HasPrefix(task.IdempotencyKey, "ai_group_schedule:7:") {
			t.Fatalf("幂等键应含 AI 与桶序号：%q", task.IdempotencyKey)
		}
		if task.TriggerRefID == nil || *task.TriggerRefID < 1 || *task.TriggerRefID > 8 {
			t.Fatalf("目标群应来自所在群列表：%+v", task)
		}
	})

	t.Run("桶序号轮转覆盖不同群", func(t *testing.T) {
		f := newFlowFixture()
		current := rt()
		f.ai.runtime = current
		f.groups.groupsOf[7] = []int64{10, 20, 30}
		// 同一桶内重复调度只投递一次（scheduleDue 挡住，即便仓储未挡）。
		f.svc.scheduleGroupMessage(context.Background(), current, f.clk.Now())
		first := f.tasks.inserted[0]
		f.tasks.inserted = nil
		// 推进到下一个桶再调度：目标群应轮转（模 3 递增）。
		next := f.clk.Now().Add(time.Duration(61) * time.Minute)
		f.svc.scheduleGroupMessage(context.Background(), current, next)
		if len(f.tasks.inserted) != 1 {
			t.Fatalf("下一桶应再投递 1 个任务，实际 %d", len(f.tasks.inserted))
		}
		if *f.tasks.inserted[0].TriggerRefID == *first.TriggerRefID {
			t.Fatalf("跨桶应轮转目标群：%d 与 %d 相同", *first.TriggerRefID, *f.tasks.inserted[0].TriggerRefID)
		}
	})

	t.Run("间隔为 0 关闭", func(t *testing.T) {
		f := newFlowFixture()
		current := rt()
		current.Behavior.Schedule = []byte(`{"group_message_interval_minutes":0}`)
		f.ai.runtime = current
		f.groups.groupsOf[7] = []int64{1}
		f.svc.scheduleGroupMessage(context.Background(), current, f.clk.Now())
		if len(f.tasks.inserted) != 0 {
			t.Fatal("interval=0 应关闭自主群发言")
		}
	})

	t.Run("未开启 allow_group_message 不调度", func(t *testing.T) {
		f := newFlowFixture()
		current := f.enableAI(7)
		current.Behavior.AllowPassiveReply = true
		f.ai.runtime = current
		f.groups.groupsOf[7] = []int64{1}
		f.svc.scheduleGroupMessage(context.Background(), current, f.clk.Now())
		if len(f.tasks.inserted) != 0 {
			t.Fatal("未开启群聊发言不应调度")
		}
	})

	t.Run("不在群不调度", func(t *testing.T) {
		f := newFlowFixture()
		current := rt()
		f.ai.runtime = current
		f.svc.scheduleGroupMessage(context.Background(), current, f.clk.Now())
		if len(f.tasks.inserted) != 0 {
			t.Fatal("不在任何群不应调度")
		}
	})
}

func TestBuildGroupPrompt(t *testing.T) {
	t.Run("返回群名与升序最近消息", func(t *testing.T) {
		f := newFlowFixture()
		f.groups.snapshots[100] = contracts.GroupSnapshot{
			GroupID: 100, Name: "项目群", Active: true,
			RecentMessages: []contracts.GroupContextMessage{
				{MessageID: 1, SenderID: 4, PlainText: "第一条"},
				{MessageID: 2, SenderID: 3, PlainText: "第二条"},
			},
		}
		group, err := f.svc.buildGroupPrompt(context.Background(), 100)
		if err != nil {
			t.Fatalf("buildGroupPrompt 失败：%v", err)
		}
		if group.Name != "项目群" || len(group.Messages) != 2 {
			t.Fatalf("群上下文错误：%+v", group)
		}
		section := groupPromptSection(group)
		if !strings.Contains(section, "项目群") || !strings.Contains(section, "第一条") || !strings.Contains(section, "第二条") {
			t.Fatalf("上下文渲染缺少群名或消息：%s", section)
		}
		// 消息应按时间升序（第一条在前）。
		first := strings.Index(section, "第一条")
		second := strings.Index(section, "第二条")
		if first > second {
			t.Fatalf("消息顺序应升序：%s", section)
		}
	})

	t.Run("群不存在按 NotFound 拒绝任务", func(t *testing.T) {
		f := newFlowFixture()
		if _, err := f.svc.buildGroupPrompt(context.Background(), 404); err == nil {
			t.Fatal("群不存在应返回错误")
		}
	})

	t.Run("群已解散按 NotFound 拒绝任务", func(t *testing.T) {
		f := newFlowFixture()
		f.groups.snapshots[100] = contracts.GroupSnapshot{GroupID: 100, Name: "解散群", Active: false}
		if _, err := f.svc.buildGroupPrompt(context.Background(), 100); err == nil {
			t.Fatal("群已解散应返回错误")
		}
	})
}

func TestGroupMessageTriggerGating(t *testing.T) {
	t.Run("taskActionAllowed：群聊任务只允许 group_message", func(t *testing.T) {
		if !taskActionAllowed(mustTask(domain.TriggerGroupMention), domain.ActionGroupMessage) {
			t.Fatal("group_mention 应允许 group_message")
		}
		if !taskActionAllowed(mustTask(domain.TriggerGroupSchedule), domain.ActionGroupMessage) {
			t.Fatal("group_schedule 应允许 group_message")
		}
		if taskActionAllowed(mustTask(domain.TriggerGroupMention), domain.ActionOriginalPost) {
			t.Fatal("group_mention 不应允许原创帖")
		}
		if taskActionAllowed(mustTask(domain.TriggerMention), domain.ActionGroupMessage) {
			t.Fatal("普通提及不应允许群消息")
		}
	})

	t.Run("ActionAllowedForTrigger：被 @ 回复需同时开启两个开关", func(t *testing.T) {
		both := contracts.BehaviorSettings{AllowPassiveReply: true, AllowGroupMessage: true}
		onlyReply := contracts.BehaviorSettings{AllowPassiveReply: true}
		onlyGroup := contracts.BehaviorSettings{AllowGroupMessage: true}
		if !domain.ActionAllowedForTrigger(both, domain.TriggerGroupMention, domain.ActionGroupMessage) {
			t.Fatal("双开关开启应允许")
		}
		if domain.ActionAllowedForTrigger(onlyReply, domain.TriggerGroupMention, domain.ActionGroupMessage) {
			t.Fatal("只开被动回复应拒绝")
		}
		if domain.ActionAllowedForTrigger(onlyGroup, domain.TriggerGroupMention, domain.ActionGroupMessage) {
			t.Fatal("只开群聊发言应拒绝（被 @ 属于被动行为）")
		}
	})

	t.Run("ParseDecision：group_message 要求正文非空", func(t *testing.T) {
		if _, err := domain.ParseDecision(`{"action":"group_message","content":"大家好","reason":"参与讨论"}`); err != nil {
			t.Fatalf("合法群消息决策应通过：%v", err)
		}
		if _, err := domain.ParseDecision(`{"action":"group_message","content":"  ","reason":"空正文"}`); err == nil {
			t.Fatal("空正文群消息应拒绝")
		}
		if !domain.ValidTrigger(domain.TriggerGroupSchedule) || !domain.ValidTrigger(domain.TriggerGroupMention) {
			t.Fatal("群聊触发类型应在白名单内")
		}
	})
}

// mustTask 构造最小任务对象。
func mustTask(trigger string) repo.Task {
	return repo.Task{TriggerType: trigger}
}
