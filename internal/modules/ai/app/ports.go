package app

import (
	"context"
	"time"
)

// TaskReader 是 agent 模块向 ai 模块提供的只读任务视图（AI 列表/详情展示用）。
// ai_tasks 表由 agent 模块拥有，因此不在这里直接查询；agent app 实现后由组合根注入，
// 未接线时列表的 today_actions 为 0、详情 recent_tasks 为空。
type TaskReader interface {
	// RecentTasks 返回某个 AI 最近的决策任务摘要（按 ID 倒序）。
	RecentTasks(ctx context.Context, aiID int64, limit int) ([]TaskBrief, error)
	// TodayActionCounts 统计一批 AI 在 [dayStart, dayStart+24h) 内成功的动作数。
	TodayActionCounts(ctx context.Context, aiIDs []int64, dayStart time.Time) (map[int64]int, error)
}

// TaskBrief 是任务摘要（HTTP 详情页 DTO 的数据来源）。
type TaskBrief struct {
	ID          int64
	TriggerType string
	ActionType  string
	Status      string
	CreatedAt   time.Time
}
