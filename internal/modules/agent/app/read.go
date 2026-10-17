package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
)

// TaskBriefResult 是任务摘要只读视图（供 ai 模块 TaskReader 适配）。
type TaskBriefResult struct {
	ID          int64
	TriggerType string
	ActionType  string
	Status      string
	CreatedAt   time.Time
}

// RecentTaskBriefs 返回某个 AI 最近的任务摘要（按 ID 倒序，最多 limit 条）。
// 消费者：组合根 internal/app 的 ai.TaskReader adapter。
func (s *Service) RecentTaskBriefs(ctx context.Context, aiID int64, limit int) ([]TaskBriefResult, error) {
	if aiID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tasks, _, err := s.deps.Tasks.ListTasks(ctx, repo.TaskFilter{AIID: aiID, Limit: limit})
	if err != nil {
		return nil, err
	}
	briefs := make([]TaskBriefResult, 0, len(tasks))
	for _, task := range tasks {
		briefs = append(briefs, TaskBriefResult{
			ID:          task.ID,
			TriggerType: task.TriggerType,
			ActionType:  task.ActionType,
			Status:      task.Status,
			CreatedAt:   task.CreatedAt,
		})
	}
	return briefs, nil
}

// TodayActionCounts 统计一批 AI 在指定日界的动作计数（ai_action_counters 当日累计）。
// 消费者：组合根 internal/app 的 ai.TaskReader adapter。
func (s *Service) TodayActionCounts(ctx context.Context, aiIDs []int64, dayStart time.Time) (map[int64]int, error) {
	result := make(map[int64]int, len(aiIDs))
	for _, aiID := range aiIDs {
		if aiID <= 0 {
			continue
		}
		count, err := s.deps.Counters.TotalActions(ctx, aiID, dayStart)
		if err != nil {
			return nil, err
		}
		result[aiID] = count
	}
	return result, nil
}
