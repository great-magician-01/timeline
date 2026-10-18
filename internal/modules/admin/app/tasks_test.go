package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// newTaskTestEnv 在基础用例环境上装配作业端口假实现。
func newTaskTestEnv(t *testing.T) (*adminTestEnv, *fakeJobs) {
	t.Helper()
	env := newAdminTestEnv(t)
	jobs := &fakeJobs{
		job: contracts.JobSnapshot{
			ID:          42,
			Type:        contracts.JobTypePostPublish,
			RefType:     "post",
			Status:      contracts.JobStatusFailed,
			Attempts:    2,
			MaxAttempts: 3,
			LastError:   "boom",
		},
	}
	env.service.jobs = jobs
	return env, jobs
}

// ===== 人工重试任务 =====

func TestRetryTaskSucceedsAndWritesAudit(t *testing.T) {
	env, jobs := newTaskTestEnv(t)
	env.clock.Advance(90 * time.Second)
	now := env.clock.Now()

	snapshot, err := env.service.RetryTask(context.Background(), adminActor(), 42)
	if err != nil {
		t.Fatalf("重试作业失败: %v", err)
	}
	if snapshot.ID != 42 || snapshot.Status != contracts.JobStatusPending {
		t.Fatalf("返回快照不符: %+v", snapshot)
	}
	// 端口参数必须带应用时钟时间，且只被调用一次。
	if jobs.getCalls != 1 {
		t.Fatalf("GetJob 调用次数 = %d，期望 1", jobs.getCalls)
	}
	if len(jobs.retried) != 1 || jobs.retried[0].JobID != 42 || !jobs.retried[0].At.Equal(now) {
		t.Fatalf("RetryJob 入参不符: %+v", jobs.retried)
	}

	entries := env.audit.entries
	if len(entries) != 1 || entries[0].Action != contracts.AuditActionTaskRetry {
		t.Fatalf("应写一条 task_retry 审计，实际: %v", env.audit.actions())
	}
	entry := entries[0]
	if entry.AdminID != adminActor().ID || entry.TargetType != "background_job" ||
		entry.TargetID == nil || *entry.TargetID != 42 || !entry.At.Equal(now) {
		t.Fatalf("审计主体/目标/时间不符: %+v", entry)
	}
	detail := detailOf(t, entry.Detail)
	if detail["previous_status"] != contracts.JobStatusFailed || detail["status"] != contracts.JobStatusPending {
		t.Fatalf("审计详情不符: %+v", detail)
	}
}

func TestRetryTaskRejectsNonFailedStatus(t *testing.T) {
	for _, status := range []string{
		contracts.JobStatusPending,
		contracts.JobStatusRunning,
		contracts.JobStatusSucceeded,
		contracts.JobStatusCanceled,
		contracts.JobStatusPaused,
	} {
		t.Run(status, func(t *testing.T) {
			env, jobs := newTaskTestEnv(t)
			jobs.job.Status = status

			_, err := env.service.RetryTask(context.Background(), adminActor(), 42)
			appErr, ok := apperr.AsKind(err)
			if !ok || appErr.Kind != apperr.KindValidation {
				t.Fatalf("期望 validation，实际: %v", err)
			}
			details, ok := appErr.Details.(map[string]string)
			if !ok || details["status"] != "当前状态为 "+status {
				t.Fatalf("校验详情不符: %#v", appErr.Details)
			}
			if len(jobs.retried) != 0 || len(env.audit.entries) != 0 {
				t.Fatalf("状态非法时不应调用 RetryJob 或写审计")
			}
		})
	}
}

func TestRetryTaskPropagatesGetJobError(t *testing.T) {
	env, jobs := newTaskTestEnv(t)
	jobs.getErr = apperr.NotFound("作业不存在")

	if _, err := env.service.RetryTask(context.Background(), adminActor(), 42); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("期望 not_found，实际: %v", err)
	}
	if len(jobs.retried) != 0 || len(env.audit.entries) != 0 {
		t.Fatalf("读取失败时不应重试或写审计")
	}
}

func TestRetryTaskPropagatesRetryJobError(t *testing.T) {
	env, jobs := newTaskTestEnv(t)
	sentinel := errors.New("retry 端口失败")
	jobs.retryErr = sentinel

	if _, err := env.service.RetryTask(context.Background(), adminActor(), 42); !errors.Is(err, sentinel) {
		t.Fatalf("期望透传端口错误，实际: %v", err)
	}
	if len(env.audit.entries) != 0 {
		t.Fatalf("重试失败时不应写审计（同事务回滚）")
	}
}

func TestRetryTaskAuditFailureReturnsError(t *testing.T) {
	env, jobs := newTaskTestEnv(t)
	sentinel := errors.New("审计写入失败")
	env.service.audit = failingAudit{err: sentinel}

	snapshot, err := env.service.RetryTask(context.Background(), adminActor(), 42)
	if !errors.Is(err, sentinel) {
		t.Fatalf("审计失败应返回错误，实际: %v", err)
	}
	if snapshot.ID != 0 || len(jobs.retried) != 1 {
		t.Fatalf("审计失败时不应返回快照（事务回滚语义）: snapshot=%+v retried=%d", snapshot, len(jobs.retried))
	}
}

// ===== 取消任务 =====

func TestCancelTaskSucceedsForCancelableStatuses(t *testing.T) {
	for _, status := range []string{
		contracts.JobStatusPending,
		contracts.JobStatusRunning,
		contracts.JobStatusPaused,
	} {
		t.Run(status, func(t *testing.T) {
			env, jobs := newTaskTestEnv(t)
			jobs.job.Status = status
			env.clock.Advance(time.Minute)
			now := env.clock.Now()

			snapshot, err := env.service.CancelTask(context.Background(), adminActor(), 42)
			if err != nil {
				t.Fatalf("取消作业失败: %v", err)
			}
			if snapshot.Status != contracts.JobStatusCanceled {
				t.Fatalf("返回状态 = %q，期望 canceled", snapshot.Status)
			}
			if len(jobs.canceled) != 1 || jobs.canceled[0].JobID != 42 || !jobs.canceled[0].At.Equal(now) {
				t.Fatalf("CancelJob 入参不符: %+v", jobs.canceled)
			}

			entries := env.audit.entries
			if len(entries) != 1 || entries[0].Action != contracts.AuditActionTaskCancel {
				t.Fatalf("应写一条 task_cancel 审计，实际: %v", env.audit.actions())
			}
			entry := entries[0]
			if entry.AdminID != adminActor().ID || entry.TargetType != "background_job" ||
				entry.TargetID == nil || *entry.TargetID != 42 || !entry.At.Equal(now) {
				t.Fatalf("审计主体/目标/时间不符: %+v", entry)
			}
			detail := detailOf(t, entry.Detail)
			if detail["previous_status"] != status || detail["status"] != contracts.JobStatusCanceled {
				t.Fatalf("审计详情不符: %+v", detail)
			}
		})
	}
}

func TestCancelTaskRejectsTerminalStatuses(t *testing.T) {
	for _, status := range []string{
		contracts.JobStatusFailed,
		contracts.JobStatusSucceeded,
		contracts.JobStatusCanceled,
	} {
		t.Run(status, func(t *testing.T) {
			env, jobs := newTaskTestEnv(t)
			jobs.job.Status = status

			_, err := env.service.CancelTask(context.Background(), adminActor(), 42)
			appErr, ok := apperr.AsKind(err)
			if !ok || appErr.Kind != apperr.KindValidation {
				t.Fatalf("期望 validation，实际: %v", err)
			}
			details, ok := appErr.Details.(map[string]string)
			if !ok || details["status"] != "当前状态为 "+status {
				t.Fatalf("校验详情不符: %#v", appErr.Details)
			}
			if len(jobs.canceled) != 0 || len(env.audit.entries) != 0 {
				t.Fatalf("状态非法时不应调用 CancelJob 或写审计")
			}
		})
	}
}

func TestCancelTaskPropagatesGetJobError(t *testing.T) {
	env, jobs := newTaskTestEnv(t)
	jobs.getErr = apperr.NotFound("作业不存在")

	if _, err := env.service.CancelTask(context.Background(), adminActor(), 42); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("期望 not_found，实际: %v", err)
	}
	if len(jobs.canceled) != 0 || len(env.audit.entries) != 0 {
		t.Fatalf("读取失败时不应取消或写审计")
	}
}

func TestCancelTaskPropagatesCancelJobError(t *testing.T) {
	env, jobs := newTaskTestEnv(t)
	jobs.job.Status = contracts.JobStatusPending
	sentinel := errors.New("cancel 端口失败")
	jobs.cancelErr = sentinel

	if _, err := env.service.CancelTask(context.Background(), adminActor(), 42); !errors.Is(err, sentinel) {
		t.Fatalf("期望透传端口错误，实际: %v", err)
	}
	if len(env.audit.entries) != 0 {
		t.Fatalf("取消失败时不应写审计（同事务回滚）")
	}
}

func TestCancelTaskAuditFailureReturnsError(t *testing.T) {
	env, jobs := newTaskTestEnv(t)
	jobs.job.Status = contracts.JobStatusPending
	sentinel := errors.New("审计写入失败")
	env.service.audit = failingAudit{err: sentinel}

	snapshot, err := env.service.CancelTask(context.Background(), adminActor(), 42)
	if !errors.Is(err, sentinel) {
		t.Fatalf("审计失败应返回错误，实际: %v", err)
	}
	if snapshot.ID != 0 || len(jobs.canceled) != 1 {
		t.Fatalf("审计失败时不应返回快照（事务回滚语义）: snapshot=%+v", snapshot)
	}
}
