package contracts

import (
	"context"
	"time"
)

// 作业类型（与 background_jobs.job_type / platform/jobs 常量一致）。
// 消费者：content（post_publish）、media（media_derivative）、agent（ai_task/memory_consolidation）。
const (
	JobTypePostPublish       = "post_publish"
	JobTypeMediaDerivative   = "media_derivative"
	JobTypeAttachmentCleanup = "attachment_cleanup"
	JobTypeAISchedule        = "ai_schedule"
	JobTypeAITask            = "ai_task"
	JobTypeMemoryConsolidate = "memory_consolidation"
	JobTypeCallLogCleanup    = "ai_call_log_cleanup"
)

// 作业状态（与 background_jobs.status / platform/jobs 常量一致）。
// 消费者：agent（任务对账）、admin（任务重试/取消）。
const (
	JobStatusPending   = "pending"
	JobStatusRunning   = "running"
	JobStatusSucceeded = "succeeded"
	JobStatusFailed    = "failed"
	JobStatusPaused    = "paused"
	JobStatusCanceled  = "canceled"
)

// JobEnqueue 描述一次入队；IdempotencyKey 重复时静默忽略（幂等）。
// 消费者：content（定时发布）、media（衍生文件）、agent（AI 任务/记忆沉淀）。
type JobEnqueue struct {
	Type           string
	RefType        string
	RefID          *int64
	IdempotencyKey string
	Payload        any
	RunAt          time.Time
	MaxAttempts    int
}

// JobSnapshot 是作业的可读快照。消费者：agent（草稿发布状态）、admin（任务列表/处置结果）。
type JobSnapshot struct {
	ID          int64
	Type        string
	RefType     string
	RefID       *int64
	Status      string
	RunAt       time.Time
	Attempts    int
	MaxAttempts int
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// JobRefQuery 按引用对象查询作业（不含分页，用于小批量状态补充）。
// 消费者：agent（AI 草稿的定时发布状态）、admin（账号停用编排）。
type JobRefQuery struct {
	RefType string
	RefIDs  []int64
	Types   []string
	Limit   int
}

// CancelPendingJobsInput 取消某引用对象下待执行/执行中的作业。
// 消费者：content（删除/取消定时帖）。
type CancelPendingJobsInput struct {
	JobType string
	RefType string
	RefID   int64
	At      time.Time
}

// PausePendingJobsInput 把一批引用对象的 pending 作业置为 paused（逻辑状态，不删除）。
// 消费者：admin（停用账号/AI 时暂停其待执行作业）。
type PausePendingJobsInput struct {
	JobType string
	RefType string
	RefIDs  []int64
	At      time.Time
}

// JobActionInput 对单个作业执行人工处置。消费者：admin（任务重试/取消）。
type JobActionInput struct {
	JobID int64
	At    time.Time
}

// PauseAllPendingInput 把某引用类型下所有 pending 作业置为 paused（不按 ref_id 过滤）。
// 消费者：agent（关闭全局 AI 开关时暂停 ref_type='ai' 的全部待执行作业，等价旧行为）。
// JobType 为空表示不限作业类型。
type PauseAllPendingInput struct {
	JobType string
	RefType string
	At      time.Time
}

// JobsQueue 是后台作业队列的跨模块端口。
//
// 消费者：content、media、agent、admin。
// 实现方：platform/jobs 的 Queue/Runner（组合根注入轻量适配器）。
type JobsQueue interface {
	// Enqueue 写入一条作业；幂等键重复时返回 nil（不重复执行）。
	Enqueue(ctx context.Context, in JobEnqueue) error

	// CancelPending 取消某引用对象下所有 pending/running 作业。
	CancelPending(ctx context.Context, in CancelPendingJobsInput) error

	// PausePending 暂停一批引用对象的 pending 作业，返回受影响数量。
	PausePending(ctx context.Context, in PausePendingJobsInput) (int, error)

	// PauseAllPending 暂停某引用类型下全部 pending 作业，返回受影响数量。
	PauseAllPending(ctx context.Context, in PauseAllPendingInput) (int, error)

	// RetryJob 把 failed 作业重置为 pending（等价人工重试），返回最新快照。
	RetryJob(ctx context.Context, in JobActionInput) (JobSnapshot, error)

	// CancelJob 把 pending/running/paused 作业置为 canceled，返回最新快照。
	CancelJob(ctx context.Context, in JobActionInput) (JobSnapshot, error)

	// SetJobStatus 由作业处理器显式写作业终态（不经过运行器重试逻辑）。
	// status 只接受 JobStatusPaused / JobStatusCanceled 等终态；消费者：
	// content（post_publish 处理器：作者停用→paused、计划时间已变更→canceled）。
	SetJobStatus(ctx context.Context, jobID int64, status string, at time.Time) error

	// GetJob 读取单个作业快照；不存在返回领域 NotFound。
	GetJob(ctx context.Context, jobID int64) (JobSnapshot, error)

	// ListByRef 按引用对象批量查询作业快照。消费者：agent、admin。
	ListByRef(ctx context.Context, query JobRefQuery) ([]JobSnapshot, error)
}
