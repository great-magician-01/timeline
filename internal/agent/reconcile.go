package agent

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/api"
)

// 对账阈值：任务入队后超过该时间仍未产生可执行作业，就认为需要补投，
// 留出正常入队路径（写任务 + 投作业）完成的时间。
const (
	reconcileAfter      = 2 * time.Minute
	reconcileBatchLimit = 50
)

// reconcilePendingTasks 为“有任务但没有可执行作业”的 pending 任务补投作业。
//
// 正常路径在写入 ai_tasks 后会立刻投递 background_jobs；两步之间进程退出、
// 连接中断或投递失败会留下永远 pending 且没有作业的任务（调度器只会记录一次错误）。
// 这里按幂等键补投，并重新激活被取消/暂停/失败的作业，保证任务最终会被执行或取消。
func (s *Service) reconcilePendingTasks(ctx context.Context) error {
	pool := s.pools.Pool()
	if pool == nil || s.runner == nil {
		return nil
	}
	rows, err := pool.Query(ctx, `
		SELECT t.id
		FROM ai_tasks AS t
		WHERE t.status = 'pending'
		  AND t.created_at < now() - $1::interval
		  AND NOT EXISTS (
			SELECT 1 FROM background_jobs AS j
			WHERE j.ref_type = $2 AND j.ref_id = t.id
			  AND j.status IN ('pending', 'running')
		  )
		ORDER BY t.id
		LIMIT $3`, reconcileAfter.String(), aiTaskJobRefType, reconcileBatchLimit)
	if err != nil {
		return err
	}
	var taskIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		taskIDs = append(taskIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(taskIDs) == 0 {
		return nil
	}

	for _, taskID := range taskIDs {
		if err := s.enqueueAIJob(ctx, pool, taskID); err != nil {
			return err
		}
		// 幂等键已存在且处于取消/失败/暂停态时，Enqueue 会静默跳过；
		// 这里把它重新激活，否则任务永远不会被执行。
		if _, err := pool.Exec(ctx, `
			UPDATE background_jobs
			SET status = 'pending', run_at = now(), attempts = 0, last_error = '', updated_at = now()
			WHERE idempotency_key = $1 AND status IN ('canceled', 'failed', 'paused')`,
			aiTaskJobPrefix+api.FormatID(taskID),
		); err != nil {
			return err
		}
	}
	s.log.Info("已为游离的 AI 任务补投执行作业", "count", len(taskIDs))
	return nil
}
