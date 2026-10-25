package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

const reconcileBatchLimit = 100

// taskJobIdempotencyKey 返回 AI 任务执行作业的幂等键（与 enqueueTaskJob 一致）。
func taskJobIdempotencyKey(taskID int64) string { return "ai_task:" + apiID(taskID) }

// Reconcile 修复“任务存在但作业缺失/已终态”和“running 超时未完成”的任务：
//   - pending 任务只有在存在 pending/running 作业时视为已投递；否则通过平台重激活
//     （或按同一幂等键补投）重新投递执行作业；
//   - running 且超过租约仍为 running 的任务视为执行进程已丢失：标记 failed 并重投作业，
//     由 ClaimTask 的 failed 分支重新领取；
//   - 单个任务处理失败不影响其它任务，返回首个错误供调度日志记录。
func (s *Service) Reconcile(ctx context.Context) error {
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	pending, err := s.deps.Tasks.ListPendingTasks(ctx, reconcileBatchLimit)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		ids := make([]int64, 0, len(pending))
		for _, task := range pending {
			ids = append(ids, task.ID)
		}
		existing, err := s.deps.Jobs.ListByRef(ctx, contracts.JobRefQuery{
			RefType: domain.RefTask, RefIDs: ids, Types: []string{contracts.JobTypeAITask}, Limit: 200,
		})
		if err != nil {
			return err
		}
		live := map[int64]bool{}
		for _, job := range existing {
			if job.RefID == nil {
				continue
			}
			if job.Status == contracts.JobStatusPending || job.Status == contracts.JobStatusRunning {
				live[*job.RefID] = true
			}
		}
		for _, task := range pending {
			if live[task.ID] {
				continue
			}
			if err := s.reactivateOrEnqueueTaskJob(ctx, task.ID, task.ScheduledAt); err != nil {
				record(err)
			}
		}
	}

	// running 超时判定下推到 SQL（started_at < now-lease），命中
	// ai_tasks_running (started_at) WHERE status='running' 部分索引，避免每轮扫全表；
	// started_at IS NULL 的行不满足比较条件，与旧逻辑的「跳过」一致。
	now := s.now()
	cutoff := now.Add(-taskLease)
	running, _, err := s.deps.Tasks.ListTasks(ctx, repo.TaskFilter{
		Status: domain.TaskStatusRunning, StartedBefore: &cutoff, Limit: reconcileBatchLimit,
	})
	if err != nil {
		return err
	}
	for _, task := range running {
		if err := s.deps.Tasks.SetTaskStatus(ctx, task.ID, domain.TaskStatusFailed,
			domain.ErrorTypeDatabase+": 任务执行超时未完成，已重新投递", &now); err != nil {
			record(err)
			continue
		}
		if err := s.reactivateOrEnqueueTaskJob(ctx, task.ID, task.ScheduledAt); err != nil {
			record(err)
		}
		s.deps.Log.Warn("恢复超时未完成的 AI 任务", "task_id", task.ID, "ai_id", task.AIID)
	}
	return firstErr
}

// reactivateOrEnqueueTaskJob 重激活任务对应的 ai_task 作业；作业确实不存在时按
// 同一幂等键补投（幂等）。平台未提供重激活能力时退化为 Enqueue（终态作业会被幂等跳过）。
func (s *Service) reactivateOrEnqueueTaskJob(ctx context.Context, taskID int64, scheduledAt *time.Time) error {
	at := s.now()
	runAt := at
	if scheduledAt != nil && scheduledAt.After(at) {
		runAt = *scheduledAt
	}
	key := taskJobIdempotencyKey(taskID)
	reactivated, err := s.deps.Jobs.Reactivate(ctx, key, runAt, at)
	if err != nil {
		return err
	}
	if reactivated {
		return nil
	}
	// 作业不存在（如任务入队后进程中断）：按同一幂等键补投；已存在终态作业时 no-op。
	if err := s.enqueueTaskJob(ctx, taskID); err != nil {
		return err
	}
	s.deps.Log.Warn("AI 任务没有可执行作业，已按幂等键补投", "task_id", taskID)
	return nil
}
