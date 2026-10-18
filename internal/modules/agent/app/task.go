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
	"github.com/great-magician-01/timeline/internal/platform/clock"
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
}

// maxTaskAttempts 是可恢复任务的最大执行次数（与作业层 MaxAttempts 语义一致）。
const maxTaskAttempts = 3

// RunTask 执行一条 AI 任务（作业处理器与人工生成共用）。
func (s *Service) RunTask(ctx context.Context, taskID int64) error {
	now := s.now()
	attempts, claimed, err := s.deps.Tasks.ClaimTask(ctx, taskID, now, taskLease)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	task, err := s.deps.Tasks.GetTask(ctx, taskID)
	if err != nil {
		// 已领取却读不到任务：标记失败并交给作业层重试，不能把任务留在 running。
		return s.failTaskInfrastructure(ctx, taskID, err)
	}
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
			AsDraft:       task.TriggerRefType == domain.GenerationModeDraft,
			ScheduledAt:   task.ScheduledAt,
			At:            now,
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
	default:
		return outcome, apperr.Validation("不支持的动作")
	}
}

// taskActionAllowed 判断触发类型与动作的组合是否允许：
// 管理端生成与定时原创是内容生成任务，只允许产出原创帖/引用帖（noop 在上游处理）；
// 提及与评论触发只允许被动回复类动作。
func taskActionAllowed(task repo.Task, action string) bool {
	switch task.TriggerType {
	case domain.TriggerManual, domain.TriggerSchedule:
		return action == domain.ActionOriginalPost || action == domain.ActionQuote
	case domain.TriggerMention, domain.TriggerComment:
		return action == domain.ActionReply || action == domain.ActionComment || action == domain.ActionLike
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

// formMemory 在动作成功后形成一条近期记忆并记录来源。
func (s *Service) formMemory(ctx context.Context, task repo.Task, rt contracts.Runtime, result taskResult, at time.Time) error {
	if result.Outcome == nil || result.Outcome.MemoryNote == "" {
		return nil
	}
	keywords := domainKeywords(result.Outcome.MemoryNote)
	memory, err := s.deps.AI.CreateMemory(ctx, contracts.CreateMemoryInput{
		AIID:           rt.AIID,
		MemoryType:     "recent",
		Content:        truncateRunes(result.Outcome.MemoryNote, 1000),
		Category:       "event",
		Keywords:       keywords,
		Importance:     3,
		SourceType:     "action",
		SourceRefType:  result.Outcome.ResultRefType,
		SourceRefID:    result.Outcome.ResultRefID,
		InferenceLevel: "fact",
		At:             at,
	})
	if err != nil {
		return err
	}
	if result.Outcome.ResultRefType != "" && result.Outcome.ResultRefID != nil {
		if err := s.deps.AI.AddMemorySources(ctx, memory.ID, []contracts.MemorySourceInput{{
			MemoryID:   memory.ID,
			SourceType: result.Outcome.ResultRefType,
			SourceID:   *result.Outcome.ResultRefID,
			At:         at,
		}}); err != nil {
			return err
		}
	}
	return nil
}

// maybeEnqueueConsolidation 近期记忆达到阈值时投递沉淀作业（按 AI + 日期幂等）。
func (s *Service) maybeEnqueueConsolidation(ctx context.Context, aiID int64, at time.Time) error {
	count, err := s.deps.AI.CountRecentMemories(ctx, aiID)
	if err != nil || count < consolidationThreshold {
		return err
	}
	key := "memory_consolidation:" + apiID(aiID) + ":" + at.UTC().Format("20060102")
	return s.deps.Jobs.Enqueue(ctx, contracts.JobEnqueue{
		Type:           contracts.JobTypeMemoryConsolidate,
		RefType:        domain.RefAI,
		RefID:          &aiID,
		IdempotencyKey: key,
		Payload:        jobPayload{AIID: aiID},
		RunAt:          at,
		MaxAttempts:    3,
		At:             at,
	})
}

// maxConsolidationItemRunes 是沉淀输入中单条近期记忆的字符上限：10 条近期记忆
// 拼成不超过 ai 模块记忆正文上限（1000）的摘要，避免沉淀写入因超长被拒绝。
const maxConsolidationItemRunes = 96

// memoryContentRuneLimit 与 modules/ai/domain.MaxMemoryContentRunes 保持一致（1000）。
const memoryContentRuneLimit = 1000

// consolidateMemories 把近期记忆沉淀为一条长期记忆，并标记被取代的近期记忆。
// 沉淀正文必须满足 ai 模块的记忆约束（分类只能是 fact/preference/relationship/event/topic，
// 正文不超过 1000 字符），来源关联只允许 post/comment（0007 CHECK）。
func (s *Service) consolidateMemories(ctx context.Context, aiID int64) error {
	recent, err := s.deps.AI.RecentMemories(ctx, aiID, consolidationThreshold)
	if err != nil || len(recent) == 0 {
		return err
	}
	at := s.now()
	var builder strings.Builder
	builder.WriteString("近期经历摘要：")
	ids := make([]int64, 0, len(recent))
	for index, memory := range recent {
		if index > 0 {
			builder.WriteString("；")
		}
		builder.WriteString(truncateRunes(memory.Content, maxConsolidationItemRunes))
		ids = append(ids, memory.ID)
	}
	content := truncateRunes(builder.String(), memoryContentRuneLimit)
	longTerm, err := s.deps.AI.CreateMemory(ctx, contracts.CreateMemoryInput{
		AIID:       aiID,
		MemoryType: "long_term",
		Content:    content,
		// ai_memories.category 没有 summary；沉淀语义由 inference_level=summary 表达。
		Category:       "event",
		Keywords:       domainKeywords(content),
		Importance:     5,
		SourceType:     "consolidation",
		InferenceLevel: "summary",
		At:             at,
	})
	if err != nil {
		return err
	}
	if err := s.deps.AI.SupersedeMemories(ctx, contracts.SupersedeMemoryInput{
		AIID: aiID, MemoryIDs: ids, SupersededByID: longTerm.ID, At: at,
	}); err != nil {
		return err
	}
	// 聚合被取代近期记忆的内容来源（post/comment），写入长期记忆的来源关联供后续失效复核；
	// 没有可关联来源时跳过；来源关联失败只告警，不回滚已写入的长期记忆。
	sources := s.consolidationSources(ctx, recent)
	if len(sources) == 0 {
		return nil
	}
	inputs := make([]contracts.MemorySourceInput, 0, len(sources))
	for _, source := range sources {
		inputs = append(inputs, contracts.MemorySourceInput{
			MemoryID: longTerm.ID, SourceType: source.SourceType, SourceID: source.SourceID, At: at,
		})
	}
	if err := s.deps.AI.AddMemorySources(ctx, longTerm.ID, inputs); err != nil {
		s.deps.Log.Warn("写入沉淀记忆来源失败", "err", err, "ai_id", aiID, "memory_id", longTerm.ID)
	}
	return nil
}

// consolidationSources 聚合一批近期记忆的公开内容来源：直接引用（source_ref_*）
// 与 ai_memory_sources 关联的 post/comment，去重后返回。
func (s *Service) consolidationSources(
	ctx context.Context,
	memories []contracts.Memory,
) []contracts.MemorySourceRef {
	seen := map[string]bool{}
	refs := make([]contracts.MemorySourceRef, 0, len(memories))
	add := func(sourceType string, sourceID int64) {
		if sourceType != domain.RefPost && sourceType != domain.RefComment {
			return
		}
		if sourceID <= 0 {
			return
		}
		key := sourceType + ":" + apiID(sourceID)
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, contracts.MemorySourceRef{SourceType: sourceType, SourceID: sourceID})
	}
	ids := make([]int64, 0, len(memories))
	for _, memory := range memories {
		ids = append(ids, memory.ID)
		if memory.SourceRefID != nil {
			add(memory.SourceRefType, *memory.SourceRefID)
		}
	}
	linked, err := s.deps.AI.ListMemorySources(ctx, ids)
	if err != nil {
		s.deps.Log.Warn("读取记忆来源失败，沉淀只使用直接引用来源", "err", err)
		return refs
	}
	for _, source := range linked {
		add(source.SourceType, source.SourceID)
	}
	return refs
}

const reconcileBatchLimit = 100

// taskJobIdempotencyKey 返回 AI 任务执行作业的幂等键（与 enqueueTaskJob 一致）。
func taskJobIdempotencyKey(taskID int64) string { return "ai_task:" + apiID(taskID) }

// Reconcile 修复“任务存在但作业缺失/已终态”和“running 超时未完成”的任务：
//   - pending 任务只有在存在 pending/running 作业时视为已投递；否则通过平台重激活
//     （或按同一幂等键补投）重新投递执行作业；
//   - running 且超过租约仍为 running 的任务视为执行进程已丢失：标记 failed 并重投作业，
//     由 ClaimTask 的 failed 分支重新领取；
//   - 单个任务处理失败不影响其它任务，返回首个错误供调度日志记录。
func (s *Service) Reconcile(ctx context.Context) error {
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	pending, err := s.deps.Tasks.ListPendingTasks(ctx, reconcileBatchLimit)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		ids := make([]int64, 0, len(pending))
		for _, task := range pending {
			ids = append(ids, task.ID)
		}
		existing, err := s.deps.Jobs.ListByRef(ctx, contracts.JobRefQuery{
			RefType: domain.RefTask, RefIDs: ids, Types: []string{contracts.JobTypeAITask}, Limit: 200,
		})
		if err != nil {
			return err
		}
		live := map[int64]bool{}
		for _, job := range existing {
			if job.RefID == nil {
				continue
			}
			if job.Status == contracts.JobStatusPending || job.Status == contracts.JobStatusRunning {
				live[*job.RefID] = true
			}
		}
		for _, task := range pending {
			if live[task.ID] {
				continue
			}
			if err := s.reactivateOrEnqueueTaskJob(ctx, task.ID, task.ScheduledAt); err != nil {
				record(err)
			}
		}
	}

	running, _, err := s.deps.Tasks.ListTasks(ctx, repo.TaskFilter{
		Status: domain.TaskStatusRunning, Limit: reconcileBatchLimit,
	})
	if err != nil {
		return err
	}
	now := s.now()
	for _, task := range running {
		if task.StartedAt == nil || now.Sub(*task.StartedAt) < taskLease {
			continue
		}
		if err := s.deps.Tasks.SetTaskStatus(ctx, task.ID, domain.TaskStatusFailed,
			domain.ErrorTypeDatabase+": 任务执行超时未完成，已重新投递", &now); err != nil {
			record(err)
			continue
		}
		if err := s.reactivateOrEnqueueTaskJob(ctx, task.ID, task.ScheduledAt); err != nil {
			record(err)
		}
		s.deps.Log.Warn("恢复超时未完成的 AI 任务", "task_id", task.ID, "ai_id", task.AIID)
	}
	return firstErr
}

// reactivateOrEnqueueTaskJob 重激活任务对应的 ai_task 作业；作业确实不存在时按
// 同一幂等键补投（幂等）。平台未提供重激活能力时退化为 Enqueue（终态作业会被幂等跳过）。
func (s *Service) reactivateOrEnqueueTaskJob(ctx context.Context, taskID int64, scheduledAt *time.Time) error {
	at := s.now()
	runAt := at
	if scheduledAt != nil && scheduledAt.After(at) {
		runAt = *scheduledAt
	}
	key := taskJobIdempotencyKey(taskID)
	reactivated, err := s.deps.Jobs.Reactivate(ctx, key, runAt, at)
	if err != nil {
		return err
	}
	if reactivated {
		return nil
	}
	// 作业不存在（如任务入队后进程中断）：按同一幂等键补投；已存在终态作业时 no-op。
	if err := s.enqueueTaskJob(ctx, taskID); err != nil {
		return err
	}
	s.deps.Log.Warn("AI 任务没有可执行作业，已按幂等键补投", "task_id", taskID)
	return nil
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

// domainKeywords 从文本提取去重关键词（长度 2-32 的非空片段）。
func domainKeywords(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '，' || r == '。' || r == '；' ||
			r == '：' || r == ',' || r == '.' || r == ';' || r == ':' || r == '!' || r == '?'
	})
	seen := map[string]bool{}
	keywords := make([]string, 0, 8)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len([]rune(field)) < 2 || len([]rune(field)) > 32 {
			continue
		}
		if seen[field] {
			continue
		}
		seen[field] = true
		keywords = append(keywords, field)
		if len(keywords) >= 8 {
			break
		}
	}
	return keywords
}

// actionCatalog 生成可用动作提示。
func actionCatalog(behavior contracts.BehaviorSettings) string {
	actions := make([]string, 0, 8)
	for _, action := range []string{
		domain.ActionOriginalPost, domain.ActionQuote, domain.ActionComment, domain.ActionReply,
		domain.ActionLike, domain.ActionUnlike, domain.ActionFollow, domain.ActionUnfollow,
		domain.ActionRepost, domain.ActionNoop,
	} {
		if domain.ActionAllowed(behavior, action) {
			actions = append(actions, action)
		}
	}
	return strings.Join(actions, "、")
}

// memoryIDs 提取记忆 ID。
func memoryIDs(refs []repo.MemoryRef) []int64 {
	ids := make([]int64, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.MemoryID)
	}
	return ids
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

// ensure unused imports referenced（编译期使用）。
var _ = clock.DayStart
var _ = apiID
var _ = time.Time{}
