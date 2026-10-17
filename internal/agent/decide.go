package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/great-magician-01/callable"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/ai"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/social"
)

// ── 纯函数：动作开关、频率、冷却与活跃时段 ──

// actionAllowed 判断行为策略是否开启了某个动作。
// noop 不需要单独开关；reply 由 allow_passive_reply 控制。
func actionAllowed(behavior ai.BehaviorSettings, action string) bool {
	switch strings.TrimSpace(action) {
	case actionOriginalPost:
		return behavior.AllowOriginalPost
	case actionComment:
		return behavior.AllowActiveComment
	case actionReply:
		return behavior.AllowPassiveReply
	case actionLike, actionUnlike:
		// 取消点赞沿用“点赞能力”开关：能点赞才谈得上取消。
		return behavior.AllowLike
	case actionFollow:
		return behavior.AllowFollow
	case actionUnfollow:
		return behavior.AllowUnfollow
	case actionRepost:
		return behavior.AllowRepost
	case actionQuote:
		return behavior.AllowQuote
	case actionNoop, "":
		return true
	default:
		return false
	}
}

// actionAllowedForTrigger 按触发来源区分“主动评论”和“被动回复”：
// 被提及或自己的内容收到新评论属于被动回复（allow_passive_reply），
// 主动浏览候选内容时发表的评论才是主动评论（allow_active_comment）。
// 两者是独立开关（docs/REQUIREMENTS.md 12.3），否则只开启被动回复的 AI
// 永远无法回应提及。
func actionAllowedForTrigger(behavior ai.BehaviorSettings, trigger, action string) bool {
	switch strings.TrimSpace(action) {
	case actionComment:
		if trigger == triggerMention || trigger == triggerComment {
			return behavior.AllowPassiveReply
		}
		return behavior.AllowActiveComment
	case actionReply:
		return behavior.AllowPassiveReply
	default:
		return actionAllowed(behavior, action)
	}
}

// taskActionAllowed 限制特定触发类型的动作范围：
// 管理端生成与定时原创任务是内容生成任务，只允许产出原创帖、引用帖或 noop，
// 互动类动作交给主动互动与被动回复触发处理。
func taskActionAllowed(task aiTask, action string) bool {
	switch task.TriggerType {
	case triggerManual, triggerSchedule:
		switch action {
		case actionOriginalPost, actionQuote, actionNoop:
			return true
		default:
			return false
		}
	default:
		return true
	}
}

// proactiveAllowed 判断是否开启了至少一个主动互动动作。
func proactiveAllowed(behavior ai.BehaviorSettings) bool {
	for _, action := range []string{
		actionComment, actionLike, actionUnlike, actionFollow, actionUnfollow, actionRepost, actionQuote,
	} {
		if actionAllowed(behavior, action) {
			return true
		}
	}
	return false
}

// validCandidateScope 判断候选范围配置是否可用；none 表示禁用主动互动。
func validCandidateScope(scope string) bool {
	switch strings.TrimSpace(scope) {
	case scopeGlobalPublic, scopeFollowed, scopeTags, scopeAuthors:
		return true
	default:
		return false
	}
}

// withinActiveHours 判断北京时间整点是否落在启用时段内。
// start > end 视为跨零点（例如 22 → 6）；start == end 视为全天开放；非法取值视为全天开放。
func withinActiveHours(start, end, hour int) bool {
	if start < 0 || start > 23 || end < 0 || end > 23 {
		return true
	}
	if start == end {
		return true
	}
	if hour < 0 || hour > 23 {
		return false
	}
	if start < end {
		return hour >= start && hour < end
	}
	return hour >= start || hour < end
}

// underDailyLimit 判断当日计数是否未达上限；limit <= 0 表示不限制。
func underDailyLimit(count, limit int) bool {
	if limit <= 0 {
		return true
	}
	return count < limit
}

// cooldownElapsed 判断距离上次成功是否已超过冷却时间。
// cooldownSeconds <= 0 表示不设冷却；从未成功过时视为已过冷却。
func cooldownElapsed(last, now time.Time, cooldownSeconds int) bool {
	if cooldownSeconds <= 0 || last.IsZero() {
		return true
	}
	if now.Before(last) {
		return false
	}
	return now.Sub(last) >= time.Duration(cooldownSeconds)*time.Second
}

// replyDepthAllowed 判断是否仍允许 AI 继续回复：
// 父评论深度为 1（顶层）时，回复产生的深度为 2，因此要求 parentDepth < maxDepth。
// maxDepth 为 0 表示 AI 不参与回复。
func replyDepthAllowed(parentDepth, maxDepth int) bool {
	if maxDepth <= 0 {
		return false
	}
	return parentDepth < maxDepth
}

// isRetryableTaskStatus 判断任务状态是否允许首次领取执行。
// 只有 pending（新任务）与 failed（可恢复失败）可以直接进入 running；
// running 的重新领取由 claimTask 按 started_at 超时窗口单独判定。
func isRetryableTaskStatus(status string) bool {
	return status == statusPending || status == statusFailed
}

// actionGateState 是“执行动作前二次校验”所需的开关状态快照。
type actionGateState struct {
	GlobalEnabled  bool
	AIExists       bool
	ProfileEnabled bool
	ActorExists    bool
	ActorActive    bool
}

// evaluateActionGate 判断执行动作前是否必须停止任务：
// 返回非空状态与原因表示应终止（canceled），空串表示可以继续执行动作。
// 全局开关优先于单个 AI 配置，便于把“紧急停止”记录为统一原因。
func evaluateActionGate(state actionGateState) (status, reason string) {
	switch {
	case !state.GlobalEnabled:
		return statusCanceled, "全局 AI 开关已关闭"
	case !state.AIExists:
		return statusCanceled, "AI 账号不存在"
	case !state.ProfileEnabled:
		return statusCanceled, "该 AI 已停用"
	case !state.ActorExists:
		return statusCanceled, "AI 账号不存在"
	case !state.ActorActive:
		return statusCanceled, "AI 账号已停用"
	default:
		return "", ""
	}
}

// actionGate 在模型调用结束后重新读取开关与账号状态（不信任入队时的快照）。
func (s *Service) actionGate(
	ctx context.Context,
	q access.Querier,
	aiID int64,
) (taskResult, bool, error) {
	state := actionGateState{}
	enabled, err := s.globalAIEnabled(ctx, q)
	if err != nil {
		return taskResult{}, false, err
	}
	state.GlobalEnabled = enabled

	rt, err := s.ai.RuntimeByAI(ctx, q, aiID)
	switch {
	case isNotFound(err):
		state.AIExists = false
	case err != nil:
		return taskResult{}, false, err
	default:
		state.AIExists = true
		state.ProfileEnabled = rt.Profile.IsEnabled
	}

	actor, err := loadActor(ctx, q, aiID)
	switch {
	case isNotFound(err):
		state.ActorExists = false
	case err != nil:
		return taskResult{}, false, err
	default:
		state.ActorExists = true
		state.ActorActive = actor.Active
	}

	status, reason := evaluateActionGate(state)
	if status == "" {
		return taskResult{}, false, nil
	}
	return taskResult{Status: status, Decision: reason}, true, nil
}

// retryPending 判断一次失败后是否还应交给作业层重试。
func retryPending(attempts, maxAttempts int) bool {
	return attempts < maxAttempts
}

// classifyCallError 把模型调用错误映射为任务状态与错误分类：
// 429/5xx/传输层（StatusCode = 0）→ failed（可有限重试）；其它 4xx → rejected（不重试）；
// 超时 → failed；上下文取消 → canceled。
func classifyCallError(err error) (status, errorType, message string) {
	if err == nil {
		return statusSucceeded, "", ""
	}
	message = err.Error()
	var apiErr *callable.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 0:
			return statusFailed, errorTypeTransport, message
		case apiErr.StatusCode == 429:
			return statusFailed, errorTypeAPI429, message
		case apiErr.StatusCode >= 500:
			return statusFailed, errorTypeAPI5xx, message
		default:
			return statusRejected, errorTypeAPI4xx, message
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return statusFailed, errorTypeTimeout, message
	}
	if errors.Is(err, context.Canceled) {
		return statusCanceled, errorTypeTimeout, message
	}
	return statusFailed, errorTypeInternal, message
}

// classifyActionError 把动作执行错误映射为任务状态：
// 业务校验（*api.Failure）→ rejected；数据库等内部错误 → failed。
func classifyActionError(err error) (status, errorType string) {
	if err == nil {
		return statusSucceeded, ""
	}
	var failure *api.Failure
	if errors.As(err, &failure) {
		switch failure.Status {
		case 404:
			return statusRejected, errorTypeInvalidTarget
		case 401, 403:
			return statusRejected, errorTypeForbidden
		case 409, 400:
			return statusRejected, errorTypeInvalidTarget
		case 429:
			return statusRejected, errorTypeRateLimited
		default:
			return statusRejected, errorTypeInvalidAction
		}
	}
	return statusFailed, errorTypeDatabase
}

// ── 频率、冷却与每日上限（依赖数据库） ──

// counterDate 返回北京时间当天的日期（用于 ai_action_counters.counter_date）。
func counterDate(now time.Time) time.Time {
	local := now.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
}

// actionCount 读取某个动作当天的执行次数；不存在时返回 0。
func (s *Service) actionCount(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	action string,
	now time.Time,
) (int, error) {
	var count int
	err := q.QueryRow(ctx, `
		SELECT count FROM ai_action_counters
		WHERE ai_id = $1 AND action_type = $2 AND counter_date = $3`,
		aiID, action, counterDate(now)).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return count, err
}

// totalActionCount 读取当天全部动作的总次数。
func (s *Service) totalActionCount(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	now time.Time,
) (int, error) {
	var count int
	err := q.QueryRow(ctx, `
		SELECT COALESCE(SUM(count), 0) FROM ai_action_counters
		WHERE ai_id = $1 AND counter_date = $2`, aiID, counterDate(now)).Scan(&count)
	return count, err
}

// lastSuccessAt 读取某个动作最近一次成功时间；targetID 非空时限定同一目标。
// 同名动作语义：unlike 复用 like 的成功记录，避免出现“取消点赞”把点赞冷却状态清空。
func (s *Service) lastSuccessAt(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	action string,
	targetID *int64,
) (time.Time, error) {
	actions := []string{action}
	switch action {
	case actionUnlike:
		actions = append(actions, actionLike)
	case actionUnfollow:
		actions = append(actions, actionFollow)
	}
	sql := `SELECT MAX(finished_at) FROM ai_tasks
		WHERE ai_id = $1 AND action_type = ANY($2) AND status = 'succeeded'`
	args := []any{aiID, actions}
	if targetID != nil {
		sql += ` AND target_id = $3`
		args = append(args, *targetID)
	}
	var last *time.Time
	if err := q.QueryRow(ctx, sql, args...).Scan(&last); err != nil {
		return time.Time{}, err
	}
	if last == nil {
		return time.Time{}, nil
	}
	return *last, nil
}

// limitReached 返回非空原因表示该动作未开启、已达到当日上限或仍处于冷却时间。
func (s *Service) limitReached(
	ctx context.Context,
	q access.Querier,
	rt ai.Runtime,
	trigger string,
	action string,
	targetID *int64,
	now time.Time,
) (string, error) {
	if !actionAllowedForTrigger(rt.Behavior, trigger, action) {
		return "该 AI 未开启对应动作", nil
	}
	total, err := s.totalActionCount(ctx, q, rt.AIID, now)
	if err != nil {
		return "", err
	}
	if !underDailyLimit(total, rt.Behavior.DailyLimit("total")) {
		return "已达到当日动作总上限", nil
	}
	count, err := s.actionCount(ctx, q, rt.AIID, action, now)
	if err != nil {
		return "", err
	}
	if !underDailyLimit(count, rt.Behavior.DailyLimit(action)) {
		return "已达到该动作的当日上限", nil
	}
	last, err := s.lastSuccessAt(ctx, q, rt.AIID, action, targetID)
	if err != nil {
		return "", err
	}
	if !cooldownElapsed(last, now, rt.Behavior.CooldownSeconds) {
		return "该动作仍在冷却时间内", nil
	}
	return "", nil
}

// bumpActionCounter 动作成功后在同一事务内累加计数。
func (s *Service) bumpActionCounter(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	action string,
	now time.Time,
) error {
	counterID, err := s.ids.Next()
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO ai_action_counters (id, ai_id, action_type, counter_date, count, updated_at)
		VALUES ($1, $2, $3, $4, 1, now())
		ON CONFLICT (ai_id, action_type, counter_date)
		DO UPDATE SET count = ai_action_counters.count + 1, updated_at = now()`,
		counterID, aiID, action, counterDate(now))
	return err
}

// ── 决策目标校验 ──

// decisionTarget 是校验通过后的动作目标。
type decisionTarget struct {
	Type     string
	ID       int64
	PostID   int64
	QuotedID *int64
}

// validateDecisionTarget 在执行动作前重新校验动作白名单、目标类型、公开性、
// 账号状态、AI 间互动与讨论深度；返回非空原因表示应丢弃该动作（任务 rejected）。
func (s *Service) validateDecisionTarget(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	behavior ai.BehaviorSettings,
	decision Decision,
) (decisionTarget, string, error) {
	viewer := aiViewer(aiID)
	switch decision.Action {
	case actionOriginalPost:
		if strings.TrimSpace(decision.Content) == "" {
			return decisionTarget{}, "原创帖缺少正文", nil
		}
		return decisionTarget{Type: "ai"}, "", nil

	case actionComment:
		if decision.TargetType != targetPost {
			return decisionTarget{}, "评论必须指定 post 目标", nil
		}
		postID, err := parseTargetID(decision.TargetID)
		if err != nil {
			return decisionTarget{}, "评论目标 ID 无效", nil
		}
		post, err := social.LoadVisiblePost(ctx, q, viewer, postID)
		if err != nil {
			if isNotFound(err) {
				return decisionTarget{}, "评论目标不存在或不是公开内容", nil
			}
			return decisionTarget{}, "", err
		}
		if skip, err := s.checkTargetAuthor(ctx, q, post.AuthorID, behavior); err != nil || skip != "" {
			return decisionTarget{}, skip, err
		}
		if strings.TrimSpace(decision.Content) == "" {
			return decisionTarget{}, "评论缺少正文", nil
		}
		return decisionTarget{Type: targetPost, ID: post.ID, PostID: post.ID}, "", nil

	case actionReply:
		if decision.TargetType != targetComment {
			return decisionTarget{}, "回复必须指定 comment 目标", nil
		}
		commentID, err := parseTargetID(decision.TargetID)
		if err != nil {
			return decisionTarget{}, "回复目标 ID 无效", nil
		}
		comment, err := loadCommentForAgent(ctx, q, commentID)
		if err != nil {
			if isNotFound(err) {
				return decisionTarget{}, "回复目标不存在或已删除", nil
			}
			return decisionTarget{}, "", err
		}
		if _, err := social.LoadVisiblePost(ctx, q, viewer, comment.PostID); err != nil {
			if isNotFound(err) {
				return decisionTarget{}, "回复目标所在内容不是公开内容", nil
			}
			return decisionTarget{}, "", err
		}
		if skip, err := s.checkTargetAuthor(ctx, q, comment.AuthorID, behavior); err != nil || skip != "" {
			return decisionTarget{}, skip, err
		}
		depth, err := s.social.CommentDepth(ctx, q, comment.ID)
		if err != nil {
			if isNotFound(err) {
				return decisionTarget{}, "回复目标不存在", nil
			}
			return decisionTarget{}, "", err
		}
		if !replyDepthAllowed(depth, behavior.MaxReplyDepth) {
			return decisionTarget{}, "讨论串已达到 AI 回复深度上限", nil
		}
		if strings.TrimSpace(decision.Content) == "" {
			return decisionTarget{}, "回复缺少正文", nil
		}
		return decisionTarget{Type: targetComment, ID: comment.ID, PostID: comment.PostID}, "", nil

	case actionLike, actionUnlike, actionRepost, actionQuote:
		target := decision.TargetType
		if target == "" {
			target = targetPost
		}
		switch target {
		case targetPost:
			postID, err := parseTargetID(decision.TargetID)
			if err != nil {
				return decisionTarget{}, "目标帖子 ID 无效", nil
			}
			post, err := social.LoadVisiblePost(ctx, q, viewer, postID)
			if err != nil {
				if isNotFound(err) {
					return decisionTarget{}, "目标内容不存在或不是公开内容", nil
				}
				return decisionTarget{}, "", err
			}
			if skip, err := s.checkTargetAuthor(ctx, q, post.AuthorID, behavior); err != nil || skip != "" {
				return decisionTarget{}, skip, err
			}
			if decision.Action == actionQuote && strings.TrimSpace(decision.Content) == "" {
				return decisionTarget{}, "引用帖缺少正文", nil
			}
			if decision.Action == actionUnlike {
				// 取消点赞只允许作用于自己当前点过赞的公开帖子。
				active, err := s.likeActive(ctx, q, aiID, targetPost, post.ID)
				if err != nil {
					return decisionTarget{}, "", err
				}
				if !active {
					return decisionTarget{}, "该帖子不在当前点赞列表中", nil
				}
			}
			quoted := post.ID
			return decisionTarget{Type: targetPost, ID: post.ID, PostID: post.ID, QuotedID: &quoted}, "", nil
		case targetComment:
			if decision.Action != actionLike {
				return decisionTarget{}, "只有点赞支持评论目标", nil
			}
			commentID, err := parseTargetID(decision.TargetID)
			if err != nil {
				return decisionTarget{}, "目标评论 ID 无效", nil
			}
			comment, err := loadCommentForAgent(ctx, q, commentID)
			if err != nil {
				if isNotFound(err) {
					return decisionTarget{}, "目标评论不存在或已删除", nil
				}
				return decisionTarget{}, "", err
			}
			post, err := social.LoadVisiblePost(ctx, q, viewer, comment.PostID)
			if err != nil {
				if isNotFound(err) {
					return decisionTarget{}, "目标评论所在内容不是公开内容", nil
				}
				return decisionTarget{}, "", err
			}
			if skip, err := s.checkTargetAuthor(ctx, q, post.AuthorID, behavior); err != nil || skip != "" {
				return decisionTarget{}, skip, err
			}
			if skip, err := s.checkTargetAuthor(ctx, q, comment.AuthorID, behavior); err != nil || skip != "" {
				return decisionTarget{}, skip, err
			}
			return decisionTarget{Type: targetComment, ID: comment.ID, PostID: comment.PostID}, "", nil
		default:
			return decisionTarget{}, "目标类型与动作不匹配", nil
		}

	case actionFollow, actionUnfollow:
		if decision.TargetType != targetUser {
			return decisionTarget{}, "关注类动作必须指定 user 目标", nil
		}
		userID, err := parseTargetID(decision.TargetID)
		if err != nil {
			return decisionTarget{}, "目标用户 ID 无效", nil
		}
		if userID == aiID {
			return decisionTarget{}, "不能对自己执行关注类动作", nil
		}
		target, err := loadActor(ctx, q, userID)
		if err != nil {
			if isNotFound(err) {
				return decisionTarget{}, "目标用户不存在", nil
			}
			return decisionTarget{}, "", err
		}
		if !target.Active {
			return decisionTarget{}, "目标账号已停用", nil
		}
		if target.IsAI && !behavior.AllowAIToAI {
			return decisionTarget{}, "AI 间互动未开启", nil
		}
		if decision.Action == actionUnfollow {
			// 取关只允许作用于自己当前生效的关注关系（待审批申请不算）。
			following, err := access.IsFollowing(ctx, q, aiID, target.ID)
			if err != nil {
				return decisionTarget{}, "", err
			}
			if !following {
				return decisionTarget{}, "该用户不在当前关注列表中", nil
			}
		}
		return decisionTarget{Type: targetUser, ID: target.ID}, "", nil

	case actionNoop:
		return decisionTarget{}, "", nil

	default:
		return decisionTarget{}, "动作不在白名单内", nil
	}
}

// parseTargetID 解析模型返回的目标 ID（雪花 ID 字符串）。
func parseTargetID(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, api.ErrValidation("缺少目标 ID")
	}
	return api.ParseID(value)
}

// checkTargetAuthor 校验目标作者账号状态与 AI 间互动开关。
func (s *Service) checkTargetAuthor(
	ctx context.Context,
	q access.Querier,
	authorID int64,
	behavior ai.BehaviorSettings,
) (string, error) {
	author, err := loadActor(ctx, q, authorID)
	if err != nil {
		if isNotFound(err) {
			return "目标作者不存在", nil
		}
		return "", err
	}
	if !author.Active {
		return "目标作者账号已停用", nil
	}
	if author.IsAI && !behavior.AllowAIToAI {
		return "AI 间互动未开启", nil
	}
	return "", nil
}

// loadCommentForAgent 读取评论；不存在或已删除时返回 404（AI 上下文按不存在处理）。
func loadCommentForAgent(ctx context.Context, q access.Querier, commentID int64) (model.Comment, error) {
	comment, err := model.ScanComment(q.QueryRow(ctx,
		`SELECT `+model.CommentColumns+` FROM comments WHERE id = $1`, commentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Comment{}, api.ErrNotFound("评论不存在或无权访问")
	}
	if err != nil {
		return model.Comment{}, err
	}
	if comment.Deleted() {
		return model.Comment{}, api.ErrNotFound("评论不存在或无权访问")
	}
	return comment, nil
}

// formatErrorSummary 生成写入 ai_tasks.error 的错误摘要。
func formatErrorSummary(errorType, message string) string {
	trimmed := truncateRunes(strings.TrimSpace(message), 500)
	switch {
	case errorType == "":
		return trimmed
	case trimmed == "":
		return errorType
	default:
		return fmt.Sprintf("%s: %s", errorType, trimmed)
	}
}
