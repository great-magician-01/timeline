package contracts

import (
	"context"
	"time"
)

// CancelTasksInput 取消某账号待执行的 AI 任务（例如管理员停用 AI 账号）。
// 消费者：admin（用户停用编排：auth 状态 + jobs 暂停 + agent 任务取消）。
type CancelTasksInput struct {
	ActorID int64 // 执行处置的管理员；用于日志/审计关联
	AIID    int64 // 被取消任务的 AI 账号
	At      time.Time
}

// AgentService 是 agent 模块对外发布的跨模块能力。
//
// 消费者：admin。
// 实现方：modules/agent/app 的 App 类型（组合根注入）。
type AgentService interface {
	// CancelPendingTasksForActor 把某 AI 账号的 pending 任务置为 canceled，返回取消数量。
	// 消费者：admin（停用 AI 账号时与用户/会话/作业处置在同一事务内执行）。
	CancelPendingTasksForActor(ctx context.Context, in CancelTasksInput) (int, error)
}
