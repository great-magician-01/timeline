package app

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// fakeSettings 是 contracts.SettingsReader 的测试实现，只关心发帖限频配置。
type fakeSettings struct {
	values contracts.SystemSettings
	err    error
}

func (f fakeSettings) Settings(context.Context) (contracts.SystemSettings, error) {
	return f.values, f.err
}

func (f fakeSettings) WebSearch(context.Context) (contracts.WebSearchSettings, error) {
	return contracts.WebSearchSettings{}, nil
}

// fakeJobs 是 contracts.JobsQueue 的测试实现，记录 Enqueue/Reactivate/CancelPending 调用。
type fakeJobs struct {
	enqueued      []contracts.JobEnqueue
	reactivated   []reactivateCall
	canceled      []contracts.CancelPendingJobsInput
	reactivate    bool // Reactivate 返回的「是否重新激活」结果
	reactivateErr error
}

type reactivateCall struct {
	key   string
	runAt time.Time
	at    time.Time
}

func (f *fakeJobs) Enqueue(_ context.Context, in contracts.JobEnqueue) error {
	f.enqueued = append(f.enqueued, in)
	return nil
}

func (f *fakeJobs) CancelPending(_ context.Context, in contracts.CancelPendingJobsInput) error {
	f.canceled = append(f.canceled, in)
	return nil
}

func (f *fakeJobs) PausePending(context.Context, contracts.PausePendingJobsInput) (int, error) {
	return 0, nil
}

func (f *fakeJobs) PauseAllPending(context.Context, contracts.PauseAllPendingInput) (int, error) {
	return 0, nil
}

func (f *fakeJobs) RetryJob(context.Context, contracts.JobActionInput) (contracts.JobSnapshot, error) {
	return contracts.JobSnapshot{}, nil
}

func (f *fakeJobs) CancelJob(context.Context, contracts.JobActionInput) (contracts.JobSnapshot, error) {
	return contracts.JobSnapshot{}, nil
}

func (f *fakeJobs) SetJobStatus(context.Context, int64, string, time.Time) error { return nil }

func (f *fakeJobs) GetJob(context.Context, int64) (contracts.JobSnapshot, error) {
	return contracts.JobSnapshot{}, nil
}

func (f *fakeJobs) ListByRef(context.Context, contracts.JobRefQuery) ([]contracts.JobSnapshot, error) {
	return nil, nil
}

func (f *fakeJobs) Reactivate(_ context.Context, idempotencyKey string, runAt, at time.Time) (bool, error) {
	f.reactivated = append(f.reactivated, reactivateCall{key: idempotencyKey, runAt: runAt, at: at})
	return f.reactivate, f.reactivateErr
}

// 编译期断言：fakeJobs 必须实现完整的作业端口。
var _ contracts.JobsQueue = (*fakeJobs)(nil)

func publishJobKey(postID int64, at time.Time) string {
	return "post_publish:" + strconv.FormatInt(postID, 10) + ":" + strconv.FormatInt(at.Unix(), 10)
}

func newTestService(t *testing.T, deps Deps) *Service {
	t.Helper()
	if deps.Clock == nil {
		deps.Clock = clock.NewFake()
	}
	return New(deps)
}

// TestNewInstallsDefaultPostLimiter 防回归：组合根未注入限频器时 New 必须按默认阈值兜底创建，
// 否则发帖限频会静默失效（本次审查发现的 P0）。
func TestNewInstallsDefaultPostLimiter(t *testing.T) {
	svc := newTestService(t, Deps{})
	if svc.deps.PostLimiter == nil {
		t.Fatal("New 必须兜底创建发帖限频器")
	}
	// 默认阈值 30 次/10 分钟：前 30 次放行，第 31 次返回 rate_limited。
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		if err := svc.consumePostQuota(ctx, 1); err != nil {
			t.Fatalf("第 %d 次发帖不应被限频: %v", i+1, err)
		}
	}
	err := svc.consumePostQuota(ctx, 1)
	if !apperr.IsKind(err, apperr.KindRateLimited) {
		t.Fatalf("超过默认阈值应返回 rate_limited，实际 %v", err)
	}
	if e, ok := apperr.AsKind(err); !ok || e.RetryAfter <= 0 {
		t.Fatalf("限频错误必须带正的 RetryAfter，实际 %+v", err)
	}
}

// TestConsumePostQuotaUsesSettings 校验阈值/窗口每次从系统设置读取并即时生效。
func TestConsumePostQuotaUsesSettings(t *testing.T) {
	svc := newTestService(t, Deps{
		Settings: fakeSettings{values: contracts.SystemSettings{
			PostRateLimit:  2,
			PostRateWindow: time.Minute,
		}},
	})
	ctx := context.Background()
	if err := svc.consumePostQuota(ctx, 7); err != nil {
		t.Fatalf("第 1 次发帖应放行: %v", err)
	}
	if err := svc.consumePostQuota(ctx, 7); err != nil {
		t.Fatalf("第 2 次发帖应放行: %v", err)
	}
	err := svc.consumePostQuota(ctx, 7)
	if !apperr.IsKind(err, apperr.KindRateLimited) {
		t.Fatalf("第 3 次发帖应返回 rate_limited，实际 %v", err)
	}
	e, ok := apperr.AsKind(err)
	if !ok || e.RetryAfter <= 0 {
		t.Fatalf("限频错误必须带正的 RetryAfter，实际 %+v", err)
	}
	// 不同用户的桶互相独立。
	if err := svc.consumePostQuota(ctx, 8); err != nil {
		t.Fatalf("其他用户不应共享限频桶: %v", err)
	}
}

// TestEnqueuePublishJobReactivates 校验「取消后重新设置同一时间」路径会调用 Reactivate，
// 且幂等键与 runAt/事务 now 保持一致；未被重新激活时按幂等成功处理、不报错。
func TestEnqueuePublishJobReactivates(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai)
	runAt := now.Add(2 * time.Hour)
	jobs := &fakeJobs{reactivate: true}
	svc := New(Deps{Clock: clock.NewFake(now), Jobs: jobs})

	if err := svc.enqueuePublishJob(context.Background(), 42, runAt, now); err != nil {
		t.Fatalf("入队定时发布作业失败: %v", err)
	}
	if len(jobs.enqueued) != 1 {
		t.Fatalf("应入队 1 条作业，实际 %d", len(jobs.enqueued))
	}
	wantKey := publishJobKey(42, runAt)
	enqueued := jobs.enqueued[0]
	if enqueued.IdempotencyKey != wantKey || !enqueued.RunAt.Equal(runAt) ||
		enqueued.Type != contracts.JobTypePostPublish || enqueued.RefID == nil || *enqueued.RefID != 42 {
		t.Fatalf("入队参数异常: %+v（期望 key=%s）", enqueued, wantKey)
	}
	if len(jobs.reactivated) != 1 {
		t.Fatalf("设置定时后必须调用一次 Reactivate，实际 %d 次", len(jobs.reactivated))
	}
	call := jobs.reactivated[0]
	if call.key != wantKey || !call.runAt.Equal(runAt) || !call.at.Equal(now) {
		t.Fatalf("Reactivate 参数异常: %+v（期望 key=%s runAt=%v at=%v）", call, wantKey, runAt, now)
	}

	// 幂等键已存在且作业仍 pending 时 Reactivate 返回 false，不应视为错误。
	jobs = &fakeJobs{reactivate: false}
	svc = New(Deps{Clock: clock.NewFake(now), Jobs: jobs})
	if err := svc.enqueuePublishJob(context.Background(), 42, runAt, now); err != nil {
		t.Fatalf("未被重新激活（幂等命中）不应报错: %v", err)
	}
	if len(jobs.reactivated) != 1 {
		t.Fatalf("幂等命中路径也应调用 Reactivate，实际 %d 次", len(jobs.reactivated))
	}
}

// TestCancelPublishJobsUsesGivenTime 校验取消作业使用调用方传入的事务 now，
// 不再在内部二次取时（一次事务一个 now）。
func TestCancelPublishJobsUsesGivenTime(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai)
	jobs := &fakeJobs{}
	svc := New(Deps{Clock: clock.NewFake(now.Add(time.Hour)), Jobs: jobs})
	if err := svc.cancelPublishJobs(context.Background(), 9, now); err != nil {
		t.Fatalf("取消定时作业失败: %v", err)
	}
	if len(jobs.canceled) != 1 || !jobs.canceled[0].At.Equal(now) {
		t.Fatalf("CancelPending 必须使用传入的 now: %+v", jobs.canceled)
	}
}

// TestCollectLatestPublishJobs 校验按 ref 取最新有效作业、忽略终态作业。
func TestCollectLatestPublishJobs(t *testing.T) {
	refA, refB := int64(1), int64(2)
	snapshots := []contracts.JobSnapshot{
		{ID: 10, RefID: &refA, Status: contracts.JobStatusCanceled},
		{ID: 9, RefID: &refA, Status: contracts.JobStatusFailed, LastError: "旧失败"},
		{ID: 12, RefID: &refA, Status: contracts.JobStatusPending},
		{ID: 11, RefID: &refB, Status: contracts.JobStatusPaused},
		{ID: 13, RefID: &refB, Status: contracts.JobStatusSucceeded},
	}
	result := map[int64]contracts.JobSnapshot{}
	collectLatestPublishJobs(snapshots, result)
	if got := result[refA]; got.ID != 12 || got.Status != contracts.JobStatusPending {
		t.Fatalf("refA 应取最新 pending 作业，实际 %+v", got)
	}
	if got := result[refB]; got.ID != 11 || got.Status != contracts.JobStatusPaused {
		t.Fatalf("refB 应取最新 paused 作业，实际 %+v", got)
	}
}

// TestActiveDraftAttachmentIDsEmptyInput 校验空入参不访问数据库、返回空结果。
func TestActiveDraftAttachmentIDsEmptyInput(t *testing.T) {
	svc := New(Deps{})
	ids, err := svc.ActiveDraftAttachmentIDs(context.Background(), nil)
	if err != nil || len(ids) != 0 {
		t.Fatalf("空入参应返回空结果: %v, %v", ids, err)
	}
}
