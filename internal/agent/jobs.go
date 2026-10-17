package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/great-magician-01/callable"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/jobs"
)

// taskResult 描述一次任务执行的终态（写回 ai_tasks 与后续记忆形成）。
type taskResult struct {
	Status        string
	ActionType    string
	Decision      string
	TargetType    string
	TargetID      *int64
	ResultRefType string
	ResultRefID   *int64
	ProviderID    *int64
	Model         string
	ErrorType     string
	ErrorMessage  string
	// Outcome 是成功执行的动作结果；仅 succeeded 时非空。
	Outcome *actionOutcome
	// Retryable 表示可恢复失败，作业层应按退避重试（最多 maxTaskAttempts 次）。
	Retryable bool
}

// HandleAITaskJob 执行 jobs.TypeAITask 作业（一个作业对应一条 ai_tasks）。
// 重复投递、任务已终态、任务被取消都视为处理成功，保证重启与重试幂等。
func (s *Service) HandleAITaskJob(ctx context.Context, job jobs.Job) error {
	taskID, ok := jobRefID(job, "task_id")
	if !ok {
		return nil
	}
	pool := s.pools.Pool()
	if pool == nil {
		return fmt.Errorf("数据库尚未就绪")
	}
	task, err := scanTask(pool.QueryRow(ctx, `SELECT `+taskColumns+` FROM ai_tasks WHERE id = $1`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !taskClaimable(task.Status) {
		// succeeded / noop / rejected / canceled：不再重复执行；
		// running 是否可重新领取由 claimTask 按 started_at 是否超时决定。
		return nil
	}

	attempts, claimed, err := s.claimTask(ctx, pool, taskID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	task.Attempts = attempts
	task.Status = statusRunning

	result, runErr := s.runTask(ctx, task)
	if runErr != nil {
		// 无法判定结果（数据库等错误）：把任务置回 failed 以便下次重试，并交给作业层退避。
		if _, err := pool.Exec(ctx, `
			UPDATE ai_tasks SET status = 'failed', error = $2 WHERE id = $1 AND status = 'running'`,
			taskID, formatErrorSummary(errorTypeDatabase, runErr.Error())); err != nil {
			s.log.Error("回写 AI 任务失败状态失败", "err", err, "task_id", taskID)
		}
		return runErr
	}
	if err := s.updateTaskResult(ctx, pool, taskID, result); err != nil {
		return err
	}

	switch {
	case result.Status == statusSucceeded:
		s.afterAction(ctx, task, result)
	case result.Status == statusFailed && result.Retryable && retryPending(attempts, maxTaskAttempts):
		// 交回作业层按退避重试（作业 attempts 与任务 attempts 各自受限）。
		return fmt.Errorf("AI 任务执行失败（第 %d 次）：%s", attempts, truncateRunes(result.ErrorMessage, 200))
	}
	return nil
}

// jobRefID 读取作业引用的对象 ID：优先使用 ref_id，其次兼容 payload 中的字符串 ID。
func jobRefID(job jobs.Job, payloadKey string) (int64, bool) {
	if job.RefID != nil && *job.RefID > 0 {
		return *job.RefID, true
	}
	if len(job.Payload) == 0 {
		return 0, false
	}
	var payload map[string]any
	if err := job.PayloadJSON(&payload); err != nil {
		return 0, false
	}
	raw, ok := payload[payloadKey]
	if !ok {
		return 0, false
	}
	text, ok := raw.(string)
	if !ok {
		return 0, false
	}
	id, err := api.ParseID(text)
	if err != nil {
		return 0, false
	}
	return id, true
}

// claimTask 原子领取任务：只有 pending/failed（或超时未完成的 running）才能进入 running。
// 重新领取 running 时刷新 started_at，避免同一超时窗口内被反复领取。
func (s *Service) claimTask(ctx context.Context, q access.Querier, taskID int64) (int, bool, error) {
	var attempts int
	err := q.QueryRow(ctx, `
		UPDATE ai_tasks
		SET status = 'running', started_at = now(), attempts = attempts + 1
		WHERE id = $1 AND (
			status IN ('pending', 'failed')
			OR (status = 'running' AND started_at < now() - interval '10 minutes')
		)
		RETURNING attempts`, taskID).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return attempts, true, nil
}

// taskClaimable 判断任务状态是否值得尝试领取：
// pending/failed 直接可领，running 交给 claimTask 按超时窗口判定（重启恢复路径）。
func taskClaimable(status string) bool {
	switch status {
	case statusPending, statusFailed, statusRunning:
		return true
	default:
		return false
	}
}

// updateTaskResult 写回任务终态（决策摘要、目标、结果引用、模型与错误）。
func (s *Service) updateTaskResult(
	ctx context.Context,
	q access.Querier,
	taskID int64,
	result taskResult,
) error {
	_, err := q.Exec(ctx, `
		UPDATE ai_tasks
		SET status = $2,
		    action_type = COALESCE(NULLIF($3, ''), action_type),
		    decision_summary = $4,
		    target_type = $5,
		    target_id = $6,
		    result_ref_type = $7,
		    result_ref_id = $8,
		    provider_id = $9,
		    model = $10,
		    error = $11,
		    finished_at = now()
		WHERE id = $1`,
		taskID, result.Status, result.ActionType, truncateRunes(result.Decision, 500),
		nullString(result.TargetType), result.TargetID,
		nullString(result.ResultRefType), result.ResultRefID,
		result.ProviderID, result.Model,
		formatErrorSummary(result.ErrorType, result.ErrorMessage))
	return err
}

// runTask 重新校验开关与任务有效性，组装上下文、调用模型并执行动作。
// 返回的 error 仅表示无法判定结果的技术性错误（数据库等），业务失败都在 taskResult 中表达。
func (s *Service) runTask(ctx context.Context, task aiTask) (taskResult, error) {
	pool := s.pools.Pool()
	if pool == nil {
		return taskResult{}, fmt.Errorf("数据库尚未就绪")
	}
	now := time.Now()

	enabled, err := s.globalAIEnabled(ctx, pool)
	if err != nil {
		return taskResult{}, err
	}
	if !enabled {
		return taskResult{Status: statusCanceled, Decision: "全局 AI 开关已关闭"}, nil
	}
	rt, err := s.ai.RuntimeByAI(ctx, pool, task.AID)
	if err != nil {
		if isNotFound(err) {
			return taskResult{Status: statusCanceled, Decision: "AI 账号不存在"}, nil
		}
		return taskResult{}, err
	}
	if !rt.Profile.IsEnabled {
		return taskResult{Status: statusCanceled, Decision: "该 AI 已停用"}, nil
	}
	actor, err := loadActor(ctx, pool, task.AID)
	if err != nil {
		if isNotFound(err) {
			return taskResult{Status: statusCanceled, Decision: "AI 账号不存在"}, nil
		}
		return taskResult{}, err
	}
	if !actor.Active {
		return taskResult{Status: statusCanceled, Decision: "AI 账号已停用"}, nil
	}

	providerID := rt.Provider.ID
	result := taskResult{ProviderID: &providerID, Model: rt.Model}
	if rt.Provider.ID == 0 || !rt.Provider.IsEnabled {
		result.Status = statusFailed
		result.ErrorType = errorTypeConfig
		result.ErrorMessage = "模型接口预设不可用"
		result.Retryable = true
		return result, nil
	}
	if !withinActiveHours(rt.Behavior.ActiveHoursStart, rt.Behavior.ActiveHoursEnd, now.Hour()) {
		result.Status = statusNoop
		result.Decision = "当前不在该 AI 的启用时段"
		return result, nil
	}

	// 组装上下文（候选、记忆与当前事件），注入过的记忆会被记录并触碰。
	prompt, err := s.buildPromptInput(ctx, pool, rt, task)
	if err != nil {
		if isNotFound(err) {
			result.Status = statusRejected
			result.ErrorType = errorTypeInvalidTarget
			result.ErrorMessage = "触发来源已不可见"
			return result, nil
		}
		return taskResult{}, err
	}
	if task.TriggerType == triggerProactive && len(prompt.Candidates) == 0 {
		result.Status = statusNoop
		result.Decision = "没有合适的候选内容"
		return result, nil
	}

	systemPrompt, userPrompt := assemblePrompts(rt, prompt)
	resp, err := s.callDecisionModel(ctx, rt, callRecord{
		TaskID:  &task.ID,
		AID:     rt.AIID,
		Attempt: task.Attempts,
		Params:  rt.Params,
	}, systemPrompt, userPrompt, decisionSchema())
	if err != nil {
		status, errorType, message := classifyCallError(err)
		result.Status = status
		result.ErrorType = errorType
		result.ErrorMessage = message
		result.Retryable = status == statusFailed
		return result, nil
	}

	// 模型返回后、执行动作前再次读取全局开关、AI 启用状态与账号状态：
	// 等待模型期间管理员可能已经全局停止或停用该 AI（docs/WORKFLOWS.md 7.5.3）。
	gate, blocked, err := s.actionGate(ctx, pool, task.AID)
	if err != nil {
		return taskResult{}, err
	}
	if blocked {
		gate.ProviderID = result.ProviderID
		gate.Model = result.Model
		return gate, nil
	}

	decision, err := parseDecision(responseText(resp))
	if err != nil {
		result.Status = statusRejected
		result.Decision = "模型输出无法解析"
		if errors.Is(err, errInvalidAction) {
			result.ErrorType = errorTypeInvalidAction
		} else {
			result.ErrorType = errorTypeInvalidJSON
		}
		result.ErrorMessage = err.Error()
		return result, nil
	}
	result.Decision = decision.Reason

	if decision.Action == actionNoop {
		result.Status = statusNoop
		if result.Decision == "" {
			result.Decision = "模型选择不执行任何操作"
		}
		return result, nil
	}
	if !actionAllowedForTrigger(rt.Behavior, task.TriggerType, decision.Action) {
		result.Status = statusRejected
		result.ActionType = decision.Action
		result.ErrorType = errorTypeForbidden
		result.ErrorMessage = "该动作未在行为策略中开启"
		return result, nil
	}
	if !taskActionAllowed(task, decision.Action) {
		result.Status = statusRejected
		result.ActionType = decision.Action
		result.ErrorType = errorTypeInvalidAction
		result.ErrorMessage = "该触发类型只允许产出原创帖或引用帖"
		return result, nil
	}

	var targetScope *proactiveTargetScope
	if task.TriggerType == triggerProactive {
		targetScope = buildProactiveTargetScope(prompt.Candidates)
	}
	target, skip, err := s.validateDecisionTarget(ctx, pool, rt.AIID, rt.Behavior, decision, targetScope)
	if err != nil {
		return taskResult{}, err
	}
	if skip != "" {
		result.Status = statusRejected
		result.ActionType = decision.Action
		result.ErrorType = errorTypeInvalidTarget
		result.ErrorMessage = skip
		return result, nil
	}
	if target.Type != "" && target.ID > 0 {
		targetID := target.ID
		result.TargetType = target.Type
		result.TargetID = &targetID
	}
	if skip, err := s.limitReached(ctx, pool, rt, task.TriggerType, decision.Action, targetRefID(target), now); err != nil {
		return taskResult{}, err
	} else if skip != "" {
		result.Status = statusRejected
		result.ActionType = decision.Action
		result.ErrorType = errorTypeRateLimited
		result.ErrorMessage = skip
		return result, nil
	}

	// 执行动作：动作、计数与任务结果在同一事务内提交。
	tx, err := pool.Begin(ctx)
	if err != nil {
		return taskResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	outcome, err := s.executeDecision(ctx, tx, rt, task, decision, target, now)
	if err != nil {
		status, errorType := classifyActionError(err)
		result.Status = status
		result.ActionType = decision.Action
		result.ErrorType = errorType
		result.ErrorMessage = err.Error()
		result.Retryable = status == statusFailed
		return result, nil
	}
	if err := s.finalizeAction(ctx, tx, rt, outcome, now); err != nil {
		return taskResult{}, err
	}
	result.Status = statusSucceeded
	result.ActionType = outcome.ActionType
	result.Decision = firstNonEmpty(decision.Reason, outcome.Summary)
	if task.ScheduledAt != nil {
		// 定时发布时优先写清“计划发布时间”，避免摘要被模型理由淹没。
		result.Decision = firstNonEmpty(outcome.Summary, decision.Reason)
	}
	if outcome.TargetType != "" {
		result.TargetType = outcome.TargetType
	}
	if outcome.TargetID != nil {
		result.TargetID = outcome.TargetID
	}
	result.ResultRefType = outcome.ResultRefType
	result.ResultRefID = outcome.ResultRefID
	result.Outcome = &outcome

	if err := s.updateTaskResult(ctx, tx, task.ID, result); err != nil {
		return taskResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return taskResult{}, err
	}
	return result, nil
}

// afterAction 在动作提交之后做后续处理：形成近期记忆、复核来源记忆与沉淀投递。
// 全部失败只记日志，不影响已经成功提交的动作（需求 7.5.6）。
func (s *Service) afterAction(ctx context.Context, task aiTask, result taskResult) {
	if result.Outcome == nil {
		return
	}
	s.log.Info("AI 任务执行成功",
		"task_id", task.ID, "ai_id", task.AID, "action", result.ActionType,
		"summary", truncateRunes(result.Decision, 200))

	if err := s.formRecentMemory(ctx, task, result); err != nil {
		s.log.Warn("形成近期记忆失败", "err", err, "task_id", task.ID, "ai_id", task.AID)
	}
	if err := s.recheckSourceMemories(ctx, task.AID); err != nil {
		s.log.Warn("复核来源记忆失败", "err", err, "ai_id", task.AID)
	}
	if err := s.maybeEnqueueConsolidation(ctx, task.AID, time.Now()); err != nil {
		s.log.Warn("投递记忆沉淀作业失败", "err", err, "ai_id", task.AID)
	}
}

// targetRefID 返回用于冷却判断的目标 ID：帖子、评论与用户都参与目标级冷却；
// 原创帖等没有目标 ID 的动作返回 nil，只受动作级冷却约束。
func targetRefID(target decisionTarget) *int64 {
	switch target.Type {
	case targetPost, targetComment, targetUser:
	default:
		return nil
	}
	if target.ID <= 0 {
		return nil
	}
	id := target.ID
	return &id
}

// responseText 安全读取响应正文。
func responseText(resp *callable.Response) string {
	if resp == nil {
		return ""
	}
	return resp.Text
}
