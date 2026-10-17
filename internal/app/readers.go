package app

import (
	"context"
	"time"

	adminrepo "github.com/great-magician-01/timeline/internal/modules/admin/repo"
	agentapp "github.com/great-magician-01/timeline/internal/modules/agent/app"
	aiapp "github.com/great-magician-01/timeline/internal/modules/ai/app"
	authapp "github.com/great-magician-01/timeline/internal/modules/auth/app"
)

// 编译期断言：全局 AI 任务只读视图与个人主页统计适配器满足模块端口。
var (
	_ aiapp.TaskReader           = aiTaskReaderAdapter{}
	_ authapp.ProfileStatsReader = profileStatsAdapter{}
)

// aiTaskReaderAdapter 把 agent app 的只读任务视图适配为 ai 模块的 TaskReader。
// 消费者：ai 模块（AI 列表 today_actions、详情 recent_tasks）。
type aiTaskReaderAdapter struct {
	agent *agentapp.Service
}

func (a aiTaskReaderAdapter) RecentTasks(ctx context.Context, aiID int64, limit int) ([]aiapp.TaskBrief, error) {
	briefs, err := a.agent.RecentTaskBriefs(ctx, aiID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]aiapp.TaskBrief, 0, len(briefs))
	for _, brief := range briefs {
		result = append(result, aiapp.TaskBrief{
			ID:          brief.ID,
			TriggerType: brief.TriggerType,
			ActionType:  brief.ActionType,
			Status:      brief.Status,
			CreatedAt:   brief.CreatedAt,
		})
	}
	return result, nil
}

func (a aiTaskReaderAdapter) TodayActionCounts(ctx context.Context, aiIDs []int64, dayStart time.Time) (map[int64]int, error) {
	return a.agent.TodayActionCounts(ctx, aiIDs, dayStart)
}

// profileStatsAdapter 用 admin 只读 read-model 的跨表聚合恢复个人主页统计。
// 消费者：auth 模块 GetProfile。
type profileStatsAdapter struct {
	read *adminrepo.ReadModel
}

func (a profileStatsAdapter) ProfileStats(ctx context.Context, userID int64) (authapp.ProfileStats, error) {
	stats, err := a.read.UserStats(ctx, userID)
	if err != nil {
		return authapp.ProfileStats{}, err
	}
	return authapp.ProfileStats{
		Posts:         stats.Posts,
		Comments:      stats.Comments,
		Followers:     stats.Followers,
		Following:     stats.Following,
		LikesGiven:    stats.LikesGiven,
		LikesReceived: stats.LikesReceived,
		Reposts:       stats.Reposts,
	}, nil
}

// lazyTaskReader 延迟绑定 ai→agent 的只读视图（ai 先于 agent 构造）。
type lazyTaskReader struct{ aiapp.TaskReader }
