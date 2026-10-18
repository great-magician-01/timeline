package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
)

// actionGate 在模型返回后、执行动作前重新读取全局开关、AI 启用状态与账号状态：
// 等待模型期间管理员可能已经全局停止或停用该 AI，不能信任任务开始时的快照
// （docs/WORKFLOWS.md 7.5 第 3 条）。返回 blocked=true 时任务应 canceled、不执行动作。
func (s *Service) actionGate(ctx context.Context, aiID int64) (taskResult, bool, error) {
	values, err := s.deps.Settings.Settings(ctx)
	if err != nil {
		return taskResult{}, false, err
	}
	if !values.AIGlobalEnabled {
		return taskResult{Status: domain.TaskStatusCanceled, Decision: "全局 AI 开关已关闭"}, true, nil
	}
	rt, err := s.deps.AI.RuntimeByAI(ctx, aiID)
	if err != nil {
		if isNotFound(err) {
			return taskResult{Status: domain.TaskStatusCanceled, Decision: "AI 账号不存在"}, true, nil
		}
		return taskResult{}, false, err
	}
	if !rt.Profile.IsEnabled {
		return taskResult{Status: domain.TaskStatusCanceled, Decision: "该 AI 已停用"}, true, nil
	}
	actor, err := s.deps.Users.GetUser(ctx, aiID)
	if err != nil {
		if isNotFound(err) {
			return taskResult{Status: domain.TaskStatusCanceled, Decision: "AI 账号不存在"}, true, nil
		}
		return taskResult{}, false, err
	}
	if !actor.IsActive() {
		return taskResult{Status: domain.TaskStatusCanceled, Decision: "AI 账号已停用"}, true, nil
	}
	return taskResult{}, false, nil
}
