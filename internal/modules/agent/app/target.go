package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/modules/agent/domain"
	"github.com/great-magician-01/timeline/internal/modules/agent/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// proactiveTargetScope 是主动互动任务在服务器端允许的目标集合。
//
// 仅依赖提示词让模型不要越界并不够：搜索结果与候选摘要都是不可信输入，
// 模型输出可能引用候选集合之外的公开内容。动作执行前必须用本任务真实的候选
// 集合再次授权（docs/WORKFLOWS.md 7.4 第 8 步、docs/REQUIREMENTS.md 13.3）。
type proactiveTargetScope struct {
	posts map[int64]struct{}
	users map[int64]struct{}
}

// buildProactiveTargetScope 用本次注入提示词的候选内容构建授权集合：
// 允许的帖子是候选帖本身，允许的用户是候选帖作者。
func buildProactiveTargetScope(candidates []contracts.PostCandidate) *proactiveTargetScope {
	scope := &proactiveTargetScope{
		posts: make(map[int64]struct{}, len(candidates)),
		users: make(map[int64]struct{}, len(candidates)),
	}
	for _, candidate := range candidates {
		if candidate.Post.ID > 0 {
			scope.posts[candidate.Post.ID] = struct{}{}
		}
		if candidate.Post.AuthorID > 0 {
			scope.users[candidate.Post.AuthorID] = struct{}{}
		}
	}
	return scope
}

// allowsPost 判断帖子是否在本次候选范围内；nil 表示非主动互动任务，不做候选范围限制。
func (s *proactiveTargetScope) allowsPost(postID int64) bool {
	if s == nil {
		return true
	}
	_, ok := s.posts[postID]
	return ok
}

// allowsUser 判断用户是否在本次候选范围内；nil 表示非主动互动任务，不做候选范围限制。
func (s *proactiveTargetScope) allowsUser(userID int64) bool {
	if s == nil {
		return true
	}
	_, ok := s.users[userID]
	return ok
}

// checkTargetAuthor 校验目标作者账号状态与 AI 间互动开关（docs/WORKFLOWS.md 7.2 第 4/6 条）。
// 返回非空原因表示应拒绝该动作；错误只表示基础设施读取失败。
func (s *Service) checkTargetAuthor(
	ctx context.Context,
	aiID, authorID int64,
	behavior contracts.BehaviorSettings,
) (string, error) {
	if authorID <= 0 {
		return "目标作者不存在", nil
	}
	// AI 自己的内容属于“自身数据”，不受 AI 间互动开关限制。
	if authorID == aiID {
		return "", nil
	}
	author, err := s.deps.Users.GetUser(ctx, authorID)
	if err != nil {
		if isNotFound(err) {
			return "目标作者不存在", nil
		}
		return "", err
	}
	if !author.IsActive() {
		return "目标作者账号已停用", nil
	}
	if author.IsAI && !behavior.AllowAIToAI {
		return "AI 间互动未开启", nil
	}
	return "", nil
}

// validateTarget 在执行动作前重新校验动作白名单、目标类型、公开性、候选范围、
// 关系列表、账号状态与讨论深度；返回非空原因表示应丢弃该动作（任务 rejected）。
//
// scope 只在主动互动任务中非空：此时目标必须属于本次候选集合。取消点赞/取关
// 属于“恢复既有关系”，按 docs/WORKFLOWS.md 7.4 第 9 条由当前关系列表单独授权，
// 但仍要求目标内容公开可用。
func (s *Service) validateTarget(
	ctx context.Context,
	rt contracts.Runtime,
	task repo.Task,
	decision domain.Decision,
	scope *proactiveTargetScope,
) (targetRef, string, error) {
	switch decision.Action {
	case domain.ActionOriginalPost:
		return targetRef{}, "", nil
	case domain.ActionGroupMessage:
		// 群消息目标就是任务来源群：执行时重新授权成员资格与群状态
		// （docs/REQUIREMENTS.md：任务执行时重新授权）。
		if task.TriggerRefType != domain.RefGroup || task.TriggerRefID == nil {
			return targetRef{}, "群聊任务缺少目标群", nil
		}
		if s.deps.Groups == nil {
			return targetRef{}, "", apperr.Internal("群聊上下文依赖未装配")
		}
		member, err := s.deps.Groups.IsMemberActive(ctx, *task.TriggerRefID, rt.AIID)
		if err != nil {
			return targetRef{}, "", err
		}
		if !member {
			return targetRef{}, "该 AI 已不是群成员", nil
		}
		return targetRef{Type: domain.RefGroup, ID: *task.TriggerRefID}, "", nil
	case domain.ActionFollow, domain.ActionUnfollow:
		targetID, err := domain.ParseTargetID(derefString(decision.TargetID))
		if err != nil {
			return targetRef{}, "关注目标无效", nil
		}
		if targetID == rt.AIID {
			return targetRef{}, "不能关注自己", nil
		}
		if !scope.allowsUser(targetID) {
			return targetRef{}, "目标用户不在该 AI 的候选范围内", nil
		}
		if skip, err := s.checkTargetAuthor(ctx, rt.AIID, targetID, rt.Behavior); err != nil || skip != "" {
			return targetRef{}, skip, err
		}
		user, err := s.deps.Users.GetUser(ctx, targetID)
		if err != nil {
			if isNotFound(err) {
				return targetRef{}, "目标用户不存在", nil
			}
			return targetRef{}, "", err
		}
		if user.IsSuspended() {
			return targetRef{}, "目标账号已停用", nil
		}
		if decision.Action == domain.ActionUnfollow {
			following, err := s.deps.Social.IsFollowing(ctx, rt.AIID, targetID)
			if err != nil {
				return targetRef{}, "", err
			}
			if !following {
				return targetRef{}, "该用户不在当前关注列表中", nil
			}
		}
		return targetRef{Type: domain.RefUser, ID: targetID}, "", nil
	case domain.ActionLike, domain.ActionUnlike:
		return s.validateLikeTarget(ctx, rt, decision, scope)
	case domain.ActionRepost:
		targetID, err := domain.ParseTargetID(derefString(decision.TargetID))
		if err != nil {
			return targetRef{}, "目标 ID 无效", nil
		}
		post, err := s.deps.Content.GetVisiblePost(ctx, model.GuestViewer(), targetID)
		if err != nil {
			return targetRef{}, "目标帖子不可见", nil
		}
		if post.Visibility != model.VisibilityPublic {
			return targetRef{}, "只能转发公开内容", nil
		}
		if !scope.allowsPost(post.ID) {
			return targetRef{}, "目标不在该 AI 的候选范围内", nil
		}
		if skip, err := s.checkTargetAuthor(ctx, rt.AIID, post.AuthorID, rt.Behavior); err != nil || skip != "" {
			return targetRef{}, skip, err
		}
		return targetRef{Type: domain.RefPost, ID: targetID, PostID: targetID, AuthorID: post.AuthorID}, "", nil
	case domain.ActionComment:
		targetID, err := domain.ParseTargetID(derefString(decision.TargetID))
		if err != nil {
			return targetRef{}, "目标 ID 无效", nil
		}
		post, err := s.deps.Content.GetVisiblePost(ctx, model.GuestViewer(), targetID)
		if err != nil {
			return targetRef{}, "目标帖子不可见", nil
		}
		if !scope.allowsPost(post.ID) {
			return targetRef{}, "目标不在该 AI 的候选范围内", nil
		}
		if skip, err := s.checkTargetAuthor(ctx, rt.AIID, post.AuthorID, rt.Behavior); err != nil || skip != "" {
			return targetRef{}, skip, err
		}
		return targetRef{Type: domain.RefPost, ID: targetID, PostID: targetID, AuthorID: post.AuthorID}, "", nil
	case domain.ActionReply:
		targetID, err := domain.ParseTargetID(derefString(decision.TargetID))
		if err != nil {
			return targetRef{}, "目标 ID 无效", nil
		}
		comment, err := s.deps.Social.GetComment(ctx, targetID)
		if err != nil {
			return targetRef{}, "", err
		}
		if comment.Deleted() {
			return targetRef{}, "评论已删除", nil
		}
		if _, err := s.deps.Content.GetVisiblePost(ctx, model.GuestViewer(), comment.PostID); err != nil {
			return targetRef{}, "评论所在内容不可见", nil
		}
		if !scope.allowsPost(comment.PostID) {
			return targetRef{}, "目标不在该 AI 的候选范围内", nil
		}
		depth, err := s.deps.Social.CommentDepth(ctx, targetID)
		if err != nil {
			return targetRef{}, "", err
		}
		if !domain.ReplyDepthAllowed(depth, rt.Behavior.MaxReplyDepth) {
			return targetRef{}, "回复深度超限", nil
		}
		if skip, err := s.checkTargetAuthor(ctx, rt.AIID, comment.AuthorID, rt.Behavior); err != nil || skip != "" {
			return targetRef{}, skip, err
		}
		return targetRef{Type: domain.RefComment, ID: targetID, PostID: comment.PostID, AuthorID: comment.AuthorID}, "", nil
	case domain.ActionQuote:
		targetID, err := domain.ParseTargetID(derefString(decision.TargetID))
		if err != nil {
			return targetRef{}, "目标 ID 无效", nil
		}
		post, err := s.deps.Content.GetVisiblePost(ctx, model.GuestViewer(), targetID)
		if err != nil {
			return targetRef{}, "目标帖子不可见", nil
		}
		if post.Visibility != model.VisibilityPublic {
			return targetRef{}, "只能引用公开内容", nil
		}
		if !scope.allowsPost(post.ID) {
			return targetRef{}, "目标不在该 AI 的候选范围内", nil
		}
		if skip, err := s.checkTargetAuthor(ctx, rt.AIID, post.AuthorID, rt.Behavior); err != nil || skip != "" {
			return targetRef{}, skip, err
		}
		return targetRef{Type: domain.RefPost, ID: targetID, PostID: targetID, AuthorID: post.AuthorID}, "", nil
	default:
		return targetRef{}, "不支持的动作", nil
	}
}

// validateLikeTarget 校验点赞/取消点赞目标：目标必须公开可用；取消点赞只能作用于
// AI 当前自己点赞过的帖子或评论（关系列表授权）。
func (s *Service) validateLikeTarget(
	ctx context.Context,
	rt contracts.Runtime,
	decision domain.Decision,
	scope *proactiveTargetScope,
) (targetRef, string, error) {
	targetID, err := domain.ParseTargetID(derefString(decision.TargetID))
	if err != nil {
		return targetRef{}, "目标 ID 无效", nil
	}
	targetType := decision.TargetType
	if targetType == domain.RefUser || targetType == "" {
		targetType = domain.RefPost
	}
	if targetType == domain.RefComment {
		comment, err := s.deps.Social.GetComment(ctx, targetID)
		if err != nil {
			return targetRef{}, "", err
		}
		if comment.Deleted() {
			return targetRef{}, "评论已删除", nil
		}
		if _, err := s.deps.Content.GetVisiblePost(ctx, model.GuestViewer(), comment.PostID); err != nil {
			return targetRef{}, "评论所在内容不可见", nil
		}
		if !scope.allowsPost(comment.PostID) {
			return targetRef{}, "目标不在该 AI 的候选范围内", nil
		}
		if skip, err := s.checkTargetAuthor(ctx, rt.AIID, comment.AuthorID, rt.Behavior); err != nil || skip != "" {
			return targetRef{}, skip, err
		}
		if decision.Action == domain.ActionUnlike {
			liked, err := s.deps.Social.IsCommentLiked(ctx, rt.AIID, targetID)
			if err != nil {
				return targetRef{}, "", err
			}
			if !liked {
				return targetRef{}, "该评论不在当前点赞列表中", nil
			}
		}
		return targetRef{Type: domain.RefComment, ID: targetID, PostID: comment.PostID, AuthorID: comment.AuthorID}, "", nil
	}
	post, err := s.deps.Content.GetVisiblePost(ctx, model.GuestViewer(), targetID)
	if err != nil {
		return targetRef{}, "目标帖子不可见", nil
	}
	if post.Visibility != model.VisibilityPublic {
		return targetRef{}, "只能互动公开内容", nil
	}
	if !scope.allowsPost(post.ID) {
		return targetRef{}, "目标不在该 AI 的候选范围内", nil
	}
	if skip, err := s.checkTargetAuthor(ctx, rt.AIID, post.AuthorID, rt.Behavior); err != nil || skip != "" {
		return targetRef{}, skip, err
	}
	if decision.Action == domain.ActionUnlike {
		liked, err := s.deps.Social.IsPostLiked(ctx, rt.AIID, targetID)
		if err != nil {
			return targetRef{}, "", err
		}
		if !liked {
			return targetRef{}, "该帖子不在当前点赞列表中", nil
		}
	}
	return targetRef{Type: domain.RefPost, ID: targetID, PostID: targetID, AuthorID: post.AuthorID}, "", nil
}
