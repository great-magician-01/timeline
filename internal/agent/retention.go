package agent

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/settings"
)

// 调用记录的保留期与截断上限可通过系统设置覆盖（docs/WORKFLOWS.md 7.7.6）。
const (
	callLogCleanupBatchSize = 5000
	callLogCleanupMaxRounds = 20
)

// RunCallLogCleanup 把超过保留期的调用记录标记为已删除（逻辑删除）：
// 只更新 ai_call_logs.is_deleted，不执行物理删除，也不触碰任务与审计记录。
// 由运行器注册为每日周期任务。
func (s *Service) RunCallLogCleanup(ctx context.Context) {
	pool := s.pools.Pool()
	if pool == nil {
		return
	}
	retentionDays, err := s.callLogRetentionDays(ctx)
	if err != nil {
		s.log.Warn("读取调用记录保留期失败，使用默认值", "err", err, "days", settings.DefaultCallLogRetentionDays)
		retentionDays = settings.DefaultCallLogRetentionDays
	}
	if retentionDays <= 0 {
		// 0 或负值表示关闭自动清理，保留全部记录供管理员手工处理。
		return
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var total int64
	for round := 0; round < callLogCleanupMaxRounds; round++ {
		tag, err := pool.Exec(ctx, `
			UPDATE ai_call_logs SET is_deleted = TRUE
			WHERE id IN (
				SELECT id FROM ai_call_logs
				WHERE NOT is_deleted AND created_at < $1
				ORDER BY id
				LIMIT $2
			)`, cutoff, callLogCleanupBatchSize)
		if err != nil {
			s.log.Error("清理超期调用记录失败", "err", err, "cleaned", total)
			return
		}
		affected := tag.RowsAffected()
		total += affected
		if affected < callLogCleanupBatchSize {
			break
		}
	}
	if total > 0 {
		s.log.Info("已标记超期调用记录为删除", "count", total, "retention_days", retentionDays)
	}
}

// callLogRetentionDays 读取系统设置；缺失或格式非法时使用默认保留期。
func (s *Service) callLogRetentionDays(ctx context.Context) (int, error) {
	pool := s.pools.Pool()
	if pool == nil {
		return settings.DefaultCallLogRetentionDays, nil
	}
	values := settings.LoadOrDefaults(ctx, pool)
	return values.CallLogRetentionDays, nil
}
