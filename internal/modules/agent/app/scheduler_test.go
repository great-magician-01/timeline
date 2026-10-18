package app

// scheduler.go 用例测试：调度到期判断、定时原创/主动互动投递、提及与评论触发扫描、
// 调度周期入口。全部使用 fake 端口与 fake clock。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestScheduleBucket(t *testing.T) {
	base := time.Date(2026, 10, 18, 12, 34, 56, 0, time.UTC)
	cases := []struct {
		name     string
		interval int
		want     int64
	}{
		{"非正间隔退化为当前秒", 0, base.Unix()},
		{"负数间隔退化为当前秒", -5, base.Unix()},
		{"60 分钟桶", 60, base.Unix() / 3600},
		{"360 分钟桶", 360, base.Unix() / 21600},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := scheduleBucket(base, testCase.interval); got != testCase.want {
				t.Fatalf("scheduleBucket(%d) = %d，期望 %d", testCase.interval, got, testCase.want)
			}
		})
	}
}

func TestScheduleDue(t *testing.T) {
	now := testClock().Now()
	t.Run("间隔非正不调度", func(t *testing.T) {
		f := newFlowFixture()
		due, err := f.svc.scheduleDue(context.Background(), 1, domain.TriggerSchedule, 0, now)
		if err != nil || due {
			t.Fatalf("interval<=0 应返回 (false,nil)，实际 (%v,%v)", due, err)
		}
	})
	t.Run("最近成功未超过间隔", func(t *testing.T) {
		f := newFlowFixture()
		last := now.Add(-10 * time.Minute)
		f.counters.last[domain.TriggerSchedule] = &last
		due, err := f.svc.scheduleDue(context.Background(), 1, domain.TriggerSchedule, 60, now)
		if err != nil || due {
			t.Fatalf("冷却未到不应调度，实际 (%v,%v)", due, err)
		}
	})
	t.Run("最近成功超过间隔且无新任务", func(t *testing.T) {
		f := newFlowFixture()
		last := now.Add(-2 * time.Hour)
		f.counters.last[domain.TriggerSchedule] = &last
		due, err := f.svc.scheduleDue(context.Background(), 1, domain.TriggerSchedule, 60, now)
		if err != nil || !due {
			t.Fatalf("应允许调度，实际 (%v,%v)", due, err)
		}
		if len(f.tasks.listFilters) != 1 || f.tasks.listFilters[0].TriggerType != domain.TriggerSchedule {
			t.Fatalf("应按触发类型查询最近任务: %+v", f.tasks.listFilters)
		}
	})
	t.Run("最近创建过任务未超过间隔", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.listResult = []repo.Task{{ID: 1, CreatedAt: now.Add(-5 * time.Minute)}}
		due, err := f.svc.scheduleDue(context.Background(), 1, domain.TriggerSchedule, 60, now)
		if err != nil || due {
			t.Fatalf("最近创建过任务不应调度，实际 (%v,%v)", due, err)
		}
	})
	t.Run("最近创建任务超过间隔", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.listResult = []repo.Task{{ID: 1, CreatedAt: now.Add(-3 * time.Hour)}}
		due, err := f.svc.scheduleDue(context.Background(), 1, domain.TriggerSchedule, 60, now)
		if err != nil || !due {
			t.Fatalf("应允许调度，实际 (%v,%v)", due, err)
		}
	})
	t.Run("读取最近成功时间失败", func(t *testing.T) {
		f := newFlowFixture()
		f.counters.lastErr = errBoom
		if _, err := f.svc.scheduleDue(context.Background(), 1, domain.TriggerSchedule, 60, now); !errors.Is(err, errBoom) {
			t.Fatalf("应透传计数错误: %v", err)
		}
	})
	t.Run("读取最近任务失败", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.listErr = errBoom
		if _, err := f.svc.scheduleDue(context.Background(), 1, domain.TriggerSchedule, 60, now); !errors.Is(err, errBoom) {
			t.Fatalf("应透传任务查询错误: %v", err)
		}
	})
}

func TestScheduleOriginalPost(t *testing.T) {
	t.Run("动作未开启不调度", func(t *testing.T) {
		f := newFlowFixture()
		rt := f.enableAI(1)
		rt.Behavior.AllowOriginalPost = false
		f.svc.scheduleOriginalPost(context.Background(), rt, f.clk.Now())
		if len(f.jobs.enqueueCalls) != 0 || len(f.tasks.inserted) != 0 {
			t.Fatalf("未开启原创不应投递: %+v", f.jobs.enqueueCalls)
		}
	})
	t.Run("间隔配置为零不调度", func(t *testing.T) {
		f := newFlowFixture()
		rt := f.enableAI(1)
		rt.Behavior.Schedule = []byte(`{"post_interval_minutes":0}`)
		f.svc.scheduleOriginalPost(context.Background(), rt, f.clk.Now())
		if len(f.tasks.inserted) != 0 {
			t.Fatalf("间隔为零不应投递: %+v", f.tasks.inserted)
		}
	})
	t.Run("到期投递定时原创任务", func(t *testing.T) {
		f := newFlowFixture()
		rt := f.enableAI(1)
		f.svc.scheduleOriginalPost(context.Background(), rt, f.clk.Now())
		if len(f.tasks.inserted) != 1 {
			t.Fatalf("应写入 1 条任务，实际 %+v", f.tasks.inserted)
		}
		insert := f.tasks.inserted[0]
		if insert.TriggerType != domain.TriggerSchedule || insert.ActionType != domain.ActionOriginalPost || insert.AIID != 1 {
			t.Fatalf("定时原创任务参数错误: %+v", insert)
		}
		if !strings.HasPrefix(insert.IdempotencyKey, "ai_schedule:post:1:") {
			t.Fatalf("幂等键错误: %s", insert.IdempotencyKey)
		}
		if len(f.jobs.enqueueCalls) != 1 || f.jobs.enqueueCalls[0].Type != contracts.JobTypeAITask {
			t.Fatalf("应投递 ai_task 作业: %+v", f.jobs.enqueueCalls)
		}
	})
	t.Run("冷却中不调度", func(t *testing.T) {
		f := newFlowFixture()
		rt := f.enableAI(1)
		last := f.clk.Now().Add(-10 * time.Minute)
		f.counters.last[domain.TriggerSchedule] = &last
		f.svc.scheduleOriginalPost(context.Background(), rt, f.clk.Now())
		if len(f.tasks.inserted) != 0 {
			t.Fatalf("冷却中不应投递: %+v", f.tasks.inserted)
		}
	})
}

func TestScheduleProactive(t *testing.T) {
	t.Run("未开启主动互动不调度", func(t *testing.T) {
		f := newFlowFixture()
		rt := f.enableAI(1)
		rt.Behavior = contracts.BehaviorSettings{CandidateScopeType: domain.ScopeGlobalPublic}
		f.svc.scheduleProactive(context.Background(), rt, f.clk.Now())
		if len(f.tasks.inserted) != 0 {
			t.Fatalf("未开启主动动作不应投递: %+v", f.tasks.inserted)
		}
	})
	t.Run("候选范围无效不调度", func(t *testing.T) {
		f := newFlowFixture()
		rt := f.enableAI(1)
		rt.Behavior.CandidateScopeType = domain.ScopeNone
		f.svc.scheduleProactive(context.Background(), rt, f.clk.Now())
		if len(f.tasks.inserted) != 0 {
			t.Fatalf("候选范围无效不应投递: %+v", f.tasks.inserted)
		}
	})
	t.Run("间隔为零不调度", func(t *testing.T) {
		f := newFlowFixture()
		rt := f.enableAI(1)
		rt.Behavior.CandidateScopeType = domain.ScopeGlobalPublic
		rt.Behavior.Schedule = []byte(`{"proactive_interval_minutes":0}`)
		f.svc.scheduleProactive(context.Background(), rt, f.clk.Now())
		if len(f.tasks.inserted) != 0 {
			t.Fatalf("间隔为零不应投递: %+v", f.tasks.inserted)
		}
	})
	t.Run("到期投递主动互动任务", func(t *testing.T) {
		f := newFlowFixture()
		rt := f.enableAI(1)
		rt.Behavior.CandidateScopeType = domain.ScopeGlobalPublic
		f.svc.scheduleProactive(context.Background(), rt, f.clk.Now())
		if len(f.tasks.inserted) != 1 {
			t.Fatalf("应写入 1 条任务，实际 %+v", f.tasks.inserted)
		}
		insert := f.tasks.inserted[0]
		if insert.TriggerType != domain.TriggerProactive || insert.ActionType != domain.ActionNoop {
			t.Fatalf("主动互动任务参数错误: %+v", insert)
		}
		if !strings.HasPrefix(insert.IdempotencyKey, "ai_schedule:proactive:1:") {
			t.Fatalf("幂等键错误: %s", insert.IdempotencyKey)
		}
	})
}

func TestScanMentionTriggers(t *testing.T) {
	f := newFlowFixture()
	f.notices.mentions = []contracts.MentionRef{
		{ID: 11, MentionedUserID: 1, SourceType: domain.RefPost, SourceID: 5001},
		{ID: 12, MentionedUserID: 1, SourceType: domain.RefComment, SourceID: 5002},
	}
	rt := f.enableAI(1)
	f.svc.scanMentionTriggers(context.Background(), rt, f.clk.Now())
	if len(f.tasks.inserted) != 2 {
		t.Fatalf("应投递 2 条提及任务，实际 %+v", f.tasks.inserted)
	}
	if f.tasks.inserted[0].IdempotencyKey != "mention:11:1" || f.tasks.inserted[1].IdempotencyKey != "mention:12:1" {
		t.Fatalf("提及幂等键错误: %+v", f.tasks.inserted)
	}
	if f.tasks.inserted[0].TriggerType != domain.TriggerMention || f.tasks.inserted[0].ActionType != domain.ActionReply {
		t.Fatalf("提及任务类型错误: %+v", f.tasks.inserted[0])
	}
	if f.tasks.inserted[0].TriggerRefType != domain.RefPost || f.tasks.inserted[1].TriggerRefType != domain.RefComment {
		t.Fatalf("提及引用类型错误: %+v", f.tasks.inserted)
	}
	if len(f.notices.queries) != 1 || f.notices.queries[0].MentionedUserID != 1 ||
		f.notices.queries[0].Limit != schedulerTriggerLimit {
		t.Fatalf("提及扫描参数错误: %+v", f.notices.queries)
	}

	f.notices.err = errBoom
	f.svc.scanMentionTriggers(context.Background(), rt, f.clk.Now())
	if len(f.tasks.inserted) != 2 {
		t.Fatalf("扫描失败不应再投递: %+v", f.tasks.inserted)
	}
}

func TestScanCommentTriggers(t *testing.T) {
	f := newFlowFixture()
	f.social.newComments = []model.Comment{
		{ID: 77, PostID: 9, AuthorID: 5, Status: model.CommentStatusPublished},
	}
	rt := f.enableAI(1)
	f.svc.scanCommentTriggers(context.Background(), rt, f.clk.Now())
	if len(f.tasks.inserted) != 1 {
		t.Fatalf("应投递 1 条评论触发任务，实际 %+v", f.tasks.inserted)
	}
	insert := f.tasks.inserted[0]
	if insert.TriggerType != domain.TriggerComment || insert.ActionType != domain.ActionReply ||
		insert.TriggerRefType != domain.RefComment || insert.IdempotencyKey != "comment-trigger:77:1" {
		t.Fatalf("评论触发任务参数错误: %+v", insert)
	}

	f.social.newCommentsErr = errBoom
	f.svc.scanCommentTriggers(context.Background(), rt, f.clk.Now())
	if len(f.tasks.inserted) != 1 {
		t.Fatalf("扫描失败不应再投递: %+v", f.tasks.inserted)
	}
}

func TestRunSchedulerSkipsWhenGlobalDisabled(t *testing.T) {
	f := newFlowFixture()
	f.settings.fakeSettings.values = contracts.SystemSettings{AIGlobalEnabled: false}
	f.svc.RunScheduler(context.Background())
	if len(f.tasks.inserted) != 0 || len(f.jobs.enqueueCalls) != 0 {
		t.Fatalf("全局关闭时不应调度: %+v", f.tasks.inserted)
	}
}

func TestRunSchedulerStopsWhenSettingsUnavailable(t *testing.T) {
	f := newFlowFixture()
	f.settings.settingsErr = errBoom
	f.ai.enabledAIIDs = []int64{1}
	f.svc.RunScheduler(context.Background())
	if len(f.ai.recentLimit) != 0 {
		t.Fatalf("设置不可用时应提前返回: %+v", f.ai.recentLimit)
	}
}

func TestRunSchedulerSkipsWhenEnabledAIListFails(t *testing.T) {
	f := newFlowFixture()
	f.ai.enabledErr = errBoom
	f.svc.RunScheduler(context.Background())
	if len(f.tasks.inserted) != 0 {
		t.Fatalf("读取 AI 列表失败不应调度: %+v", f.tasks.inserted)
	}
}

func TestRunSchedulerSkipsRuntimeErrorAndContinues(t *testing.T) {
	f := newFlowFixture()
	f.ai.enabledAIIDs = []int64{1}
	f.ai.runtimeErr = apperr.NotFound("AI 不存在")
	f.svc.RunScheduler(context.Background())
	if len(f.tasks.inserted) != 0 {
		t.Fatalf("运行时读取失败应跳过该 AI: %+v", f.tasks.inserted)
	}
}

func TestRunSchedulerSchedulesAllTriggerTypes(t *testing.T) {
	f := newFlowFixture()
	rt := f.enableAI(1)
	rt.Behavior.CandidateScopeType = domain.ScopeGlobalPublic
	f.ai.runtime = rt
	f.ai.enabledAIIDs = []int64{1}
	f.notices.mentions = []contracts.MentionRef{{ID: 21, SourceType: domain.RefPost, SourceID: 3001}}
	f.social.newComments = []model.Comment{{ID: 22, PostID: 9, AuthorID: 5, Status: model.CommentStatusPublished}}

	f.svc.RunScheduler(context.Background())

	keys := make([]string, 0, len(f.tasks.inserted))
	for _, insert := range f.tasks.inserted {
		keys = append(keys, insert.IdempotencyKey)
	}
	joined := strings.Join(keys, ",")
	for _, want := range []string{"ai_schedule:post:1:", "ai_schedule:proactive:1:", "mention:21:1", "comment-trigger:22:1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("调度缺少 %q: %s", want, joined)
		}
	}
	if len(f.tasks.inserted) != 4 || len(f.jobs.enqueueCalls) != 4 {
		t.Fatalf("应投递 4 个任务/作业，实际 tasks=%d jobs=%d: %s",
			len(f.tasks.inserted), len(f.jobs.enqueueCalls), joined)
	}
}

func TestRunSchedulerContinuesAfterReconcileError(t *testing.T) {
	f := newFlowFixture()
	rt := f.enableAI(1)
	f.ai.runtime = rt
	f.ai.enabledAIIDs = []int64{1}
	f.tasks.pendingErr = errBoom
	f.svc.RunScheduler(context.Background())
	if len(f.tasks.inserted) == 0 {
		t.Fatal("reconcile 失败后仍应继续调度")
	}
}
