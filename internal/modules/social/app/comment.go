package app

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/social/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/audit"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/richtext"
)

// ListComments 列出顶层评论（按 ID 升序分页），每项带前两条回复预览；游客可读。
func (s *Service) ListComments(ctx context.Context, viewer model.Viewer, postID, cursor int64, limit int) (CommentPage, error) {
	post, err := s.loadVisiblePost(ctx, viewer, postID)
	if err != nil {
		return CommentPage{}, err
	}
	rows, err := s.deps.Comments.ListTopLevel(ctx, postID, cursor, limit+1)
	if err != nil {
		return CommentPage{}, err
	}
	items, hasMore := trimPage(rows, limit)

	rootIDs := make([]int64, 0, len(items))
	for _, comment := range items {
		rootIDs = append(rootIDs, comment.ID)
	}
	previews, err := s.deps.Comments.ReplyPreviews(ctx, rootIDs, domain.ReplyPreviewSize)
	if err != nil {
		return CommentPage{}, err
	}

	// 一次性批量组装评论视图（作者摘要 + 我是否点赞 + 删除权限）。
	all := make([]model.Comment, 0, len(items))
	all = append(all, items...)
	for _, replies := range previews {
		all = append(all, replies...)
	}
	views, err := s.commentViews(ctx, viewer, post.AuthorID, all)
	if err != nil {
		return CommentPage{}, err
	}

	page := CommentPage{Items: make([]Comment, 0, len(items)), Previews: map[int64][]Comment{}, HasMore: hasMore}
	for _, comment := range items {
		page.Items = append(page.Items, views[comment.ID])
		page.NextCursor = comment.ID
	}
	for rootID, replies := range previews {
		mapped := make([]Comment, 0, len(replies))
		for _, reply := range replies {
			mapped = append(mapped, views[reply.ID])
		}
		page.Previews[rootID] = mapped
	}
	return page, nil
}

// ListReplies 列出某条顶层评论下的全部回复（按 ID 升序分页）；游客可读。
func (s *Service) ListReplies(ctx context.Context, viewer model.Viewer, commentID, cursor int64, limit int) (CommentPage, error) {
	comment, err := s.deps.Comments.Get(ctx, commentID)
	if err != nil {
		return CommentPage{}, err
	}
	post, err := s.loadVisiblePost(ctx, viewer, comment.PostID)
	if err != nil {
		return CommentPage{}, err
	}
	rows, err := s.deps.Comments.ListReplies(ctx, commentID, cursor, limit+1)
	if err != nil {
		return CommentPage{}, err
	}
	items, hasMore := trimPage(rows, limit)
	views, err := s.commentViews(ctx, viewer, post.AuthorID, items)
	if err != nil {
		return CommentPage{}, err
	}
	page := CommentPage{Items: make([]Comment, 0, len(items)), HasMore: hasMore}
	for _, reply := range items {
		page.Items = append(page.Items, views[reply.ID])
		page.NextCursor = reply.ID
	}
	return page, nil
}

// CreateComment 发表评论或回复：正文先经 richtext 渲染为安全 HTML 再入库。
func (s *Service) CreateComment(
	ctx context.Context,
	viewer model.Viewer,
	postID int64,
	format, source string,
	parentID *int64,
) (Comment, error) {
	post, err := s.requireInteractivePost(ctx, viewer, postID)
	if err != nil {
		return Comment{}, err
	}
	if !richtext.ValidFormat(format) {
		return Comment{}, apperr.Validation("评论内容格式无效")
	}
	if err := s.allowAction(s.deps.CommentLimiter, viewer); err != nil {
		return Comment{}, err
	}

	var created model.Comment
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		now := s.now()

		var parent *model.Comment
		if parentID != nil {
			row, err := s.deps.Comments.GetForUpdate(ctx, *parentID)
			if err != nil {
				if apperr.IsKind(err, apperr.KindNotFound) {
					return apperr.Validation("父评论不存在")
				}
				return err
			}
			if row.PostID != postID {
				return apperr.Validation("父评论不属于该帖子")
			}
			if row.Deleted() {
				return apperr.Validation("父评论已删除，不能回复")
			}
			if row.AuthorID != viewer.ID {
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
			PostID:        postID,
			AuthorID:      viewer.ID,
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
		if _, err := s.bumpPostCounter(ctx, postID, contracts.PostCounterComment, 1, now); err != nil {
			return err
		}
		return s.notifyComment(ctx, viewer.ID, created, post, parent, plain, now)
	})
	if err != nil {
		return Comment{}, err
	}

	briefs, err := s.deps.Users.GetUserBriefs(ctx, []int64{viewer.ID})
	if err != nil {
		return Comment{}, err
	}
	return Comment{
		Row:       created,
		Author:    briefs[viewer.ID],
		LikedByMe: false,
		CanDelete: true,
	}, nil
}

// notifyComment 生成评论与回复通知，并在公开帖子下重建提及。
// 被回复者就是帖子作者时只发一条通知（docs/WORKFLOWS.md 9.1）。
func (s *Service) notifyComment(
	ctx context.Context,
	actorID int64,
	comment model.Comment,
	post model.Post,
	parent *model.Comment,
	plain string,
	at time.Time,
) error {
	commentID := comment.ID
	postID := post.ID
	notify := func(kind, noticeType string, recipientID int64) error {
		return s.deps.Notices.CreateNotification(ctx, contracts.NotificationInput{
			IdempotencyKey: notifyKey(kind, commentID),
			RecipientID:    recipientID,
			ActorID:        actorID,
			Type:           noticeType,
			TargetType:     model.NoticeTargetComment,
			TargetID:       commentID,
			PostID:         &postID,
			CommentID:      &commentID,
			At:             at,
		})
	}
	switch {
	case parent == nil:
		if err := notify("comment", model.NoticeTypeComment, post.AuthorID); err != nil {
			return err
		}
	case parent.AuthorID == post.AuthorID:
		if err := notify("reply", model.NoticeTypeReply, parent.AuthorID); err != nil {
			return err
		}
	default:
		if err := notify("reply", model.NoticeTypeReply, parent.AuthorID); err != nil {
			return err
		}
		if err := notify("comment", model.NoticeTypeComment, post.AuthorID); err != nil {
			return err
		}
	}
	// 提及只从公开内容生成，非公开评论不触发 AI 读取与提及通知。
	if post.Visibility != model.VisibilityPublic {
		return nil
	}
	// 一次调用同时完成「重建提及 + 写提及通知」，notice 内部只解析一次用户名。
	return s.deps.Notices.PublishMentions(ctx, contracts.PublishMentionsInput{
		SourceType:   model.NoticeSourceComment,
		SourceID:     commentID,
		PostID:       postID,
		AuthorID:     actorID,
		PlainText:    plain,
		MentionNames: richtext.ExtractMentions(plain),
		At:           at,
	})
}

// DeleteComment 逻辑删除评论：评论作者、帖子作者或管理员；管理员操作写审计。
// 不级联删除子回复，同步回退帖子评论数与父评论回复数（幂等）。
func (s *Service) DeleteComment(ctx context.Context, viewer model.Viewer, commentID int64) error {
	if err := s.requireWritable(viewer); err != nil {
		return err
	}
	return s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		comment, err := s.deps.Comments.GetForUpdate(ctx, commentID)
		if err != nil {
			return err
		}
		post, err := s.deps.Posts.GetPost(ctx, comment.PostID)
		if err != nil {
			if apperr.IsKind(err, apperr.KindNotFound) {
				return apperr.NotFound("评论不存在")
			}
			return err
		}
		if !viewer.IsAdmin && viewer.ID != comment.AuthorID && viewer.ID != post.AuthorID {
			// 无权处置时按「不存在」处理，不暴露他人评论。
			return apperr.NotFound("评论不存在")
		}
		if comment.Deleted() {
			return nil
		}

		now := s.now()
		byAuthor := !viewer.IsAdmin && viewer.ID == comment.AuthorID
		byPostAuthor := !viewer.IsAdmin && viewer.ID == post.AuthorID && viewer.ID != comment.AuthorID
		changed, err := s.deps.Comments.MarkDeleted(ctx, commentID, viewer.IsAdmin, byAuthor, byPostAuthor, now)
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
		// 已删除评论不再触发 AI 读取或提及通知。
		if err := s.deps.Notices.DeactivateMentions(ctx, model.NoticeSourceComment, commentID, now); err != nil {
			return err
		}
		if viewer.IsAdmin {
			targetID := commentID
			if err := s.deps.Audit.Write(ctx, audit.Entry{
				AdminID:    viewer.ID,
				Action:     audit.ActionCommentDelete,
				TargetType: "comment",
				TargetID:   &targetID,
				Detail:     map[string]any{"deleted_by_admin": true},
				At:         now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// CommentDepth 返回评论的回复深度（顶层 = 1），用于 AI 讨论深度限制。
func (s *Service) CommentDepth(ctx context.Context, commentID int64) (int, error) {
	if commentID <= 0 {
		return 0, apperr.NotFound("评论不存在")
	}
	depth := 1
	current := commentID
	for depth < domain.MaxCommentDepth {
		parent, err := s.deps.Comments.ParentID(ctx, current)
		if err != nil {
			return 0, err
		}
		if parent == nil {
			return depth, nil
		}
		current = *parent
		depth++
	}
	return depth, nil
}

// commentViews 批量组装评论视图，避免逐条查询作者与点赞状态。
func (s *Service) commentViews(
	ctx context.Context,
	viewer model.Viewer,
	postAuthorID int64,
	comments []model.Comment,
) (map[int64]Comment, error) {
	result := make(map[int64]Comment, len(comments))
	if len(comments) == 0 {
		return result, nil
	}
	authorIDs := make([]int64, 0, len(comments))
	commentIDs := make([]int64, 0, len(comments))
	for _, comment := range comments {
		authorIDs = append(authorIDs, comment.AuthorID)
		commentIDs = append(commentIDs, comment.ID)
	}
	briefs, err := s.deps.Users.GetUserBriefs(ctx, authorIDs)
	if err != nil {
		return nil, err
	}
	liked := map[int64]bool{}
	if viewer.ID > 0 && !viewer.IsAI {
		liked, err = s.deps.Likes.ActiveStates(ctx, viewer.ID, domain.TargetComment, commentIDs)
		if err != nil {
			return nil, err
		}
	}
	for _, comment := range comments {
		item := Comment{
			Row:       comment,
			Author:    briefs[comment.AuthorID],
			LikedByMe: liked[comment.ID],
			CanDelete: domain.CanDeleteComment(comment, postAuthorID, viewer),
		}
		if comment.Deleted() {
			item.LikedByMe = false
			item.CanDelete = false
		}
		result[comment.ID] = item
	}
	return result, nil
}
