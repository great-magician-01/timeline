package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// taskResult 是任务终态内部结构。
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
	MemoryNote    string
	Outcome       *actionOutcome
	Retryable     bool
}

type targetRef struct {
	Type     string
	ID       int64
	PostID   int64
	AuthorID int64
}

// promptInput 是决策上下文。
type promptInput struct {
	Event      string
	Candidates []contracts.PostCandidate
	Thread     []contracts.CommentBrief
	Recent     []contracts.Memory
	LongTerm   []contracts.Memory
	Liked      []contracts.LikedPostRef
	Followees  []contracts.FolloweeRef
	// Group 是群聊任务的上下文（群名 + 最近消息）；非群聊任务为 nil。
	Group *groupPromptContext
}

// maxTaskAttempts 是可恢复任务的最大执行次数（与作业层 MaxAttempts 语义一致）。
const maxTaskAttempts = 3

// RunTask 执行一条 AI 任务（作业处理器与人工生成共用）。
func (s *Service) RunTask(ctx context.Context, taskID int64) error {
	now := s.now()
	// 领取与读取合并为一次 UPDATE ... RETURNING，不再额外 GetTask 往返。
	task, claimed, err := s.deps.Tasks.ClaimTask(ctx, taskID, now, taskLease)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	attempts := task.Attempts
	result := taskResult{ActionType: domain.ActionNoop}
	finalize := func() error { return s.finishTask(ctx, task, result, attempts) }

	values, err := s.deps.Settings.Settings(ctx)
	if err != nil {
		return s.failTaskInfrastructure(ctx, task.ID, err)
	}
	if !values.AIGlobalEnabled {
		result.Status = domain.TaskStatusCanceled
		result.Decision = "全局 AI 开关已关闭"
		return finalize()
	}
	rt, err := s.deps.AI.RuntimeByAI(ctx, task.AIID)
	if err != nil {
		if isNotFound(err) {
			result.Status = domain.TaskStatusCanceled
			result.Decision = "AI 账号不存在"
			return finalize()
		}
		return s.failTaskInfrastructure(ctx, task.ID, err)
	}
	if !rt.Profile.IsEnabled {
		result.Status = domain.TaskStatusCanceled
		result.Decision = "该 AI 已停用"
		return finalize()
	}
	actor, err := s.deps.Users.GetUser(ctx, task.AIID)
	if err != nil {
		if isNotFound(err) {
			result.Status = domain.TaskStatusCanceled
			result.Decision = "AI 账号不存在"
			return finalize()
		}
		return s.failTaskInfrastructure(ctx, task.ID, err)
	}
	if !actor.IsActive() {
		result.Status = domain.TaskStatusCanceled
		result.Decision = "AI 账号已停用"
		return finalize()
	}
	result.ProviderID = ptrInt64(rt.Provider.ID)
	result.Model = rt.Model
	if !domain.WithinActiveHours(rt.Behavior.ActiveHoursStart, rt.Behavior.ActiveHoursEnd, now.Hour()) {
		result.Status = domain.TaskStatusNoop
		result.Decision = "当前不在该 AI 的启用时段"
		return finalize()
	}

	prompt, refs, err := s.buildPrompt(ctx, task, rt)
	if err != nil {
		if isNotFound(err) {
			result.Status = domain.TaskStatusRejected
			result.ErrorType = domain.ErrorTypeInvalidTarget
			result.ErrorMessage = "触发来源已不可见"
			return finalize()
		}
		return s.failTaskInfrastructure(ctx, task.ID, err)
	}
	if task.TriggerType == domain.TriggerProactive && len(prompt.Candidates) == 0 {
		result.Status = domain.TaskStatusNoop
		result.Decision = "没有合适的候选内容"
		return finalize()
	}
	// 主动互动任务的候选集合就是本次动作的授权范围，模型返回后必须再次比对。
	var scope *proactiveTargetScope
	if task.TriggerType == domain.TriggerProactive {
		scope = buildProactiveTargetScope(prompt.Candidates)
	}
	s.injectMemoryRefs(ctx, task, refs)

	systemPrompt, userPrompt := s.assemblePrompts(rt, prompt)
	resp, err := s.callDecisionModel(ctx, rt, task.ID, prompt, systemPrompt, userPrompt, decisionFormat())
	if err != nil {
		status, errorType, retryable := classifyModelError(err)
		result.Status = status
		result.ErrorType = errorType
		result.ErrorMessage = truncateRunes(err.Error(), 500)
		result.Retryable = retryable && domain.RetryPending(attempts, maxTaskAttempts)
		return finalize()
	}
	// 等待模型期间管理员可能已经全局停止或停用该 AI：执行动作前重新读取开关与账号状态
	// （docs/WORKFLOWS.md 7.5 第 3 条），不信任任务开始时的快照。
	gate, blocked, gateErr := s.actionGate(ctx, task.AIID)
	if gateErr != nil {
		return s.failTaskInfrastructure(ctx, task.ID, gateErr)
	}
	if blocked {
		gate.ProviderID = result.ProviderID
		gate.Model = result.Model
		result = gate
		return finalize()
	}
	decision, err := domain.ParseDecision(resp.Text)
	if err != nil {
		result.Status = domain.TaskStatusRejected
		result.ErrorType = domain.ErrorTypeInvalidJSON
		result.ErrorMessage = "模型输出无法解析"
		return finalize()
	}
	result.Decision = decision.Reason
	if decision.Action == domain.ActionNoop {
		result.Status = domain.TaskStatusNoop
		if result.Decision == "" {
			result.Decision = "模型选择不执行任何操作"
		}
		return finalize()
	}
	if !domain.ActionAllowedForTrigger(rt.Behavior, task.TriggerType, decision.Action) {
		result.Status = domain.TaskStatusRejected
		result.ActionType = decision.Action
		result.ErrorType = domain.ErrorTypeForbidden
		result.ErrorMessage = "该动作未在行为策略中开启"
		return finalize()
	}
	if !taskActionAllowed(task, decision.Action) {
		result.Status = domain.TaskStatusRejected
		result.ActionType = decision.Action
		result.ErrorType = domain.ErrorTypeInvalidTarget
		result.ErrorMessage = "该触发类型不允许该动作"
		return finalize()
	}
	if skip, err := s.limitReached(ctx, rt.AIID, rt.Behavior, task.TriggerType, decision.Action, now); err != nil {
		return s.failTaskInfrastructure(ctx, task.ID, err)
	} else if skip != "" {
		result.Status = domain.TaskStatusRejected
		result.ActionType = decision.Action
		result.ErrorType = domain.ErrorTypeRateLimited
		result.ErrorMessage = skip
		return finalize()
	}
	target, skip, err := s.validateTarget(ctx, rt, task, decision, scope)
	if err != nil {
		if isNotFound(err) {
			result.Status = domain.TaskStatusRejected
			result.ActionType = decision.Action
			result.ErrorType = domain.ErrorTypeInvalidTarget
			result.ErrorMessage = "目标内容不存在或不可见"
			return finalize()
		}
		return s.failTaskInfrastructure(ctx, task.ID, err)
	}
	if skip != "" {
		result.Status = domain.TaskStatusRejected
		result.ActionType = decision.Action
		result.ErrorType = domain.ErrorTypeInvalidTarget
		result.ErrorMessage = skip
		return finalize()
	}

	err = s.deps.Tx.WithTx(ctx, func(txCtx context.Context) error {
		outcome, execErr := s.executeAction(txCtx, rt, task, decision, target, now)
		if execErr != nil {
			return execErr
		}
		result.Status = domain.TaskStatusSucceeded
		result.ActionType = outcome.ActionType
		result.TargetType = outcome.TargetType
		result.TargetID = outcome.TargetID
		result.ResultRefType = outcome.ResultRefType
		result.ResultRefID = outcome.ResultRefID
		result.Outcome = &outcome
		result.MemoryNote = outcome.MemoryNote
		if outcome.ActionType != "" && outcome.ActionType != domain.ActionNoop && !outcome.Skipped {
			if err := s.bumpCounter(txCtx, rt.AIID, outcome.ActionType, now); err != nil {
				return err
			}
		}
		if err := s.updateTaskResult(txCtx, task.ID, result, now); err != nil {
			return err
		}
		if err := s.formMemory(txCtx, task, rt, result, now); err != nil {
			s.deps.Log.Warn("形成记忆失败", "err", err, "task_id", task.ID)
		}
		return nil
	})
	if err != nil {
		// 动作执行失败：业务规则拒绝（目标不存在/无权/已删除/限频）不重试，
		// 只有基础设施或依赖不可用错误才交给作业层有限重试。
		status, errorType, retryable := classifyActionError(err)
		result.Status = status
		result.ErrorType = errorType
		result.ErrorMessage = truncateRunes(err.Error(), 500)
		result.Retryable = retryable && domain.RetryPending(attempts, maxTaskAttempts)
		return finalize()
	}
	s.afterAction(ctx, task, result)
	return nil
}

// classifyActionError 把执行动作时的错误归类为任务终态与错误类型。
func classifyActionError(err error) (status, errorType string, retryable bool) {
	var appErr *apperr.Error
	if errors.As(err, &appErr) {
		switch appErr.Kind {
		case apperr.KindNotFound:
			return domain.TaskStatusRejected, domain.ErrorTypeInvalidTarget, false
		case apperr.KindForbidden, apperr.KindUnauthenticated, apperr.KindSuspended:
			return domain.TaskStatusRejected, domain.ErrorTypeForbidden, false
		case apperr.KindInvalid, apperr.KindValidation, apperr.KindConflict:
			return domain.TaskStatusRejected, domain.ErrorTypeInvalidTarget, false
		case apperr.KindRateLimited:
			return domain.TaskStatusRejected, domain.ErrorTypeRateLimited, false
		case apperr.KindUnavailable:
			return domain.TaskStatusFailed, domain.ErrorTypeConfig, true
		}
	}
	return domain.TaskStatusFailed, domain.ErrorTypeDatabase, true
}

// failTaskInfrastructure 把无法判定结果的技术错误写回 failed 并返回原因，
// 交由作业层按退避重试；绝不把任务留在 running（docs/WORKFLOWS.md 7.5）。
func (s *Service) failTaskInfrastructure(ctx context.Context, taskID int64, cause error) error {
	at := s.now()
	message := truncateRunes(domain.ErrorTypeDatabase+": "+cause.Error(), 500)
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callLogWriteTimeout)
	defer cancel()
	if err := s.deps.Tasks.SetTaskStatus(writeCtx, taskID, domain.TaskStatusFailed, message, &at); err != nil {
		s.deps.Log.Warn("回写 AI 任务失败状态失败", "err", err, "task_id", taskID)
	}
	return cause
}

// finishTask 写回任务终态；可恢复失败返回错误，交给作业层按退避有限重试。
func (s *Service) finishTask(ctx context.Context, task repo.Task, result taskResult, attempts int) error {
	if result.Status == "" {
		result.Status = domain.TaskStatusFailed
	}
	now := s.now()
	if err := s.updateTaskResult(ctx, task.ID, result, now); err != nil {
		return err
	}
	if result.Status == domain.TaskStatusFailed && result.Retryable {
		return fmt.Errorf("AI 任务可恢复失败（第 %d 次）：%s",
			attempts, truncateRunes(result.ErrorMessage, 200))
	}
	return nil
}

// afterAction 在动作成功提交后做后续处理；全部失败只记日志，不影响已提交的动作。
func (s *Service) afterAction(ctx context.Context, task repo.Task, result taskResult) {
	now := s.now()
	if result.Status == domain.TaskStatusSucceeded {
		if err := s.maybeEnqueueConsolidation(ctx, task.AIID, now); err != nil {
			s.deps.Log.Warn("投递记忆沉淀作业失败", "err", err, "ai_id", task.AIID)
		}
	}
	// 来源内容可能已被删除或改为非公开：复核并停用相关记忆（失败不影响动作）。
	if err := s.recheckSourceMemories(ctx, task.AIID); err != nil {
		s.deps.Log.Warn("复核记忆来源失败", "err", err, "ai_id", task.AIID)
	}
}

func (s *Service) updateTaskResult(ctx context.Context, taskID int64, result taskResult, at time.Time) error {
	errorMessage := ""
	if result.ErrorType != "" || result.ErrorMessage != "" {
		errorMessage = truncateRunes(result.ErrorType+": "+result.ErrorMessage, 500)
	}
	return s.deps.Tasks.UpdateTaskResult(ctx, repo.TaskResultUpdate{
		TaskID:          taskID,
		Status:          result.Status,
		ActionType:      result.ActionType,
		DecisionSummary: truncateRunes(result.Decision, 500),
		TargetType:      result.TargetType,
		TargetID:        result.TargetID,
		ResultRefType:   result.ResultRefType,
		ResultRefID:     result.ResultRefID,
		ProviderID:      result.ProviderID,
		Model:           result.Model,
		Error:           errorMessage,
		FinishedAt:      at,
	})
}

// actionOutcome 描述一次动作执行结果。
type actionOutcome struct {
	ActionType    string
	Summary       string
	MemoryNote    string
	TargetType    string
	TargetID      *int64
	ResultRefType string
	ResultRefID   *int64
	Skipped       bool
}

// executeAction 经 contracts 端口执行动作（必须在事务 ctx 内调用）。
func (s *Service) executeAction(ctx context.Context, rt contracts.Runtime, task repo.Task,
	decision domain.Decision, target targetRef, now time.Time) (actionOutcome, error) {
	outcome := actionOutcome{ActionType: decision.Action}
	switch decision.Action {
	case domain.ActionOriginalPost, domain.ActionQuote:
		input := contracts.ActorPostInput{
			AuthorID:      rt.AIID,
			ContentFormat: "markdown",
			ContentSource: decision.Content,
			Visibility:    model.VisibilityPublic,
			// 该路径作者恒为 AI 账号（rt.AIID）；写入作者类型快照供时间线按 AI 筛选。
			AuthorIsAI:  true,
			AsDraft:     task.TriggerRefType == domain.GenerationModeDraft,
			ScheduledAt: task.ScheduledAt,
			At:          now,
		}
		if decision.Action == domain.ActionQuote {
			postID := target.ID
			input.QuotedPostID = &postID
		}
		post, err := s.deps.Content.CreatePostForActor(ctx, input)
		if err != nil {
			return outcome, err
		}
		postID := post.ID
		outcome.ResultRefType = domain.RefPost
		outcome.ResultRefID = &postID
		switch {
		case input.AsDraft:
			outcome.Summary = "已生成 AI 草稿，等待管理员发布"
		case task.ScheduledAt != nil:
			outcome.Summary = "已创建定时 AI 帖子"
		case decision.Action == domain.ActionQuote:
			outcome.Summary = "引用转发了帖子 " + apiID(postID)
			outcome.MemoryNote = "引用转发了一条公开帖子：" + truncateRunes(decision.Content, 200)
		default:
			outcome.Summary = "发布了原创帖 " + apiID(postID)
			outcome.MemoryNote = "发布了一条原创帖：" + truncateRunes(decision.Content, 200)
		}
		return outcome, nil
	case domain.ActionComment, domain.ActionReply:
		input := contracts.ActorCommentInput{
			ActorID:       rt.AIID,
			IsAI:          true,
			PostID:        target.PostID,
			ContentFormat: "markdown",
			ContentSource: decision.Content,
			At:            now,
		}
		if decision.Action == domain.ActionReply {
			parentID := target.ID
			input.ParentID = &parentID
		}
		comment, err := s.deps.Social.CreateCommentForActor(ctx, input)
		if err != nil {
			return outcome, err
		}
		commentID := comment.ID
		outcome.ResultRefType = domain.RefComment
		outcome.ResultRefID = &commentID
		targetID := target.PostID
		outcome.TargetType = domain.RefPost
		outcome.TargetID = &targetID
		outcome.Summary = "评论了帖子 " + apiID(target.PostID)
		outcome.MemoryNote = "评论了一条公开内容：" + truncateRunes(decision.Content, 200)
		return outcome, nil
	case domain.ActionLike, domain.ActionUnlike:
		targetType := decision.TargetType
		if targetType == domain.RefUser || targetType == "" {
			targetType = domain.RefPost
		}
		input := contracts.LikeInput{ActorID: rt.AIID, TargetType: targetType, TargetID: target.ID, At: now}
		var active bool
		var count int
		var err error
		if decision.Action == domain.ActionLike {
			active, count, err = s.deps.Social.LikeForActor(ctx, input)
		} else {
			active, count, err = s.deps.Social.UnlikeForActor(ctx, input)
		}
		if err != nil {
			return outcome, err
		}
		outcome.Skipped = active == (decision.Action == domain.ActionUnlike)
		targetTypeCopy := target.Type
		outcome.TargetType = targetTypeCopy
		outcome.TargetID = &target.ID
		outcome.Summary = fmt.Sprintf("%s %s（当前计数 %d）", decision.Action, apiID(target.ID), count)
		outcome.MemoryNote = "对内容执行了 " + decision.Action
		return outcome, nil
	case domain.ActionFollow, domain.ActionUnfollow:
		input := contracts.FollowInput{ActorID: rt.AIID, TargetID: target.ID, At: now}
		var state string
		var err error
		if decision.Action == domain.ActionFollow {
			state, err = s.deps.Social.FollowForActor(ctx, input)
		} else {
			state, err = s.deps.Social.UnfollowForActor(ctx, input)
		}
		if err != nil {
			return outcome, err
		}
		outcome.TargetType = domain.RefUser
		outcome.TargetID = &target.ID
		outcome.Summary = fmt.Sprintf("%s 用户 %s（状态 %s）", decision.Action, apiID(target.ID), state)
		outcome.MemoryNote = "对用户执行了 " + decision.Action
		return outcome, nil
	case domain.ActionRepost:
		postID := target.PostID
		if postID <= 0 {
			postID = target.ID
		}
		input := contracts.RepostInput{ActorID: rt.AIID, PostID: postID, At: now}
		_, count, err := s.deps.Social.RepostForActor(ctx, input)
		if err != nil {
			return outcome, err
		}
		outcome.TargetType = domain.RefPost
		outcome.TargetID = &postID
		outcome.Summary = fmt.Sprintf("转发了帖子 %s（当前计数 %d）", apiID(postID), count)
		outcome.MemoryNote = "转发了一条公开帖子"
		return outcome, nil
	case domain.ActionGroupMessage:
		// AI 代发群消息：与人类发送同一校验与渲染管线（group app 内部路径）。
		// 首期群消息不写入分层记忆，只记录任务结果。
		if s.deps.GroupWriter == nil {
			return outcome, apperr.Internal("群消息写入依赖未装配")
		}
		messageID, err := s.deps.GroupWriter.WriteFromAI(ctx, contracts.GroupMessageInput{
			GroupID:       target.ID,
			SenderID:      rt.AIID,
			ContentFormat: "markdown",
			ContentSource: decision.Content,
		})
		if err != nil {
			return outcome, err
		}
		groupID := target.ID
		outcome.ResultRefType = domain.RefGroup
		outcome.ResultRefID = &messageID
		outcome.TargetType = domain.RefGroup
		outcome.TargetID = &groupID
		outcome.Summary = "在群聊 " + apiID(groupID) + " 发言（message_id=" + apiID(messageID) + "）"
		return outcome, nil
	default:
		return outcome, apperr.Validation("不支持的动作")
	}
}

// taskActionAllowed 判断触发类型与动作的组合是否允许：
// 管理端生成与定时原创是内容生成任务，只允许产出原创帖/引用帖（noop 在上游处理）；
// 提及与评论触发只允许被动回复类动作；群聊任务（@提及/自主发言）只允许群消息。
func taskActionAllowed(task repo.Task, action string) bool {
	switch task.TriggerType {
	case domain.TriggerManual, domain.TriggerSchedule:
		return action == domain.ActionOriginalPost || action == domain.ActionQuote
	case domain.TriggerMention, domain.TriggerComment:
		return action == domain.ActionReply || action == domain.ActionComment || action == domain.ActionLike
	case domain.TriggerGroupMention, domain.TriggerGroupSchedule:
		return action == domain.ActionGroupMessage
	default:
		return true
	}
}

// buildPrompt 组装决策上下文与注入的记忆引用。
func (s *Service) buildPrompt(ctx context.Context, task repo.Task, rt contracts.Runtime) (promptInput, []repo.MemoryRef, error) {
	prompt := promptInput{}
	var refs []repo.MemoryRef
	// 触发事件来源：只接受公开可见内容（AI 访问者视角），非公开内容不得进入模型上下文。
	switch {
	case task.TriggerRefType == domain.RefComment && task.TriggerRefID != nil:
		comment, err := s.deps.Social.GetComment(ctx, *task.TriggerRefID)
		if err != nil {
			return prompt, nil, err
		}
		if comment.Deleted() {
			return prompt, nil, apperr.NotFound("评论不存在或已删除")
		}
		if _, err := s.deps.Content.GetVisiblePost(ctx, model.GuestViewer(), comment.PostID); err != nil {
			return prompt, nil, apperr.NotFound("评论所在内容不存在或不可见")
		}
		prompt.Event = "你收到一条评论（comment_id=" + apiID(comment.ID) + "）：\n" + truncateRunes(comment.PlainText, 800)
		thread, err := s.deps.Social.ListRecentComments(ctx, contracts.CommentListQuery{PostID: comment.PostID, Limit: 10})
		if err == nil {
			prompt.Thread = thread
		}
	case task.TriggerRefType == domain.RefPost && task.TriggerRefID != nil:
		post, err := s.deps.Content.GetVisiblePost(ctx, model.GuestViewer(), *task.TriggerRefID)
		if err != nil {
			return prompt, nil, err
		}
		excerpt, _ := s.deps.Content.GetPostExcerpt(ctx, post.ID)
		prompt.Event = "事件来源帖子（post_id=" + apiID(post.ID) + "）：\n" + truncateRunes(excerpt, 800)
	case task.TriggerRefType == domain.RefGroup && task.TriggerRefID != nil:
		// 群聊任务（@提及/自主发言）：任务执行时重新授权成员资格与群状态。
		if s.deps.Groups == nil {
			return prompt, nil, apperr.Internal("群聊上下文依赖未装配")
		}
		group, err := s.buildGroupPrompt(ctx, *task.TriggerRefID)
		if err != nil {
			return prompt, nil, err
		}
		prompt.Group = &group
	}
	// 候选内容（主动互动）。
	if task.TriggerType == domain.TriggerProactive {
		candidates, err := s.deps.Content.ListCandidatePosts(ctx, contracts.CandidateQuery{
			ExcludeAuthorID: rt.AIID,
			AllowAIToAI:     rt.Behavior.AllowAIToAI,
			ScopeType:       rt.Behavior.CandidateScopeType,
			ScopeTags:       parseStringList(rt.Behavior.CandidateScopeParams, "tags"),
			ScopeUsernames:  parseStringList(rt.Behavior.CandidateScopeParams, "usernames"),
			Limit:           maxCandidateCount,
		})
		if err != nil {
			return prompt, nil, err
		}
		prompt.Candidates = candidates
	}
	// 记忆。
	recent, err := s.deps.AI.RecentMemories(ctx, rt.AIID, maxRecentMemoryCount)
	if err != nil {
		return prompt, nil, err
	}
	prompt.Recent = recent
	keywords := topicKeywords(prompt)
	longTerm, err := s.deps.AI.SearchLongTerm(ctx, contracts.SearchLongTermInput{
		AIID: rt.AIID, Keywords: keywords, Limit: maxLongTermMemoryCount,
	})
	if err != nil {
		return prompt, nil, err
	}
	prompt.LongTerm = longTerm
	for _, memory := range append(append([]contracts.Memory{}, recent...), longTerm...) {
		refs = append(refs, repo.MemoryRef{MemoryID: memory.ID, MemoryType: memory.MemoryType})
	}
	if len(refs) > 0 {
		if err := s.deps.AI.TouchMemories(ctx, memoryIDs(refs), s.now()); err != nil {
			s.deps.Log.Warn("更新记忆使用时间失败", "err", err)
		}
	}
	// 可取消关系（点赞/关注）。
	if rt.Behavior.AllowLike {
		if liked, err := s.deps.Social.ListRecentLikedPosts(ctx, rt.AIID, maxCandidateCount); err == nil {
			prompt.Liked = liked
		}
	}
	if rt.Behavior.AllowUnfollow {
		if followees, err := s.deps.Social.ListRecentFollowees(ctx, rt.AIID, maxCandidateCount); err == nil {
			prompt.Followees = followees
		}
	}
	return prompt, refs, nil
}

// injectMemoryRefs 把注入过的记忆写入 ai_task_memory_refs（失败仅记日志）。
func (s *Service) injectMemoryRefs(ctx context.Context, task repo.Task, refs []repo.MemoryRef) {
	if len(refs) == 0 {
		return
	}
	seed, err := s.newID()
	if err != nil {
		return
	}
	if err := s.deps.Tasks.AddMemoryRefs(ctx, task.ID, refs, seed); err != nil {
		s.deps.Log.Warn("写入任务记忆引用失败", "err", err)
	}
}

// assemblePrompts 组装 system/user prompt。
func (s *Service) assemblePrompts(rt contracts.Runtime, prompt promptInput) (string, string) {
	var system strings.Builder
	system.WriteString("你是时间线社区中的 AI 用户「")
	system.WriteString(firstNonEmpty(rt.Nickname, rt.Username))
	system.WriteString("」。只以第一人称行动，遵守社区规则；只能输出给定的 JSON 决策结构；")
	system.WriteString("无法产出合适动作时必须输出 action=noop。禁止编造不存在的 ID，禁止假设自己拥有未列出的权限。\n")
	if rt.Profile.SystemPrompt != "" {
		system.WriteString("角色设定：\n")
		system.WriteString(truncateRunes(rt.Profile.SystemPrompt, 2000))
		system.WriteString("\n")
	}
	if rt.Profile.Personality != "" {
		system.WriteString("性格：" + truncateRunes(rt.Profile.Personality, 500) + "\n")
	}
	system.WriteString("可用动作：" + actionCatalog(rt.Behavior) + "\n")
	var user strings.Builder
	if prompt.Event != "" {
		user.WriteString("【触发事件】\n" + prompt.Event + "\n\n")
	}
	if len(prompt.Thread) > 0 {
		user.WriteString("【最近讨论】\n")
		for _, comment := range prompt.Thread {
			user.WriteString("- " + comment.AuthorName + "：" + truncateRunes(comment.Content, 200) + "\n")
		}
		user.WriteString("\n")
	}
	if len(prompt.Candidates) > 0 {
		user.WriteString("【候选公开内容】（只能选择下列 post_id）\n")
		for _, candidate := range prompt.Candidates {
			user.WriteString("- post_id=" + apiID(candidate.Post.ID) + " 作者=" +
				firstNonEmpty(candidate.AuthorName, candidate.Username) + "：" + truncateRunes(candidate.Summary, 200) + "\n")
		}
		user.WriteString("\n")
	}
	if prompt.Group != nil {
		user.WriteString(groupPromptSection(*prompt.Group) + "\n\n")
	}
	if len(prompt.Recent) > 0 || len(prompt.LongTerm) > 0 {
		user.WriteString("【你的记忆】\n")
		for _, memory := range prompt.Recent {
			user.WriteString("- (近期) " + truncateRunes(memory.Content, 300) + "\n")
		}
		for _, memory := range prompt.LongTerm {
			user.WriteString("- (长期) " + truncateRunes(memory.Content, 300) + "\n")
		}
		user.WriteString("\n")
	}
	if len(prompt.Liked) > 0 || len(prompt.Followees) > 0 {
		user.WriteString("【可取消的关系】（只能取消下列目标）\n")
		for _, item := range prompt.Liked {
			user.WriteString("- 点赞 post_id=" + apiID(item.PostID) + "：" + truncateRunes(item.Summary, 120) + "\n")
		}
		for _, item := range prompt.Followees {
			user.WriteString("- 关注 user_id=" + apiID(item.UserID) + "：@" + item.Username + "\n")
		}
		user.WriteString("\n")
	}
	user.WriteString("请输出一个 JSON 决策对象。")
	return system.String(), user.String()
}

// parseStringList 解析 candidate_scope_params 的字符串数组键。
func parseStringList(raw []byte, key string) []string {
	if len(raw) == 0 {
		return nil
	}
	values := map[string][]string{}
	if err := jsonUnmarshal(raw, &values); err != nil {
		return nil
	}
	return values[key]
}

// topicKeywords 从上下文提取检索关键词。
func topicKeywords(prompt promptInput) []string {
	text := prompt.Event
	for _, candidate := range prompt.Candidates {
		text += " " + candidate.Summary
	}
	return domainKeywords(text)
}

// actionCatalog 生成可用动作提示。
func actionCatalog(behavior contracts.BehaviorSettings) string {
	actions := make([]string, 0, 8)
	for _, action := range []string{
		domain.ActionOriginalPost, domain.ActionQuote, domain.ActionComment, domain.ActionReply,
		domain.ActionLike, domain.ActionUnlike, domain.ActionFollow, domain.ActionUnfollow,
		domain.ActionRepost, domain.ActionGroupMessage, domain.ActionNoop,
	} {
		if domain.ActionAllowed(behavior, action) {
			actions = append(actions, action)
		}
	}
	return strings.Join(actions, "、")
}

// apiID 把雪花 ID 序列化为字符串（与 shared/format 一致，避免 JavaScript 精度丢失）。
func apiID(id int64) string { return strconv.FormatInt(id, 10) }

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// isNotFound 判断领域错误是否为 NotFound。
func isNotFound(err error) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Kind == apperr.KindNotFound
}

// jsonUnmarshal 是 json.Unmarshal 的薄封装（便于统一替换为严格解码）。
func jsonUnmarshal(data []byte, dst any) error { return json.Unmarshal(data, dst) }
