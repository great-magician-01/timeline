package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// GenerateDraft 创建“管理员手动生成”任务（模式 draft：产出 AI 草稿等待发布）。
func (s *Service) GenerateDraft(ctx context.Context, adminID, aiID int64, in GenerationInput) (TaskView, error) {
	return s.createGenerationTask(ctx, adminID, aiID, domain.GenerationModeDraft, in)
}

// PublishDirect 创建“立即发布”生成任务（模式 direct）。
func (s *Service) PublishDirect(ctx context.Context, adminID, aiID int64, in GenerationInput) (TaskView, error) {
	return s.createGenerationTask(ctx, adminID, aiID, domain.GenerationModeDirect, in)
}

func (s *Service) createGenerationTask(ctx context.Context, adminID, aiID int64,
	mode string, in GenerationInput) (TaskView, error) {
	if adminID <= 0 {
		return TaskView{}, apperr.Unauthenticated("请先登录")
	}
	if aiID <= 0 {
		return TaskView{}, apperr.Validation("缺少 AI 账号")
	}
	if _, err := s.deps.AI.RuntimeByAI(ctx, aiID); err != nil {
		return TaskView{}, err
	}
	at := s.now()
	if in.ScheduledAt != nil && !in.ScheduledAt.After(at) {
		return TaskView{}, apperr.Validation("定时发布时间必须晚于当前时间")
	}
	taskID, inserted, err := s.Enqueue(ctx, EnqueueInput{
		AID:            aiID,
		TriggerType:    domain.TriggerManual,
		TriggerRefType: mode,
		ActionType:     domain.ActionOriginalPost,
		IdempotencyKey: "ai_generate:" + mode + ":" + apiID(aiID) + ":" + apiID(at.UnixNano()),
		ScheduledAt:    in.ScheduledAt,
	})
	if err != nil {
		return TaskView{}, err
	}
	if !inserted {
		return TaskView{}, apperr.Conflict("duplicate", "同一生成任务已存在")
	}
	if err := s.deps.Audit.Write(ctx, contracts.AuditEntry{
		AdminID:    adminID,
		Action:     contracts.AuditActionAIGenerate,
		TargetType: domain.RefAI,
		TargetID:   &aiID,
		Detail:     map[string]any{"mode": mode, "task_id": apiID(taskID)},
		At:         at,
	}); err != nil {
		return TaskView{}, err
	}
	task, err := s.deps.Tasks.GetTask(ctx, taskID)
	if err != nil {
		return TaskView{}, err
	}
	return s.taskView(ctx, task)
}

// ListAIDrafts 列出某 AI 的草稿（含定时发布作业状态）。
func (s *Service) ListAIDrafts(ctx context.Context, aiID int64, limit int, cursor int64) ([]DraftView, bool, error) {
	if aiID <= 0 {
		return nil, false, apperr.Validation("缺少 AI 账号")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	drafts, err := s.deps.Content.ListDraftsByAuthor(ctx, contracts.DraftListQuery{
		AuthorID: aiID, Limit: limit + 1, Cursor: cursor,
	})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(drafts) > limit
	if hasMore {
		drafts = drafts[:limit]
	}
	postIDs := make([]int64, 0, len(drafts))
	for _, draft := range drafts {
		postIDs = append(postIDs, draft.Post.ID)
	}
	jobsByRef := map[int64]contracts.JobSnapshot{}
	if len(postIDs) > 0 {
		records, jobErr := s.deps.Jobs.ListByRef(ctx, contracts.JobRefQuery{
			RefType: "post", RefIDs: postIDs, Types: []string{contracts.JobTypePostPublish}, Limit: len(postIDs) * 2,
		})
		if jobErr != nil {
			return nil, false, jobErr
		}
		for _, job := range records {
			if job.RefID != nil {
				jobsByRef[*job.RefID] = job
			}
		}
	}
	views := make([]DraftView, 0, len(drafts))
	for _, draft := range drafts {
		view := DraftView{Post: draft.Post, ContentHTML: draft.ContentHTML}
		if job, ok := jobsByRef[draft.Post.ID]; ok {
			view.PublishStatus = job.Status
			view.PublishError = job.LastError
			runAt := job.RunAt
			view.PendingRunAt = &runAt
		}
		views = append(views, view)
	}
	return views, hasMore, nil
}

// UpdateAIDraft 管理员编辑 AI 草稿；已发布内容追加新版本。
func (s *Service) UpdateAIDraft(ctx context.Context, adminID, aiID, postID int64, format, source string) error {
	if adminID <= 0 {
		return apperr.Unauthenticated("请先登录")
	}
	post, err := s.deps.Content.GetPost(ctx, postID)
	if err != nil {
		return err
	}
	if post.AuthorID != aiID {
		return apperr.NotFound("草稿不存在或无权操作")
	}
	if post.Status == model.PostStatusDeleted {
		return apperr.NotFound("草稿不存在")
	}
	at := s.now()
	if err := s.deps.Content.UpdateDraftForActor(ctx, contracts.UpdateDraftForActorInput{
		PostID: postID, ContentFormat: format, ContentSource: source, At: at,
	}); err != nil {
		return err
	}
	return s.deps.Audit.Write(ctx, contracts.AuditEntry{
		AdminID:    adminID,
		Action:     contracts.AuditActionAIGenerate,
		TargetType: domain.RefPost,
		TargetID:   &postID,
		Detail:     map[string]any{"action": "update_draft", "ai_id": apiID(aiID)},
		At:         at,
	})
}

// PublishAIDraft 管理员发布 AI 草稿。
func (s *Service) PublishAIDraft(ctx context.Context, adminID, aiID, postID int64) error {
	if adminID <= 0 {
		return apperr.Unauthenticated("请先登录")
	}
	post, err := s.deps.Content.GetPost(ctx, postID)
	if err != nil {
		return err
	}
	if post.AuthorID != aiID {
		return apperr.NotFound("草稿不存在或无权操作")
	}
	at := s.now()
	if err := s.deps.Content.PublishPostForActor(ctx, contracts.PublishPostForActorInput{
		ActorID: adminID, PostID: postID, At: at,
	}); err != nil {
		return err
	}
	return s.deps.Audit.Write(ctx, contracts.AuditEntry{
		AdminID:    adminID,
		Action:     contracts.AuditActionAIPublish,
		TargetType: domain.RefPost,
		TargetID:   &postID,
		Detail:     map[string]any{"ai_id": apiID(aiID)},
		At:         at,
	})
}

// ListAITasks 分页读取 AI 任务及注入记忆。
// 任务对应的记忆引用一次批量读取（task_id = ANY），避免逐任务往返。
func (s *Service) ListAITasks(ctx context.Context, filter repo.TaskFilter) ([]TaskView, bool, error) {
	tasks, hasMore, err := s.deps.Tasks.ListTasks(ctx, filter)
	if err != nil {
		return nil, false, err
	}
	refsByTask := map[int64][]repo.MemoryRef{}
	if len(tasks) > 0 {
		taskIDs := make([]int64, 0, len(tasks))
		for _, task := range tasks {
			taskIDs = append(taskIDs, task.ID)
		}
		refsByTask, err = s.deps.Tasks.ListMemoryRefsByTaskIDs(ctx, taskIDs)
		if err != nil {
			return nil, false, err
		}
	}
	views := make([]TaskView, 0, len(tasks))
	for _, task := range tasks {
		views = append(views, TaskView{Task: task, MemoryRefs: refsByTask[task.ID]})
	}
	return views, hasMore, nil
}

func (s *Service) taskView(ctx context.Context, task repo.Task) (TaskView, error) {
	refs, err := s.deps.Tasks.ListMemoryRefs(ctx, task.ID)
	if err != nil {
		return TaskView{}, err
	}
	return TaskView{Task: task, MemoryRefs: refs}, nil
}

// RetryAITask 人工重试失败/被拒绝的任务。
func (s *Service) RetryAITask(ctx context.Context, adminID, taskID int64) (repo.Task, error) {
	task, err := s.deps.Tasks.GetTask(ctx, taskID)
	if err != nil {
		return repo.Task{}, err
	}
	if task.Status != domain.TaskStatusFailed && task.Status != domain.TaskStatusRejected {
		return repo.Task{}, apperr.Validation("只有失败或已拒绝的任务可以重试")
	}
	at := s.now()
	if err := s.deps.Tasks.SetTaskStatus(ctx, taskID, domain.TaskStatusPending, "", nil); err != nil {
		return repo.Task{}, err
	}
	// 任务重试必须保证确实有可执行作业：已有 failed 作业人工重试、canceled/paused 作业
	// 重新激活；若旧作业已 succeeded 无法恢复，则用一次性幂等键补投新的执行作业。
	if err := s.requeueTaskForAdminRetry(ctx, taskID, at); err != nil {
		return repo.Task{}, err
	}
	if err := s.deps.Audit.Write(ctx, contracts.AuditEntry{
		AdminID:    adminID,
		Action:     contracts.AuditActionTaskRetry,
		TargetType: domain.RefTask,
		TargetID:   &taskID,
		Detail:     map[string]any{"previous_status": task.Status},
		At:         at,
	}); err != nil {
		return repo.Task{}, err
	}
	return s.deps.Tasks.GetTask(ctx, taskID)
}

// CancelAITask 取消 pending/running/paused 任务。
func (s *Service) CancelAITask(ctx context.Context, adminID, taskID int64) (repo.Task, error) {
	task, err := s.deps.Tasks.GetTask(ctx, taskID)
	if err != nil {
		return repo.Task{}, err
	}
	switch task.Status {
	case domain.TaskStatusPending, domain.TaskStatusRunning, domain.TaskStatusPaused:
	default:
		return repo.Task{}, apperr.Validation("当前状态不能取消")
	}
	at := s.now()
	if err := s.deps.Tasks.SetTaskStatus(ctx, taskID, domain.TaskStatusCanceled, "管理员取消", &at); err != nil {
		return repo.Task{}, err
	}
	// 作业的 ref_id 是任务 ID、ref_type='ai_task'：按引用查询后取消，不能把 taskID 当作业 ID。
	if jobs, jobErr := s.deps.Jobs.ListByRef(ctx, contracts.JobRefQuery{
		RefType: domain.RefTask, RefIDs: []int64{taskID}, Types: []string{contracts.JobTypeAITask}, Limit: 10,
	}); jobErr == nil {
		for _, job := range jobs {
			switch job.Status {
			case contracts.JobStatusPending, contracts.JobStatusRunning, contracts.JobStatusPaused:
				if _, cancelErr := s.deps.Jobs.CancelJob(ctx, contracts.JobActionInput{JobID: job.ID, At: at}); cancelErr != nil {
					s.deps.Log.Warn("取消 AI 任务作业失败", "err", cancelErr, "task_id", taskID, "job_id", job.ID)
				}
			}
		}
	} else {
		s.deps.Log.Warn("查询 AI 任务作业失败", "err", jobErr, "task_id", taskID)
	}
	if err := s.deps.Audit.Write(ctx, contracts.AuditEntry{
		AdminID:    adminID,
		Action:     contracts.AuditActionTaskCancel,
		TargetType: domain.RefTask,
		TargetID:   &taskID,
		Detail:     map[string]any{"previous_status": task.Status},
		At:         at,
	}); err != nil {
		return repo.Task{}, err
	}
	return s.deps.Tasks.GetTask(ctx, taskID)
}

// ListCallLogs 分页读取模型调用记录。
func (s *Service) ListCallLogs(ctx context.Context, filter repo.CallLogFilter) ([]CallLogView, bool, error) {
	logs, hasMore, err := s.deps.CallLogs.ListCallLogs(ctx, filter)
	if err != nil {
		return nil, false, err
	}
	views := make([]CallLogView, 0, len(logs))
	views = append(views, logs...)
	return views, hasMore, nil
}

// GetCallLog 读取单条调用记录。
func (s *Service) GetCallLog(ctx context.Context, logID int64) (CallLogView, error) {
	return s.deps.CallLogs.GetCallLog(ctx, logID)
}

// GetTask 是管理用例读取单条任务的辅助方法。
func (s *Service) GetTask(ctx context.Context, taskID int64) (TaskView, error) {
	task, err := s.deps.Tasks.GetTask(ctx, taskID)
	if err != nil {
		return TaskView{}, err
	}
	return s.taskView(ctx, task)
}

// requeueTaskForAdminRetry 保证人工重试的任务确实有可执行作业：
// pending/running 作业直接复用；failed 作业人工重试；canceled/paused 作业重新激活；
// 已 succeeded 等无法恢复的终态用一次性幂等键补投新作业。
func (s *Service) requeueTaskForAdminRetry(ctx context.Context, taskID int64, at time.Time) error {
	jobs, err := s.deps.Jobs.ListByRef(ctx, contracts.JobRefQuery{
		RefType: domain.RefTask, RefIDs: []int64{taskID}, Types: []string{contracts.JobTypeAITask}, Limit: 10,
	})
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return s.enqueueTaskJob(ctx, taskID)
	}
	job := jobs[0]
	switch job.Status {
	case contracts.JobStatusPending, contracts.JobStatusRunning:
		return nil
	case contracts.JobStatusFailed:
		_, err := s.deps.Jobs.RetryJob(ctx, contracts.JobActionInput{JobID: job.ID, At: at})
		return err
	case contracts.JobStatusCanceled, contracts.JobStatusPaused:
		reactivated, err := s.deps.Jobs.Reactivate(ctx, taskJobIdempotencyKey(taskID), at, at)
		if err != nil {
			return err
		}
		if reactivated {
			return nil
		}
	}
	s.deps.Log.Warn("AI 任务旧作业已终态，按一次性幂等键重新投递",
		"task_id", taskID, "job_id", job.ID, "status", job.Status)
	return s.deps.Jobs.Enqueue(ctx, contracts.JobEnqueue{
		Type:           contracts.JobTypeAITask,
		RefType:        domain.RefTask,
		RefID:          &taskID,
		IdempotencyKey: taskJobIdempotencyKey(taskID) + ":retry:" + apiID(at.UnixNano()),
		Payload:        jobPayload{TaskID: taskID},
		RunAt:          at,
		MaxAttempts:    3,
		At:             at,
	})
}

// scheduledAtFromRaw 解析 RFC3339 定时时间（供 http 层复用）。
func scheduledAtFromRaw(raw string, now time.Time) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, apperr.Validation("定时发布时间格式无效")
	}
	if !at.After(now) {
		return nil, apperr.Validation("定时发布时间必须晚于当前时间")
	}
	return &at, nil
}
