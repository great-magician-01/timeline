package agent

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/api"
)

// 对账阈值：任务入队后超过该时间仍未产生可执行作业，就认为需要补投，
// 留出正常入队路径（写任务 + 投作业）完成的时间。
const (
	reconcileAfter      = 2 * time.Minute
	reconcileBatchLimit = 50
	// staleRunningAfter 与 claimTask 的超时窗口一致：超过该时间仍为 running 的任务
	// 视为执行进程已丢失，可以重新领取。
	staleRunningAfter = 10 * time.Minute
)

// reconcilePendingTasks 为“有任务但没有可执行作业”的任务补投作业：
//   - pending 超过 reconcileAfter 的任务：正常入队路径可能在两步之间中断；
//   - running 超过 staleRunningAfter 的任务：进程重启后旧作业已被回收并标记完成，
//     任务却仍停在 running，需要重新投递执行作业才能走到 claimTask 的超时重领分支。
//
// 正常路径在写入 ai_tasks 后会立刻投递 background_jobs；两步之间进程退出、
// 连接中断或投递失败会留下永远 pending 且没有作业的任务（调度器只会记录一次错误）。
// 这里按幂等键补投，并重新激活被取消/暂停/失败的作业，保证任务最终会被执行或取消。
func (s *Service) reconcilePendingTasks(ctx context.Context) error {
	pool := s.pools.Pool()
	if pool == nil || s.runner == nil {
		return nil
	}
	pendingIDs, err := s.tasksNeedingJob(ctx, pool,
		`t.status = 'pending' AND t.created_at < now() - $1::interval`, reconcileAfter)
	if err != nil {
		return err
	}
	staleRunningIDs, err := s.tasksNeedingJob(ctx, pool,
		`t.status = 'running' AND t.started_at < now() - $1::interval`, staleRunningAfter)
	if err != nil {
		return err
	}
	if len(pendingIDs) == 0 && len(staleRunningIDs) == 0 {
		return nil
	}
	for _, taskID := range pendingIDs {
		if err := s.requeueTaskJob(ctx, pool, taskID, []string{"canceled", "failed", "paused"}); err != nil {
			return err
		}
	}
	for _, taskID := range staleRunningIDs {
		// running 任务的旧作业已经被作业层标记为成功，必须一并重新激活，
		// 否则幂等键会让新作业无法投递。
		if err := s.requeueTaskJob(ctx, pool, taskID,
			[]string{"succeeded", "canceled", "failed", "paused"}); err != nil {
			return err
		}
	}
	s.log.Info("已为游离的 AI 任务补投执行作业",
		"pending", len(pendingIDs), "stale_running", len(staleRunningIDs))
	return nil
}

// tasksNeedingJob 查询满足条件且没有可执行作业的任务 ID。
func (s *Service) tasksNeedingJob(
	ctx context.Context,
	pool *pgxpool.Pool,
	condition string,
	interval time.Duration,
) ([]int64, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.id
		FROM ai_tasks AS t
		WHERE `+condition+`
		  AND NOT EXISTS (
			SELECT 1 FROM background_jobs AS j
			WHERE j.ref_type = $2 AND j.ref_id = t.id
			  AND j.status IN ('pending', 'running')
		  )
		ORDER BY t.id
		LIMIT $3`, interval.String(), aiTaskJobRefType, reconcileBatchLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, reconcileBatchLimit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// requeueTaskJob 投递任务作业，并把已结束的旧作业重新置为 pending。
func (s *Service) requeueTaskJob(
	ctx context.Context,
	pool *pgxpool.Pool,
	taskID int64,
	reactivate []string,
) error {
	if err := s.enqueueAIJob(ctx, pool, taskID); err != nil {
		return err
	}
	// 幂等键已存在且处于结束态时，Enqueue 会静默跳过；这里把它重新激活，
	// 否则任务永远不会被执行。
	if _, err := pool.Exec(ctx, `
		UPDATE background_jobs
		SET status = 'pending', run_at = now(), attempts = 0, last_error = '', updated_at = now()
		WHERE idempotency_key = $1 AND status = ANY($2)`,
		aiTaskJobPrefix+api.FormatID(taskID), reactivate,
	); err != nil {
		return err
	}
	return nil
}
