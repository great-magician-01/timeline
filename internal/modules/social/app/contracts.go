package app

import (
	"context"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/audit"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/richtext"
)

// 编译期断言：social app 实现 contracts.SocialService（写路径 ForActor + agent/admin 读路径）。
var _ contracts.SocialService = (*Service)(nil)

// agent 上下文预算（与旧 agent 实现保持一致）。
const (
	maxRecentCommentRunes = 200
	maxLikedSummaryRunes  = 80
)

// loadActor 读取以指定作者身份执行动作的账号，并校验可写状态：
// AI 身份允许执行动作，但停用账号拒绝（由 shared/access.RequireActorWritable 统一判断）。
func (s *Service) loadActor(ctx context.Context, actorID int64) (model.User, error) {
	if actorID <= 0 {
		return model.User{}, apperr.Unauthenticated("请先登录")
	}
	actor, err := s.deps.Users.GetUser(ctx, actorID)
	if err != nil {
		return model.User{}, err
	}
	if err := access.RequireActorWritable(actor); err != nil {
		return model.User{}, err
	}
	return actor, nil
}

// eventTime 优先使用调用方传入的 at（同一事务应复用业务 now），为零时回退时钟。
func (s *Service) eventTime(at time.Time) time.Time {
	if at.IsZero() {
		return s.now()
	}
	return at
}

// requireActorVisiblePost 读取帖子并按动作主体做可见性校验；AI 只能看到公开内容。
func (s *Service) requireActorVisiblePost(ctx context.Context, actor model.User, postID int64) (model.Post, error) {
	post, err := s.deps.Posts.GetPost(ctx, postID)
	if err != nil {
		return model.Post{}, err
	}
	if actor.IsAI && post.Visibility != model.VisibilityPublic {
		return model.Post{}, apperr.NotFound("内容不存在或无权访问")
	}
	following, err := s.deps.Follows.FollowingSet(ctx, actor.ID)
	if err != nil {
		return model.Post{}, err
	}
	viewer := model.ViewerFrom(actor)
	if err := access.RequireVisiblePost(viewer, post, following); err != nil {
		return model.Post{}, err
	}
	return post, nil
}

// requireActorInteractivePost 在可见性之上校验「已发布 + 作者未停用」的互动条件。
func (s *Service) requireActorInteractivePost(ctx context.Context, actor model.User, postID int64) (model.Post, error) {
	post, err := s.requireActorVisiblePost(ctx, actor, postID)
	if err != nil {
		return model.Post{}, err
	}
	suspended, err := s.authorSuspended(ctx, post.AuthorID)
	if err != nil {
		return model.Post{}, err
	}
	if err := access.CanInteract(model.ViewerFrom(actor), post, suspended); err != nil {
		return model.Post{}, err
	}
	return post, nil
}

// ===== 写路径（以指定作者身份执行） =====

// CreateCommentForActor 发表评论或回复（含计数、通知、提及）。消费者：agent。
func (s *Service) CreateCommentForActor(ctx context.Context, in contracts.ActorCommentInput) (model.Comment, error) {
	actor, err := s.loadActor(ctx, in.ActorID)
	if err != nil {
		return model.Comment{}, err
	}
	if !richtext.ValidFormat(in.ContentFormat) {
		return model.Comment{}, apperr.Validation("评论内容格式无效")
	}
	post, err := s.requireActorInteractivePost(ctx, actor, in.PostID)
	if err != nil {
		return model.Comment{}, err
	}
	return s.createCommentFor(ctx, actor.ID, post, in.ContentFormat, in.ContentSource, in.ParentID, s.eventTime(in.At))
}

// createCommentFor 在事务内写入评论/回复（HTTP 与端口路径共用）。
func (s *Service) createCommentFor(
	ctx context.Context,
	actorID int64,
	post model.Post,
	format, source string,
	parentID *int64,
	at time.Time,
) (model.Comment, error) {
	var created model.Comment
	err := s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		now := at
		var parent *model.Comment
		if parentID != nil {
			row, err := s.deps.Comments.GetForUpdate(ctx, *parentID)
			if err != nil {
				if apperr.IsKind(err, apperr.KindNotFound) {
					return apperr.Validation("父评论不存在")
				}
				return err
			}
			if row.PostID != post.ID {
				return apperr.Validation("父评论不属于该帖子")
			}
			if row.Deleted() {
				return apperr.Validation("父评论已删除，不能回复")
			}
			if row.AuthorID != actorID {
				suspended, err := s.authorSuspended(ctx, row.AuthorID)
				if err != nil {
					return err
				}
				if suspended {
					return apperr.Forbidden("对方账号已停用，不能产生新的互动")
				}
			}
			parent = &row
		}

		safeHTML, plain := richtext.Render(format, source)
		if err := domain.ValidateCommentBody(safeHTML, plain); err != nil {
			return err
		}
		id, err := s.newID()
		if err != nil {
			return err
		}
		row := model.Comment{
			ID:            id,
			PostID:        post.ID,
			AuthorID:      actorID,
			ContentFormat: format,
			ContentSource: source,
			ContentHTML:   safeHTML,
			PlainText:     plain,
			Status:        model.CommentStatusPublished,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if parent != nil {
			parentCommentID, rootCommentID := domain.ThreadPosition(parent.ID, parent.RootCommentID)
			row.ParentCommentID = parentCommentID
			row.RootCommentID = rootCommentID
		}
		created, err = s.deps.Comments.Insert(ctx, row)
		if err != nil {
			return err
		}
		if row.ParentCommentID != nil {
			if _, err := s.deps.Comments.AdjustReplyCount(ctx, *row.ParentCommentID, 1, now); err != nil {
				return err
			}
		}
		if _, err := s.bumpPostCounter(ctx, post.ID, contracts.PostCounterComment, 1, now); err != nil {
			return err
		}
		return s.notifyComment(ctx, actorID, created, post, parent, plain, now)
	})
	if err != nil {
		return model.Comment{}, err
	}
	return created, nil
}

// DeleteCommentAsAdmin 管理员逻辑删除评论（含计数、提及失效与审计）。消费者：admin。
func (s *Service) DeleteCommentAsAdmin(ctx context.Context, in contracts.DeleteCommentInput) error {
	actor, err := s.loadActor(ctx, in.ActorID)
	if err != nil {
		return err
	}
	if !actor.IsAdmin() || actor.IsAI {
		return apperr.Forbidden("需要管理员权限")
	}
	return s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		comment, err := s.deps.Comments.GetForUpdate(ctx, in.CommentID)
		if err != nil {
			return err
		}
		if comment.Deleted() {
			return nil
		}
		if _, err := s.deps.Posts.GetPost(ctx, comment.PostID); err != nil {
			if apperr.IsKind(err, apperr.KindNotFound) {
				return apperr.NotFound("评论不存在")
			}
			return err
		}
		now := s.eventTime(in.At)
		changed, err := s.deps.Comments.MarkDeleted(ctx, in.CommentID, true, false, false, now)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		if _, err := s.bumpPostCounter(ctx, comment.PostID, contracts.PostCounterComment, -1, now); err != nil {
			return err
		}
		if comment.ParentCommentID != nil {
			if _, err := s.deps.Comments.AdjustReplyCount(ctx, *comment.ParentCommentID, -1, now); err != nil {
				return err
			}
		}
		if err := s.deps.Notices.DeactivateMentions(ctx, model.NoticeSourceComment, in.CommentID, now); err != nil {
			return err
		}
		targetID := in.CommentID
		return s.deps.Audit.Write(ctx, audit.Entry{
			AdminID:    actor.ID,
			Action:     audit.ActionCommentDelete,
			TargetType: "comment",
			TargetID:   &targetID,
			Detail:     map[string]any{"deleted_by_admin": true},
			At:         now,
		})
	})
}

// LikeForActor 点赞帖子或评论；返回 (是否生效, 最新计数)。消费者：agent。
func (s *Service) LikeForActor(ctx context.Context, in contracts.LikeInput) (bool, int, error) {
	actor, err := s.loadActor(ctx, in.ActorID)
	if err != nil {
		return false, 0, err
	}
	if err := domain.ValidateTargetType(in.TargetType); err != nil {
		return false, 0, err
	}
	var result LikeResult
	switch in.TargetType {
	case domain.TargetPost:
		post, err := s.requireActorInteractivePost(ctx, actor, in.TargetID)
		if err != nil {
			return false, 0, err
		}
		result, err = s.setPostLike(ctx, actor.ID, post, true)
		if err != nil {
			return false, 0, err
		}
	case domain.TargetComment:
		comment, err := s.deps.Comments.Get(ctx, in.TargetID)
		if err != nil {
			return false, 0, err
		}
		if _, err := s.requireActorInteractivePost(ctx, actor, comment.PostID); err != nil {
			return false, 0, err
		}
		if comment.Deleted() {
			return false, 0, apperr.Forbidden("该评论已删除，不能互动")
		}
		result, err = s.setCommentLike(ctx, actor.ID, comment, true)
		if err != nil {
			return false, 0, err
		}
	}
	return result.Liked, result.Count, nil
}

// UnlikeForActor 取消点赞；返回 (是否仍生效=false, 最新计数)。消费者：agent。
func (s *Service) UnlikeForActor(ctx context.Context, in contracts.LikeInput) (bool, int, error) {
	actor, err := s.loadActor(ctx, in.ActorID)
	if err != nil {
		return false, 0, err
	}
	if err := domain.ValidateTargetType(in.TargetType); err != nil {
		return false, 0, err
	}
	count := 0
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		now := s.eventTime(in.At)
		_, changed, err := s.deps.Likes.Deactivate(ctx, actor.ID, in.TargetType, in.TargetID, now)
		if err != nil {
			return err
		}
		if in.TargetType == domain.TargetPost {
			if changed {
				value, err := s.bumpPostCounter(ctx, in.TargetID, contracts.PostCounterLike, -1, now)
				if err != nil {
					return err
				}
				count = value
				return nil
			}
			post, err := s.deps.Posts.GetPost(ctx, in.TargetID)
			if err != nil {
				// 目标从未存在时保持取消点赞的幂等语义（旧实现返回计数 0）。
				if apperr.IsKind(err, apperr.KindNotFound) {
					count = 0
					return nil
				}
				return err
			}
			count = post.LikeCount
			return nil
		}
		if changed {
			value, err := s.deps.Comments.AdjustLikeCount(ctx, in.TargetID, -1, now)
			if err != nil {
				return err
			}
			count = value
			return nil
		}
		comment, err := s.deps.Comments.Get(ctx, in.TargetID)
		if err != nil {
			// 目标从未存在时保持取消点赞的幂等语义（旧实现返回计数 0）。
			if apperr.IsKind(err, apperr.KindNotFound) {
				count = 0
				return nil
			}
			return err
		}
		count = comment.LikeCount
		return nil
	})
	if err != nil {
		return false, 0, err
	}
	return false, count, nil
}

// FollowForActor 关注（含审批/通知），返回 model.FollowState*。消费者：agent。
func (s *Service) FollowForActor(ctx context.Context, in contracts.FollowInput) (string, error) {
	actor, err := s.loadActor(ctx, in.ActorID)
	if err != nil {
		return "", err
	}
	if in.ActorID <= 0 || in.TargetID <= 0 {
		return "", apperr.Validation("关注对象无效")
	}
	if in.ActorID == in.TargetID {
		return "", apperr.Validation("不能关注自己")
	}
	target, err := s.deps.Users.GetUser(ctx, in.TargetID)
	if err != nil {
		return "", err
	}
	if target.IsSuspended() {
		return "", apperr.Forbidden("对方账号已停用，不能关注")
	}
	return s.followForActor(ctx, actor.ID, target, s.eventTime(in.At))
}

// followForActor 在事务内执行关注（HTTP 与端口路径共用）。
func (s *Service) followForActor(ctx context.Context, actorID int64, target model.User, at time.Time) (string, error) {
	state := model.FollowStateNone
	err := s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		active, err := s.deps.Follows.Following(ctx, actorID, target.ID)
		if err != nil {
			return err
		}
		if active {
			state = model.FollowStateFollowing
			return nil
		}
		now := at
		id, err := s.newID()
		if err != nil {
			return err
		}
		if !target.RequireFollowApproval || target.IsAI {
			rowID, activeAt, changed, err := s.deps.Follows.InsertActive(ctx, id, actorID, target.ID, now)
			if err != nil {
				return err
			}
			state = model.FollowStateFollowing
			if !changed {
				return nil
			}
			return s.deps.Notices.CreateNotification(ctx, contracts.NotificationInput{
				IdempotencyKey: followNotifyKey(rowID, activeAt),
				RecipientID:    target.ID,
				ActorID:        actorID,
				Type:           model.NoticeTypeFollow,
				TargetType:     model.NoticeTargetUser,
				TargetID:       actorID,
				At:             now,
			})
		}
		requestID, changed, err := s.deps.Requests.InsertPending(ctx, id, actorID, target.ID, now)
		if err != nil {
			return err
		}
		state = model.FollowStatePending
		if !changed {
			return nil
		}
		return s.deps.Notices.CreateNotification(ctx, contracts.NotificationInput{
			IdempotencyKey: notifyKey("follow_request", requestID),
			RecipientID:    target.ID,
			ActorID:        actorID,
			Type:           model.NoticeTypeFollowRequest,
			TargetType:     model.NoticeTargetUser,
			TargetID:       actorID,
			At:             now,
		})
	})
	if err != nil {
		return "", err
	}
	return state, nil
}

// UnfollowForActor 取消关注/取消申请，返回 model.FollowState*。消费者：agent。
func (s *Service) UnfollowForActor(ctx context.Context, in contracts.FollowInput) (string, error) {
	actor, err := s.loadActor(ctx, in.ActorID)
	if err != nil {
		return "", err
	}
	if in.TargetID <= 0 {
		return model.FollowStateNone, nil
	}
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		now := s.eventTime(in.At)
		if _, err := s.deps.Follows.Deactivate(ctx, actor.ID, in.TargetID, now); err != nil {
			return err
		}
		_, err := s.deps.Requests.CancelPending(ctx, actor.ID, in.TargetID, now)
		return err
	})
	if err != nil {
		return "", err
	}
	return model.FollowStateNone, nil
}

// RepostForActor 纯转发；返回 (是否生效, 最新计数)。消费者：agent。
func (s *Service) RepostForActor(ctx context.Context, in contracts.RepostInput) (bool, int, error) {
	actor, err := s.loadActor(ctx, in.ActorID)
	if err != nil {
		return false, 0, err
	}
	post, err := s.requireActorInteractivePost(ctx, actor, in.PostID)
	if err != nil {
		return false, 0, err
	}
	if post.Visibility != model.VisibilityPublic {
		return false, 0, apperr.Forbidden("仅公开内容可以转发")
	}
	result, err := s.setRepost(ctx, actor.ID, post, true)
	if err != nil {
		return false, 0, err
	}
	return result.Reposted, result.Count, nil
}

// UnrepostForActor 取消纯转发；返回 (false, 最新计数)。消费者：agent。
func (s *Service) UnrepostForActor(ctx context.Context, in contracts.RepostInput) (bool, int, error) {
	actor, err := s.loadActor(ctx, in.ActorID)
	if err != nil {
		return false, 0, err
	}
	if in.PostID <= 0 {
		return false, 0, apperr.Validation("转发目标无效")
	}
	result, err := s.setRepost(ctx, actor.ID, model.Post{ID: in.PostID}, false)
	if err != nil {
		return false, 0, err
	}
	return false, result.Count, nil
}

// ===== 读路径（agent/admin/content/notice 复用） =====

// GetComment 按 ID 读取评论（不过滤状态）；不存在返回领域 NotFound。
func (s *Service) GetComment(ctx context.Context, commentID int64) (model.Comment, error) {
	return s.deps.Comments.Get(ctx, commentID)
}

// ListRecentComments 读取公开帖子的最近讨论（作者展示名 + 纯文本，限定已发布评论）。
func (s *Service) ListRecentComments(ctx context.Context, query contracts.CommentListQuery) ([]contracts.CommentBrief, error) {
	if query.PostID <= 0 || query.Limit <= 0 {
		return nil, nil
	}
	comments, err := s.deps.Comments.RecentByPost(ctx, query.PostID, query.Limit)
	if err != nil {
		return nil, err
	}
	authorIDs := make([]int64, 0, len(comments))
	for _, comment := range comments {
		authorIDs = append(authorIDs, comment.AuthorID)
	}
	briefs, err := s.deps.Users.GetUserBriefs(ctx, authorIDs)
	if err != nil {
		return nil, err
	}
	// 反转成时间正序，便于模型理解讨论进展；停用作者的历史评论不再进入上下文。
	result := make([]contracts.CommentBrief, 0, len(comments))
	for i := len(comments) - 1; i >= 0; i-- {
		comment := comments[i]
		brief, ok := briefs[comment.AuthorID]
		if !ok || brief.IsSuspended {
			continue
		}
		result = append(result, contracts.CommentBrief{
			ID:         comment.ID,
			AuthorName: firstNonEmpty(brief.Nickname, brief.Username),
			Content:    truncateRunes(strings.TrimSpace(comment.PlainText), maxRecentCommentRunes),
		})
	}
	return result, nil
}

// ListUserReplies 按作者分页读取回复评论（(created_at,id) 倒序；可见性由调用方结合帖子过滤）。
func (s *Service) ListUserReplies(ctx context.Context, query contracts.ReplyQuery) ([]model.Comment, error) {
	if query.AuthorID <= 0 {
		return nil, nil
	}
	return s.deps.Comments.ListByAuthor(ctx, query.AuthorID, query.AfterAt, query.AfterID, query.Limit)
}

// ListNewCommentsOnAuthorPosts 读取某作者公开帖子下、指定时间后的新评论；
// ExcludeAuthorID > 0 时排除该作者自己的评论，避免 AI 回复自己的评论形成自循环。
func (s *Service) ListNewCommentsOnAuthorPosts(ctx context.Context, query contracts.CommentTriggerQuery) ([]model.Comment, error) {
	if query.AuthorID <= 0 {
		return nil, nil
	}
	postIDs, err := s.deps.Posts.ListPostIDsByAuthor(ctx, query.AuthorID)
	if err != nil {
		return nil, err
	}
	if len(postIDs) == 0 {
		return nil, nil
	}
	posts, err := s.deps.Posts.GetPosts(ctx, postIDs)
	if err != nil {
		return nil, err
	}
	publicIDs := make([]int64, 0, len(posts))
	for _, post := range posts {
		if post.Status == model.PostStatusPublished && post.Visibility == model.VisibilityPublic {
			publicIDs = append(publicIDs, post.ID)
		}
	}
	if len(publicIDs) == 0 {
		return nil, nil
	}
	return s.deps.Comments.ListByPostIDsSince(ctx, publicIDs, query.ExcludeAuthorID, query.Since, query.Limit)
}

// IsPostLiked 判断用户是否已点赞帖子。
func (s *Service) IsPostLiked(ctx context.Context, userID, postID int64) (bool, error) {
	return s.deps.Likes.IsActive(ctx, userID, domain.TargetPost, postID)
}

// IsCommentLiked 判断用户是否已点赞评论。
func (s *Service) IsCommentLiked(ctx context.Context, userID, commentID int64) (bool, error) {
	return s.deps.Likes.IsActive(ctx, userID, domain.TargetComment, commentID)
}

// IsReposted 判断用户是否已转发帖子。
func (s *Service) IsReposted(ctx context.Context, userID, postID int64) (bool, error) {
	return s.deps.Reposts.IsActive(ctx, userID, postID)
}

// ViewerPostStates 批量读取访问者对帖子的点赞/转发状态。
func (s *Service) ViewerPostStates(ctx context.Context, userID int64, postIDs []int64) (contracts.PostViewerStates, error) {
	states := contracts.PostViewerStates{}
	if userID <= 0 || len(postIDs) == 0 {
		return states, nil
	}
	liked, err := s.deps.Likes.ActiveStates(ctx, userID, domain.TargetPost, postIDs)
	if err != nil {
		return contracts.PostViewerStates{}, err
	}
	reposted, err := s.deps.Reposts.ActiveStates(ctx, userID, postIDs)
	if err != nil {
		return contracts.PostViewerStates{}, err
	}
	states.Liked = liked
	states.Reposted = reposted
	return states, nil
}

// ListRecentLikedPosts 读取用户最近点赞且仍公开可用的帖子（附摘要）。
func (s *Service) ListRecentLikedPosts(ctx context.Context, userID int64, limit int) ([]contracts.LikedPostRef, error) {
	if userID <= 0 || limit <= 0 {
		return nil, nil
	}
	postIDs, err := s.deps.Likes.LikedPostIDs(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	if len(postIDs) == 0 {
		return nil, nil
	}
	posts, err := s.deps.Posts.GetPosts(ctx, postIDs)
	if err != nil {
		return nil, err
	}
	texts, err := s.deps.Posts.GetPostPlainTexts(ctx, postIDs)
	if err != nil {
		return nil, err
	}
	result := make([]contracts.LikedPostRef, 0, len(postIDs))
	for _, postID := range postIDs {
		post, ok := posts[postID]
		if !ok || post.Status != model.PostStatusPublished || post.Visibility != model.VisibilityPublic {
			continue
		}
		result = append(result, contracts.LikedPostRef{
			PostID:  postID,
			Summary: truncateRunes(strings.TrimSpace(texts[postID]), maxLikedSummaryRunes),
		})
	}
	return result, nil
}

// ListRecentFollowees 读取用户最近关注的账号。
func (s *Service) ListRecentFollowees(ctx context.Context, userID int64, limit int) ([]contracts.FolloweeRef, error) {
	if userID <= 0 || limit <= 0 {
		return nil, nil
	}
	rows, err := s.deps.Follows.ListRecentFollowees(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	userIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		userIDs = append(userIDs, row.UserID)
	}
	users, err := s.deps.Users.GetUsers(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	result := make([]contracts.FolloweeRef, 0, len(rows))
	for _, row := range rows {
		user, ok := users[row.UserID]
		if !ok {
			continue
		}
		result = append(result, contracts.FolloweeRef{
			UserID:   user.ID,
			Username: user.Username,
			Nickname: user.Nickname,
		})
	}
	return result, nil
}

// ListTimelineReposts 读取生效中的纯转发事件，供 content 合并时间线。
func (s *Service) ListTimelineReposts(ctx context.Context, query contracts.RepostTimelineQuery) ([]contracts.RepostEvent, error) {
	repoQuery := RepostEventQuery{
		AuthorID:     query.AuthorID,
		FollowingIDs: query.FollowingIDs,
		AfterAt:      query.AfterAt,
		AfterID:      query.AfterID,
		Limit:        query.Limit,
	}
	switch query.Filter {
	case "human", "ai":
		ids, err := s.deps.Users.ListUserIDsByAIFlag(ctx, query.Filter == "ai")
		if err != nil {
			return nil, err
		}
		repoQuery.AllowedIDs = ids
	}
	if repoQuery.Limit <= 0 {
		return nil, nil
	}
	events, err := s.deps.Reposts.ListEvents(ctx, repoQuery)
	if err != nil {
		return nil, err
	}
	result := make([]contracts.RepostEvent, 0, len(events))
	for _, event := range events {
		result = append(result, contracts.RepostEvent{
			ID:        event.ID,
			UserID:    event.UserID,
			PostID:    event.PostID,
			CreatedAt: event.CreatedAt,
		})
	}
	return result, nil
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// truncateRunes 按字符数截断文本（超长追加省略号）。
func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
