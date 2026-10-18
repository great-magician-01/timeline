package app

// service.go 用例测试：入队、作业入口、调用记录清理、动作计数/限额、全局开关与
// 模型调用记录工具。全部使用 fake 端口，不依赖数据库、网络与真实 sleep。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/callable"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/jobs"
	"github.com/great-magician-01/timeline/internal/platform/settings"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestNewFillsDefaultClockAndLogger(t *testing.T) {
	svc := New(Deps{})
	if svc.deps.Clock == nil {
		t.Fatal("nil Clock 应回退系统时钟")
	}
	if svc.deps.Log == nil {
		t.Fatal("nil Log 应回退默认 logger")
	}
}

func TestNewIDRejectsGeneratorFailureAndNonPositiveID(t *testing.T) {
	cases := []struct {
		name string
		ids  IDGenerator
	}{
		{"生成器返回错误", &stuckIDs{err: errBoom}},
		{"生成器返回零", &stuckIDs{value: 0}},
		{"生成器返回负数", &stuckIDs{value: -3}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			svc := New(Deps{IDs: testCase.ids})
			id, err := svc.newID()
			if id != 0 {
				t.Fatalf("失败时不应返回 ID，实际 %d", id)
			}
			if !apperr.IsKind(err, apperr.KindInternal) {
				t.Fatalf("期望 Internal 错误，实际 %v", err)
			}
		})
	}
}

func TestEnqueueValidation(t *testing.T) {
	f := newFlowFixture()
	now := f.clk.Now()
	past := now.Add(-time.Minute)
	cases := []struct {
		name string
		in   EnqueueInput
	}{
		{"缺少 AI 账号", EnqueueInput{TriggerType: domain.TriggerManual, ActionType: domain.ActionOriginalPost, IdempotencyKey: "k"}},
		{"缺少幂等键", EnqueueInput{AID: 1, TriggerType: domain.TriggerManual, ActionType: domain.ActionOriginalPost}},
		{"触发类型无效", EnqueueInput{AID: 1, TriggerType: "unknown", ActionType: domain.ActionOriginalPost, IdempotencyKey: "k"}},
		{"动作类型无效", EnqueueInput{AID: 1, TriggerType: domain.TriggerManual, ActionType: "unknown", IdempotencyKey: "k"}},
		{"定时时间早于当前", EnqueueInput{
			AID: 1, TriggerType: domain.TriggerManual, ActionType: domain.ActionOriginalPost,
			IdempotencyKey: "k", ScheduledAt: &past,
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := f.svc.Enqueue(context.Background(), testCase.in)
			if !apperr.IsKind(err, apperr.KindValidation) {
				t.Fatalf("期望 Validation 错误，实际 %v", err)
			}
		})
	}
	if len(f.tasks.inserted) != 0 {
		t.Fatalf("校验失败不应写入任务: %+v", f.tasks.inserted)
	}
}

func TestEnqueueCreatesTaskAndJob(t *testing.T) {
	f := newFlowFixture()
	now := f.clk.Now()
	scheduled := now.Add(2 * time.Hour)
	aiID := int64(1)
	taskID, enqueued, err := f.svc.Enqueue(context.Background(), EnqueueInput{
		AID:            aiID,
		TriggerType:    domain.TriggerSchedule,
		TriggerRefType: domain.RefAI,
		TriggerRefID:   &aiID,
		ActionType:     domain.ActionOriginalPost,
		IdempotencyKey: "ai_schedule:post:1:42",
		ScheduledAt:    &scheduled,
	})
	if err != nil {
		t.Fatalf("入队不应失败: %v", err)
	}
	if !enqueued || taskID != 1001 {
		t.Fatalf("期望写入任务 1001 并投递作业，实际 task=%d enqueued=%v", taskID, enqueued)
	}
	if len(f.tasks.inserted) != 1 {
		t.Fatalf("应写入 1 条任务，实际 %d", len(f.tasks.inserted))
	}
	insert := f.tasks.inserted[0]
	if insert.AIID != aiID || insert.TriggerType != domain.TriggerSchedule ||
		insert.ActionType != domain.ActionOriginalPost ||
		!insert.CreatedAt.Equal(now) || insert.ScheduledAt == nil || !insert.ScheduledAt.Equal(scheduled) {
		t.Fatalf("任务写入参数错误: %+v", insert)
	}
	if len(f.jobs.enqueueCalls) != 1 {
		t.Fatalf("应投递 1 个作业，实际 %d", len(f.jobs.enqueueCalls))
	}
	enqueue := f.jobs.enqueueCalls[0]
	if enqueue.Type != contracts.JobTypeAITask || enqueue.RefType != domain.RefTask ||
		enqueue.IdempotencyKey != "ai_task:1001" || enqueue.MaxAttempts != 3 {
		t.Fatalf("作业参数错误: %+v", enqueue)
	}
	if enqueue.RefID == nil || *enqueue.RefID != taskID {
		t.Fatalf("作业引用任务 ID 错误: %+v", enqueue.RefID)
	}
	if !enqueue.RunAt.Equal(scheduled) || !enqueue.At.Equal(now) {
		t.Fatalf("定时任务作业计划时间应为 ScheduledAt: runAt=%v at=%v", enqueue.RunAt, enqueue.At)
	}
	payload, ok := enqueue.Payload.(jobPayload)
	if !ok || payload.TaskID != taskID {
		t.Fatalf("作业负载应包含任务 ID: %+v", enqueue.Payload)
	}
}

func TestEnqueueImmediateRunAtIsNow(t *testing.T) {
	f := newFlowFixture()
	now := f.clk.Now()
	if _, _, err := f.svc.Enqueue(context.Background(), EnqueueInput{
		AID: 1, TriggerType: domain.TriggerMention, ActionType: domain.ActionReply, IdempotencyKey: "k",
	}); err != nil {
		t.Fatalf("入队不应失败: %v", err)
	}
	if len(f.jobs.enqueueCalls) != 1 || !f.jobs.enqueueCalls[0].RunAt.Equal(now) {
		t.Fatalf("立即执行任务的 RunAt 应为当前时间: %+v", f.jobs.enqueueCalls)
	}
}

func TestEnqueueIdempotentDuplicateSkipsJob(t *testing.T) {
	f := newFlowFixture()
	f.tasks.duplicate = true
	taskID, enqueued, err := f.svc.Enqueue(context.Background(), EnqueueInput{
		AID: 1, TriggerType: domain.TriggerManual, ActionType: domain.ActionOriginalPost, IdempotencyKey: "k",
	})
	if err != nil {
		t.Fatalf("幂等命中不应返回错误: %v", err)
	}
	if taskID != 0 || enqueued {
		t.Fatalf("幂等命中应返回 (0,false)，实际 (%d,%v)", taskID, enqueued)
	}
	if len(f.jobs.enqueueCalls) != 0 {
		t.Fatalf("幂等命中不应投递作业: %+v", f.jobs.enqueueCalls)
	}
}

func TestEnqueuePropagatesInfrastructureErrors(t *testing.T) {
	t.Run("生成 ID 失败", func(t *testing.T) {
		f := newFlowFixture()
		f.svc = New(Deps{Clock: f.clk, Log: testLogger(), IDs: &stuckIDs{err: errBoom}, Tasks: f.tasks, Jobs: f.jobs})
		_, _, err := f.svc.Enqueue(context.Background(), EnqueueInput{
			AID: 1, TriggerType: domain.TriggerManual, ActionType: domain.ActionOriginalPost, IdempotencyKey: "k",
		})
		if !apperr.IsKind(err, apperr.KindInternal) || len(f.tasks.inserted) != 0 {
			t.Fatalf("生成 ID 失败应直接返回 Internal: err=%v inserted=%+v", err, f.tasks.inserted)
		}
	})
	t.Run("写入任务失败", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.insertErr = errBoom
		_, enqueued, err := f.svc.Enqueue(context.Background(), EnqueueInput{
			AID: 1, TriggerType: domain.TriggerManual, ActionType: domain.ActionOriginalPost, IdempotencyKey: "k",
		})
		if !errors.Is(err, errBoom) || enqueued {
			t.Fatalf("应透传仓储错误: err=%v enqueued=%v", err, enqueued)
		}
	})
	t.Run("任务已写入但读取失败", func(t *testing.T) {
		f := newFlowFixture()
		f.tasks.getTaskErr = errBoom
		taskID, enqueued, err := f.svc.Enqueue(context.Background(), EnqueueInput{
			AID: 1, TriggerType: domain.TriggerManual, ActionType: domain.ActionOriginalPost, IdempotencyKey: "k",
		})
		if !errors.Is(err, errBoom) || enqueued || taskID != 1001 {
			t.Fatalf("读取任务失败应返回已写入的任务 ID 与错误: task=%d enqueued=%v err=%v", taskID, enqueued, err)
		}
	})
	t.Run("投递作业失败", func(t *testing.T) {
		f := newFlowFixture()
		f.jobs.enqueueErr = errBoom
		taskID, enqueued, err := f.svc.Enqueue(context.Background(), EnqueueInput{
			AID: 1, TriggerType: domain.TriggerManual, ActionType: domain.ActionOriginalPost, IdempotencyKey: "k",
		})
		if !errors.Is(err, errBoom) || enqueued || taskID != 1001 {
			t.Fatalf("投递作业失败应返回错误且不标记入队: task=%d enqueued=%v err=%v", taskID, enqueued, err)
		}
	})
}

func TestHandleAITaskJobInvalidPayloadIgnored(t *testing.T) {
	f := newFlowFixture()
	cases := [][]byte{
		nil,
		[]byte("{invalid"),
		[]byte(`{"ai_id":9}`),
		[]byte(`{"task_id":0}`),
	}
	for _, payload := range cases {
		if err := f.svc.HandleAITaskJob(context.Background(), jobs.Job{ID: 1, Payload: payload}); err != nil {
			t.Fatalf("无效负载应视为处理完成，payload=%s err=%v", payload, err)
		}
	}
	if len(f.tasks.claims) != 0 {
		t.Fatalf("无效负载不应领取任务: %+v", f.tasks.claims)
	}
}

func TestHandleAITaskJobRunsTask(t *testing.T) {
	f := newFlowFixture()
	f.tasks.claimed = true
	f.tasks.claimAttempts = 1
	f.tasks.tasks[101] = repo.Task{ID: 101, AIID: 1}
	f.users.users[1] = model.User{ID: 1, Username: "ai", Status: model.StatusActive, IsAI: true}
	f.settings.fakeSettings.values = contracts.SystemSettings{AIGlobalEnabled: false}
	if err := f.svc.HandleAITaskJob(context.Background(), jobs.Job{ID: 1, Payload: []byte(`{"task_id":101}`)}); err != nil {
		t.Fatalf("作业执行不应失败: %v", err)
	}
	if len(f.tasks.claims) != 1 || f.tasks.claims[0] != 101 {
		t.Fatalf("应领取负载中的任务 101，实际 %+v", f.tasks.claims)
	}
}

func TestHandleMemoryJob(t *testing.T) {
	f := newFlowFixture()
	cases := [][]byte{nil, []byte("{invalid"), []byte(`{"task_id":1}`)}
	for _, payload := range cases {
		if err := f.svc.HandleMemoryJob(context.Background(), jobs.Job{ID: 2, Payload: payload}); err != nil {
			t.Fatalf("无效负载应忽略: payload=%s err=%v", payload, err)
		}
	}
	if len(f.ai.recentLimit) != 0 {
		t.Fatalf("无效负载不应触发沉淀读取: %+v", f.ai.recentLimit)
	}
	if err := f.svc.HandleMemoryJob(context.Background(), jobs.Job{ID: 2, Payload: []byte(`{"ai_id":7}`)}); err != nil {
		t.Fatalf("记忆沉淀作业不应失败: %v", err)
	}
	if len(f.ai.recentLimit) != 1 || f.ai.recentLimit[0] != consolidationThreshold {
		t.Fatalf("应按沉淀阈值读取近期记忆: %+v", f.ai.recentLimit)
	}
}

func TestRunCallLogCleanup(t *testing.T) {
	t.Run("设置读取失败使用默认保留期", func(t *testing.T) {
		f := newFlowFixture()
		f.settings.settingsErr = errBoom
		f.callLogs.markCounts = []int{2}
		now := f.clk.Now()
		f.svc.RunCallLogCleanup(context.Background())
		if len(f.callLogs.cutoffs) != 1 {
			t.Fatalf("默认保留期仍应清理一次，实际 %d", len(f.callLogs.cutoffs))
		}
		want := now.Add(-time.Duration(settings.DefaultCallLogRetentionDays) * 24 * time.Hour)
		if !f.callLogs.cutoffs[0].Equal(want) {
			t.Fatalf("清理截止时间错误：期望 %v 实际 %v", want, f.callLogs.cutoffs[0])
		}
	})
	t.Run("保留期为零直接跳过", func(t *testing.T) {
		f := newFlowFixture()
		f.settings.fakeSettings.values = contracts.SystemSettings{CallLogRetentionDays: 0}
		f.svc.RunCallLogCleanup(context.Background())
		if len(f.callLogs.cutoffs) != 0 {
			t.Fatalf("保留期为零不应清理: %+v", f.callLogs.cutoffs)
		}
	})
	t.Run("按批循环直到不足一批", func(t *testing.T) {
		f := newFlowFixture()
		f.settings.fakeSettings.values = contracts.SystemSettings{CallLogRetentionDays: 7}
		f.callLogs.markCounts = []int{200, 3}
		now := f.clk.Now()
		f.svc.RunCallLogCleanup(context.Background())
		if len(f.callLogs.cutoffs) != 2 {
			t.Fatalf("满批应继续清理，实际调用 %d 次", len(f.callLogs.cutoffs))
		}
		want := now.Add(-7 * 24 * time.Hour)
		for _, cutoff := range f.callLogs.cutoffs {
			if !cutoff.Equal(want) {
				t.Fatalf("截止时间错误: %v", cutoff)
			}
		}
	})
	t.Run("清理出错立即停止", func(t *testing.T) {
		f := newFlowFixture()
		f.settings.fakeSettings.values = contracts.SystemSettings{CallLogRetentionDays: 7}
		f.callLogs.markErr = errBoom
		f.svc.RunCallLogCleanup(context.Background())
		if len(f.callLogs.cutoffs) != 1 {
			t.Fatalf("清理出错应停止，实际调用 %d 次", len(f.callLogs.cutoffs))
		}
	})
}

func TestBumpCounter(t *testing.T) {
	f := newFlowFixture()
	now := f.clk.Now()
	if err := f.svc.bumpCounter(context.Background(), 5, domain.ActionLike, now); err != nil {
		t.Fatalf("计数不应失败: %v", err)
	}
	if len(f.counters.bumps) != 1 {
		t.Fatalf("应累加一次计数，实际 %d", len(f.counters.bumps))
	}
	bump := f.counters.bumps[0]
	if bump.id != 1001 || bump.aiID != 5 || bump.action != domain.ActionLike {
		t.Fatalf("计数参数错误: %+v", bump)
	}
	if !bump.dayStart.Equal(clock.DayStart(now)) || !bump.at.Equal(now) {
		t.Fatalf("计数的日界/时间错误: %+v", bump)
	}

	f.svc = New(Deps{IDs: &stuckIDs{err: errBoom}, Counters: f.counters})
	if err := f.svc.bumpCounter(context.Background(), 5, domain.ActionLike, now); !apperr.IsKind(err, apperr.KindInternal) {
		t.Fatalf("生成计数 ID 失败应返回 Internal: %v", err)
	}
}

func TestLimitReached(t *testing.T) {
	now := testClock().Now()
	rateLimits := func(raw string) json.RawMessage { return json.RawMessage(raw) }
	cases := []struct {
		name     string
		behavior contracts.BehaviorSettings
		counts   map[string]int
		totals   map[int64]int
		last     map[string]*time.Time
		trigger  string
		wantSkip string
		wantErr  bool
	}{
		{
			name:     "动作日上限已到",
			behavior: contracts.BehaviorSettings{RateLimits: rateLimits(`{"daily_like":3}`)},
			counts:   map[string]int{domain.ActionLike: 3},
			wantSkip: "已达到该动作的每日上限",
		},
		{
			name:     "动作日上限未到",
			behavior: contracts.BehaviorSettings{RateLimits: rateLimits(`{"daily_like":3}`)},
			counts:   map[string]int{domain.ActionLike: 2},
		},
		{
			name:     "每日总上限已到",
			behavior: contracts.BehaviorSettings{RateLimits: rateLimits(`{"daily_total":2}`)},
			totals:   map[int64]int{1: 2},
			wantSkip: "已达到每日动作总上限",
		},
		{
			name:     "冷却时间未到",
			behavior: contracts.BehaviorSettings{CooldownSeconds: 300},
			last:     map[string]*time.Time{domain.TriggerMention: timePtr(now.Add(-time.Minute))},
			trigger:  domain.TriggerMention,
			wantSkip: "冷却时间未到",
		},
		{
			name:     "retry 触发跳过冷却",
			behavior: contracts.BehaviorSettings{CooldownSeconds: 300},
			last:     map[string]*time.Time{domain.TriggerRetry: timePtr(now.Add(-time.Minute))},
			trigger:  domain.TriggerRetry,
		},
		{
			name:     "冷却已过",
			behavior: contracts.BehaviorSettings{CooldownSeconds: 60},
			last:     map[string]*time.Time{domain.TriggerMention: timePtr(now.Add(-2 * time.Minute))},
			trigger:  domain.TriggerMention,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := newFlowFixture()
			f.counters.actionCounts = testCase.counts
			f.counters.totals = testCase.totals
			f.counters.last = testCase.last
			trigger := testCase.trigger
			if trigger == "" {
				trigger = domain.TriggerProactive
			}
			skip, err := f.svc.limitReached(context.Background(), 1, testCase.behavior, trigger, domain.ActionLike, now)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("期望错误")
				}
				return
			}
			if err != nil {
				t.Fatalf("限额判断不应失败: %v", err)
			}
			if skip != testCase.wantSkip {
				t.Fatalf("期望 skip=%q，实际 %q", testCase.wantSkip, skip)
			}
		})
	}

	t.Run("计数读取失败", func(t *testing.T) {
		f := newFlowFixture()
		f.counters.countErr = errBoom
		behavior := contracts.BehaviorSettings{RateLimits: rateLimits(`{"daily_like":3}`)}
		if _, err := f.svc.limitReached(context.Background(), 1, behavior, domain.TriggerProactive, domain.ActionLike, now); !errors.Is(err, errBoom) {
			t.Fatalf("应透传计数错误: %v", err)
		}
	})
	t.Run("总计数读取失败", func(t *testing.T) {
		f := newFlowFixture()
		f.counters.totalErr = errBoom
		behavior := contracts.BehaviorSettings{RateLimits: rateLimits(`{"daily_total":3}`)}
		if _, err := f.svc.limitReached(context.Background(), 1, behavior, domain.TriggerProactive, domain.ActionLike, now); !errors.Is(err, errBoom) {
			t.Fatalf("应透传总计数错误: %v", err)
		}
	})
	t.Run("最近成功时间读取失败", func(t *testing.T) {
		f := newFlowFixture()
		f.counters.lastErr = errBoom
		behavior := contracts.BehaviorSettings{CooldownSeconds: 60}
		if _, err := f.svc.limitReached(context.Background(), 1, behavior, domain.TriggerMention, domain.ActionLike, now); !errors.Is(err, errBoom) {
			t.Fatalf("应透传最近成功时间错误: %v", err)
		}
	})
}

func TestCancelPendingTasksForActor(t *testing.T) {
	f := newFlowFixture()
	if _, err := f.svc.CancelPendingTasksForActor(context.Background(), contracts.CancelTasksInput{}); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("缺少 AI 账号应返回 Validation: %v", err)
	}

	at := f.clk.Now().Add(time.Minute)
	f.tasks.pending = []repo.Task{{ID: 1, AIID: 3}, {ID: 2, AIID: 3}, {ID: 3, AIID: 4}}
	canceled, err := f.svc.CancelPendingTasksForActor(context.Background(), contracts.CancelTasksInput{AIID: 3, At: at})
	if err != nil {
		t.Fatalf("取消任务不应失败: %v", err)
	}
	if canceled != 2 {
		t.Fatalf("应取消 2 条任务，实际 %d", canceled)
	}
	if len(f.tasks.statuses) != 2 {
		t.Fatalf("应写回 2 条任务状态，实际 %+v", f.tasks.statuses)
	}
	for _, record := range f.tasks.statuses {
		if record.status != domain.TaskStatusCanceled || record.errMsg != "账号处置取消" {
			t.Fatalf("取消状态/原因错误: %+v", record)
		}
		if record.at == nil || !record.at.Equal(at) {
			t.Fatalf("取消时间应使用输入时间: %+v", record.at)
		}
	}

	f.tasks.listErr = errBoom
	if _, err := f.svc.CancelPendingTasksForActor(context.Background(), contracts.CancelTasksInput{AIID: 3}); !errors.Is(err, errBoom) {
		t.Fatalf("应透传列表错误: %v", err)
	}

	f = newFlowFixture()
	f.tasks.pending = []repo.Task{{ID: 9, AIID: 3}}
	f.tasks.statusErrs = errBoom
	if _, err := f.svc.CancelPendingTasksForActor(context.Background(), contracts.CancelTasksInput{AIID: 3}); !errors.Is(err, errBoom) {
		t.Fatalf("应透传状态写入错误: %v", err)
	}
}

func TestGlobalState(t *testing.T) {
	f := newFlowFixture()
	state, err := f.svc.GlobalState(context.Background())
	if err != nil || !state.Enabled {
		t.Fatalf("全局开关应为开启: state=%+v err=%v", state, err)
	}
	f.settings.settingsErr = errBoom
	if _, err := f.svc.GlobalState(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("应透传设置读取错误: %v", err)
	}
}

func TestSetGlobalEnableWritesSettingAndAudit(t *testing.T) {
	f := newFlowFixture()
	at := f.clk.Now()
	if err := f.svc.SetGlobal(context.Background(), 100, true); err != nil {
		t.Fatalf("打开全局开关不应失败: %v", err)
	}
	if len(f.settings.setCalls) != 1 {
		t.Fatalf("应写入 1 次设置，实际 %d", len(f.settings.setCalls))
	}
	write := f.settings.setCalls[0]
	if write.Key != contracts.SettingKeyAIGlobalEnabled || write.Value != true || write.AdminID != 100 || !write.At.Equal(at) {
		t.Fatalf("设置写入参数错误: %+v", write)
	}
	if len(f.jobs.enqueued) != 0 || len(f.jobs.pausedAll) != 0 || len(f.tasks.statuses) != 0 {
		t.Fatalf("打开开关不应暂停/取消任何作业或任务: jobs=%+v statuses=%+v", f.jobs.pausedAll, f.tasks.statuses)
	}
	if len(f.audit.entries) != 1 {
		t.Fatalf("应写 1 条审计，实际 %d", len(f.audit.entries))
	}
	entry := f.audit.entries[0]
	detail, _ := entry.Detail.(map[string]any)
	if entry.Action != contracts.AuditActionGlobalAIStart || entry.AdminID != 100 ||
		entry.TargetType != "system_settings" || detail["enabled"] != true {
		t.Fatalf("审计参数错误: %+v", entry)
	}
}

func TestSetGlobalErrors(t *testing.T) {
	f := newFlowFixture()
	f.settings.setErr = errBoom
	if err := f.svc.SetGlobal(context.Background(), 100, true); !errors.Is(err, errBoom) {
		t.Fatalf("设置写入失败应透传: %v", err)
	}

	f = newFlowFixture()
	f.audit.err = errBoom
	if err := f.svc.SetGlobal(context.Background(), 100, true); !errors.Is(err, errBoom) {
		t.Fatalf("审计失败应透传: %v", err)
	}
}

func TestSetGlobalDisableCancelsTasksAndPausesJobs(t *testing.T) {
	f := newFlowFixture()
	f.tasks.pending = []repo.Task{{ID: 1, AIID: 8}, {ID: 2, AIID: 9}}
	if err := f.svc.SetGlobal(context.Background(), 100, false); err != nil {
		t.Fatalf("关闭全局开关不应失败: %v", err)
	}
	if len(f.tasks.statuses) != 2 {
		t.Fatalf("应取消 2 条 pending 任务，实际 %+v", f.tasks.statuses)
	}
	if len(f.jobs.pausedAll) != 2 {
		t.Fatalf("应暂停两类作业，实际 %+v", f.jobs.pausedAll)
	}
	if len(f.audit.entries) != 1 || f.audit.entries[0].Action != contracts.AuditActionGlobalAIStop {
		t.Fatalf("应写全局停止审计: %+v", f.audit.entries)
	}
}

func TestCallModelRoundRejectsNilRequest(t *testing.T) {
	svc := New(Deps{Log: testLogger()})
	if _, err := svc.callModelRound(context.Background(), contracts.Runtime{}, 1, nil, 1, false); !apperr.IsKind(err, apperr.KindInternal) {
		t.Fatalf("空请求应返回 Internal: %v", err)
	}
}

func TestRenderRequestAndResponseContent(t *testing.T) {
	if renderRequestContent(nil) != "" {
		t.Fatal("nil 请求应渲染为空串")
	}
	message := callable.Message{Role: callable.RoleAssistant}
	message.Parts = append(message.Parts,
		callable.TextPart{Text: "正文"},
		callable.ToolCallPart{ID: "call_1", Name: "web_search", Arguments: `{"query":"x"}`},
		callable.ToolResultPart{ToolCallID: "call_1", Name: "web_search", Content: "结果", IsError: false},
	)
	rendered := renderRequestContent(callable.NewRequest(message))
	for _, want := range []string{"[assistant]", "正文", "[tool_call name=web_search]", `{"query":"x"}`, "[tool_result name=web_search]", "结果"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("请求渲染缺少 %q: %s", want, rendered)
		}
	}

	if renderResponseContent(nil) != "" {
		t.Fatal("nil 响应应渲染为空串")
	}
	resp := &callable.Response{
		Text:      "模型文本",
		ToolCalls: []callable.ToolCall{{ID: "1", Name: "web_search", Arguments: `{"query":"y"}`}},
	}
	rendered = renderResponseContent(resp)
	if !strings.Contains(rendered, "模型文本") || !strings.Contains(rendered, `[tool_call name=web_search] {"query":"y"}`) {
		t.Fatalf("响应渲染错误: %s", rendered)
	}
}

func TestPtrHelpers(t *testing.T) {
	if ptrInt(0) != nil || ptrInt(-1) != nil || *ptrInt(3) != 3 {
		t.Fatal("ptrInt 应丢弃非正数")
	}
	if ptrInt64(0) != nil || *ptrInt64(7) != 7 {
		t.Fatal("ptrInt64 应丢弃非正数")
	}
	if derefString(nil) != "" || derefString(strPtr("abc")) != "abc" {
		t.Fatal("derefString 解引用错误")
	}
}

func timePtr(value time.Time) *time.Time { return &value }
